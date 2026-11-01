# Icomic AI Service

FastAPI Python microservice skeleton for Icomic AI service.

## Responsibilities

In Stage 1 (monorepo skeleton and contracts):
- Minimal runnable FastAPI application with health check endpoint.
- Pydantic models and contract tests conforming to `contracts/openapi/ai-service.yaml`.

Downstream streams will implement ports and adapters:
- `LanguagePort`: Text and dialogue completion.
- `VisionPort`: Multimodal image review and colour palette suggestion.
- `ImagePort`: Image generation.
- `StoragePort`: Object storage for images and assembled PDFs.
- `QueuePort`: Background job queue.
- `ClockPort`: Deterministic time control.

## Running Locally

To run the AI service locally using `uv`:

```bash
cd ai
uv run uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload
```

## Running Tests

Hermetic contract tests run locally and in CI:

```bash
cd ai
uv run pytest
```
