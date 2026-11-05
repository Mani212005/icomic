# Meta Adapter Configuration and Live Swap Guide

This guide provides the exact step-by-step procedure for swapping inert fake adapters with real Meta AI adapters once Meta API credentials and credits are provisioned.
It documents the required environment variables, secret handling, adapter implementation requirements, manual verification procedures, rollback steps, and pending captain decisions.

## 1. Current State: Implemented Meta Adapters and Hermetic Fakes

The real Meta adapter classes (`MetaLanguageAdapter`, `MetaVisionAdapter`, and `MetaImageAdapter` located in `ai/app/adapters/`) are implemented behind existing port contracts.
They are selected exclusively via configuration (`ADAPTER_LANGUAGE=meta`, `ADAPTER_VISION=meta`, `ADAPTER_IMAGE=meta`), while the default configuration remains deterministic fake adapters.
No API keys are read at import time, and `META_API_KEY` is read lazily only when an adapter is constructed with meta selected.
Live verification remains manual and strictly isolated from CI pipelines.

The Icomic service stack runs entirely hermetically on deterministic fake adapters by default:
- `FakeLanguageAdapter`: Generates deterministic text responses derived from the SHA-256 hash of the input prompt.
- `FakeVisionAdapter`: Returns fixed, valid colour palette suggestions and image review confirmations.
- `FakeImageAdapter`: Generates valid 64x64 test images with fixed C2PA provenance metadata stubs.
- `FakeStorageAdapter`: Stores generated image assets and export files in memory or temporary local storage.
- `FakeQueueAdapter`: Executes asynchronous generation jobs synchronously in-process with idempotency deduplication.
- `FakeClockAdapter`: Provides controllable time progression from a fixed UTC epoch.

The automated test suites and continuous integration pipelines enforce this hermetic boundary.
The repository hermetic guard (`scripts/check_hermetic.py`) scans all code, tests, and workflows on every run.
CI fails immediately if any live Meta hosts, live API markers, or `META_API_KEY` reads are introduced into code, tests, or workflows.

## 2. Environment Variables and Configuration

When transitioning to real Meta API endpoints, adapter selection is controlled entirely through configuration variables.
No application code or service routing needs to be altered.

### 2.1 Configuration Variables

The following environment variables configure the Python AI service and background workers:

| Variable | Fake Default | Meta Production Value | Purpose |
| --- | --- | --- | --- |
| `ADAPTER_LANGUAGE` | `fake` | `meta` | Selects the Meta language model adapter for script analysis, chapter outlining, and panel dialogue generation. |
| `ADAPTER_VISION` | `fake` | `meta` | Selects the Meta vision model adapter for line art review and colour palette suggestion. |
| `ADAPTER_IMAGE` | `fake` | `meta` | Selects the Meta image model adapter (`muse-image-1.0`) for character references, draft sketches, and final line art. |
| `ADAPTER_STORAGE` | `fake` | `s3` (or `fake`) | Selects the object storage adapter for image assets and exported PDF documents. |
| `ADAPTER_QUEUE` | `fake` | `redis` | Selects the job queue adapter for asynchronous background processing. |
| `ADAPTER_CLOCK` | `fake` | `system` | Selects the real system wall-clock adapter. |
| `ADAPTER_MEDIA_FETCH` | `fake` | `http` | Selects in-memory storage URL resolution or the allowlisted HTTP media fetch adapter. |
| `META_IMAGE_HOSTS` | *(empty)* | Comma-separated exact hostnames | Allows configured HTTPS Meta-hosted image URLs when `ADAPTER_MEDIA_FETCH=http`; hosts must resolve to public addresses. |
| `META_API_KEY` | *(unset)* | `<real-api-key>` | Bearer token for authenticating requests against Meta API endpoints. |
| `META_LANGUAGE_MODEL` | `muse-spark-1.3` (Meta only) | Optional override | Adapter default for script, panel and moderation services. |
| `META_VISION_MODEL` | `muse-spark-1.3` (Meta only) | Optional override | Adapter default for colour palette suggestions and vision review. |
| `META_IMAGE_MODEL` | `muse-image-1.0` (Meta only) | Optional override | Adapter default for image generation. |
| `META_API_BASE_URL` | `https://api.meta.ai/v1` (Meta only) | Optional override | Base URL for chat completions and Responses requests. |
| `META_REASONING_EFFORT` | `low` (Meta only) | `minimal`, `low`, `medium`, `high`, `xhigh` | Text/vision reasoning effort, validated at construction; per-call `reasoning_effort` overrides it. `none` is rejected. |
| `META_REASONING_RESERVE_TOKENS` | `1024` (Meta only) | non-negative integer | Generic adapter fallback; product stages explicitly select the answer/reserve/effort table in `docs/GENERATION_POLICY.md`. Manual checks use the larger of this value and their table reserve. |
| `IMAGE_WORKER_CONCURRENCY` | `2` | `2` | Number of concurrent image generation worker processes. |

