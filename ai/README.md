# Icomic AI Service

FastAPI Python microservice for Icomic AI pipeline, built on the Ports and Adapters (hexagonal) architecture.

## Responsibilities

The Python AI service handles all AI dispatch, adapter wiring, prompt moderation, script analysis, panel text, character references, draft sketches, line art, colour suggestions, and PDF compilation for the Icomic platform.

Per architecture rules:
- Python talks to AI adapter ports only (language, vision, image, storage, queue, clock).
- Services depend strictly on port interfaces via dependency injection.
- Development and CI run completely hermetic with deterministic fake adapters (no network access, no API key required).
- Real Meta adapters are implemented behind existing ports and selected only by configuration.

## Ports and Adapters Architecture

### 1. Adapter Ports (`app/adapters/*/port.py`)

- `LanguagePort`: Text and dialogue completion and token estimation.
- `VisionPort`: Multimodal image review and colour palette suggestion.
- `ImagePort`: Image generation with C2PA provenance tracking (`ImageResult`).
- `StoragePort`: Object storage for images and assembled PDFs.
- `QueuePort`: Background job queue with idempotency keys.
- `ClockPort`: Deterministic time control.

### 2. Deterministic Fake Adapters (`app/adapters/*/fake.py`)

Default adapters for local development and CI:
- `FakeLanguageAdapter`: Deterministic responses based on input SHA-256 hash.
- `FakeVisionAdapter`: Fixed palette suggestions with valid hex colours.
- `FakeImageAdapter`: Generates valid 64x64 test images and deterministic C2PA provenance metadata.
- `FakeStorageAdapter`: In-memory object store.
- `FakeQueueAdapter`: In-process synchronous queue enforcing idempotency deduplication.
- `FakeClockAdapter`: Controllable time starting from a fixed UTC epoch.

### 3. Config-Selected Adapters (`ADAPTER_*`)

Adapters are selected via environment variables, defaulting to `fake`:
- `ADAPTER_LANGUAGE`: `fake` (default) or `meta` (real `MetaLanguageAdapter`)
- `ADAPTER_VISION`: `fake` (default) or `meta` (real `MetaVisionAdapter`)
- `ADAPTER_IMAGE`: `fake` (default) or `meta` (real `MetaImageAdapter` targeting `muse-image-1.0`)
- `ADAPTER_STORAGE`: `fake` (default) or `s3` (stub)
- `ADAPTER_QUEUE`: `fake` (default) or `redis` (stub)
- `ADAPTER_CLOCK`: `fake` (default) or `system`
- `ADAPTER_MEDIA_FETCH`: `fake` (default, in-memory storage only) or `http` (allowlisted URL fetcher)

Storage configuration:
- `STORAGE_BASE_URL`: Base URL used by `FakeStorageAdapter` to construct stored artifact URLs, defaulting to `http://localhost:8000/storage/fake`. In fake mode, containerized or ephemeral test environments can use the reachable AI service address (e.g. `http://ai:8000/storage/fake`). The HTTP media fetch adapter requires this URL to use a public address.
- `META_IMAGE_HOSTS`: Comma-separated exact hostnames allowed for externally hosted images when `ADAPTER_MEDIA_FETCH=http`. External hosts must use HTTPS and resolve only to public IP addresses.

When `meta` is selected, adapters read credentials lazily at construction time.
Script, panel, moderation and colour services use the selected adapter's default model unless an explicit service `model_id` is supplied. Meta defaults to `muse-spark-1.3` for language and vision; `META_LANGUAGE_MODEL` and `META_VISION_MODEL` configure those adapter defaults.
If credentials are not configured, adapters raise a descriptive `RuntimeError`.
All outbound HTTP interactions support injectable transports for hermetic unit testing.
A dedicated smoke verification script is provided in `scripts/manual/meta_live_check.py` for manual validation, generating a sanitized `.live-diagnostics.json` record on every run.
Contributor tier models (`*-contributor`) train Meta models on submitted prompts and completions, so all Meta adapters reject them by default unless `META_ALLOW_CONTRIBUTOR_TIER=1` is set.
Adapter parsers robustly handle string and multipart content, expose reasoning tokens and `finish_reason` on `TokenUsage`, and raise `MetaEmptyResponseError` on empty content.
Both Meta text and vision adapters read and validate reasoning configuration at construction:

