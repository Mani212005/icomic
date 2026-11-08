# Icomic - Architecture

## 1. Monorepo Layout

```
icomic/
├── contracts/                  # Shared source of truth for inter-service APIs
│   ├── openapi/
│   │   └── ai-service.yaml     # Go <-> Python OpenAPI spec (versioned)
│   └── schemas/
│       ├── job.schema.json     # Generation job event shape
│       └── credit.schema.json  # Credit ledger event shape
├── frontend/                   # Svelte single-page application
│   ├── src/
│   │   ├── routes/             # SvelteKit pages
│   │   ├── lib/
│   │   │   ├── editor/         # Comic book editor components
│   │   │   ├── colour/         # Colour editor + paint-bucket fill (client-side)
│   │   │   └── api/            # Type-safe API client (generated from OpenAPI)
│   │   └── app.html
│   ├── package.json
│   └── svelte.config.js
├── backend/                    # Go API server
│   ├── cmd/server/             # Entry point
│   ├── internal/
│   │   ├── api/                # HTTP handlers, middleware, routes
│   │   ├── domain/             # Domain types, business rules
│   │   ├── jobs/               # Job creation, queue dispatch
│   │   ├── credits/            # Credit ledger, quota, kill switch
│   │   ├── auth/               # Authentication and sessions
│   │   └── db/                 # PostgreSQL queries, migrations
│   ├── gen/                    # Types generated or validated from contracts/openapi/
│   └── go.mod
├── ai/                         # Python FastAPI AI service
│   ├── app/
│   │   ├── main.py             # FastAPI entry point
│   │   ├── adapters/           # Port implementations
│   │   │   ├── language/
│   │   │   │   ├── port.py     # LanguagePort protocol
│   │   │   │   ├── fake.py     # FakeLanguageAdapter (deterministic, no network)
│   │   │   │   └── meta.py     # MetaLanguageAdapter (real, deferred)
│   │   │   ├── vision/
│   │   │   │   ├── port.py     # VisionPort protocol
│   │   │   │   ├── fake.py     # FakeVisionAdapter
│   │   │   │   └── meta.py     # MetaVisionAdapter (deferred)
│   │   │   ├── image/
│   │   │   │   ├── port.py     # ImagePort protocol
│   │   │   │   ├── fake.py     # FakeImageAdapter (returns test PNG, no network)
│   │   │   │   └── meta.py     # MetaImageAdapter (muse-image-1.0, deferred)
│   │   │   ├── storage/
│   │   │   │   ├── port.py     # StoragePort protocol
│   │   │   │   ├── fake.py     # FakeStorageAdapter (in-memory or tmp dir)
│   │   │   │   └── s3.py       # S3Adapter (real)
│   │   │   ├── queue/
│   │   │   │   ├── port.py     # QueuePort protocol
│   │   │   │   ├── fake.py     # FakeQueueAdapter (in-process)
│   │   │   │   └── redis.py    # RedisQueueAdapter (real)
│   │   │   └── clock/
│   │   │       ├── port.py     # ClockPort protocol
│   │   │       ├── fake.py     # FakeClockAdapter (controllable time)
│   │   │       └── system.py   # SystemClockAdapter (real)
│   │   ├── services/           # Business logic, uses ports only
│   │   │   ├── script.py
│   │   │   ├── panel.py
│   │   │   ├── character.py
│   │   │   ├── draft.py
│   │   │   ├── line_art.py
│   │   │   ├── colour.py
│   │   │   └── moderation.py
│   │   └── gen/                # Models generated or validated from contracts/openapi/
│   ├── tests/
│   │   ├── unit/               # Hermetic unit tests, DI with fake adapters
│   │   ├── contract/           # Contract tests: adapter vs fake, Go-Python interface
│   │   └── live/               # Optional live-API checks, never in CI
│   ├── pyproject.toml
│   └── requirements.txt
├── deploy/
│   ├── docker-compose.yml      # Local dev stack (fake adapters, no real API)
│   ├── docker-compose.prod.yml # Production overrides (real adapters, secrets)
│   └── migrations/             # PostgreSQL migration files (ordered, numbered)
├── docs/
│   ├── SPEC.md                 # Product specification (v2)
│   └── ARCHITECTURE.md         # This file
├── .github/
│   └── workflows/
│       ├── ci.yml              # Lint, test (hermetic, no live API), OpenAPI lint
│       └── lint-openapi.yml    # Schema and OpenAPI lint job
├── AGENTS.md
└── README.md
```

