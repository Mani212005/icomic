"""Structural validation tests for Icomic PostgreSQL migrations.

Validates that:
- Migrations have paired up and down files with sequential version ordering.
- Down migrations fully reverse up migrations in proper reverse dependency order.
- All entities and columns defined in docs/SPEC.md Section 9 are present with correct types.
- All generation job properties and enums match contracts/schemas/job.schema.json.
- All credit ledger properties and enums match contracts/schemas/credit.schema.json.
- Foreign key references, unique constraints, and indexes are defined.
- Stale approval rule requirements (approved_at, updated_at, completed_at) are satisfied.
- Provenance tracking fields and completion invariants are enforced.
- Owner decisions (users are global: no geo-blocking columns; 18+ gate deferred: no age enforcement).
- Validation is completely hermetic and runs with zero external dependencies.
"""

import json
from pathlib import Path
import re
import unittest

DEPLOY_DIR = Path(__file__).resolve().parent
REPO_ROOT = DEPLOY_DIR.parent
MIGRATIONS_DIR = DEPLOY_DIR / "migrations"
CONTRACTS_DIR = REPO_ROOT / "contracts"


def strip_sql_comments(sql: str) -> str:
    """Removes single-line SQL comments while preserving structure."""
    lines = []
    for line in sql.splitlines():
        # Remove comment part
        comment_idx = line.find("--")
        if comment_idx != -1:
            line = line[:comment_idx]
        lines.append(line)
    return "\n".join(lines)


def split_sql_tokens(text: str) -> list[str]:
    """Splits text by top-level commas, respecting parentheses and single quotes."""
    tokens = []
    current = []
    paren_depth = 0
    in_quote = False
    quote_char = ""

    for char in text:
        if in_quote:
            current.append(char)
            if char == quote_char:
                in_quote = False
        elif char in ("'", '"'):
            in_quote = True
            quote_char = char
            current.append(char)
        elif char == "(":
            paren_depth += 1
            current.append(char)
        elif char == ")":
            paren_depth -= 1
            current.append(char)
        elif char == "," and paren_depth == 0:
            token = "".join(current).strip()
            if token:
                tokens.append(token)
            current = []
        else:
            current.append(char)

    last_token = "".join(current).strip()
    if last_token:
        tokens.append(last_token)
    return tokens


