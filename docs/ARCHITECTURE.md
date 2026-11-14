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
│   │   │   └── api/            # Handwritten API client and types (JavaScript, not generated)
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
│   │   └── gen/                # Models generated from contracts/openapi/ai-service.yaml
│   ├── tests/
│   │   ├── unit/               # Hermetic unit tests, DI with fake adapters
│   │   └── contract/           # Contract tests: adapter vs fake, Go-Python interface
│   ├── pyproject.toml
│   └── requirements.txt
├── deploy/
│   ├── docker-compose.yml      # Local stack (fakes by default, local-disk storage volume)
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
| **Redis** | Redis | Job queue; pending, running, retried, and cancelled job envelopes (a job cancelled before it reached the queue is cancelled in PostgreSQL and its outbox envelope; see 5.2.1) |
| **Object storage** | S3-compatible | Approved working copies (draft, painted page or ink page, reference), exported PDFs (generated images are Meta-hosted) |

**Communication rules:**

- Svelte talks only to the Go API over HTTPS.
- Go talks to PostgreSQL (direct), Redis (direct), and the Python AI service (HTTP, internal network).
- Python talks to AI adapter ports only (language, vision, image, storage, queue, clock).
- No service talks directly to a Meta API endpoint except the Python `meta.py` adapter implementations (which remain inert behind the ports until the whole project is built on fakes and Meta API keys are provided).
- The Svelte frontend never holds or sees API keys.

**Stored media is never public.** Browsers read artifacts only through the Go API `GET /media/{key}`, never from the storage host (so no Docker-internal hostnames reach the browser):