---

## 2. Service Boundaries and Ownership

| Service | Technology | Owns |
|---|---|---|
| **Frontend** | Svelte | UI rendering, colour editor, paint-bucket fill tool (client-side), job polling, user flows |
| **Go API** | Go + net/http | Auth, session, project/chapter/character/page/panel CRUD, credit ledger, quota enforcement, kill switch, job creation, hashed user ID, approval gates, presigned download URLs |
| **Python AI service** | Python + FastAPI | All AI dispatch, adapter wiring, prompt pre-moderation, script analysis, panel text, character description, draft sketch, finished painted pages (or explicit ink), colour suggestion, PDF assembly |
| **PostgreSQL** | Postgres | Durable project state, credit ledger, job records, stage approvals, colour palettes |
| **Redis** | Redis | Job queue; pending, running, retried, and cancelled job envelopes |
| **Object storage** | S3-compatible | Approved working copies (draft, painted page or ink page, reference), exported PDFs (generated images are Meta-hosted) |

**Communication rules:**

- Svelte talks only to the Go API over HTTPS.
- Go talks to PostgreSQL (direct), Redis (direct), and the Python AI service (HTTP, internal network).
- Python talks to AI adapter ports only (language, vision, image, storage, queue, clock).
- No service talks directly to a Meta API endpoint except the Python `meta.py` adapter implementations (which remain inert behind the ports until the whole project is built on fakes and Meta API keys are provided).
- The Svelte frontend never holds or sees API keys.

---

## 3. Ports and Adapters

All AI calls in the Python service go through adapter ports. A port is a Python Protocol class. Adapters implement the port. Services are wired to an adapter at startup via dependency injection; they never import a concrete adapter directly.

This makes the product run end-to-end with no network and no key (using fake adapters), and makes the real Meta adapters a config-selected swap.

### 3.1 Port Definitions

#### LanguagePort

```python
class LanguagePort(Protocol):
    def complete(self, model: str, messages: list[Message], **kwargs) -> str: ...
    def estimate_tokens(self, messages: list[Message]) -> TokenUsage: ...
```

#### VisionPort

```python
class VisionPort(Protocol):
    def review(self, model: str, image_bytes: bytes, prompt: str) -> str: ...
    def suggest_palette(self, model: str, image_bytes: bytes, panel_context: str) -> PaletteSuggestion: ...
```

#### ImagePort

```python
class ImagePort(Protocol):
    def generate(self, prompt: str, reference_images: list[bytes], hashed_user_id: str, style: str) -> ImageResult: ...
```

`ImageResult` carries: `image_bytes`, `provenance_metadata` (raw, must be preserved), `model_id`, `request_id`.

#### StoragePort

```python
class StoragePort(Protocol):
    def put(self, key: str, data: bytes, content_type: str) -> str: ...  # returns URL
    def get(self, key: str) -> bytes: ...
    def delete(self, key: str) -> None: ...
```

#### MediaFetchPort

```python
class MediaFetchPort(Protocol):
    def fetch(self, url: str) -> bytes: ...
```

`FakeMediaFetchAdapter` resolves only URLs under the configured storage base URL through the in-memory storage port and never uses the network.
`HTTPMediaFetchAdapter` is selected with `ADAPTER_MEDIA_FETCH=http`; the default is `fake`.
The HTTP adapter accepts only the configured `STORAGE_BASE_URL` origin and path or exact hosts listed in `META_IMAGE_HOSTS`.
External image hosts require HTTPS, and the adapter rejects redirects, non-public DNS results or IP literals, empty responses, responses over 10 MiB, and requests exceeding five seconds.
The HTTP adapter has an injectable transport so automated tests remain hermetic.

#### QueuePort

```python
class QueuePort(Protocol):
    def enqueue(self, queue: str, payload: dict, idempotency_key: str) -> str: ...
    def dequeue(self, queue: str, timeout_s: int) -> JobEnvelope | None: ...
    def ack(self, envelope: JobEnvelope) -> None: ...
    def nack(self, envelope: JobEnvelope, delay_s: int) -> None: ...
```

