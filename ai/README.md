# Icomic AI Service

FastAPI Python microservice for AI model dispatch using a ports-and-adapters architecture.

## Responsibilities

- Script analysis, character extraction, and chapter outlining.
- Panel text and scene description generation.
- Draft sketch layout generation (one image per page).
- Final black-and-white line art generation.
- Colour palette suggestion.
- Prompt pre-moderation.
- PDF assembly with AI-generation disclosures and provenance metadata preservation.

## Ports and Adapters

All AI and external interactions are abstracted behind ports:

- `LanguagePort`: Text and dialogue completion.
- `VisionPort`: Multimodal image review and colour palette suggestion.
- `ImagePort`: Image generation (`muse-image-1.0`).
- `StoragePort`: Object storage for images and assembled PDFs.
- `QueuePort`: Background job queue.
- `ClockPort`: Deterministic time control.

Deterministic fake adapters run by default in development and testing.
Switching to real Meta adapters is done via environment variables (`ADAPTER_IMAGE=meta`, etc.).

## Running Locally

To run the AI service locally using `uv`:

```bash
cd ai
uv run uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload
```

## Running Tests

Hermetic unit and contract tests run with fake adapters and need no API keys or external services:

```bash
cd ai
uv run pytest
```