- Every authenticated JSON response has its internal storage URLs (`.../storage/fake/<key>` or `/media/<key>`) replaced by an absolute `/media/<key>?exp&u&sig` link. The link is an HMAC over the user, the key and the expiry, valid for 25 to 30 minutes, and identical across reads within a five-minute window so browsers can cache it. `MEDIA_SIGNING_SECRET` sets the key (a random per-process key is used when unset, so links never survive a restart); `MEDIA_PUBLIC_BASE_URL` sets the link origin (default: the request's scheme and host).
- `GET /media/{key}` accepts such a link or an authenticated caller, then requires that the caller owns the key. Ownership comes from the resource id inside the key (`characters/<id>/`, `pages/<id>/`, `finishes/<id>/`, `exports/<id>/`) resolved through the project, so a link minted for one user never opens another user's artifact and keys of unknown shape are not served. Anonymous requests get 401; other users and missing keys get 404.
- The upstream read is bounded: only the configured AI storage (and image host, when one is configured) is contacted, redirects are never followed, the call has a 60 second deadline and a 64 MiB cap, and only a 200 with a known length is relayed. Upstream headers are not copied; content type is limited to PNG, JPEG, WebP, GIF and PDF (otherwise opaque bytes), with `nosniff`, a sandboxing CSP, `Cache-Control: private, max-age=300`, and `attachment` for non-images. `/api/v1/exports/{id}/download` uses the same bounded client.
- Whether storage stays behind this proxy or moves to provider-signed URLs is a captain decision (D03); the proxy sits behind the `ImageHostingSeam` and does not constrain it.

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
POST /internal/v1/assemble-pdf        (format pdf, or png_zip for the page PNG ZIP)
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

**Compatibility change: generation requests answer with a receipt (W21).** The five debited public endpoints (`POST /characters/{id}/reference-image`, `/pages/{id}/draft`, `/pages/{id}/line-art`, `/pages/{id}/painted-page`, `/pages/{id}/colour-palette/suggest`) no longer wait for the provider. Earlier versions held the HTTP request open until Python returned the saved result and answered with the finished image URL (or the palette), so a browser could not poll or cancel its first submission and a dropped connection (a flaky tunnel, a proxy timeout) left the client with "Failed to fetch" even though the server finished, inviting a paid retry. Now the request answers `202` as soon as the PostgreSQL transaction that creates the job, takes the debit and stores the dispatch envelope has committed. The body is a receipt (`GenerationReceipt` in `contracts/openapi/ai-service.yaml`, routes in `contracts/openapi/generation-api.yaml`): `job`, `job_id`, `status` (`pending`, or the job's current status when the request repeats a known key), `entity_type` (`character` or `page`), `entity_id`, `project_id`, and `variant` for painted pages. The receipt carries no result and no `image_url` or `palette`: the client polls `GET /api/v1/generation-jobs/{id}` by the receipt's job ID and reads the finished entity once the job is `done`. A failed provider call is no longer the HTTP answer; the job settles `failed` (known failure, refunded) or `uncertain` (unknown outcome, debit kept) with its error message, code and retry guidance. The painted-page route, which answered `200` with the page, now answers `202` like the others. The `Idempotency-Key` header makes a repeated request return the same receipt with the job's current status at any point, never a second job or debit, so a client that lost the connection repeats the request (the frontend retries a dropped request with the same key, and remembers the job ID per project so a reload resumes polling it). A second request without a key while one is active for the same entity and stage is refused with `409`. Text, outline, panel and PDF export requests carry no debit or envelope and still wait for their result; they are not covered by this change.

Delivery is the outbox's job, not the request's. After the commit the request only wakes the dispatcher and returns. Go sends the committed envelope to Python through the internal HTTP queue submission: Python serializes the operation and validated request into `QueuePort`, and only the consumer invokes services. The fake queue drains synchronously inside that call, while `ADAPTER_QUEUE=redis` starts one consumer per AI service process during application startup. The internal contract is unchanged: Go's dispatcher goroutine, not the browser's request, waits for the saved result. Paid Go endpoints accept an optional `Idempotency-Key` header scoped to the authenticated user, path and operation; omitting it creates a new attempt.

The Redis adapter atomically saves the envelope and pending state, claims deliveries with a 15 minute lease, and saves terminal responses and costs before acknowledgement. Repeated submissions and redeliveries return that saved response without invoking a provider again. Redis must use persistent storage and AOF; the Compose stack enables AOF with synchronous fsync. Queue status and responses survive AI process restarts, while PostgreSQL remains the owner of the public job and credit state. Go saves image links before marking persistent jobs done, and failed-job polling retries an idempotent refund transaction if the original delivery was interrupted.

**Durable dispatch outbox.** `CreateGeneration` inserts a `generation_dispatches` row beside the job and the debit for every debited job (character reference, draft sketch, line art, painted page, colour palette). The row holds the operation and the exact validated request with the job ID stamped in, which is also the queue key. A committed row is claimable at once. The creating request returns its receipt and wakes the dispatcher; it does not deliver. One bounded dispatcher (at most two envelopes in flight, shared by the wake-up path, the poll loop and restart recovery) claims a row with a 20 minute lease, retires it when the job reaches a terminal state, and releases it for a retry when saving a finished result fails. `Server.RunDispatcher` polls every 5 seconds for rows whose retry delay or lease lapsed. On start it also takes rows still leased to the previous process, since a restart ends that process's deliveries; this assumes one backend instance, and a second instance would only repeat idempotent sends. The in-memory job service keeps the same outbox, so hermetic and fake-stack runs follow the same receipt-then-delivery path. It resends the envelope through the same idempotent queue: the queue runs a job it never saw exactly once and otherwise returns the saved result, failure or uncertain outcome without calling a provider. A saved success goes through the same finalizer the request path uses (entity link, then job `done`), so a result the provider finished but Go never linked is applied after a restart without another provider call or charge. A failed outcome is refunded and an unknown one keeps its debit, as for the request path. An unreachable queue is not a provider outcome: the row is retried, and after 5 attempts the job fails and is refunded. Text, outline, panel and export jobs carry no debit and no envelope. Uncommitted or rolled-back work never has a row.

A consumer that stops during a provider call leaves an uncertain outcome. When its lease expires the delivery becomes failed, rather than automatically repeating a potentially billed operation. A new generation requires a new key. Completed response records do not expire automatically. Shutdown stops accepting worker progress waits; pending envelopes remain available to the next consumer.

Character references, draft pages, finished pages and PDF exports use a fresh UUID directory for every service execution. A later generation, including one that writes bytes before failing, cannot overwrite an earlier artifact. Page cleanup stores its raw source beside the working image in that same directory. The queue's saved response binds the attempt to the exact asset URL; Go stores the current image URL and job ID on the character/page, and each export record retains its own PDF URL. Internal queue replays return the saved URL. Public request replay semantics are a separate concern: some Go handlers still return the entity's current URL. Existing legacy artifact URLs remain readable, and no storage migration is required. Fake object storage remains volatile across restarts; these keys do not provide durable storage or define retention.

### 5.2.1 Cancellation (W23)

`POST /api/v1/generation-jobs/{id}/cancel` (owner only: `403` for another user, `404` for an unknown job; contract in `contracts/openapi/generation-api.yaml`, answer `CancelJobResponse`) is pending-only. It never claims to stop a provider call. The answer carries `outcome`, the `job`, the `refunded` amount and a plain `message`:

| Outcome | When | What happened |
| --- | --- | --- |
| `cancelled` | The job is `pending` and its dispatch envelope has never been claimed (no attempt), or it has no envelope. | One PostgreSQL transaction sets the job to terminal `cancelled`, marks the envelope `cancelled` (migration `000014`, so the dispatcher never claims or sends it), inserts the refund ledger row for the whole debit and releases the book, daily and monthly image slot. No provider call and no cost. |
| `settling` | The job was already claimed by the dispatcher (an attempt was counted) or is `running`. | Nothing changes. The job runs to its own terminal state with its result and cost kept, and the user is charged. |
| `finished` | The job is already `done`, `failed` or `uncertain`. | Nothing changes; the job is returned as it is. |

Repeating a cancel returns the same answer and never refunds twice (`cancelled` returns the stored refund amount). A cancelled job's refund is looked up by job, so repeats are idempotent at the ledger as well.

Cancel and claim of one envelope have exactly one winner. `CancelGeneration` takes the policy, user and job locks in the same order as creation and refunds, then locks the envelope row `FOR UPDATE`. The dispatcher's claim takes the same row with `FOR UPDATE SKIP LOCKED` and counts an attempt. A claim that commits first makes the cancel answer `settling` (an attempt is counted, so the request may already be with the provider); a cancel that commits first leaves a `cancelled` envelope that every later claim, including restart recovery, skips. `FinishDispatch` only retires `pending` envelopes, so it never rewrites `cancelled`.

Terminal states are never silently reversed. `TransitionGeneration` (and the in-memory twin) refuses to move a job out of `done`, `failed`, `uncertain` or `cancelled` into a different status, and refuses any write to a `cancelled` job: a late completion, failure, start or reconciliation cannot overwrite a cancellation, and a cancelled job can never be finished or charged. The dispatcher also re-reads the job before sending and only sends a `pending` or `running` one.

The frontend `GenerationRunner` offers **Cancel generation** once the receipt names a job. `cancelled` unlocks the controls with "Cancelled, nothing was charged."; `settling` keeps them locked, shows "Already with the model, finishing. You will be charged." and unlocks only when the job is terminal, then tells the user the result was kept and charged. Best-effort provider cancellation is out of scope.

### 5.3 Approval Gate Invariant

No image generation job is created unless the prerequisite approvals for that stage are recorded in `stage_approvals`.

Go enforces this check transactionally before inserting into `generation_jobs` and dispatching to the queue:

- **Character reference generation:** requires a valid submitted script for `project_id`. No prior stage approval is required.
- **Draft sketch generation (per page):** requires approved `character_reference` records for all project characters AND an approved `chapter_outline` for the page's parent chapter (`entity_id=chapter_id`).
- **Chapter outline generation (project):** requires at least one character and every character approved and current. A project with no characters cannot skip this gate.
- **Panel text generation (per chapter):** requires a current approved `chapter_outline` for that chapter (`entity_id=chapter_id`), so panels are never generated from an unapproved or edited outline.
- **Direct-from-brief painted finish (per page):** requires approved character references and chapter outline; draft approval is not required. Draft-conditioned painting and explicit ink require an approved `draft_page` for that page (`entity_id=page_id`).

Regeneration rules:
- Regeneration of character reference images, draft sketches, or line art is explicitly permitted: a prior completed (`status=done`) or failed (`status=failed`) job for the same entity does not block creating a new generation job with a new idempotency key.
- Concurrency guard: Go rejects creating a duplicate job only if an active job (`status=pending` or `status=running`) already exists for that entity and stage.
- Stale approvals after regeneration or edit: an approval only counts if its `approved_at` is newer than the entity's latest completed generation job or edit, so regenerating or editing automatically makes the approval stale and blocks downstream generation until it is re-approved.

Approvals describe real artifacts and commit atomically:
- Approving a character, draft or line art requires that the artifact exists (a reference image, a draft image, a line-art image; extras are approved as profiles without an image). An empty approval is refused with HTTP 400.
- An approval stamps its entity and appends its `stage_approvals` row in one transaction, conditioned on the entity's `updated_at` as validated. If the entity was edited in between, the approval fails with HTTP 409 instead of blessing a changed version.
- A draft, line art or painted page can only be approved if the job that made it started after the last edit of the page's panels; otherwise the approval is refused with HTTP 409 and the art must be regenerated. Panel edits and panel moves touch `panels.updated_at`, which the page-level gates read. Overlay-lettered pages are exempt from the currency check, because their text is composited after the art.
- A panel edit and the staling of every page holding the panel commit together.

Editor edits keep what they do not touch, and show approvals truthfully:
- `PATCH /api/v1/characters/{id}` merges `visual_attributes` into the stored map (RFC 7386): only the named keys change, a `null` value removes a key, and age, hair, `reference_role` and any other attribute survive an edit that does not name them. The editor sends only the keys it shows (`appearance`, `clothing`).
- An outline keeps the whole model answer, not just the summary: `chapters.important_events` and `chapters.character_names` (JSONB arrays of text, at most 100 items) and `chapters.ending` (migration `000015`, with a down script that drops them). `PATCH /api/v1/chapters/{id}` edits them with `title` and `summary`. Changing any of that text on an approved chapter sets its `status` back to `pending`, because the approval covered the old text; saving unchanged text keeps it. Panel generation sends the edited events, cast and ending to the AI service as `important_events`, `character_names` and `ending`, so panels follow the approved outline, and the same text is moderated.
- `POST /api/v1/projects/{id}/analyze` returns `warnings`: the service's (for example a very short script) and a chapter-count suitability warning when the chosen count would give each chapter under one page (5 panels) or over six pages (30 panels) of the script's panel budget. The list is empty, never null, when there is nothing to say; the editor shows it on the Characters stage after analysis.
- The editor distinguishes three approval states (`approvalState` in `frontend/src/lib/api/types.js`): never approved is `pending`, approved and not changed since is `approved`, approved and changed since is `stale`. Only `stale` shows the Stale tag, the Edited label and the stage banner; a never-approved item only offers Approve. `hasValidApproval` is true only for `approved`, so a missing approval never unlocks the next stage. Outlines carry their approval in `status` (the server clears it on an edit), so they are pending or approved and never stale.

Page allocation and regeneration are atomic:
- `PATCH /api/v1/pages/{id}` takes `panel_ids`, the complete ordered list for that page. Listing a panel that sits on another page of the chapter moves it. The request is rejected with HTTP 400, and nothing is written, if an ID is unknown, belongs to another chapter or repeats, if the page would exceed the configured maximum, if the page would lose a panel without it appearing on another page, or if a source page would be left empty. A change stales draft, line art and painted approvals of every page it touches.
- `POST /api/v1/chapters/{id}/panels/add`, `POST /api/v1/chapters/{id}/panels/move` and `DELETE /api/v1/chapters/{id}/panels/{panel_id}` are the chapter-scoped structure edits (`db.AddPanel`, `MovePanel`, `DeletePanel`). Each plans over the chapter's stored pages in one transaction under the chapter row lock (`domain.PlanPanelAdd`, `PlanPanelMove`, which reuses `PlanPanelReassignment`, and `PlanPanelDelete`), then applies the plan: it updates the pages whose panels changed and stales their approvals, touches their panels, inserts or deletes the panel and any page it creates or empties, and renumbers pages and panels in reading order. `ReassignPanels` runs through the same transaction, so `PATCH /pages/{id}` keeps panel numbers in order too. Panel numbers are unique per chapter, so changed panels first move out of range and then onto their number. Removing a page that holds generated art is refused with `ErrArtExists` (HTTP 409) and writes nothing. The answer names `changed_page_ids` and `removed_page_ids`; the client reloads the chapter. A created or changed overlay page gets its lettering layout refreshed after the commit.
- Regenerating the chapter outline replaces all chapters of the project in one transaction, and regenerating panels replaces the chapter's panels and pages in one transaction. A failure partway leaves the previous revision intact, and duplicate chapter or panel numbers cannot be stored. Replacement is refused with HTTP 409, before any provider call, when any affected page already holds generated art, because it would delete paid work.

The approval gate invariant (no image generation before prerequisite approval) is enforced strictly and transactionally by Go before queue dispatch. In accordance with Section 2 communication rules, the Python service talks to AI adapter ports only and does not perform reverse callbacks into Go.

The default finish is `defaultPaintedPageVariant` in `backend/internal/api/handlers.go`, currently `direct-from-brief`; changing that constant switches the default in one place. The API keeps `variant=ink` as the explicit ink selection.

### 5.3.1 Export Gate and Provenance

`POST /projects/{id}/export` takes an optional `{format, page_size}` (`pdf` or `png`; `a4`, `us_letter` or `custom`; defaults PDF on A4). `custom` is the 6.625 x 10.25 in comic page. PNG pages keep their native size. Overlay-lettered pages export in both formats from the same stored layout and the same review gate: the PDF draws it as vector text, and the PNG ZIP rasterizes it onto the artwork (`ai/app/services/lettering/raster.py`, glyph outlines of the bundled fonts, bubbles antialiased, every other pixel unchanged). The original provider file stays under `sources/`, and the manifest records `lettering: drawn` or `withheld` per overlay page. A stale or flagged layout is withheld from both formats and returned as a typed `warnings` entry, which the Export view shows.

Export refuses with `409 EXPORT_NOT_READY` and a per-page `details.problems` list unless every page has a current approved image (line art, uploaded colour or painted page; stale approvals do not count) and the Meta provenance recorded when that image was generated. No placeholder art is ever drawn. If storage no longer holds a page image or an original attachment when the PDF is assembled, the AI service fails with `409 EXPORT_INCOMPLETE`.

Go records the `provenance_metadata` of every generated draft, line art and painted page in `artifact_provenance`, keyed by the unique image URL and written once. `provider_raw` inside it is the provider's object verbatim, or null when the provider sent none; the remaining keys are Icomic diagnostics. `provenance_metadata_preserved` is true only when every AI source file is attached byte-identical (the raw file when page cleanup produced a derivative) and its SHA-256 equals the recorded `source_hash`. An export that cannot show that is not completed (`409 EXPORT_PROVENANCE_NOT_PRESERVED`). Icomic records provenance; it does not verify C2PA signatures, and no export text claims it does. Pages generated before provenance was recorded must be regenerated before they can be exported.

### 5.4 No Image Before Approval - Summary

```
character reference approved -> chapter_outline approved -> panel text generated
panel text ready (no separate approval gate) -> draft sketch approved -> painted finish approved (or explicit ink approved) -> colour approved for ink pages
approved character references anchored across all subsequent page generations
```

### 5.5 Book Budget, Per-User Quota and Global Kill Switch

The Go API checks before creating any generation job:

1. Is the global kill switch enabled? If yes, reject paid generation with HTTP 429, `code: generation_stopped` and a message saying finished pages, downloads and exports still work.
2. For image jobs, has the project reached its lifetime book budget (default **40**), or the user reached their rolling 24-hour cap (**80**) or rolling 30-day cap (**800**)? If yes, reject with HTTP 429. Public image endpoints return `code: book_budget_reached` or `code: daily_image_limit_reached` for the first two limits.

The global stop switch is the operator flag (`KILL_SWITCH`, the runtime provider, or `generation_policy.kill_switch_active`, which the spend threshold also sets) and halts **paid generation only**: every job type that can spend at the provider (character reference, draft, finished page, colour palette and the text jobs). It does not halt anything that spends nothing: reading jobs and projects, downloading finished pages and exports, and PDF/PNG export (`pdf_assembly` is exempt from the switch and from the spend-threshold check in `CreateGeneration`). The dispatcher also honours it: while the switch is on, `pumpDispatches` and `ClaimDispatches` claim nothing, so a job that was committed but not yet sent is held `pending` (it can still be cancelled for a full refund) and is sent once the switch is turned off. A job claimed before the switch was turned on is already on its way and finishes. The job whose own debit trips the spend threshold is held like any other unsent job.

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

Two manual entry points call the real Meta API: `scripts/manual/meta_live_check.py` (adapter
smoke check) and `scripts/e2e/comic_e2e.py --live` (the full comic flow). Neither lives under
`pytest`, and neither is referenced by CI.

They:
- Require `META_API_KEY`; the smoke check also needs `META_LIVE_CHECK=1` and the comic flow
  needs `ICOMIC_E2E_LIVE=1`.
- Enforce a hard budget and refuse a call that would pass it before it is sent.
- Write a prompt-free record of every call, for review afterwards.

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
make dev   # docker compose -f deploy/docker-compose.yml up --build
```

This starts:
- PostgreSQL (migrations and seeds run first)
- Redis
- Go API (`ADAPTER_*=fake`)
- Python AI service (`ADAPTER_*=fake`, generated files on the `ai_data` volume)
- Svelte dev server

There is no `docker-compose.yml` at the repository root; run Compose from `deploy/` or pass `-f`.
What a local run needs, what survives a restart and how to back up are in [PERSONAL_RUN.md](PERSONAL_RUN.md).

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

### 8.3 Concurrency

The AI service runs one background consumer thread (`GenerationWorker`), so provider calls are
sequential. At a realistic ~5 seconds per image that is about 12 images a minute, far below the
150 RPM team limit, and there is no concurrency setting. (`IMAGE_WORKER_CONCURRENCY` appeared in
earlier drafts of these docs; no code ever read it.)

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