The checked-in Compose files omit model and endpoint overrides so they use the adapter defaults defined in `ai/app/adapters/meta_http.py`. Services pass no model override unless explicitly configured at construction. Hermetic tests check Compose and env templates against those defaults and exercise service requests through mocked Meta transports. Language and vision use `/chat/completions`; image generation uses `/responses`.

#### Upload, response and conditioning budgets

Timeouts are positive finite seconds, configured independently per HTTP phase.
`META_IMAGE_CONNECT_TIMEOUT_SECONDS`, `META_IMAGE_WRITE_TIMEOUT_SECONDS` and `META_IMAGE_READ_TIMEOUT_SECONDS` default to 10, 120 and 300 respectively.
Text and vision use `META_TEXT_CONNECT_TIMEOUT_SECONDS`, `META_TEXT_WRITE_TIMEOUT_SECONDS` and `META_TEXT_READ_TIMEOUT_SECONDS`, defaulting to 10, 30 and 120.
The connection-pool timeout equals the connect timeout.
An explicitly injected client timeout remains supported for callers and tests.
The image read budget allows over six times the longest observed successful image job (45 seconds including local processing), while upload has a separate budget for conditioning bytes.
These are independent phase budgets, not a promise that an entire request completes within either one.
Any transmitted POST that times out still has an unknown outcome; it is never automatically resent and its cost reservation remains held.

Named character references are selected from names in the allocated panels' visual scene, action and image-prompt fields, using case-insensitive whole-name matching.
Dialogue and narration alone do not put an off-screen character into the cast.
The approved composition remains first, followed by selected character identities and any explicit style reference.
Legacy URL-only callers must supply the page's selected references themselves; their inputs remain subject to the same limits.
`META_IMAGE_REFERENCE_MAX_SIDE` defaults to 1024 pixels and `META_IMAGE_REFERENCE_WEBP_QUALITY` to 85.
Conditioning copies preserve aspect ratio and reduce resolution further if necessary to fit `META_IMAGE_MAX_REFERENCE_BYTES` (262144 bytes per image).
Already bounded WebP inputs can be reused unchanged.
Stored master images and their provenance remain untouched; the brief records both source and conditioning hashes, dimensions and byte sizes.
`META_IMAGE_MAX_TOTAL_REFERENCE_BYTES` defaults to 2097152 bytes including composition and style inputs; `META_IMAGE_MAX_REQUEST_BYTES` defaults to 3145728 bytes for the complete encoded request.
An `ImagePayloadError` refuses oversized inputs before the generation POST and before reserving provider cost.
Capped cost reports record exact serialized payload bytes, attached-image bytes/hashes/dimensions and HTTP elapsed seconds, without prompts, image contents or credentials.

### 2.2 Key Placement and Secrets Management