def parse_create_tables(sql: str) -> dict[str, dict]:
    """Parses CREATE TABLE statements and extracts columns and constraints."""
    tables = {}
    clean_sql = strip_sql_comments(sql)

    # Find each CREATE TABLE statement
    pattern = re.compile(r"CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s*\(", re.IGNORECASE)
    pos = 0
    while True:
        match = pattern.search(clean_sql, pos)
        if not match:
            break
        table_name = match.group(1).lower()
        start_paren = match.end() - 1

        # Scan for matching closing parenthesis
        paren_depth = 0
        end_paren = -1
        in_quote = False
        for i in range(start_paren, len(clean_sql)):
            char = clean_sql[i]
            if char in ("'", '"'):
                if in_quote and char == quote_char:
                    in_quote = False
                elif not in_quote:
                    in_quote = True
                    quote_char = char
            elif not in_quote:
                if char == "(":
                    paren_depth += 1
                elif char == ")":
                    paren_depth -= 1
                    if paren_depth == 0:
                        end_paren = i
                        break

        if end_paren == -1:
            break

        body = clean_sql[start_paren + 1 : end_paren].strip()
        pos = end_paren + 1

        # Parse column and constraint definitions
        definitions = split_sql_tokens(body)
        columns = {}
        foreign_keys = {}
        unique_constraints = []
        check_constraints = []

        for item in definitions:
            item_clean = " ".join(item.split())
            item_upper = item_clean.upper()

            if item_upper.startswith("CONSTRAINT"):
                parts = item_clean.split(None, 2)
                c_name = parts[1]
                c_def = parts[2]
                c_def_upper = c_def.upper()
                if "FOREIGN KEY" in c_def_upper:
                    fk_match = re.search(r"FOREIGN\s+KEY\s*\(([^)]+)\)\s*REFERENCES\s+(\w+)\s*\(([^)]+)\)", c_def, re.IGNORECASE)
                    if fk_match:
                        col = fk_match.group(1).strip().lower()
                        target_tbl = fk_match.group(2).strip().lower()
                        target_col = fk_match.group(3).strip().lower()
                        foreign_keys[col] = (target_tbl, target_col)
                elif "UNIQUE" in c_def_upper:
                    u_match = re.search(r"UNIQUE\s*\(([^)]+)\)", c_def, re.IGNORECASE)
                    if u_match:
                        cols = [c.strip().lower() for c in u_match.group(1).split(",")]
                        unique_constraints.append(cols)
                elif "CHECK" in c_def_upper:
                    check_constraints.append(c_def)
            elif item_upper.startswith("FOREIGN KEY"):
                fk_match = re.search(r"FOREIGN\s+KEY\s*\(([^)]+)\)\s*REFERENCES\s+(\w+)\s*\(([^)]+)\)", item_clean, re.IGNORECASE)
                if fk_match:
                    col = fk_match.group(1).strip().lower()
                    target_tbl = fk_match.group(2).strip().lower()
                    target_col = fk_match.group(3).strip().lower()
                    foreign_keys[col] = (target_tbl, target_col)
            elif item_upper.startswith("PRIMARY KEY"):
                pass
            elif item_upper.startswith("UNIQUE"):
                u_match = re.search(r"UNIQUE\s*\(([^)]+)\)", item_clean, re.IGNORECASE)
                if u_match:
                    cols = [c.strip().lower() for c in u_match.group(1).split(",")]
                    unique_constraints.append(cols)
            elif item_upper.startswith("CHECK"):
                check_constraints.append(item_clean)
            else:
                # Column definition: <col_name> <data_type> [<constraints>...]
                parts = item_clean.split(None, 1)
                col_name = parts[0].strip().lower()
                rest = parts[1] if len(parts) > 1 else ""

                # Inline REFERENCES check
                ref_match = re.search(r"REFERENCES\s+(\w+)(?:\s*\(([^)]+)\))?", rest, re.IGNORECASE)
                if ref_match:
                    target_tbl = ref_match.group(1).strip().lower()
                    target_col = ref_match.group(2).strip().lower() if ref_match.group(2) else "id"
                    foreign_keys[col_name] = (target_tbl, target_col)

                # Inline CHECK check
                chk_match = re.search(r"CHECK\s*\((.+)\)", rest, re.IGNORECASE)
                if chk_match:
                    check_constraints.append(chk_match.group(0))

                columns[col_name] = {
                    "raw": item_clean,
                    "type": rest.split()[0].upper() if rest else "",
                    "not_null": "NOT NULL" in rest.upper(),
                    "has_default": "DEFAULT" in rest.upper(),
                }

        tables[table_name] = {
            "columns": columns,
            "foreign_keys": foreign_keys,
            "unique_constraints": unique_constraints,
            "check_constraints": check_constraints,
            "raw": body,
        }

    return tables


def migrated_columns(sql: str) -> dict[str, set[str]]:
    """Collect CREATE columns and subsequent ADD/DROP COLUMN upgrades in order."""
    tables = {name: set(meta["columns"]) for name, meta in parse_create_tables(sql).items()}
    for table, changes in re.findall(r"ALTER\s+TABLE\s+(\w+)\s+([^;]+);", strip_sql_comments(sql), re.I):
        for change in split_sql_tokens(changes):
            match = re.match(r"(ADD|DROP)\s+COLUMN\s+(?:IF\s+(?:NOT\s+)?EXISTS\s+)?(\w+)", change, re.I)
            if match:
                action, column = match.groups()
                if action.upper() == "ADD":
                    tables[table.lower()].add(column.lower())
                else:
                    tables[table.lower()].discard(column.lower())
    return tables


