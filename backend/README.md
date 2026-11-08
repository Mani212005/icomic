# Icomic Backend API

Go API service for Icomic (docs/ARCHITECTURE.md section 7).

## Features and Architecture

- **PostgreSQL Migrations and Schema**: Migration scripts located in `deploy/migrations/` creating all 13 core tables (`plans`, `users`, `projects`, `chapters`, `generation_jobs`, `characters`, `panels`, `pages`, `colour_palettes`, `colour_regions`, `stage_approvals`, `credit_ledger`, `exports`).
- **Persistence Behind Repository Interfaces**: Persistence is abstracted behind `db.Repository`. An in-memory fake (`db.InMemoryRepository`) is used for hermetic unit tests with zero external database dependencies. A production PostgreSQL implementation (`db.PostgresRepository`) is selected when `DATABASE_URL` is configured.
- **Credit Ledger and Cost Management**:
  - One backend pricing configuration supplies image/palette estimate rates, model IDs, generation debits and `POST /api/v1/projects/:id/estimate` quotes.
  - Defaults retain the documented fake/development basis: $0.01 per image attempt and $0.005 per palette suggestion. They are labeled unverified until `PRICING_VERIFIED=true`; this is not an approved retail price or provider quote.
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
  - Accounts (`internal/accounts`, `docs/ACCOUNTS.md`): guest sessions, email and password sign-up and sign-in with argon2id, server-side revocable sessions in an `HttpOnly` `SameSite=Lax` cookie, per-session CSRF token, sign-in rate limiting, and a project limit (guest 1, registered up to 100) enforced inside the creating transaction. The dev identity is registered and unlimited.
  - Strict resource ownership checks across all project, chapter, character, panel, page, and export endpoints.
  - Owner-authorized media: `GET /media/{key}` needs a short-lived scoped link (minted into every JSON response) or an authenticated owner, checks key ownership, and reads storage through a bounded, redirect-free client (`MEDIA_SIGNING_SECRET`, `MEDIA_PUBLIC_BASE_URL`, `MEDIA_DIR`; see `docs/ARCHITECTURE.md` section 2).
- **Pluggable Policy Seams for Captain Decisions**:
  - Image Hosting (`auth.ImageHostingSeam`): Pluggable media resolver behind the owner-authorized `/media` route (`DefaultImageHosting`). Documented `TODO(captain)`.
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

Pricing can be configured without a provider call using `META_IMAGE_MODEL`,
`META_VISION_MODEL`, `META_IMAGE_PRICE_USD`, `META_COLOUR_ESTIMATE_USD`, and
`PRICING_VERSION`. `PRICING_VERIFIED` controls the quote's verification label
and defaults to `false`. Amounts must be finite, positive USD values; an
invalid configured amount prevents startup. If a model or price is overridden
without `PRICING_VERSION`, the backend derives a version from the effective
configuration. These values are estimates and do not determine a user-facing
selling price.

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

## Public request limits

All authenticated API routes validate their JSON before repository writes, jobs,
credit debits or AI calls. Requests must be a single UTF-8 JSON object with known
fields and valid field types. Duplicate fields, trailing JSON, null values,
invalid resource UUIDs, unsupported enums and negative counts return HTTP 400.
Existing compact (32 hexadecimal character) in-memory job IDs remain accepted.
Chapter count zero on project creation keeps automatic planning; explicit updates
and chapter-generation inputs require a positive count. Art styles remain free
text. No fixed ceiling on planned panels is introduced.

Resource safeguards are configured separately from image quotas and spending:

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `MAX_JSON_BYTES` | 1048576 | Maximum request JSON bytes, including chunked bodies |
| `MAX_SCRIPT_BYTES` | 524288 | Maximum decoded script UTF-8 bytes |
| `MAX_SCRIPT_WORDS` | 100000 | Maximum script words (whitespace separated) |
| `MAX_CHAPTERS` | 1000 | Maximum explicit/saved project chapter count |
| `MAX_TEXT_FIELD_BYTES` | 16384 | Maximum bytes per other text field or nested attribute string |
| `MAX_COLOUR_IMAGE_BYTES` | 10485760 | Maximum PNG/WebP upload bytes |
| `MAX_COLOUR_IMAGE_PIXELS` | 40000000 | Maximum width times height, checked before image storage |
| `REQUESTS_PER_IP_PER_MINUTE` | 600 | Public API requests per socket peer IP |
| `REQUESTS_PER_USER_PER_MINUTE` | 300 | Authenticated requests per user across IPs |
| `REQUEST_ADMISSION_KEYS` | 10000 | Maximum combined IP/user admission entries |
| `HTTP_READ_HEADER_TIMEOUT` | 5s | Deadline for receiving request headers |
| `HTTP_READ_TIMEOUT` | 30s | Deadline for receiving the complete request |
| `HTTP_WRITE_TIMEOUT` | 20m | Response deadline, accommodating the current synchronous AI path |
| `HTTP_IDLE_TIMEOUT` | 60s | Idle keep-alive connection deadline |
| `HTTP_MAX_HEADER_BYTES` | 32768 | Maximum request header bytes (Go may allow its additional read buffer) |

All settings must be positive; invalid settings stop server startup. These defaults
are engineering resource bounds, not approved launch capacity. Operators should
set launch values from capacity and product requirements. Saved scripts and
chapter counts are rechecked before text generation if limits are lowered.
Oversized bodies/images return HTTP 413. Other validation failures return 400
(or 415 for unsupported upload formats). PNG/WebP dimensions are decoded without
allocating full pixel buffers; the AI storage endpoint still verifies image bytes.

Admission uses bounded in-process one-minute windows and returns HTTP 429 with
`Retry-After: 60` when either count or identity capacity is exhausted. Expired
entries are reclaimed on admission; there is no background worker. Unauthenticated
API traffic counts toward the IP limit. Forwarding headers are not trusted: behind
a reverse proxy, the IP limit applies to the proxy connection peer. Multiple Go
replicas each have their own limit; use deployment-level admission for a shared
public limit. Health checks and media delivery are outside API admission.

`CORS_ALLOWED_ORIGIN` accepts an exact origin or a comma-separated origin list
(default `http://localhost:5173`). Matched origins receive credentials permission
and `Vary: Origin`; denied/absent origins receive no allow-origin header. `*`
explicitly allows any origin without credentials. CORS controls browser access;
authentication and request validation also run for non-browser clients.
