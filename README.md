# Icomic

AI-powered comic book generator built with a staged, approval-gated pipeline.
A user provides a written script and the system converts it into a comic book through defined stages: character extraction, chapter outlining, rough draft sketches, final line art, colouring with paint-bucket fill, assembly, and PDF export.
All AI features use Meta models only.
Image generation requires explicit prior user approval and estimated cost confirmation.

The full product specification is in [docs/SPEC.md](docs/SPEC.md).
The system architecture and ports-and-adapters design is in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Monorepo Layout

```
icomic/
├── contracts/                  # Shared source of truth for inter-service APIs
│   ├── openapi/
│   │   └── ai-service.yaml     # Go <-> Python OpenAPI spec (versioned)
│   └── schemas/
│       ├── job.schema.json     # Generation job event shape
│       └── credit.schema.json  # Credit ledger event shape
├── frontend/                   # Svelte single-page application
│   ├── src/
│   │   ├── routes/             # SvelteKit pages
│   │   ├── lib/
│   │   │   ├── editor/         # Comic book editor components
│   │   │   ├── colour/         # Colour editor + paint-bucket fill (client-side)
│   │   │   └── api/            # API client
│   │   └── app.html
│   ├── package.json
│   └── svelte.config.js
├── backend/                    # Go API server
│   ├── cmd/server/             # Entry point
│   ├── internal/
│   │   ├── api/                # HTTP handlers, middleware, routes
│   │   ├── domain/             # Domain types, business rules
│   │   ├── jobs/               # Job creation, queue dispatch
│   │   ├── credits/            # Credit ledger, quota, kill switch
│   │   ├── auth/               # Authentication, age gate, geo-block
│   │   └── db/                 # PostgreSQL queries, repositories
│   ├── gen/                    # Types and client generated/validated from contracts
│   └── go.mod
├── ai/                         # Python FastAPI AI service
│   ├── app/
│   │   ├── main.py             # FastAPI entry point
│   │   ├── adapters/           # Port implementations
│   │   │   ├── language/       # LanguagePort (fake & meta)
│   │   │   ├── vision/         # VisionPort (fake & meta)
│   │   │   ├── image/          # ImagePort (fake & meta)
│   │   │   ├── storage/        # StoragePort (fake & s3)
│   │   │   ├── queue/          # QueuePort (fake & redis)
│   │   │   └── clock/          # ClockPort (fake & system)
│   │   ├── services/           # Business logic using ports only
│   │   └── gen/                # Models generated/validated from contracts
│   ├── tests/
│   │   ├── unit/               # Hermetic unit tests with fake adapters
│   │   ├── contract/           # Boundary contract tests
│   │   └── live/               # Optional live checks (excluded from CI)
│   ├── pyproject.toml
│   └── requirements.txt
├── deploy/
│   ├── docker-compose.yml      # Local dev stack (fake adapters, zero network)
│   ├── docker-compose.prod.yml # Production overrides
│   └── migrations/             # PostgreSQL migrations
├── docs/                       # Specifications and architecture docs
└── .github/workflows/          # CI workflows
```

## Running the Dev Environment

The local development environment uses Docker Compose to run all services with deterministic fake adapters:

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

# 2. Go Backend unit and contract tests
make test-backend

# 3. Python AI service unit and contract tests
make test-ai

# 4. Frontend unit tests
make test-frontend
```

## Continuous Integration

GitHub Actions workflows run on every push and pull request:

- `.github/workflows/ci.yml`: Runs contract validation, Go lint and tests, Python Ruff checks and pytest, and Svelte frontend tests and build.
- `.github/workflows/lint-openapi.yml`: Validates OpenAPI specifications and JSON schemas whenever `contracts/**` changes.
