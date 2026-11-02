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
- `FakeImageAdapter`: Generates valid 64x64 test PNG images with fixed C2PA provenance metadata stubs.
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
| `META_API_KEY` | *(unset)* | `<real-api-key>` | Bearer token for authenticating requests against Meta API endpoints. |
| `IMAGE_WORKER_CONCURRENCY` | `2` | `2` | Number of concurrent image generation worker processes. |

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
- `review(model: str, image_bytes: bytes, prompt: str) -> str`: Submits image bytes and inspection prompts to Meta's multimodal model.
- `suggest_palette(model: str, image_bytes: bytes, panel_context: str) -> PaletteSuggestion`: Analyzes line art and panel context to return theme names and regional hex colour suggestions.

#### MetaImageAdapter (`ImagePort`)
- `generate(prompt: str, reference_images: list[bytes], hashed_user_id: str, style: str) -> ImageResult`: Calls Meta's image generation endpoint (`muse-image-1.0`) with text prompt, optional turnaround reference images, and style descriptors.
- Must return an `ImageResult` dataclass containing `image_bytes`, `provenance_metadata`, `model_id`, and `request_id`.

### 4.2 Error Mapping

The adapters must map external HTTP error responses into structured domain exceptions:
- **HTTP 400 (Content Policy Violation)**: Must be caught and transformed into a structured domain error containing the policy message without raising an unhandled runtime crash.
  The Go backend catches this error to display a polite, non-technical explanation allowing the user to revise their prompt.
- **HTTP 429 (Rate Limit Exceeded)**: Must be mapped to a retryable error indicating backoff delay based on the `Retry-After` header.
- **HTTP 401 / HTTP 403 (Authentication / Authorization Failure)**: Must be mapped to an unrecoverable configuration error with immediate administrative logging.
- **HTTP 5xx (Upstream Meta Service Failure)**: Must be mapped to a transient upstream failure that triggers exponential backoff retries within configured limits.

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

### 4.7 Provenance Preservation

- Meta's image models embed invisible watermarks and C2PA provenance metadata into generated images.
- The `MetaImageAdapter` must extract and preserve the raw `provenance_metadata` dictionary returned in API responses.
- Downstream processing (canvas manipulation, thumbnailing, PDF compilation) must never strip, alter, or obscure provenance metadata or watermarks.
- The PDF export pipeline must verify that provenance metadata is preserved and record `exports.provenance_metadata_preserved = true` in the database.

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
   - Confirm that generation completes successfully and returns a valid PNG image.
   - Inspect the returned image payload to confirm C2PA provenance metadata is present.
   - Verify through outbound logging that `hashed_user_id` was attached to the request payload.

5. **Approval Gate and Credit Ledger Verification**:
   - Confirm that image generation was blocked until user approval was recorded in the UI.
   - Confirm that the user's credit ledger was debited exactly $0.01 for the generated image.
   - Test a prompt containing policy-violating keywords to confirm graceful error handling without unhandled crashes.

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