#### ClockPort

```python
class ClockPort(Protocol):
    def now(self) -> datetime: ...
    def sleep(self, seconds: float) -> None: ...
```

### 3.2 Fake Adapters

Each port has a deterministic fake adapter that runs with no network and no key. The fake adapters are the default in development and in all CI tests.

| Port | Fake behaviour |
|---|---|
| FakeLanguageAdapter | Returns deterministic structured character, outline and panel JSON for story operations; other prompts use an input hash |
| FakeVisionAdapter | Returns a fixed palette suggestion; review returns a fixed OK string |
| FakeImageAdapter | Returns a small test PNG (e.g., a 64x64 white image with a border); provenance_metadata is a fixed JSON stub |
| FakeStorageAdapter | Stores blobs in memory (or a temp dir); returns local file URIs |
| FakeMediaFetchAdapter | Resolves stored URLs from the in-memory storage port; never uses the network |
| FakeQueueAdapter | Executes jobs synchronously in-process |
| FakeClockAdapter | Starts at a fixed epoch; `advance(dt)` moves time forward for tests |

Fake adapters must be importable without any network dependency, credential, or external process.

### 3.3 Real Meta Adapters (Deferred)

Meta API keys come only after the whole project is built on fakes, with the Meta adapters remaining inert behind the ports until then.

The `meta.py` adapters implement the same ports using the real `api.meta.ai` endpoints. They are selected by setting `ADAPTER_IMAGE=meta`, `ADAPTER_LANGUAGE=meta`, etc. in environment config.

Tests and CI exercise real adapter classes only with injected mock HTTP transports; they never call live Meta endpoints.

**MetaImageAdapter specifics:**
- Endpoint: `POST https://api.meta.ai/v1/responses` using the Responses image-generation tool (text-to-image and image-to-image with reference images).
- Pricing: flat $0.01 per returned image.
- Rate limit: 150 requests per minute per team. The worker concurrency must be configured to stay within this.
- Must attach `hashed_user_id` on every call.
- Must preserve the `provenance_metadata` field from the response without modification.
- Must handle HTTP 400 content-policy errors by returning a structured error (not raising an unhandled exception) so the Go backend can surface a plain user message.
- Must NOT be called before the approval gate in Go has recorded the user's approval.
- Returns Meta-hosted URLs or Meta-delivered bytes per Meta's API; the image hosting seam (ImageHostingSeam in Go, ImageHostingPolicy in Python) stays configurable.

---

## 4. Go API - Python Service Contract

### 4.1 Contract Location

The contract is the OpenAPI 3.1 spec at `contracts/openapi/ai-service.yaml`.

This file is the single source of truth for the HTTP interface between Go and Python.

### 4.2 Versioning

The spec is versioned in the `info.version` field. Breaking changes bump the major version and require a coordinated deploy.

### 4.3 Type Generation and Validation

- **Go side:** types in `backend/gen/` are generated from the spec using `oapi-codegen` (or equivalent). They are checked into source control and regenerated when the spec changes.
- **Python side:** request and response models in `ai/app/gen/` are generated or validated from the spec using `datamodel-code-generator` (or equivalent). Pydantic models validate every request and response at runtime.
- **CI enforces:** a lint step (`spectral` or equivalent) validates the spec on every commit. A contract-test step sends real HTTP requests from a Go test client to a live Python test server using fake adapters and asserts schema compliance.

### 4.4 Key Endpoints (Go calls Python)

```
POST /internal/v1/analyze-script
POST /internal/v1/generate-character-reference
POST /internal/v1/generate-chapter-outline
POST /internal/v1/generate-panel-text
POST /internal/v1/generate-draft-sketch
POST /internal/v1/generate-line-art
POST /internal/v1/suggest-colour-palette
POST /internal/v1/moderate-prompt
POST /internal/v1/assemble-pdf
GET  /internal/v1/job/:id/status
```

All requests include a `job_id` (idempotency key) and `hashed_user_id`.
The colour suggestion request carries `line_art_image_url` and `panel_context`; the Python service resolves the approved image bytes through `MediaFetchPort` and passes both inputs to `VisionPort.suggest_palette`.

