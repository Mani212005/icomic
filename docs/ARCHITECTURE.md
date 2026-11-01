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
│   │   ├── auth/               # Authentication, age gate, geo-block
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
| **Go API** | Go + net/http | Auth, session, project/chapter/character/page/panel CRUD, credit ledger, quota enforcement, kill switch, job creation, age gate, geo-block, hashed user ID, approval gates, presigned download URLs |
| **Python AI service** | Python + FastAPI | All AI dispatch, adapter wiring, prompt pre-moderation, script analysis, panel text, character description, draft sketch, line art, colour suggestion, PDF assembly |
| **PostgreSQL** | Postgres | Durable project state, credit ledger, job records, stage approvals, colour palettes |
| **Redis** | Redis | Job queue; pending, running, retried, and cancelled job envelopes |
| **Object storage** | S3-compatible | Generated images (draft, line art, reference), exported PDFs |

**Communication rules:**

- Svelte talks only to the Go API over HTTPS.
- Go talks to PostgreSQL (direct), Redis (direct), and the Python AI service (HTTP, internal network).
- Python talks to AI adapter ports only (language, vision, image, storage, queue, clock).
- No service talks directly to a Meta API endpoint except the Python `meta.py` adapter implementations (deferred until credits).
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
| FakeLanguageAdapter | Returns templated deterministic strings seeded from input hash; token counts are fixed |
| FakeVisionAdapter | Returns a fixed palette suggestion; review returns a fixed OK string |
| FakeImageAdapter | Returns a small test PNG (e.g., a 64x64 white image with a border); provenance_metadata is a fixed JSON stub |
| FakeStorageAdapter | Stores blobs in memory (or a temp dir); returns local file URIs |
| FakeQueueAdapter | Executes jobs synchronously in-process |
| FakeClockAdapter | Starts at a fixed epoch; `advance(dt)` moves time forward for tests |

Fake adapters must be importable without any network dependency, credential, or external process.

### 3.3 Real Meta Adapters (Deferred)

The `meta.py` adapters implement the same ports using the real `api.meta.ai` endpoints. They are selected by setting `ADAPTER_IMAGE=meta`, `ADAPTER_LANGUAGE=meta`, etc. in environment config.

Real adapters are never imported in tests or CI.

**MetaImageAdapter specifics:**
- Endpoint: `POST https://api.meta.ai/v1/images/generations` (text-to-image and image-to-image with reference images).
- Pricing: flat $0.01 per returned image.
- Rate limit: 150 requests per minute per team. The worker concurrency must be configured to stay within this.
- Must attach `hashed_user_id` on every call.
- Must preserve the `provenance_metadata` field from the response without modification.
- Must handle HTTP 400 content-policy errors by returning a structured error (not raising an unhandled exception) so the Go backend can surface a plain user message.
- Must NOT be called before the approval gate in Go has recorded the user's approval.

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

---

## 5. Job and Credit-Ledger Invariants

### 5.1 Credit Ledger

- Every image generation attempt writes a `credit_ledger` row inside the same PostgreSQL transaction that creates the `generation_jobs` row.
- A job must not be dispatched to the queue unless the ledger debit is committed.
- If a job fails and is eligible for a refund, a `refund` row is inserted in a separate transaction.
- `balance_after` is computed on the backend; the frontend is never trusted for credit amounts.

### 5.2 Idempotent Jobs

- Every job carries an `idempotency_key` (UUID, set at creation, unique constraint on `generation_jobs`).
- Re-enqueueing a job with the same key is a no-op: the worker checks the key, finds the existing record, and returns the existing result without generating a new image or charging credits.
- This makes worker retries on queue delivery failures safe.

### 5.3 Approval Gate Invariant

No image generation job is created unless the prerequisite approvals for that stage are recorded in `stage_approvals`.

Go enforces this check transactionally before inserting into `generation_jobs` and dispatching to the queue:

