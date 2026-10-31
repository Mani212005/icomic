# Icomic

AI-powered comic book generator. A user provides a written script and the system turns it into a comic book: chapter outline, character profiles and reference images, panel-by-panel storyboard, generated panels, an editor, and PDF export. Every AI task uses Meta models only, and image generation runs only after the user approves the storyboard and the estimated cost.

The full product specification is in [docs/SPEC.md](docs/SPEC.md).

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