---

## 5. Job and Credit-Ledger Invariants

### 5.1 Credit Ledger

- Every image generation attempt writes a `credit_ledger` row inside the same PostgreSQL transaction that creates the `generation_jobs` row.
- A job must not be dispatched to the queue unless the ledger debit is committed.
- A debited job also commits its provider request in the same transaction (`generation_dispatches`, section 5.2), so a crash after the debit cannot leave a charged job that was never attempted.
- If a job fails and is eligible for a refund, a `refund` row is inserted in a separate transaction.
- `balance_after` is computed on the backend; the frontend is never trusted for credit amounts.

### 5.2 Idempotent Jobs

- Every job carries an `idempotency_key` (UUID, set at creation, unique constraint on `generation_jobs`).
- Re-enqueueing a job with the same key is a no-op: the worker checks the key, finds the existing record, and returns the existing result without generating a new image or charging credits.
- This makes worker retries on queue delivery failures safe.

The generation HTTP endpoints preserve their existing completed-result responses. After the PostgreSQL creation/debit transaction returns, Go calls `DispatchAsync` with the internal HTTP queue submission. Python serializes the operation and validated request into `QueuePort`; only the consumer invokes services. The fake queue drains synchronously, while `ADAPTER_QUEUE=redis` starts one consumer per AI service process during application startup. The HTTP request waits for the saved result, so frontend polling and existing response fields remain compatible. Paid Go endpoints accept an optional `Idempotency-Key` header scoped to the authenticated user, path and operation; omitting it creates a new attempt.

The Redis adapter atomically saves the envelope and pending state, claims deliveries with a 15 minute lease, and saves terminal responses and costs before acknowledgement. Repeated submissions and redeliveries return that saved response without invoking a provider again. Redis must use persistent storage and AOF; the Compose stack enables AOF with synchronous fsync. Queue status and responses survive AI process restarts, while PostgreSQL remains the owner of the public job and credit state. Go saves image links before marking persistent jobs done, and failed-job polling retries an idempotent refund transaction if the original response was interrupted.

**Durable dispatch outbox.** `CreateGeneration` inserts a `generation_dispatches` row beside the job and the debit for every debited job (character reference, draft sketch, line art, painted page, colour palette). The row holds the operation and the exact validated request with the job ID stamped in, which is also the queue key. The request that committed the job owns its row for a 20 minute lease and delivers it at once, as before; it retires the row when the job reaches a terminal state, or releases it when saving a finished result fails. One bounded dispatcher (`Server.RunDispatcher`, at most two envelopes in flight, polling every 5 seconds) claims rows whose lease lapsed. On start it also takes rows still leased to the previous process, since a restart ends that process's requests; this assumes one backend instance, and a second instance would only repeat idempotent sends. It resends the envelope through the same idempotent queue: the queue runs a job it never saw exactly once and otherwise returns the saved result, failure or uncertain outcome without calling a provider. A saved success goes through the same finalizer the request path uses (entity link, then job `done`), so a result the provider finished but Go never linked is applied after a restart without another provider call or charge. A failed outcome is refunded and an unknown one keeps its debit, as for the request path. An unreachable queue is not a provider outcome: the row is retried, and after 5 attempts the job fails and is refunded. Text, outline, panel and export jobs carry no debit and no envelope. Uncommitted or rolled-back work never has a row.

A consumer that stops during a provider call leaves an uncertain outcome. When its lease expires the delivery becomes failed, rather than automatically repeating a potentially billed operation. A new generation requires a new key. Completed response records do not expire automatically. Shutdown stops accepting worker progress waits; pending envelopes remain available to the next consumer.

Character references, draft pages, finished pages and PDF exports use a fresh UUID directory for every service execution. A later generation, including one that writes bytes before failing, cannot overwrite an earlier artifact. Page cleanup stores its raw source beside the working image in that same directory. The queue's saved response binds the attempt to the exact asset URL; Go stores the current image URL and job ID on the character/page, and each export record retains its own PDF URL. Internal queue replays return the saved URL. Public request replay semantics are a separate concern: some Go handlers still return the entity's current URL. Existing legacy artifact URLs remain readable, and no storage migration is required. Fake object storage remains volatile across restarts; these keys do not provide durable storage or define retention.