- **Character reference generation:** requires a valid submitted script for `project_id`. No prior stage approval is required.
- **Draft sketch generation (per page):** requires approved `character_reference` records for all project characters AND an approved `chapter_outline` for the page's parent chapter (`entity_id=chapter_id`).
- **Final line art generation (per page):** requires an approved `draft_page` for that page (`entity_id=page_id`).

Regeneration rules:
- Regeneration of character reference images, draft sketches, or line art is explicitly permitted: a prior completed (`status=done`) or failed (`status=failed`) job for the same entity does not block creating a new generation job with a new idempotency key.
- Concurrency guard: Go rejects creating a duplicate job only if an active job (`status=pending` or `status=running`) already exists for that entity and stage.
- Stale approvals after regeneration or edit: an approval only counts if its `approved_at` is newer than the entity's latest completed generation job or edit, so regenerating or editing automatically makes the approval stale and blocks downstream generation until it is re-approved.

The approval gate invariant (no image generation before prerequisite approval) is enforced strictly and transactionally by Go before queue dispatch. In accordance with Section 2 communication rules, the Python service talks to AI adapter ports only and does not perform reverse callbacks into Go.

### 5.4 No Image Before Approval - Summary

```
character reference approved -> chapter_outline approved -> panel text generated
panel text ready (no separate approval gate) -> draft sketch approved -> line art approved -> colour approved
approved character references anchored across all subsequent page generations
```

### 5.5 Per-User Quota and Global Kill Switch

The Go API checks before creating any generation job:

1. Is the global kill switch enabled? If yes, reject with a clear message.
2. Has the user reached their daily or monthly image quota? If yes, reject.

Both checks read from PostgreSQL. Quota counters are incremented in the same transaction as the ledger debit.

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
5. Real Meta adapters drop in when credits are available - no other change needed.

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

Set in `.env.local` (never committed):

```
ADAPTER_LANGUAGE=meta
ADAPTER_VISION=meta
ADAPTER_IMAGE=meta
META_API_KEY=<key>
```

Only the adapter implementations change; the service code does not change.

### 8.3 Concurrency Setting

The image job queue concurrency is set via `IMAGE_WORKER_CONCURRENCY` (default: 2). At 2 workers with a realistic p95 generation latency of ~5 seconds per image, throughput is well below the 150 RPM team limit. Operators can increase this; the config comment must note the 150 RPM ceiling.

---

## 9. Assumptions

The following assumptions were made in drafting this document and may need captain confirmation:

1. **Draft = one image per page (not per panel).** The spec and the task brief both say "one rough page image per attempt". This is the design. Panels on a page share one draft thumbnail.
2. **`muse-image-1.0` pricing is $0.01 per image.** Verified by two independent reports (both cite `dev.meta.ai/docs/pricing-rate-limits`). No other Meta image pricing exists.
3. **Paint-bucket fill is entirely client-side.** The colour editor runs in the browser using Canvas API flood-fill. No AI call is made during fill. Only the palette suggestion step costs a language/vision model call.
4. **Gap-closing is a client-side algorithm.** Morphological dilation/erosion and the manual gap-closer stroke tool run in the browser; no server round-trip.
5. **The Python service is an internal service only.** It is not exposed to the internet. Only the Go API is public. This is enforced by Docker network configuration.
6. **Self-hosted language and vision models are in scope; self-hosted image models are not.** Meta has released open-weights language and vision models (Llama 3.x, Llama 3.2 Vision). No open-weights image model from Meta exists. The image adapter port keeps this swappable.
7. **Colour suggestion uses the vision model (image + panel context -> palette).** This is consistent with the spec requirement to suggest per-region colours based on the actual line art.
8. **`idempotency_key` on generation jobs is a UUID set by Go at job creation.** It is stored in `generation_jobs` with a unique constraint. Postgres enforces uniqueness; the application does not need a separate lock.
9. **Age verification at sign-up collects date of birth.** The specific mechanism (self-declaration, ID check, payment method inference) is a pending captain decision. The DB schema stores `date_of_birth` for the minimum viable check.
10. **Country is collected at sign-up and used for geo-blocking.** IP-based geo-detection as a secondary check is a pending captain decision.
