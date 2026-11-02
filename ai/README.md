# Icomic AI Service

FastAPI Python microservice for Icomic AI pipeline, built on the Ports and Adapters (hexagonal) architecture.

## Responsibilities

The Python AI service handles all AI dispatch, adapter wiring, prompt moderation, script analysis, panel text, character references, draft sketches, line art, colour suggestions, and PDF compilation for the Icomic platform.

Per architecture rules:
- Python talks to AI adapter ports only (language, vision, image, storage, queue, clock).
- Services depend strictly on port interfaces via dependency injection.
- Development and CI run completely hermetic with deterministic fake adapters (no network access, no API key required).
- Meta adapters are inert stubs raising clear not-configured errors until live credits are provisioned.

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
- `FakeImageAdapter`: Generates valid 64x64 test PNGs and deterministic C2PA provenance metadata.
- `FakeStorageAdapter`: In-memory object store.
- `FakeQueueAdapter`: In-process synchronous queue enforcing idempotency deduplication.
- `FakeClockAdapter`: Controllable time starting from a fixed UTC epoch.

### 3. Config-Selected Adapters (`ADAPTER_*`)

Adapters are selected via environment variables, defaulting to `fake`:
- `ADAPTER_LANGUAGE`: `fake` (default) or `meta` (inert stub)
- `ADAPTER_VISION`: `fake` (default) or `meta` (inert stub)
- `ADAPTER_IMAGE`: `fake` (default) or `meta` (inert stub)
- `ADAPTER_STORAGE`: `fake` (default) or `s3` (stub)
- `ADAPTER_QUEUE`: `fake` (default) or `redis` (stub)
- `ADAPTER_CLOCK`: `fake` (default) or `system`

Meta adapters are inert stubs that raise `RuntimeError` if instantiated, ensuring no live API calls or keys are attempted.

## AI Services (`app/services/`)

- `ScriptService`: Script text validation, word count, character profile extraction, and suggested chapter count.
- `CharacterService`: Character turnaround reference generation, asset storage, and provenance preservation.
- `PanelService`: Chapter outline generation and panel text / visual prompt generation.
- `DraftService`: Rough draft sketch generation per page for composition approval.
- `LineArtService`: Closed-contour black-and-white inking generation from approved draft.
- `ColourService`: Region-based palette suggestions using multimodal vision models.
- `ModerationService`: Pre-moderation screening against prohibited content guidelines.
- `PdfAssemblyService`: Assembles completed comic book pages into an exportable PDF preserving provenance and AI disclosures.

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