def repository_column_errors(source: str, columns: dict[str, set[str]]) -> list[str]:
    """Check the repository's single-table SQL, including filters and upsert columns.

    Unsupported SQL identifiers fail closed so new query syntax needs explicit coverage.
    This checks schema drift only, not PostgreSQL types, constraints or transactions.
    """
    keywords = set("""select from where insert into values update set delete on conflict
        do excluded returning as and or not null is exists order by asc desc limit
        for count sum coalesce now true false distinct in between case when then else end""".split())
    errors = []
    statements = re.findall(r"`([^`]+)`", source, re.S)
    for sql in statements:
        if not re.match(r"\s*(SELECT|INSERT|UPDATE|DELETE)\b", sql, re.I):
            continue
        sql = re.sub(r"'([^']|'')*'", "", sql.lower())
        sql = re.sub(r"::\s*(?:uuid|timestamptz|varchar|numeric|text|jsonb)\b", "", sql)
        table_names = set(re.findall(r"(?:from|into|update)\s+(\w+)", sql)) - {"set"}
        if len(table_names) != 1:
            errors.append(f"Add structural coverage for query: {sql}")
            continue
        table = table_names.pop()
        if table not in columns:
            errors.append(f"Unknown repository table: {table}")
            continue
        identifiers = set(re.findall(r"\b[a-z_][a-z_0-9]*\b", sql))
        for column in sorted(identifiers - keywords - {table} - columns[table]):
            errors.append(f"{table}.{column} is referenced by repository SQL but absent from migrations")
    return errors


def parse_create_indexes(sql: str) -> list[dict]:
    """Parses CREATE INDEX statements."""
    indexes = []
    clean_sql = strip_sql_comments(sql)
    pattern = re.compile(
        r"CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s+ON\s+(\w+)\s*\(([^)]+)\)",
        re.IGNORECASE,
    )
    for match in pattern.finditer(clean_sql):
        index_name = match.group(1).lower()
        table_name = match.group(2).lower()
        cols = [c.strip().lower() for c in match.group(3).split(",")]
        indexes.append({
            "name": index_name,
            "table": table_name,
            "columns": cols,
        })
    return indexes


def parse_drop_tables(sql: str) -> list[str]:
    """Parses DROP TABLE statements in order."""
    drops = []
    clean_sql = strip_sql_comments(sql)
    pattern = re.compile(r"DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?(\w+)", re.IGNORECASE)
    for match in pattern.finditer(clean_sql):
        drops.append(match.group(1).lower())
    return drops


def parse_extensions(sql: str) -> list[str]:
    """Parses CREATE EXTENSION statements."""
    extensions = []
    clean_sql = strip_sql_comments(sql)
    pattern = re.compile(r"CREATE\s+EXTENSION\s+(?:IF\s+NOT\s+EXISTS\s+)?[\"']?([a-zA-Z0-9_\-]+)[\"']?", re.IGNORECASE)
    for match in pattern.finditer(clean_sql):
        extensions.append(match.group(1).lower())
    return extensions


def parse_drop_extensions(sql: str) -> list[str]:
    """Parses DROP EXTENSION statements."""
    extensions = []
    clean_sql = strip_sql_comments(sql)
    pattern = re.compile(r"DROP\s+EXTENSION\s+(?:IF\s+EXISTS\s+)?[\"']?([a-zA-Z0-9_\-]+)[\"']?", re.IGNORECASE)
    for match in pattern.finditer(clean_sql):
        extensions.append(match.group(1).lower())
    return extensions