API credentials must be managed with strict security boundaries:
- Meta API keys belong in `.env.local` for local execution or inside a dedicated secrets manager for production deployments.
- The `.env.local` file is explicitly ignored by git and must never be committed to source control.
- Never place API keys in `deploy/docker-compose.yml`, `deploy/docker-compose.prod.yml`, Dockerfiles, code files, or documentation.
- Never expose API keys to the frontend Svelte application.
- All outbound requests to Meta API endpoints originate exclusively from the internal Python AI service.

Example `.env.local` configuration:

```bash
# Adapter selection
ADAPTER_LANGUAGE=meta
ADAPTER_VISION=meta
ADAPTER_IMAGE=meta
ADAPTER_STORAGE=s3
ADAPTER_QUEUE=redis
ADAPTER_CLOCK=system

# Authentication
META_API_KEY=meta_live_sk_example_placeholder_never_commit

# Queue and rate limit tuning
IMAGE_WORKER_CONCURRENCY=2
```

## 3. Step-by-Step Swap Procedure

Follow these steps when swapping the system from fake adapters to live Meta adapters.

### Step 1: Provision Meta API Keys and Credits
- Obtain authorized API credentials and billing credits from Meta for the required models (`muse-image-1.0`, Meta language models, and Meta multimodal models).
- Note your team's assigned rate limits (the standard ceiling is 150 requests per minute for image generation).

### Step 2: Implement Real Adapter Classes
- Real adapter classes are now implemented (`MetaLanguageAdapter`, `MetaVisionAdapter`, and `MetaImageAdapter`) satisfying all requirements in Section 4.
- Automated tests run completely hermetically using injected transports and fakes.
- Live verification remains manual using the isolated script `scripts/manual/meta_live_check.py`.

### Step 3: Populate Local Secrets
- Create a `.env.local` file in the repository root.
- Add `META_API_KEY=<your-real-key>` and set `ADAPTER_LANGUAGE=meta`, `ADAPTER_VISION=meta`, and `ADAPTER_IMAGE=meta`.
- Verify that `.env.local` is untracked and excluded by `.gitignore`.

### Step 4: Configure Worker Concurrency
- Set `IMAGE_WORKER_CONCURRENCY=2` in your environment.
- Confirm that total image generation dispatch across all workers remains well below the 150 requests per minute team limit.

### Step 5: Start the Stack with Production Configuration
- Launch the local stack with the configured environment file:

```bash
docker compose --env-file .env.local -f deploy/docker-compose.yml up --build
```

### Step 6: Execute the Manual Live Verification Checklist
- Perform the manual verification steps outlined in Section 5 to confirm end-to-end functionality.

## 4. Meta Adapter Implementation Requirements

When authoring real Meta adapter classes, developers must implement the following contracts and operational safeguards.

### 4.1 Port Method Implementations

Each adapter must strictly implement its respective Python protocol.

#### MetaLanguageAdapter (`LanguagePort`)
- `complete(model: str, messages: list[Message], **kwargs) -> str`: Executes dialogue and text completions using Meta's language model endpoint.
- `estimate_tokens(messages: list[Message]) -> TokenUsage`: Calculates or estimates input and output token counts for cost estimation.

#### MetaVisionAdapter (`VisionPort`)
- `review(model: str, image_bytes: bytes, prompt: str, **kwargs) -> str`: Submits image bytes and inspection prompts to Meta's multimodal model, forwarding `max_tokens` and `temperature`.
- `suggest_palette(model: str, image_bytes: bytes, panel_context: str, **kwargs) -> PaletteSuggestion`: Analyzes line art and panel context to return theme names and regional hex colour suggestions, forwarding `max_tokens` and `temperature`.

#### MetaImageAdapter (`ImagePort`)
- `generate(prompt: str, reference_images: list[bytes], hashed_user_id: str, style: str) -> ImageResult`: Calls Meta's image generation endpoint (`muse-image-1.0`) with text prompt, optional turnaround reference images, and style descriptors.
- Must return an `ImageResult` dataclass containing `image_bytes`, `provenance_metadata`, `model_id`, `request_id`, and `media_type`.

