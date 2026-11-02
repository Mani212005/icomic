# Icomic Deployment and Infrastructure

This directory contains container definitions, database migrations, seeds, and validation tooling for Icomic.

## Directory Contents

- `docker-compose.yml`: Local development stack running PostgreSQL, Redis, Go backend, Python AI service, and Svelte frontend.
- `docker-compose.prod.yml`: Production deployment overrides.
- `migrations/`: Versioned PostgreSQL migrations (`.up.sql` and `.down.sql`).
  - `000001_initial_schema.up.sql`: Initial schema defining all 13 core tables, indexes, constraints, and baseline seeds.
  - `000001_initial_schema.down.sql`: Fully reverses the initial schema migration in reverse dependency order.
- `seeds.sql`: Database seed data for plans and entitlements.
- `validate_migrations_test.py`: Hermetic structural test suite verifying migration pairing, reverse order, table/column presence against docs and JSON schemas, and foreign keys without requiring a live database.

## Database Schema & Migrations

The schema strictly implements the entities and constraints specified in `docs/SPEC.md` Section 9, `docs/ARCHITECTURE.md` Section 5 & 7, and `contracts/schemas/`:

- **Core entities**: `plans`, `users`, `projects`, `chapters`, `characters`, `panels`, `pages`, `colour_palettes`, `colour_regions`, `stage_approvals`, `generation_jobs`, `credit_ledger`, `exports`.
- **Stale-approval rule semantics**: Entities support freshness checks comparing `approved_at` timestamps against `updated_at` (for edits) and `generation_jobs.completed_at` (for regenerations).
- **Provenance preservation**: The `exports.provenance_metadata_preserved` column and check constraint ensure C2PA/watermark metadata is preserved prior to export completion.
- **Open captain decisions**: Verification fields `users.date_of_birth` (18+ age gate) and `users.country_code` (India geo-blocking) are defined as nullable and documented pending final captain mechanisms.

## Validation

Run the hermetic structural migration validation (requires only standard Python 3, zero database or network dependencies):

```bash
make test-migrations
# or directly:
python3 deploy/validate_migrations_test.py
```

## Running the Dev Stack

To start the full development environment with Docker Compose:

```bash
docker compose -f deploy/docker-compose.yml up --build
```

The stack exposes:

- Frontend: `http://localhost:5173`
- Go Backend API: `http://localhost:8080`
- Python AI Service: `http://localhost:8000`
- PostgreSQL: `localhost:5432`
- Redis: `localhost:6379`

## Managing Migrations with Docker Compose

When Docker Compose is running, migrations can be applied or reverted via make targets:

```bash
# Apply up migrations
make migrate-up

# Revert down migrations
make migrate-down

# Apply seed data
make migrate-seed
```