### 5.3 Approval Gate Invariant

No image generation job is created unless the prerequisite approvals for that stage are recorded in `stage_approvals`.

Go enforces this check transactionally before inserting into `generation_jobs` and dispatching to the queue:

- **Character reference generation:** requires a valid submitted script for `project_id`. No prior stage approval is required.
- **Draft sketch generation (per page):** requires approved `character_reference` records for all project characters AND an approved `chapter_outline` for the page's parent chapter (`entity_id=chapter_id`).
- **Direct-from-brief painted finish (per page):** requires approved character references and chapter outline; draft approval is not required. Draft-conditioned painting and explicit ink require an approved `draft_page` for that page (`entity_id=page_id`).

Regeneration rules:
- Regeneration of character reference images, draft sketches, or line art is explicitly permitted: a prior completed (`status=done`) or failed (`status=failed`) job for the same entity does not block creating a new generation job with a new idempotency key.
- Concurrency guard: Go rejects creating a duplicate job only if an active job (`status=pending` or `status=running`) already exists for that entity and stage.
- Stale approvals after regeneration or edit: an approval only counts if its `approved_at` is newer than the entity's latest completed generation job or edit, so regenerating or editing automatically makes the approval stale and blocks downstream generation until it is re-approved.

The approval gate invariant (no image generation before prerequisite approval) is enforced strictly and transactionally by Go before queue dispatch. In accordance with Section 2 communication rules, the Python service talks to AI adapter ports only and does not perform reverse callbacks into Go.

The default finish is `defaultPaintedPageVariant` in `backend/internal/api/handlers.go`, currently `direct-from-brief`; changing that constant switches the default in one place. The API keeps `variant=ink` as the explicit ink selection.

### 5.4 No Image Before Approval - Summary

```
character reference approved -> chapter_outline approved -> panel text generated
panel text ready (no separate approval gate) -> draft sketch approved -> painted finish approved (or explicit ink approved) -> colour approved for ink pages
approved character references anchored across all subsequent page generations
```

### 5.5 Book Budget, Per-User Quota and Global Kill Switch

The Go API checks before creating any generation job:

1. Is the global kill switch enabled? If yes, reject with a clear message.
2. For image jobs, has the project reached its lifetime book budget (default **40**), or the user reached their rolling 24-hour cap (**80**) or rolling 30-day cap (**800**)? If yes, reject with HTTP 429. Public image endpoints return `code: book_budget_reached` or `code: daily_image_limit_reached` for the first two limits.

All limits live in the singleton PostgreSQL `generation_policy` row (`max_book_images`, `max_daily_images`, `max_monthly_images`); zero disables a limit. Forty images accommodates a 12-page, 51-panel book's roughly 30 images (12 drafts, 12 finished painted or ink pages, main-character references) plus revisions. Eighty permits two books in a day, with 800 retaining a monthly guard. Deployment defaults use these limits; any elevated e2e profile belongs only to an isolated e2e database.

`CreateGeneration` locks policy then user, checks indexed `image_quota_usage` rows, and inserts the job, ledger debit and project-attributed quota increment in the **same transaction**. Concurrent admissions cannot exceed a boundary. Same-key replays return the original job before quota checks and never increment, including retries after failure or refund. A new key for regeneration or an image edit counts as a new image. Character references, drafts and finished painted page or explicit ink page count; approvals, colouring uploads, language, moderation, palettes and exports do not.

Failure or cancellation alone retains usage. A full ledger refund deletes that job's quota row in the refund transaction and releases book, daily and monthly usage exactly once; a partial refund retains the image slot. Failed refunds roll back both changes. The ledger preserves the audit trail. Project deletion nulls quota project/job references while preserving user usage and its original timestamp, so it cannot restore daily or monthly capacity. Existing usage is backfilled by upgrade migration; already-deleted jobs have no surviving project attribution.

---

### 5.6 Accounts and Project Limit