### 4.2 Error Mapping

The adapters must map external HTTP error responses into structured domain exceptions:
- **HTTP 400 (Content Policy Violation)**: Must be caught and transformed into a structured domain error containing the policy message without raising an unhandled runtime crash.
  The Go backend catches this error to display a polite, non-technical explanation allowing the user to revise their prompt.
- **HTTP 429 (Rate Limit Exceeded)**: A returned rate-limit rejection may be retried within the configured limit, respecting `Retry-After`; exhausted retries map to a rate-limit error.
- **HTTP 401 / HTTP 403 (Authentication / Authorization Failure)**: Must be mapped to an unrecoverable configuration error with immediate administrative logging.
- **HTTP 5xx (Upstream Meta Service Failure)**: The request outcome may be unknown, so map it to an explicit unknown-outcome error and do not retry automatically. A user or operator must choose whether to start a new attempt after reviewing the result. Do not assume provider idempotency.

Network timeouts and disconnects after a POST are also unknown outcomes and must not be retried automatically. The provider may have processed or billed a request before the response was lost.

### 4.3 Rate Limiting and Worker Concurrency

- Meta enforces a strict rate limit of 150 requests per minute per team for `muse-image-1.0`.
- The worker pool must throttle dispatch through `IMAGE_WORKER_CONCURRENCY`.
- At 2 concurrent workers with an average generation latency of 5 seconds per image, peak throughput is approximately 24 requests per minute, well within safe operational limits.
- Background workers should employ an in-process or Redis-backed token bucket to prevent bursts during queue drain.

### 4.4 Hashed User ID

- Meta's API terms mandate that every call to the image generation API must include a hashed identifier representing the requesting user.
- The hashed identifier must be a SHA-256 hash of the user's internal UUID.
- The hash must be generated by the Go backend or service boundary; raw user UUIDs must never be transmitted to Meta endpoints.
- The `hashed_user_id` parameter passed into `ImagePort.generate` must be forwarded in the API request payload.

### 4.5 Cost Metering and Approval Synchronization

- Image generation on `muse-image-1.0` carries a flat price of $0.01 per generated image regardless of resolution or quality settings.
- Language and vision operations consume credits based on token pricing.
- The adapter must never be invoked before the Go backend approval gate has recorded the user's explicit consent for that stage.
- Successful generation calls must return exact token counts and generation metadata so the backend credit ledger can record deductions accurately.
- No charge may be debited if generation was not attempted.

### 4.6 Prompt Pre-Moderation

- All prompts submitted by users (scripts, character descriptions, panel text) must pass pre-moderation screening via `ModerationService` before reaching Meta endpoints.
- Pre-moderation rejects prohibited content locally to avoid unnecessary API costs or account policy strikes.
- Prompts that pass local pre-moderation remain subject to Meta's upstream moderation, requiring graceful handling as described in Section 4.2.

### 4.7 Provenance Preservation and Image Container Handling

- Meta's image models embed invisible watermarks and C2PA provenance metadata into generated images.
- The real media type of returned image bytes is detected by magic numbers (PNG, JPEG, WebP, GIF) without re-encoding or modifying raw bytes.
- C2PA provenance detection scans standard PNG chunks, JPEG APP11 segments for JUMBF boxes, and WebP RIFF containers for JUMBF boxes without altering bytes.
- The `MetaImageAdapter` extracts and preserves the raw `provenance_metadata` dictionary returned in API responses and sets the detected `media_type`.
- Downstream processing (canvas manipulation, thumbnailing, PDF compilation) must never strip, alter, or obscure provenance metadata or watermarks.
- PDF export embeds JPEG bytes directly and renders PNG or WebP pixels using `/Filter /FlateDecode`. PNG and WebP originals are attached unchanged; their original provenance is preserved in those attachments, without asserting that a signature on the source image signs the rendered PDF pixels.
- The PDF export pipeline records `exports.provenance_metadata_preserved = true` in the database.

