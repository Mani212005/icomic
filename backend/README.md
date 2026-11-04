# Icomic Backend API

Go API service for Icomic (docs/ARCHITECTURE.md section 7).

## Features and Architecture

- **PostgreSQL Migrations and Schema**: Migration scripts located in `deploy/migrations/` creating all 13 core tables (`plans`, `users`, `projects`, `chapters`, `generation_jobs`, `characters`, `panels`, `pages`, `colour_palettes`, `colour_regions`, `stage_approvals`, `credit_ledger`, `exports`).
- **Persistence Behind Repository Interfaces**: Persistence is abstracted behind `db.Repository`. An in-memory fake (`db.InMemoryRepository`) is used for hermetic unit tests with zero external database dependencies. A production PostgreSQL implementation (`db.PostgresRepository`) is selected when `DATABASE_URL` is configured.
- **Credit Ledger and Cost Management**:
  - Pricing basis: $0.01 per image attempt (`muse-image-1.0`) and estimated token cost for colour suggestion.
  - Stage cost estimates: `POST /api/v1/projects/:id/estimate`.
  - Transactional debits: Credit ledger entry and generation job record written atomically.
  - Automatic refunds on eligible failed jobs or user cancellations.
  - Quota enforcement (daily and monthly) and global operator kill switch.
- **Job Model & Staged Approval Gates**:
  - Staged gates strictly enforced: Character reference approved -> Chapter outline approved -> Draft sketch approved -> Final line art approved -> Colouring.
  - Stale approval rule: Any edit or regeneration automatically makes downstream approvals stale until explicitly re-approved.
  - Concurrency guard preventing duplicate in-flight jobs.
  - Hashed user IDs (SHA-256) attached to external model calls per Meta API terms.
- **Authentication & Security**:
  - Pluggable `auth.Authenticator` architecture with `BearerTokenAuthenticator` (bearer token authentication middleware with strict ownership verification) and `DevAuthenticator` (development mode header-based authentication, enabled only when `DEV_AUTH_ENABLED=true`).
  - Strict resource ownership checks across all project, chapter, character, panel, page, and export endpoints.
- **Pluggable Policy Seams for Captain Decisions**:
  - Image Hosting (`auth.ImageHostingSeam`): Pluggable media resolver with `/media` serving or proxying route (`DefaultImageHosting`). Documented `TODO(captain)`.
- **CORS Middleware**:
  - Configurable allowed origin (`CORS_ALLOWED_ORIGIN`, defaulting to Svelte development server `http://localhost:5173`), handling preflight `OPTIONS` and credential headers.
- **Prompt Pre-Moderation**:
  - All user-derived prompts (scripts, character descriptions, panel text) pre-screened via `ModeratePrompt` before model dispatch.
  - Optional moderation endpoint: `POST /api/v1/prompts/moderate`.
- **AI Service Integration via Typed Client**:
  - `gen.AIService` interface conforming to all 10 endpoints of `contracts/openapi/ai-service.yaml`.
  - `gen.AIServiceClient` connects to the internal Python FastAPI service via `PYTHON_AI_URL`.
  - `gen.FakeAIServiceClient` provides deterministic, hermetic fake responses for unit testing with no network access and no API key.

## Running Locally

To run the Go API server with in-memory persistence and fake adapters:

```bash
cd backend
go run cmd/server/main.go
```

To run with PostgreSQL:

```bash
DATABASE_URL="postgres://icomic:icomic@localhost:5432/icomic?sslmode=disable" go run cmd/server/main.go
```

The server listens on `http://localhost:8080`.

## Testing

All tests are hermetic and run without requiring running instances of PostgreSQL or Redis:

```bash
cd backend
go test -v -race ./...
```

## Durable jobs and credits

`DATABASE_URL` selects PostgreSQL for the repository, job service and credit
service together. Without it, all three remain in-memory fakes. Apply all pending
migrations with `cmd/migrate` before starting a PostgreSQL-backed server, including
`000004_generation_persistence`, which adds entity identity and durable runtime
policy. No schema changes happen at server startup.

`jobs.GenerationStore.CreateGeneration` is the commit boundary. It locks the
policy row, user and project, reads quotas and `stage_approvals`, rejects an active
job for the same entity and stage, then inserts the job and debit in one
transaction. The unique idempotency key returns the existing job without another
debit. Callers must honor the returned `existed` flag and dispatch only after the
method returns successfully. HTTP generation remains synchronous; durable queue
dispatch is a separate integration and `PersistentJobService.DispatchAsync`
returns an explicit configuration error.

Quota counters are committed image-attempt rows in `image_quota_usage`.
Insertion increments both rolling counts atomically with the debit, using the
existing 24-hour and 30-day windows. Failed and refunded attempts still consume
quota. Quota rows and ledger history survive project/job deletion, so deletion
cannot restore spent credits or reset usage. `generation_policy` contains the operator-controlled kill switch, spend
threshold and daily/monthly limits; zero limits disable that limit. An operator
can update this singleton row in PostgreSQL without restarting the server. The
spend threshold latches the kill switch in the transaction that reaches it.

Balances are computed from the durable ledger on the backend while holding a
stable user-row lock. Every newly-created user receives the existing initial
credit adjustment. Refunds use their own later transaction, validate against the
original debit and are idempotent per job. A standalone credit-service debit
returns the debit already committed with generation creation.

`db.TransactionStarter`, `db.Transaction` and `db.Row` form the hermetic failure
injection seam. `NewPostgresRepositoryWithTransactions` accepts a fake that stages
writes until commit and discards them on rollback. The tests inject failures at
each creation boundary and at refund writes, including commit failures.
`NewPostgresRepositoryWithDB` accepts a caller-owned `*sql.DB` for isolated
PostgreSQL integration coverage without changing the production service wiring.
The default suites require no running database.

PostgreSQL statement parameters use explicit SQL casts, including nullable inputs
and repeated placeholders. The hermetic SQL parameter guard enforces this for
the generation and credit transaction statements to prevent PostgreSQL inference
errors from leaving jobs pending.

For an isolated fake Compose stack on a different localhost port, set
`ICOMIC_E2E_API_URL` when running `make e2e-comic` with `--skip-services` and
`--artifact-dir` pointing to the task's evidence directory.
