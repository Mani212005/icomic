# Icomic

AI-powered comic book generator. A user provides a written script and the system turns it into a comic book through a staged, approval-gated pipeline: character profiles and reference images, chapter outline, low-cost draft sketches, final line art, colouring, assembly, and PDF export. Every AI task uses Meta models only, and image generation runs only after the user approves prerequisite stages and the estimated cost.

The full product specification is in [docs/SPEC.md](docs/SPEC.md) and system architecture is in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Stack

- Frontend: Svelte
- Backend API: Go
- AI service: Python with FastAPI
- Database: PostgreSQL
- Job queue: Redis
- Object storage: S3-compatible
- Deployment: Docker

## Status

Specification only. No application code yet.
