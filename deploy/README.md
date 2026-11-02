# Icomic Deployment and Infrastructure

This directory contains container definitions for Icomic.

## Directory Contents

- `docker-compose.yml`: Local development stack running PostgreSQL, Redis, Go backend, Python AI service, and Svelte frontend.
- `docker-compose.prod.yml`: Production deployment overrides.

## Running the Dev Stack

To start the full development environment with Docker Compose:

```bash
docker compose -f deploy/docker-compose.yml up --build
```

The stack exposes:

- Frontend: `http://localhost:5173`
- Go Backend API: `http://localhost:8080`
- Python AI Service: `http://localhost:8000`
- PostgreSQL: `localhost:5432`
- Redis: `localhost:6379`
