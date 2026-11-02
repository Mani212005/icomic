# Icomic Deployment and Infrastructure

This directory contains container definitions, database migrations, seeds, and validation tooling for Icomic.

## Directory Contents

- `docker-compose.yml`: Local development stack running PostgreSQL, Redis, Go backend, Python AI service, and Svelte frontend on fakes with healthchecks and automatic schema migration via the migration runner.
- `docker-compose.prod.yml`: Production deployment overrides.
- `migrations/`: Versioned PostgreSQL migrations (`.up.sql` and `.down.sql`).
  - `000001_initial_schema.up.sql`: Initial schema defining all 13 core tables, indexes, constraints, and baseline seeds.
  - `000001_initial_schema.down.sql`: Fully reverses the initial schema migration in reverse dependency order.
- `seeds.sql`: Database seed data for plans, entitlements, and default development user.
- `migrate.sh`: Idempotent migration runner script applying migrations sequentially with tracking in `schema_migrations`.
- `validate_migrations_test.py`: Hermetic structural test suite verifying migration pairing, reverse order, table/column presence against docs and JSON schemas, and foreign keys without requiring a live database.

## Database Schema & Migrations

The schema strictly implements the entities and constraints specified in `docs/SPEC.md` Section 9, `docs/ARCHITECTURE.md` Section 5 & 7, and `contracts/schemas/`:

- **Core entities**: `plans`, `users`, `projects`, `chapters`, `characters`, `panels`, `pages`, `colour_palettes`, `colour_regions`, `stage_approvals`, `generation_jobs`, `credit_ledger`, `exports`.
- **Automatic Migration on Startup**: In `docker-compose.yml`, a dedicated `migration` container runs `deploy/migrate.sh` against PostgreSQL on startup, creating the `schema_migrations` tracking table, applying all `.up.sql` migrations in sequential order, and loading seed data before the Go backend starts.
- **Stale-approval rule semantics**: Entities support freshness checks comparing `approved_at` timestamps against `updated_at` (for edits) and `generation_jobs.completed_at` (for regenerations).
- **Provenance preservation**: The `exports.provenance_metadata_preserved` column and check constraint ensure C2PA/watermark metadata is preserved prior to export completion.
- **Owner decisions (2026-11-02)**:
  - Users are global: no geo-blocking or region restrictions anywhere, and no country/region columns in the schema.
  - 18+ age gate deferred: no enforced age logic or date-of-birth blocking in the database; `users.date_of_birth` remains nullable with no age check constraints.

## Validation

Run the hermetic structural migration validation (requires only standard Python 3, zero database or network dependencies):

```bash
make test-migrations
# or directly:
python3 deploy/validate_migrations_test.py
```

## Running the Dev Stack

The development stack runs entirely on deterministic fakes without external network access or real API keys.

To start the full development environment with Docker Compose:

```bash
sg docker -c 'docker compose -f deploy/docker-compose.yml up -d --build'
```

### Services and Health Endpoints

All services include container healthchecks and depends_on conditions ensuring an orderly startup:

- PostgreSQL (`icomic-postgres`): `localhost:5433` (Health: `pg_isready -U icomic -d icomic`)
- Migration (`icomic-migration`): Runs `deploy/migrate.sh` and exits with code 0 once migrations and seeds are applied.
- Redis (`icomic-redis`): `localhost:6379` (Health: `redis-cli ping`)
- Python AI Service (`icomic-ai`): `http://localhost:8000` (Health: `GET /healthz`)
  - Configured with `ADAPTER_*=fake` and deterministic ports.
  - Serves generated image and PDF assets via `GET /storage/fake/{key:path}`.
- Go Backend API (`icomic-backend`): `http://localhost:8080` (Health: `GET /healthz`)
  - Connected to PostgreSQL via `DATABASE_URL` and Redis via `REDIS_URL`.
  - Starts only after `migration` completes successfully and dependent services are healthy.
- Svelte Frontend (`icomic-frontend`): `http://localhost:5173` (Health: `GET /`)

To stop the stack and remove volumes:

```bash
sg docker -c 'docker compose -f deploy/docker-compose.yml down -v'
```

## Managing Migrations with Docker Compose

When Docker Compose is running, migrations can also be manually applied or reverted via make targets:

```bash
# Apply up migrations
make migrate-up

# Revert down migrations
make migrate-down

# Apply seed data
make migrate-seed
```