class TestMigrationsStructural(unittest.TestCase):
    """Hermetic structural tests for PostgreSQL migrations."""

    @classmethod
    def setUpClass(cls):
        cls.up_files = sorted(MIGRATIONS_DIR.glob("*.up.sql"))
        cls.down_files = sorted(MIGRATIONS_DIR.glob("*.down.sql"), reverse=True)
        cls.all_up_sql = "\n".join(f.read_text(encoding="utf-8") for f in cls.up_files)
        cls.all_down_sql = "\n".join(f.read_text(encoding="utf-8") for f in cls.down_files)
        cls.tables = parse_create_tables(cls.all_up_sql)
        cls.indexes = parse_create_indexes(cls.all_up_sql)
        cls.drops = parse_drop_tables(cls.all_down_sql)

    def test_repository_columns_exist_in_migrated_schema(self):
        # CRUD SQL lives here. Transactional job policy queries and migration-runner
        # bookkeeping need separate checks; they are outside this single-table check.
        sources = [REPO_ROOT / "backend/internal/db/postgres.go"]
        errors = []
        for path in sources:
            errors.extend(repository_column_errors(path.read_text(), migrated_columns(self.all_up_sql)))
        self.assertEqual(errors, [], "\n".join(errors))

    def test_repository_check_detects_missing_upgrade_and_filter_columns(self):
        initial = self.up_files[0].read_text()
        source = (REPO_ROOT / "backend/internal/db/postgres.go").read_text()
        errors = repository_column_errors(source, migrated_columns(initial))
        for column in ("character_actions", "camera_angle", "background"):
            self.assertTrue(any(f"panels.{column} " in error for error in errors), errors)
        self.assertEqual(
            repository_column_errors("`SELECT id FROM panels WHERE missing_column = $1`", migrated_columns(initial)),
            ["panels.missing_column is referenced by repository SQL but absent from migrations"],
        )

    def test_repository_check_distinguishes_parameter_casts_from_columns(self):
        columns = migrated_columns(self.all_up_sql)
        self.assertEqual(
            repository_column_errors(
                "`SELECT id FROM users WHERE id=$1::uuid AND created_at>=$2::timestamptz`",
                columns,
            ),
            [],
        )
        self.assertEqual(
            repository_column_errors("`UPDATE pages SET lettering=$3::jsonb WHERE id=$1::uuid AND lettering IS NOT DISTINCT FROM $2::jsonb`", columns),
            [],
        )
        self.assertEqual(
            repository_column_errors("`SELECT uuid FROM users`", columns),
            ["users.uuid is referenced by repository SQL but absent from migrations"],
        )

    def test_repository_check_validates_every_job_lookup_column(self):
        source = (REPO_ROOT / "backend/internal/db/postgres.go").read_text()
        columns = migrated_columns(self.all_up_sql)
        # Exercise the actual lookup SQL, including the cast/COALESCE around
        # entity_id. A missing job column must never escape structural checks.
        for column in columns["generation_jobs"]:
            with self.subTest(column=column):
                missing = {table: set(names) for table, names in columns.items()}
                missing["generation_jobs"].remove(column)
                errors = repository_column_errors(source, missing)
                self.assertTrue(
                    any(f"generation_jobs.{column} " in error for error in errors),
                    errors,
                )

    def test_panel_scene_upgrade_reverses_columns(self):
        upgraded = migrated_columns(self.all_up_sql)
        for column in ("character_actions", "camera_angle", "background"):
            self.assertIn(column, upgraded["panels"])
        rolled_back = migrated_columns(self.all_up_sql + "\n" + self.all_down_sql)
        for column in ("character_actions", "camera_angle", "background"):
            self.assertNotIn(column, rolled_back["panels"])

    def test_migration_files_pairing_and_ordering(self):
        """Assert up/down files are strictly paired, numbered from 1, and sequential."""
        self.assertTrue(len(self.up_files) > 0, "No .up.sql migrations found")
        self.assertEqual(
            len(self.up_files),
            len(self.down_files),
            "Every .up.sql migration must have a matching .down.sql migration",
        )

        for up_file in self.up_files:
            version_prefix = up_file.stem.split("_")[0]
            base_name = up_file.name.replace(".up.sql", "")
            matching_down = MIGRATIONS_DIR / f"{base_name}.down.sql"
            self.assertTrue(
                matching_down.exists(),
                f"Missing matching down migration for {up_file.name}",
            )

        # Assert sequential numbering starting from 000001
        for i, up_file in enumerate(self.up_files, start=1):
            expected_prefix = f"{i:06d}"
            actual_prefix = up_file.name.split("_")[0]
            self.assertEqual(
                actual_prefix,
                expected_prefix,
                f"Migration {up_file.name} must have prefix {expected_prefix}",
            )

    def test_down_fully_reverses_up(self):
        """Assert that down migration drops every table and extension created in up migration."""
        created_tables = list(self.tables.keys())
        self.assertTrue(len(created_tables) > 0)

        # Every table created must be in the drop list
        for tbl in created_tables:
            self.assertIn(
                tbl,
                self.drops,
                f"Table '{tbl}' created in up.sql is not dropped in down.sql",
            )

        # Check reverse dependency order:
        # If Table A references Table B, Table A must be dropped BEFORE Table B in down.sql
        for table_name, meta in self.tables.items():
            for col, (target_tbl, _) in meta["foreign_keys"].items():
                if target_tbl in self.drops and table_name in self.drops and target_tbl != table_name:
                    idx_dependent = self.drops.index(table_name)
                    idx_referenced = self.drops.index(target_tbl)
                    self.assertLess(
                        idx_dependent,
                        idx_referenced,
                        f"Drop order violation: table '{table_name}' references '{target_tbl}', so '{table_name}' must be dropped before '{target_tbl}' in down.sql",
                    )

        # Assert extension reversed
        created_exts = parse_extensions(self.all_up_sql)
        dropped_exts = parse_drop_extensions(self.all_down_sql)
        for ext in created_exts:
            self.assertIn(
                ext,
                dropped_exts,
                f"Extension '{ext}' created in up.sql is not dropped in down.sql",
            )

    def test_table_presence_against_docs(self):
        """Assert all 13 core tables defined in docs/SPEC.md Section 9 are created."""
        expected_tables = {
            "plans",
            "users",
            "projects",
            "chapters",
            "characters",
            "panels",
            "pages",
            "colour_palettes",
            "colour_regions",
            "stage_approvals",
            "generation_jobs",
            "credit_ledger",
            "exports",
        }
        for table in expected_tables:
            self.assertIn(table, self.tables, f"Missing table '{table}' from SPEC.md Section 9")

    def test_column_presence_against_docs(self):
        """Assert every column documented in SPEC.md Section 9 exists in the respective table."""
        spec_columns = {
            "users": [
                "id",
                "email",
                "password_hash",
                "date_of_birth",
                "plan_id",
                "created_at",
                "updated_at",
            ],
            "plans": [
                "id",
                "name",
                "entitlements",
                "created_at",
            ],
            "projects": [
                "id",
                "user_id",
                "title",
                "original_script",
                "chapter_count",
                "art_style",
                "status",
                "created_at",
                "updated_at",
            ],
            "chapters": [
                "id",
                "project_id",
                "chapter_number",
                "title",
                "summary",
                "status",
                "updated_at",
            ],
            "characters": [
                "id",
                "project_id",
                "name",
                "description",
                "reference_image_url",
                "reference_image_job_id",
                "visual_attributes",
                "approved_at",
            ],
            "panels": [
                "id",
                "chapter_id",
                "panel_number",
                "scene_description",
                "dialogue",
                "narration",
                "image_prompt",
                "status",
            ],
            "pages": [
                "id",
                "chapter_id",
                "page_number",
                "panel_ids",
                "draft_image_url",
                "draft_image_job_id",
                "draft_approved_at",
                "line_art_image_url",
                "line_art_image_job_id",
                "line_art_approved_at",
            ],
            "colour_palettes": [
                "id",
                "page_id",
                "suggestion_job_id",
                "palette",
                "user_modified_at",
            ],
            "colour_regions": [
                "id",
                "page_id",
                "region_label",
                "fill_colour_hex",
                "fill_applied_at",
                "fill_applied_by",
            ],
            "stage_approvals": [
                "id",
                "project_id",
                "entity_type",
                "entity_id",
                "approved_by_user_id",
                "approved_at",
                "credit_estimate_at_approval",
            ],
            "generation_jobs": [
                "id",
                "project_id",
                "user_id",
                "job_type",
                "stage",
                "model_id",
                "status",
                "idempotency_key",
                "estimated_cost",
                "actual_cost",
                "hashed_user_id",
                "error_message",
                "created_at",
                "completed_at",
            ],
            "credit_ledger": [
                "id",
                "user_id",
                "project_id",
                "generation_job_id",
                "transaction_type",
                "amount",
                "balance_after",
                "created_at",
            ],
            "exports": [
                "id",
                "project_id",
                "file_url",
                "format",
                "status",
                "provenance_metadata_preserved",
                "created_at",
            ],
        }

        for table, cols in spec_columns.items():
            self.assertIn(table, self.tables, f"Table '{table}' missing")
            table_cols = self.tables[table]["columns"]
            for col in cols:
                self.assertIn(
                    col,
                    table_cols,
                    f"Column '{col}' missing from table '{table}' (specified in SPEC.md Section 9)",
                )

    def test_schema_against_contracts_json_schemas(self):
        """Assert generation_jobs and credit_ledger match contracts/schemas/*.json exactly."""
        # 1. job.schema.json vs generation_jobs
        job_schema_path = CONTRACTS_DIR / "schemas" / "job.schema.json"
        self.assertTrue(job_schema_path.exists(), f"Missing schema at {job_schema_path}")
        with open(job_schema_path, "r", encoding="utf-8") as f:
            job_schema = json.load(f)

        job_cols = self.tables["generation_jobs"]["columns"]
        for prop_name in job_schema["properties"]:
            self.assertIn(
                prop_name,
                job_cols,
                f"Property '{prop_name}' from job.schema.json missing in generation_jobs table",
            )

        # Assert job_type enum matches schema enum
        job_type_enum = job_schema["properties"]["job_type"]["enum"]
        jobs_check_constraints = " ".join(self.tables["generation_jobs"]["check_constraints"])
        for jt in job_type_enum:
            self.assertIn(
                f"'{jt}'",
                jobs_check_constraints,
                f"Enum value '{jt}' for job_type missing from generation_jobs check constraint",
            )

        # Assert stage enum matches schema enum
        stage_enum = job_schema["properties"]["stage"]["enum"]
        for stg in stage_enum:
            self.assertIn(
                f"'{stg}'",
                jobs_check_constraints,
                f"Enum value '{stg}' for stage missing from generation_jobs check constraint",
            )

        # Assert status enum matches schema enum
        status_enum = job_schema["properties"]["status"]["enum"]
        for st in status_enum:
            self.assertIn(
                f"'{st}'",
                jobs_check_constraints,
                f"Enum value '{st}' for status missing from generation_jobs check constraint",
            )

        # 2. credit.schema.json vs credit_ledger
        credit_schema_path = CONTRACTS_DIR / "schemas" / "credit.schema.json"
        self.assertTrue(credit_schema_path.exists(), f"Missing schema at {credit_schema_path}")
        with open(credit_schema_path, "r", encoding="utf-8") as f:
            credit_schema = json.load(f)

        credit_cols = self.tables["credit_ledger"]["columns"]
        for prop_name in credit_schema["properties"]:
            self.assertIn(
                prop_name,
                credit_cols,
                f"Property '{prop_name}' from credit.schema.json missing in credit_ledger table",
            )

        tx_type_enum = credit_schema["properties"]["transaction_type"]["enum"]
        credit_check_constraints = " ".join(self.tables["credit_ledger"]["check_constraints"])
        for tt in tx_type_enum:
            self.assertIn(
                f"'{tt}'",
                credit_check_constraints,
                f"Enum value '{tt}' for transaction_type missing from credit_ledger check constraint",
            )

    def test_credit_ledger_nullable_job_id(self):
        """Assert generation_job_id and project_id are nullable in credit_ledger for adjustments and signup credits."""
        credit_cols = self.tables["credit_ledger"]["columns"]
        self.assertFalse(
            credit_cols["generation_job_id"]["not_null"],
            "credit_ledger.generation_job_id must be nullable for signup credits and adjustments",
        )
        self.assertFalse(
            credit_cols["project_id"]["not_null"],
            "credit_ledger.project_id must be nullable for signup credits and adjustments",
        )

    def test_foreign_key_relationships(self):
        """Assert essential foreign key relationships defined across all tables."""
        expected_fks = [
            ("users", "plan_id", "plans", "id"),
            ("projects", "user_id", "users", "id"),
            ("chapters", "project_id", "projects", "id"),
            ("characters", "project_id", "projects", "id"),
            ("characters", "reference_image_job_id", "generation_jobs", "id"),
            ("panels", "chapter_id", "chapters", "id"),
            ("pages", "chapter_id", "chapters", "id"),
            ("pages", "draft_image_job_id", "generation_jobs", "id"),
            ("pages", "line_art_image_job_id", "generation_jobs", "id"),
            ("colour_palettes", "page_id", "pages", "id"),
            ("colour_palettes", "suggestion_job_id", "generation_jobs", "id"),
            ("colour_regions", "page_id", "pages", "id"),
            ("stage_approvals", "project_id", "projects", "id"),
            ("stage_approvals", "approved_by_user_id", "users", "id"),
            ("generation_jobs", "project_id", "projects", "id"),
            ("generation_jobs", "user_id", "users", "id"),
            ("credit_ledger", "user_id", "users", "id"),
            ("credit_ledger", "project_id", "projects", "id"),
            ("credit_ledger", "generation_job_id", "generation_jobs", "id"),
            ("exports", "project_id", "projects", "id"),
        ]

        for src_tbl, src_col, tgt_tbl, tgt_col in expected_fks:
            fks = self.tables[src_tbl]["foreign_keys"]
            self.assertIn(
                src_col,
                fks,
                f"Table '{src_tbl}' column '{src_col}' must be a foreign key",
            )
            self.assertEqual(
                fks[src_col],
                (tgt_tbl, tgt_col),
                f"Table '{src_tbl}' column '{src_col}' must reference '{tgt_tbl}({tgt_col})'",
            )

    def test_stale_approval_rule_semantics(self):
        """Assert approval timestamps, updated_at timestamps, and completed_at exist for stale approval rules."""
        # Entities that track approval timestamps
        self.assertIn("approved_at", self.tables["characters"]["columns"])
        self.assertIn("draft_approved_at", self.tables["pages"]["columns"])
        self.assertIn("line_art_approved_at", self.tables["pages"]["columns"])
        self.assertIn("approved_at", self.tables["stage_approvals"]["columns"])

        # Entities that can be edited must have updated_at for freshness comparison
        for table in ["characters", "chapters", "panels", "pages", "projects", "users"]:
            self.assertIn(
                "updated_at",
                self.tables[table]["columns"],
                f"Table '{table}' requires updated_at to track edits for stale-approval detection",
            )

        # generation_jobs must have completed_at to compare against approval timestamps
        self.assertIn(
            "completed_at",
            self.tables["generation_jobs"]["columns"],
            "generation_jobs requires completed_at to track when regeneration finished",
        )

        # stage_approvals entity_type enum values
        stage_approvals_checks = " ".join(self.tables["stage_approvals"]["check_constraints"])
        for et in ["chapter_outline", "character_reference", "draft_page", "line_art_page", "colour_page"]:
            self.assertIn(
                f"'{et}'",
                stage_approvals_checks,
                f"entity_type '{et}' must be valid in stage_approvals check constraint",
            )

    def test_provenance_fields_and_invariants(self):
        """Assert provenance metadata preserved column exists and completion constraint is enforced."""
        exports_cols = self.tables["exports"]["columns"]
        self.assertIn(
            "provenance_metadata_preserved",
            exports_cols,
            "exports table must contain provenance_metadata_preserved",
        )
        # Check constraint enforcing invariant
        exports_checks = " ".join(self.tables["exports"]["check_constraints"])
        self.assertIn(
            "provenance_metadata_preserved",
            exports_checks,
            "exports table must enforce that provenance_metadata_preserved is TRUE before completion",
        )

    def test_owner_decisions_geo_and_age_gate(self):
        """Assert Owner decisions (2026-11-02): no geo-blocking columns and 18+ gate deferred."""
        users_cols = self.tables["users"]["columns"]
        # Owner decision (2): NO geo-block and NO region restriction anywhere (users are global)
        self.assertNotIn(
            "country_code",
            users_cols,
            "users table must not have country_code column per Owner decision 2026-11-02",
        )
        # Owner decision (3): 18+ gate is deferred: no enforced age logic
        self.assertIn("date_of_birth", users_cols)
        self.assertFalse(
            users_cols["date_of_birth"]["not_null"],
            "users.date_of_birth must be nullable per Owner decision 2026-11-02",
        )
        users_checks = " ".join(self.tables["users"]["check_constraints"])
        self.assertNotIn(
            "18",
            users_checks,
            "users table must not enforce age 18 check per Owner decision 2026-11-02",
        )

    def test_indexes_presence(self):
        """Assert indexes exist on primary foreign keys and lookup columns."""
        indexed_tables = {idx["table"] for idx in self.indexes}
        for table in ["users", "projects", "chapters", "characters", "panels", "pages", "stage_approvals", "generation_jobs", "credit_ledger", "exports"]:
            self.assertIn(table, indexed_tables, f"Table '{table}' must have indexes")

        # Specific index assertions
        index_names = {idx["name"] for idx in self.indexes}
        self.assertIn("idx_generation_jobs_idempotency", index_names)
        self.assertIn("idx_generation_jobs_status", index_names)
        self.assertIn("idx_stage_approvals_entity", index_names)
        self.assertIn("idx_credit_ledger_job_id", index_names)

    def test_migration_runner_artifacts_and_seeds(self):
        """Assert migration runner scripts, binaries, and seeds exist with proper permissions."""
        runner_sh = DEPLOY_DIR / "migrate.sh"
        self.assertTrue(runner_sh.exists(), "deploy/migrate.sh must exist")
        import os
        self.assertTrue(
            os.access(runner_sh, os.X_OK),
            "deploy/migrate.sh must be executable",
        )

        runner_go = REPO_ROOT / "backend" / "cmd" / "migrate" / "main.go"
        self.assertTrue(runner_go.exists(), "backend/cmd/migrate/main.go must exist")

        runner_pkg = REPO_ROOT / "backend" / "internal" / "db" / "migrate.go"
        self.assertTrue(runner_pkg.exists(), "backend/internal/db/migrate.go must exist")

        seeds_file = DEPLOY_DIR / "seeds.sql"
        self.assertTrue(seeds_file.exists(), "deploy/seeds.sql must exist")
        seeds_content = seeds_file.read_text(encoding="utf-8")
        self.assertIn("INSERT INTO plans", seeds_content)
        self.assertIn("free", seeds_content)
        self.assertIn("pro", seeds_content)
        self.assertIn("enterprise", seeds_content)


if __name__ == "__main__":
    unittest.main()
