#!/usr/bin/env bash
set -euo pipefail

# Migration runner script for Icomic.
# Applies migrations idempotently in sequential order.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

DATABASE_URL="${DATABASE_URL:-}"
if [ -z "${DATABASE_URL}" ]; then
  echo "Error: DATABASE_URL environment variable is required" >&2
  exit 1
fi

MIGRATIONS_DIR="${MIGRATIONS_DIR:-${SCRIPT_DIR}/migrations}"

# Prefer running Go migration runner if go is installed
if command -v go >/dev/null 2>&1 && [ -f "${REPO_ROOT}/backend/cmd/migrate/main.go" ]; then
  ( cd "${REPO_ROOT}/backend" && go run ./cmd/migrate -dir "${MIGRATIONS_DIR}" -database-url "${DATABASE_URL}" "$@" )
  exit 0
fi

# Fallback: idempotent execution via psql
echo "Applying migrations via psql from ${MIGRATIONS_DIR}..."
psql "${DATABASE_URL}" -c "CREATE TABLE IF NOT EXISTS schema_migrations (version VARCHAR(255) PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW());"

for f in $(ls "${MIGRATIONS_DIR}"/*.up.sql | sort -V); do
  ver=$(basename "$f" .up.sql)
  applied=$(psql "${DATABASE_URL}" -tAc "SELECT 1 FROM schema_migrations WHERE version = '${ver}';")
  if [ "${applied}" != "1" ]; then
    echo "Applying migration ${ver}..."
    psql "${DATABASE_URL}" -v ON_ERROR_STOP=1 -f "$f"
    psql "${DATABASE_URL}" -c "INSERT INTO schema_migrations (version) VALUES ('${ver}');"
  else
    echo "Migration ${ver} already applied; skipping."
  fi
done

if [[ "${*:-}" =~ "--seed" ]] || [ "${APPLY_SEEDS:-false}" = "true" ]; then
  SEEDS_FILE="${SCRIPT_DIR}/seeds.sql"
  if [ -f "${SEEDS_FILE}" ]; then
    echo "Applying seeds from ${SEEDS_FILE}..."
    psql "${DATABASE_URL}" -v ON_ERROR_STOP=1 -f "${SEEDS_FILE}"
  fi
fi

echo "All migrations applied successfully."