### 4.8 Contributor Tier Protection

Contributor tier models (such as `muse-spark-1.3-contributor`) train Meta models on user prompts and completions, so every Meta adapter strictly rejects them by default unless `META_ALLOW_CONTRIBUTOR_TIER=1` is explicitly set.

## 5. Verification Checklist

Live verification must be performed manually and isolated from automated pipelines.

### 5.1 CI and Automated Test Exclusion

- Live API calls are strictly forbidden in CI workflows and automated tests.
- CI runners operate with zero external network access and no API keys.
- Do not add `@pytest.mark.live` or live tests to standard test directories.
- Automated tests must run exclusively against fake adapters via dependency injection.

### 5.2 Manual Live Verification Checklist

Once keys and live adapters are configured in a local or staging environment, execute the following manual checklist:

1. **Service Initialization Check**:
   - Verify that all containers start without errors.
   - Verify that `PORT: 8000` responds on `GET /healthz`.
   - Verify that `ADAPTER_*` environment variables resolve to `meta` in container logs.

2. **Language Model Verification**:
   - Submit a sample comic script to `POST /api/v1/projects/:id/script`.
   - Verify that character extraction and chapter planning return realistic text generated by Meta language models.

3. **Vision Model Verification**:
   - Request colour palette suggestions for a sample panel.
   - Verify that the returned palette contains harmonious hex values relevant to the scene context.

4. **Image Generation Verification**:
   - Request generation of a character reference image.
   - Confirm that generation completes successfully and returns a valid image payload (JPEG, PNG, WebP, or GIF) with its detected media type.
   - Inspect the returned image payload to confirm C2PA provenance metadata is present.
   - Verify through outbound logging that `hashed_user_id` was attached to the request payload.

5. **Approval Gate and Credit Ledger Verification**:
   - Confirm that image generation was blocked until user approval was recorded in the UI.
   - Confirm that the user's credit ledger was debited exactly $0.01 for the generated image.
   - Test a prompt containing policy-violating keywords to confirm graceful error handling without unhandled crashes.

## Failed-response evidence for capped comic runs

The comic runner sets `ICOMIC_E2E_DIAGNOSTICS_DIR` to a unique directory inside
its mounted artifact directory. `cost-report.json` links the host-side
`diagnostics_dir` and every `diagnostic_files` entry. HTTP errors, malformed JSON,
empty or length-limited completions, invalid images and model schema failures
retain the failing provider response body. Capture happens before output parsing
and accounting validation; successful calls do not produce diagnostic files.
Token-count preflight failures are captured too.

Each complete-response diagnostic contains the HTTP status, internal operation/job IDs, error type
and the redacted body, capped at 256 KiB with an explicit truncation flag. It
contains no request payload, request URL, or HTTP headers. The configured credential,
credential-shaped strings, bearer tokens and JSON credential fields are redacted
before truncation or writing. Files use mode 0600. Artifacts placed inside the
repository receive a local `.gitignore`; keep production evidence outside the
repository and review it before sharing. Provider output can still contain story
content. Diagnostics are opt-in; no credential is read to enable capture.

