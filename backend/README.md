# Icomic Backend API

Go API service for Icomic (docs/ARCHITECTURE.md section 7).

## Features and Architecture

- **PostgreSQL Migrations and Schema**: Migration scripts located in `deploy/migrations/` creating all 13 core tables (`plans`, `users`, `projects`, `chapters`, `generation_jobs`, `characters`, `panels`, `pages`, `colour_palettes`, `colour_regions`, `stage_approvals`, `credit_ledger`, `exports`).
- **Persistence Behind Repository Interfaces**: Persistence is abstracted behind `db.Repository`. An in-memory fake (`db.MemRepository`) is used for hermetic unit tests with zero external database dependencies. A production PostgreSQL implementation (`db.PostgresRepository`) is selected when `DATABASE_URL` is configured.
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
  - User registration (`POST /api/v1/auth/signup`) and session login (`POST /api/v1/auth/login`).
  - Bearer token authentication middleware with strict ownership verification. Client-supplied headers (e.g. `X-User-ID`) are never trusted unless dev mode is explicitly enabled.
- **Pluggable Policy Seams for Captain Decisions**:
  - Image Hosting (`auth.ImageHostingSeam`): Configurable media resolver (`/media` default). Documented `TODO(captain)`.
- **AI Service Integration via Port**:
  - `ai.AIServicePort` interface conforming to `contracts/openapi/ai-service.yaml`.
  - `ai.FakeAIServiceClient` provides deterministic, hermetic fake responses for unit testing with no network access and no API key.
  - `ai.NewHTTPClient` connects to the internal Python FastAPI service in production.

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