Identity resolves through the `auth.Authenticator` seam, which now also yields an account kind (`auth.IdentityAuthenticator`): cookie sessions yield the stored `guest` or `registered` kind, and the dev identity yields `registered` with no project limit. A guest owns exactly one project and a registered user up to a configurable ceiling (default 100), both read from the singleton `account_policy` row. `CreateProjectForAccount` checks and inserts in one transaction under a per-user advisory lock, so concurrent creates cannot both pass. Sessions are server-side and revocable, the cookie is `HttpOnly` and `SameSite=Lax`, and state-changing cookie requests need a per-session CSRF token. See `docs/ACCOUNTS.md` for the model, the single place to flip a user, the threat notes, and what is not built yet (password reset email, account deletion, retention).

---

## 6. Test Strategy

### 6.1 Principles

- No live network in any test.
- No real API key in any test or CI environment.
- Every component is tested with fake adapters via dependency injection.
- Tests are deterministic and repeatable.

### 6.2 Hermetic Unit Tests

**Python (pytest):**
- Services are instantiated with fake adapters injected at construction.
- Each test exercises one service method; fakes provide controlled responses.
- Clock is injected via `FakeClockAdapter`; time is deterministic.
- Queue is `FakeQueueAdapter`; jobs run synchronously.
- Storage is `FakeStorageAdapter`; reads are memory-only.

**Go (testing package):**
- Handlers are tested with an in-process `httptest.Server`.
- Database calls use a test Postgres instance (Docker Compose) with a per-test schema.
- The Python service is replaced by a Go mock server returning schema-valid responses.
- Credit-ledger and approval gate invariants are verified by asserting transaction rollback on injected errors and rejection when prerequisite stage approvals are missing.

### 6.3 Boundary Contract Tests

- A Go contract-test binary starts a real Python test server (fake adapters, no network) and exercises every `contracts/openapi/ai-service.yaml` endpoint.
- Asserts that requests and responses conform to the schema.
- Asserts that idempotency keys are honoured (send the same key twice, expect the same result).

For each port adapter, a contract test sends inputs to both the fake and the real adapter (real adapter skipped when no key is present) and compares output shapes against the port's documented schema.

### 6.4 Structural Validation

| Artifact | Tool | Gate |
|---|---|---|
| `contracts/openapi/ai-service.yaml` | `spectral` (or equivalent) | CI |
| PostgreSQL migrations | migration tool dry-run (`migrate --dry-run`) | CI |
| OpenAPI-generated Go types vs. spec | `oapi-codegen` diff check | CI |
| OpenAPI-generated Python models vs. spec | `datamodel-code-generator` diff check | CI |
| JSON schemas in `contracts/schemas/` | `ajv` (or equivalent) | CI |
| Config files (docker-compose, env templates) | schema validation | CI |

### 6.5 Live Checks (Never in CI)

A separate `ai/tests/live/` directory holds optional tests that call the real Meta API.

These tests:
- Require `ADAPTER_IMAGE=meta` and a valid `META_API_KEY`.
- Are excluded from `pytest` by default (`-m "not live"`).
- Are never referenced in CI workflows.
- Document which endpoint and model they exercise, and what response shape they assert.

### 6.6 Delivery Process and CI

The delivery process is direct-PR with GitHub Actions CI (`.github/workflows/ci.yml`) and no external validation pipeline.

---

## 7. Build Order and Work Streams

The following streams can begin in parallel after the contracts are finalised.

```
[Contracts]
  contracts/openapi/ai-service.yaml
  contracts/schemas/*.json
         |
         +----------+----------+----------+----------+
         |          |          |          |          |
   [Database]  [Go API]  [Python AI]  [Svelte]  [CI / Dev Env]
         |          |          |          |          |
   Migrations  CRUD +     Fakes +    Editor UI  Docker Compose
   Tables      Credit     Services   Colour tool  CI workflow
   Seeds       Ledger     Contract   Export UI    Lint
               Auth       tests      Job polling  Schema checks
               Jobs                              Live check stub
               |          |          |
               +----------+----------+
                          |
                    [Export stream]
                    PDF assembly
                    Download API
                    Provenance check
```

**Dependencies by stream:**

| Stream | Depends on |
|---|---|
| Database migrations | Contracts (schema fields) |
| Go API | Database migrations, Contracts |
| Python AI service (fakes) | Contracts; can start immediately |
| Svelte frontend | Go API endpoints stable (can mock before) |
| Export | Go API (download endpoint), Python AI service (PDF assembly), Database |
| CI and dev environment | Docker Compose definitions; can scaffold immediately |

