# Icomic

AI-powered comic book generator built with a staged, approval-gated pipeline.
A user provides a written script and the system converts it into a comic book through defined stages: character extraction, chapter outlining, rough draft sketches, final line art, colouring with paint-bucket fill, assembly, and PDF export.
All AI features use Meta models only.
Image generation requires explicit prior user approval and estimated cost confirmation.

The full product specification is in [docs/SPEC.md](docs/SPEC.md).
The system architecture and ports-and-adapters design is in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Stage 1 Scope

This first stage provides:
- The versioned OpenAPI and JSON Schema contract between the Go API and Python AI service (`contracts/`).
- The monorepo skeleton across all components (`frontend`, `backend`, `ai`, `deploy`, `contracts`).
- Per-component minimal runnable stubs (Go HTTP server, FastAPI service, and Svelte app shell) with health check endpoints.
- Hermetic unit and boundary contract tests.
- CI workflows and local development environment setup.

Later parallel streams (database and migrations, Go API domain and repository, Python service adapters, Svelte editor with bucket-fill tool, export) build on this stage.

## Monorepo Layout

```
icomic/
├── contracts/                  # Shared source of truth for inter-service APIs
│   ├── openapi/
│   │   └── ai-service.yaml     # Go <-> Python OpenAPI spec (versioned)
│   └── schemas/
│       ├── job.schema.json     # Generation job event shape
│       └── credit.schema.json  # Credit ledger event shape
├── frontend/                   # Svelte single-page application skeleton
│   ├── src/
│   │   ├── routes/             # SvelteKit app shell
│   │   ├── lib/
│   │   │   └── api/            # API client (health check)
│   │   └── app.html
│   ├── package.json
│   └── svelte.config.js
├── backend/                    # Go API server skeleton
│   ├── cmd/server/             # Entry point
│   ├── internal/
│   │   └── api/                # Health check handlers and routing
│   ├── gen/                    # Types and client generated/validated from contracts
│   └── go.mod
├── ai/                         # Python FastAPI AI service skeleton
│   ├── app/
│   │   ├── main.py             # FastAPI entry point (health check stub)
│   │   └── gen/                # Models generated/validated from contracts
│   ├── tests/
│   │   └── contract/           # Boundary contract tests
│   ├── pyproject.toml
│   └── requirements.txt
├── deploy/
│   ├── docker-compose.yml      # Local dev stack (Postgres, Redis, services)
│   ├── docker-compose.prod.yml # Production overrides
│   ├── migrations/             # PostgreSQL migration files (up and down)
│   └── seeds.sql               # Database seed data for plans
├── docs/                       # Specifications and architecture docs
└── .github/workflows/          # CI workflows
```

## Running the Dev Environment

The local development environment uses Docker Compose to run all services:

```bash
docker compose -f deploy/docker-compose.yml up --build
```

Services started:

- Frontend: `http://localhost:5173`
- Go Backend API: `http://localhost:8080`
- Python AI Service: `http://localhost:8000`
- PostgreSQL: `localhost:5432`
- Redis: `localhost:6379`

## Testing and Verification

All unit and boundary-contract tests run hermetically with zero external dependencies and zero live network calls.

Run all tests across the monorepo:

```bash
make test
```

Or test each service individually:

```bash
# 1. Contract and JSON Schema validation
make test-contracts

# 2. Database migrations and schema structural validation
make test-migrations

# 3. Go Backend unit and contract tests
make test-backend

# 4. Python AI service contract tests
make test-ai

# 5. Frontend unit tests
make test-frontend
```

## Continuous Integration

GitHub Actions workflows run on every push and pull request:

- `.github/workflows/ci.yml`: Runs contract validation, database migration structural validation, Go lint and tests, Python Ruff checks and pytest, and Svelte frontend tests and build.
- `.github/workflows/lint-openapi.yml`: Validates OpenAPI specifications and JSON schemas whenever `contracts/**` changes.