Interrupted response bodies are classified as unknown outcomes, including protocol and content-decoding failures.
Their diagnostic is captured before the error leaves the HTTP client, with received and expected wire-byte counts, elapsed seconds, and only the length of the first bytes (at most 256), never those bytes themselves.
It records `body_complete=false` and `body_saved=false`.
Only Content-Type, Content-Length, Content-Encoding, Date and X-Request-ID response headers are permitted in this partial-response metadata; their values are redacted and bounded.
Authorization, cookies, arbitrary headers and partial image/JSON content are excluded.
The image reservation stays held, the queue and Go job become uncertain, and no automatic retransmission occurs.
The project remains available for an explicitly approved new attempt through the resume flags in [MULTIPAGE.md](MULTIPAGE.md#unknown-provider-outcomes-and-cost-accounting).

Historical shape fixtures and their evidence/reconstruction provenance live in
`ai/tests/fixtures/meta/observed/README.md`. Some older failures retained only
error text, not complete provider bodies; these fixtures explicitly reconstruct
the shape rather than claim to reproduce missing raw content. Fake adapters
accept an optional replay adapter; their default behavior remains deterministic.
The long-flow failure matrix runs the real Go API and Python worker with mocked
provider transports and checks every generation stage plus export. Failed output
is not published, redelivery does not repeat provider calls, exact observed usage
is retained, and unknown HTTP-error usage retains its budget reservation.

## 6. Rollback to Fake Adapters

If live Meta endpoints fail, credentials expire, or rate limits are reached, roll back immediately to fake adapters:

1. Update `.env.local` or environment configuration to restore fake adapters:
   ```bash
   ADAPTER_LANGUAGE=fake
   ADAPTER_VISION=fake
   ADAPTER_IMAGE=fake
   ```
2. Unset or clear `META_API_KEY`.
3. Restart the service stack:
   ```bash
   docker compose -f deploy/docker-compose.yml restart
   ```
4. Verify that `GET /healthz` returns healthy and the pipeline immediately operates using deterministic fake data.

## 7. Pending Captain Decisions

The following architectural and policy decisions remain reserved for the captain and must not be unilaterally decided by workers:

1. **Image Hosting Provider**:
   - Generated images are Meta-hosted per the owner decision of 2026-11-02: the real image adapter returns Meta-hosted URLs or Meta-delivered bytes per Meta's API.
   - The image hosting seam (`ImageHostingSeam` in Go, `ImageHostingPolicy` in Python) stays configurable.
   - Our own storage still holds approved working copies and export PDFs as the architecture already says.

2. **Deferred 18+ Advisory Warning**:
   - In accordance with Meta API terms, all end users must be 18 years of age or older.
   - The enforcement mechanism is currently deferred (no hard age gate or date-of-birth blocking in application code).
   - The UI presentation and copy for the advisory 18+ warning remain a pending captain decision.

3. **Region Restrictions**:
   - Region restrictions: none, users are global per the owner decision of 2026-11-02; revisit only if the owner asks.

## 8. Live Smoke Check Findings and Diagnostics

The second owner-approved live check returned HTTP 200 from `muse-spark-1.3` with `finish_reason=length`, empty content, 17 prompt tokens, and 64 completion tokens including 62 hidden reasoning tokens. The run stopped at that first failure. Vision and image were not reached; the actual image media type and C2PA signals remain open questions for the next owner-approved live check. Hermetic JPEG/WebP fixtures demonstrate parser support, not a live endpoint finding.

Third-party docs summarising Meta's API supply the following public facts; an owner-approved live check must confirm them:

- Chat completions accept `reasoning_effort` values `minimal`, `low`, `medium`, `high`, `xhigh`; `none` returns HTTP 400.
- Omitting reasoning effort leaves the amount of reasoning model-determined.
- Reasoning text is not returned, only usage `reasoning_tokens`.
- Reasoning tokens count against the output cap and are billed as output. They are already included in `completion_tokens`.

Both text and vision adapters now read `META_REASONING_EFFORT` (default `low`) and `META_REASONING_RESERVE_TOKENS` (default `1024`) at construction, never at import. Invalid effort or reserve values raise `MetaConfigurationError` at construction. Both vision methods and the language method allow a validated per-call `reasoning_effort` override.

A caller's `max_tokens` expresses the intended answer length. The wire request sends `max_tokens + reserve`, sharing that cap between reasoning and visible text. No cap is introduced when the caller omits `max_tokens`. This headroom reduces starvation but does not guarantee an answer or independently limit its visible length. An empty response, including `finish_reason=length`, raises `MetaEmptyResponseError` with `finish_reason`, `reasoning_tokens` and `token_usage`. `TokenUsage.output_tokens` copies the full completion count without adding reasoning again. The language token forecast includes the reserve in its output estimate.

The reserve has a cost consequence: worst-case token spend becomes `input_tokens * input_price + (max_tokens + reserve) * output_price`. Every reserve token can be billed, including calls that return no visible answer. At the checklist's pricing basis ($1.25/M input, $4.25/M output), the default reserve adds up to **$0.004352 per capped call**. A larger reserve increases this exposure linearly.

The manual checklist now makes these independent, single-attempt calls in order:

| Stage | Intended answer cap | Effort | Default wire cap | Worst-case estimate |
| --- | --- | --- | --- | --- |
| Language, tiny greeting prompt | 32 | `minimal` | 1056 | $0.004563 (60 input tokens) |
| Vision, tiny PNG and brief prompt | 64 | `minimal` | 1088 | $0.0050615 (350 input tokens) |
| Image | one image | n/a | n/a | $0.01 |

The full estimate is **$0.0196245**, below $0.05. Estimates use the configured reserve; a configuration that reaches $0.05 is refused before any calls. Input token counts are conservative estimates for these tiny inputs and must also be confirmed live.

Ordinary failures, including empty completions, do not skip later stages. There are no retry or fallback calls. Authentication (401/403), configuration errors, contributor-tier refusal and budget aborts stop remaining calls. Before each stage, its worst-case cost is checked against the persistent cumulative spend and the hard $5 ceiling; the legacy above-default budget override cannot bypass that ceiling. Failed transmitted calls are recorded at worst-case cost, even if usage is available. Successful token calls count all completion tokens once. The main entrypoint still requires `META_LIVE_CHECK=1` before resolving credentials or constructing clients.

Every stage has an outcome in the sanitized `.live-diagnostics.json`, including failures, budget aborts and skipped stages. It records HTTP status, response keys, token usage, finish reason, detected media type, dimensions, content length, the first 12 image bytes in hexadecimal, and image C2PA marker detection where available. It excludes keys, authorization headers, prompts, response text and exception messages. The final machine-readable `STATUS` line includes `language`, `vision`, `image`, attempted call count and spend; `PARTIAL` means some stages succeeded and others failed or were stopped. A full pass is still required for a successful exit code.

## 9. Comic flow request and accounting audit

The comic E2E calls chat completions for moderation, script analysis, outlines, panels and vision palettes; image generation uses Responses with user input and optional reference images. Chat requests must include a user or tool message. Image content belongs only in user messages, and inline MIME types must match image magic bytes. Unsupported parameters, streaming and multiple completions are rejected locally. Panel context is now in a user message. Draft and line-art references resolve to stored image bytes; missing images fail before generation rather than being encoded as URL text. Line art includes the approved draft as an actual reference.

Character, draft and line-art services detect returned image bytes and store matching `.png`, `.jpg`, `.webp` or `.gif` keys and content types without altering the source. PDF export supports PNG, JPEG and WebP; source attachments preserve PNG/WebP metadata. XML metadata is escaped. Hermetic strict-transport tests cover the entire service flow with real WebP fixtures, including vision input, PDF pixels, original attachments, moderation usage, duplicate delivery and costs. Empty text responses still raise with usage preserved; all flow token requests now set answer caps with the configured reasoning headroom.

For owner-authorized live comic E2E, use the existing `--live`, `ICOMIC_E2E_LIVE=1` and an explicit `--cost-cap-usd` (positive, at most 2 USD) gates. Pass `--prior-spend-usd <previously-accounted-total>` and `--prior-spend-source <record>` to deduct prior attempts from the same cap. The third-check reserve remains deducted. The runner configures a unique run ID and mounts its artifact directory into the AI service for a prompt-free `provider-costs-<run-id>.json` ledger. Normal fake operation does not enable this ledger.

Every `make e2e-comic` invocation provisions a fresh UUID user with a unique `example.invalid` email and an opening 100-credit fixture entry, together in a PostgreSQL transaction. The account uses the free plan and an unusable password hash; the local stack's existing development-auth header selects that user. The shared development user is never reused, existing quota history is never deleted, and quota limits are not raised. Fake validation and live runs therefore cannot consume each other's per-user allowance. The user ID is included in each cost report for audit. This runner requires the Compose PostgreSQL fixture even with `--skip-services`.

The guard reserves costs before every generation transmission, including moderation. For token calls it first counts the exact immutable input through Meta's non-generating `/responses/input_tokens` endpoint, including model-specific image tokens and injected prompt scaffolding. It reserves uncached input price times that explicit count plus output price times the explicit wire `max_tokens` (answer cap plus configured reasoning reserve). This avoids the previous full-context reservation of about $1.32 for a small call. Missing or invalid output caps are refused before any HTTP. Invalid or unavailable counts refuse generation; a direct ledger caller without an input count retains the full-context fallback. Capped vision accepts inline images so the counted bytes cannot change before generation. Image generation reserves the flat $0.01 single-image price. Only the priced `muse-spark-1.3` and `muse-image-1.0` models are allowed in capped mode.

A lock and atomic file updates preserve pending reservations across concurrent workers and restarts. Capped calls and token preflights are not retried. Pending, failed and unknown outcomes retain their worst-case reservations. Observed successful usage replaces reservations, with reasoning already included in completion tokens and cached input priced separately. Exceeding an input/output bound or its reservation stops the flow. Moderation jobs also record their actual token cost and locally blocked prompts record zero, without reusing an earlier call's usage.

The runner logs `COST ALERT` once when cumulative observed spend (prior recorded spend plus this run) reaches `--alert-usd`, default **1.0 USD**. The alert does not stop the run. Reports include `alert_threshold_usd`, `alert_raised` and `prior_spend_source`, including after failures. Prior spend, all pending/failed reservations and the third-live-check reserve still reduce the hard cap independently of the alert.

For the approved long-run budget, the explicit settings are `--cost-cap-usd 2 --alert-usd 1 --prior-spend-usd 0.080775 --prior-spend-source data/icomic-night-d-live-run/result.md`. The source is the 2026-11-04 attempt-3 report: $0.017006 prior recorded usage plus $0.063769 from attempt 3. It excludes earlier unmetered moderation and is a recorded subtotal, not a complete historical invoice. These settings do not authorize a live run by themselves.

Default hermetic CI replays the 51-panel fixture with a fake HTTP transport and exact recorded per-call token pairs from that report. With four retained characters, all 105 generation calls cost **$0.594634 USD**; the alert remains off with $0.080775 prior spend. The stress replay regenerates each page twice and explicitly replays every bounded language request once, retaining zero automatic transport retries. It costs **$1.453432 USD**, completes below the shared cap and triggers the 1 USD alert. These are simulations of the recorded prices and usage, not forecasts of new model responses.

The runner writes both a report for each run and `cost-report.json` after success, partial failure or service startup failure. Provider records supply costs at full precision, avoiding PostgreSQL's four-decimal rounding. Reports separate observed and conservatively accounted costs, identify unpriced calls, and set `pdf_path: null` on failure so a prior fake PDF cannot be mistaken for live output. The provider ledger survives even if the runner is killed. Earlier unmetered calls cannot be retrospectively priced from missing usage; carry a documented prior-spend allowance rather than claiming a complete historical bill.

Audit sources, checked 2026-11-04: [Meta image generation](https://dev.meta.ai/docs/image-generation) confirms Responses user input, reference-image parts and WebP as the default output; [vision input](https://dev.meta.ai/docs/cookbook/vision-input) describes user-only image content; [token counting](https://dev.meta.ai/docs/token-counting) documents the non-generating input count and model-specific media accounting; [long context](https://dev.meta.ai/docs/cookbook/long-context) defines the shared input/output window; [pricing](https://dev.meta.ai/docs/pricing-rate-limits) lists $1.25/M uncached input, $0.15/M cached input, $4.25/M output and $0.01 per image. Reading these public docs made no billable model calls.