- `META_REASONING_EFFORT`: `low` by default; accepts `minimal`, `low`, `medium`, `high`, `xhigh`. A per-call `reasoning_effort` kwarg overrides it. `none` and unknown values raise `MetaConfigurationError` before HTTP.
- `META_REASONING_RESERVE_TOKENS`: a non-negative integer, default `1024`. For an explicit `max_tokens`, the request sends the intended answer length plus this reserve. Without `max_tokens`, no output cap is added. Vision methods also forward `temperature`.

Third-party docs summarising Meta's API say that omitting reasoning effort leaves it model-determined, `none` returns HTTP 400, reasoning text is hidden, and reasoning tokens share the output cap and output price. The owner-approved live check must confirm those public API details. `TokenUsage.output_tokens` includes reasoning already, so cost estimates must not add `reasoning_tokens` a second time. The language token forecast includes the configured reserve as output headroom.

Cost consideration: the reserve increases the worst-case bill by `reserve * output price` per capped call. At the checklist's pricing basis of $4.25 per million output tokens, the default reserve adds up to **$0.004352 per call**, even if the visible answer is empty. The shared wire cap does not guarantee a separate visible answer length; reasoning can still exhaust it. Empty content with `finish_reason=length` raises `MetaEmptyResponseError` carrying `finish_reason`, `reasoning_tokens`, and `token_usage`.

The manual checklist uses text `max_tokens=32` and vision `max_tokens=64`, both with `reasoning_effort="minimal"`, plus one image call. Including default reserves, its worst-case estimate is **$0.0196245**. Stages run independently after ordinary failures, with one call each and no retries. The final `STATUS` reports every stage and `PARTIAL` when some succeed and others fail. Authentication/configuration failures or a budget abort stop all remaining calls. Each stage is checked against cumulative spend and the hard $5 cap, failed calls are recorded at their full worst-case cost, and all stage outcomes (including failures and skipped stages) appear in `.live-diagnostics.json`. Reserves that would make the checklist cost $0.05 or more are refused before calls.
The image adapter detects real media types (PNG, JPEG, WebP, GIF) by magic numbers without re-encoding, exposes `media_type` on `ImageResult`, and inspects C2PA JUMBF containers in JPEG APP11 and WebP containers.

## AI Services (`app/services/`)

- `ScriptService`: Script text validation, word count, character profile extraction, and suggested chapter count.
- `CharacterService`: Character turnaround reference generation, asset storage, and provenance preservation.
- `PanelService`: Chapter outline generation and panel text / visual prompt generation.
- `DraftService`: Rough draft sketch generation per page for composition approval.
- `LineArtService`: Closed-contour black-and-white inking generation from approved draft.
- `ColourService`: Region-based palette suggestions using multimodal vision models.
- `ModerationService`: Pre-moderation screening against prohibited content guidelines.
- `PdfAssemblyService`: Exports JPEG, PNG and WebP pages with AI disclosures. JPEG bytes are embedded directly; WebP is decoded with Pillow for display. PNG and WebP originals are attached unchanged to preserve their embedded provenance alongside the PDF's metadata.

## Policy Seams and Owner Decisions (`app/policies.py`)

Per Owner Decisions (2026-11-02):
1. **Global Users**: No geo-blocking or region restrictions anywhere; users are global.
2. **18+ Age Gate**: Deferred (no enforced age gate or DOB-based blocking in code; advisory warning to be added later).
3. **Image Hosting Architecture**: `ImageHostingPolicy` seam (`DirectStoragePolicy` default) swappable for future presigned URLs or CDN proxy.

## Running Locally

To run the AI service locally using `uv`:

```bash
cd ai
uv run uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload
```

## Running Tests and Linting

Hermetic unit and boundary contract tests:

```bash
cd ai
uv run ruff check .
uv run ruff format --check .
uv run pytest -v
```