**Recommended start order:**
1. Finalise `contracts/openapi/ai-service.yaml` and `contracts/schemas/`.
2. In parallel: database migrations + Go API skeleton; Python fakes + service stubs; CI workflow scaffold; Svelte scaffold.
3. Wire Go API to Python test server via contract tests.
4. Build each pipeline stage (script, character, draft, line art, colour, export) incrementally, adding the corresponding DB tables, Go handler, Python service, and Svelte page as a unit.
5. Meta API keys come only after the whole project is built on fakes, activating the inert Meta adapters behind the ports - no other change needed.

---

## 8. Development and Local Stack

### 8.1 Running with Fakes (No API Key Required)

```bash
docker compose up
```

This starts:
- PostgreSQL
- Redis
- Go API (`ADAPTER_*=fake`)
- Python AI service (`ADAPTER_*=fake`)
- Svelte dev server

The entire pipeline runs end-to-end with fake adapters. Generated images are fixed test PNGs. Language responses are deterministic stubs.

### 8.2 Switching to Real Adapters

Meta API keys come only after the whole project is built on fakes, with the Meta adapters remaining inert behind the ports until then. Once keys are available, set in `.env.local` (never committed):

```
ADAPTER_LANGUAGE=meta
ADAPTER_VISION=meta
ADAPTER_IMAGE=meta
META_API_KEY=<key>
```

Only the adapter implementations change; the service code does not change.
For the complete step-by-step procedure, configuration variables, adapter requirements, verification checklist, and rollback steps, see [docs/META_SWAP.md](META_SWAP.md).

### 8.3 Concurrency Setting

The image job queue concurrency is set via `IMAGE_WORKER_CONCURRENCY` (default: 2). At 2 workers with a realistic p95 generation latency of ~5 seconds per image, throughput is well below the 150 RPM team limit. Operators can increase this; the config comment must note the 150 RPM ceiling.

---

## 9. Assumptions

The following assumptions were made in drafting this document:

1. **Draft = one image per page (not per panel).** The spec and the task brief both say "one rough page image per attempt". This is the design. Panels on a page share one draft thumbnail.
2. **`muse-image-1.0` pricing is $0.01 per image.** Verified by two independent reports (both cite `dev.meta.ai/docs/pricing-rate-limits`). No other Meta image pricing exists.
3. **Paint-bucket fill is entirely client-side.** The colour editor runs in the browser using Canvas API flood-fill. No AI call is made during fill. Only the palette suggestion step costs a language/vision model call.
4. **Gap-closing is a client-side algorithm.** Morphological dilation/erosion and the manual gap-closer stroke tool run in the browser; no server round-trip.
5. **The Python service is an internal service only.** It is not exposed to the internet. Only the Go API is public. This is enforced by Docker network configuration.
6. **Self-hosted language and vision models are in scope; self-hosted image models are not.** Meta has released open-weights language and vision models (Llama 3.x, Llama 3.2 Vision). No open-weights image model from Meta exists. Per the owner decision of 2026-11-02, generated images are Meta-hosted: the real image adapter returns Meta-hosted URLs or Meta-delivered bytes per Meta's API, and the image hosting seam (ImageHostingSeam in Go, ImageHostingPolicy in Python) stays configurable. Our own storage still holds approved working copies and export PDFs as the architecture already says. The image adapter port keeps this swappable.
7. **Colour suggestion uses the vision model (approved image bytes + panel context -> palette).** The HTTP media fetch adapter allows only configured storage and Meta image hosts, requires HTTPS for external hosts, rejects redirects and non-public addresses, and bounds response size and time.
8. **`idempotency_key` on generation jobs is a UUID set by Go at job creation.** It is stored in `generation_jobs` with a unique constraint. Postgres enforces uniqueness; the application does not need a separate lock.
9. **Age gate is deferred.** The enforced age gate and date-of-birth collection are removed from the build scope; a warning that users must be 18+ (the threshold may be 21, to be confirmed with the owner) will be added in a later release.
10. **Geo-blocking and region restrictions are deferred.** No country-gating or geo-blocking is enforced; users can be global since the owner is in India.
