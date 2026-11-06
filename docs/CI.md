# Continuous Integration (CI) Guide

This document describes the CI pipeline for the Icomic repository and provides instructions for running all CI checks locally.

## CI Workflow Architecture

The primary CI pipeline is defined in `.github/workflows/ci.yml`. It runs on pushes to `main` and all pull requests targeting `main`. A supplementary workflow, `.github/workflows/lint-openapi.yml`, runs path-filtered contract checks when contracts or schemas change.

### Design Principles

- **Hermeticity**: Default tests run without live network calls, external APIs, or database dependencies. Tagged PostgreSQL integration tests run in a separate job against a local service container.
- **Modularity**: One stable job per component (`hermetic-guard`, `contracts`, `backend`, `backend-realdb`, `ai-service`, `frontend`), with database integration isolated from the hermetic backend suite.
- **Pinned Toolchains**: Tool versions are explicitly pinned and synchronized across configuration files (`go.mod`, `pyproject.toml`, `package.json`, `package-lock.json`, `uv.lock`, and `contracts/requirements.txt`).
- **Dependency Caching**: Native GitHub Actions caching is configured for Go modules, Python wheels / uv dependencies, and npm packages.
- **Least-Privilege Security**: Workflows explicitly specify `permissions: contents: read`.
- **Concurrency Management**: Workflows cancel superseded runs on the same ref using concurrency groups.
- **Unified Status Gate**: The aggregate job `ci-success` depends on all required component jobs and serves as the single required status check for branch protection.

## CI Jobs

| Job ID | Job Name | Responsibility |
| --- | --- | --- |
| `hermetic-guard` | Hermetic Guard | Scans codebase and workflows to ensure zero live API markers, zero real Meta hosts, zero `META_API_KEY` reads in code/tests, and no database dependencies outside the tagged PostgreSQL job; tests the generated-model drift comparator. |
| `contracts` | Contracts & Schemas Validation | Validates OpenAPI 3.1 specifications and JSON schemas against test suites. |
| `contract-models` | OpenAPI Generated Model Drift Check | Regenerates Go and Python contract models with pinned generator versions and fails if checked-in output differs. |
| `database` | Database Migrations & Schema Validation | Runs hermetic PostgreSQL migration structural validation without live database dependencies. |
| `backend` | Go Backend Checks & Tests | Runs Go format verification (`gofmt`), static analysis (`go vet`), unit and contract tests with race detection (`go test -race`), and binary compilation (`go build`). |
| `backend-realdb` | PostgreSQL Integration Tests | Runs Go integration tests tagged `realdb` against a PostgreSQL service, with a fresh schema per test; exercises migration up/down, transactional generation, credits, quotas, approvals, and concurrency. |
| `ai-service` | Python AI Service Checks & Tests | Syncs locked dependencies via `uv`, runs Ruff linting and format checks, and executes pytest boundary contract tests. |
| `frontend` | Svelte Frontend Checks & Tests | Installs npm dependencies via `npm ci`, runs Svelte type and syntax checking (`svelte-check`), executes unit tests, and compiles the production bundle (`vite build`). |
| `e2e` | End-to-End API Integration Suite | Starts real Python AI service on fakes and real Go API server on ephemeral ports, testing full project lifecycle from dev sign-in to downloaded PDF verification, plus negative paths (stale approvals, kill switch, quota exhaustion, prompt moderation). |
| `ci-success` | CI Success | Aggregate job that evaluates the completion status of all required component jobs. |

## Running Checks Locally

Developers can run all CI checks locally using `make` or the underlying native toolchains.

### OpenAPI Contract Models

The Go and Python service models are generated from `contracts/openapi/ai-service.yaml`. Go generator options live in `backend/oapi-codegen.yaml`; Python generation options and pinned tool versions live in the Makefile, with Python field overrides in `ai/datamodel-codegen-overrides.json`. Request UUID fields stay Python strings to preserve the existing model API while keeping the OpenAPI wire shape unchanged. Install the pinned tools, regenerate after changing the contract, and check the checked-in output with:

```bash
make install-contract-codegen
make generate-contract-models
make check-contract-models
```

The check regenerates into a temporary directory, so it never overwrites a developer's working files. A mismatch points to `make generate-contract-models`. `frontend/src/lib/api/` is not included: its client and types are handwritten JavaScript modules, with no generated source or generator configuration in the repository.

### 1. Run Everything

To run all linters, tests, builds, and hermetic guards:

```bash
make check
```

Or run individual phases:

```bash
make lint     # Run backend, AI, and frontend linters
make test     # Run hermetic guard, contract tests, Go tests, AI tests, and frontend tests
make build    # Build backend Go binaries and frontend Svelte production bundle
```

### 2. Hermetic Guard

Verifies that the repository contains no live API markers, real Meta hosts, API key reads, or workflow database service dependencies:

```bash
python3 scripts/check_hermetic_test.py
python3 scripts/check_hermetic.py
```

Or via make:

```bash
make guard-hermetic
```

### 3. Contracts and Schema Validation

Validates the OpenAPI 3.1 specification (`contracts/openapi/ai-service.yaml`) and JSON schemas (`contracts/schemas/*.json`):

```bash
make test-contracts
```

Direct command:

```bash
uv run --with-requirements contracts/requirements.txt python3 contracts/validate_test.py
```

### 4. Database Migrations Validation

Validates PostgreSQL migrations and seeds hermetically. The suite also compares columns named by PostgreSQL CRUD repository SQL with the schema after upgrade migrations, including panel scene fields. Removing the scene-field upgrade is covered by a negative test. This structural check catches column drift without starting PostgreSQL; it does not validate SQL execution, foreign keys, or service wiring.

The backend unit tests and `make test-e2e` use in-memory repositories. Consequently, they could pass while the Compose comic flow failed on PostgreSQL schema drift or missing persisted generation jobs. Repository failure-injection tests now require HTTP 500 from generation, editing, and approval handlers when a write fails. A missing or invalid stored line-art URL returns HTTP 409 before a palette job or debit.

Run the structural checks with:

```bash
make test-migrations
```

Direct command:

```bash
python3 deploy/validate_migrations_test.py
```

### 5. Go Backend

Run Go formatting check, vet, tests, and build:

```bash
# Verify formatting
make fmt-backend
# Or: ( cd backend && test -z "$(gofmt -l .)" )

# Go vet static analysis
make lint-backend
# Or: ( cd backend && go vet ./... )

# Unit and contract tests with race detector
make test-backend
# Or: ( cd backend && go test -v -race ./... )

# Compile backend binaries
make build-backend
# Or: ( cd backend && go build -v ./... )
```

The default backend tests remain database-free. Run the real PostgreSQL integration suite locally with Docker Compose:

```bash
make test-backend-realdb
```

The target starts and removes an isolated PostgreSQL 16 test container. It also accepts `TEST_DATABASE_URL` when an already-running local PostgreSQL service is available; the test rejects non-loopback database hosts. CI runs the tagged suite in the separate `backend-realdb` service-container job.

### 6. Python AI Service

Run Ruff linting, formatting checks, and pytest test suites:

```bash
# Lint and format check
make lint-ai
# Or: ( cd ai && uv run --frozen ruff check . && uv run --frozen ruff format --check . )

# Hermetic unit and contract tests
make test-ai
# Or: ( cd ai && uv run --frozen pytest -v )
```

Queue tests use `FakeQueueAdapter` for synchronous HTTP delivery and `fakeredis` with Lua for Redis atomic enqueue, claims, restart recovery, and response replay. No Redis process is needed. The Python runtime includes `redis`; the Redis consumer is selected only by `ADAPTER_QUEUE=redis`. Go HTTP tests assert dispatch after debit, repeated-key replay, durable lifecycle transitions, and idempotent refunds.

### 7. Svelte Frontend

Install dependencies, run type checks, unit tests, and production build:

```bash
# Install dependencies
( cd frontend && npm ci )

# Svelte type and template check
make lint-frontend
# Or: ( cd frontend && npm run check )

# Unit tests
make test-frontend
# Or: ( cd frontend && npm test )

# Production build
make build-frontend
# Or: ( cd frontend && npm run build )
```

### 8. End-to-End (E2E) API Integration Suite

Executes full-stack API integration tests driving the real Python AI service on fakes and the real Go API server as local processes with zero external network or database dependencies:

```bash
make test-e2e
```

Direct command:

```bash
( cd ai && uv run --frozen pytest -v -c ../tests/e2e/pytest.ini ../tests/e2e )
```

The e2e suite tests:
- Authentication via supported dev identity (`dev@icomic.local`, `00000000-0000-0000-0000-000000000001`)
- Project creation, script submission, and script analysis
- Character turnaround generation and approval gates
- Chapter outline generation and chapter outline approval
- Panel generation and automatic page partitioning
- Draft sketch generation and approval
- Line art inking generation and approval
- Multimodal colour palette suggestions and approval
- Project export compilation and PDF download through Go download endpoint
- Structural verification of generated PDF (valid PDF header/trailer, page count, mandatory AI generation disclosure text, and Meta C2PA provenance metadata)
- Invariant and safety negative paths:
  - Downstream generation rejection when approved upstream entity is edited (stale approval cascade) until re-approved
  - Operator runtime kill switch enforcement (HTTP 429)
  - Daily per-user image generation quota exhaustion (HTTP 429)
  - Content pre-moderation blocking prohibited prompts (HTTP 400 or blocked: true)

### 9. Workflow YAML Validation

Validate GitHub Actions workflow syntax locally with `actionlint`:

```bash
actionlint .github/workflows/ci.yml .github/workflows/lint-openapi.yml
```

### 10. One-Command Comic E2E Run

Run the end-user comic flow through the Go API and download a verified PDF:

```bash
make e2e-comic
```

The target starts PostgreSQL, Redis, the AI service, and the Go API with Docker Compose, using fake adapters by default.
It creates a project, analyzes the script, generates and colours pages, exports and verifies the PDF, then writes `/home/mani/icomic-artifacts/comic-e2e/latest.pdf`, a timestamped PDF copy, and a cost report.
Use `E2E_ARGS="--artifact-dir /tmp/comic-e2e"` to isolate PDF and report output.
Use `E2E_ARGS="--script-file tests/fixtures/the_tide_clock.txt --artifact-dir /tmp/comic-long"` for the original 3,130-word fixture: 51 panels, twelve comic pages and thirteen PDF pages. The runner verifies generated/persisted counts and exact PDF count. See [multipage pacing and call inventory](MULTIPAGE.md).
Use `make e2e-comic E2E_ARGS="--skip-services"` when the local Compose services are already running.
The runner supports an explicitly gated live mode for later manual use; CI and hermetic tests exercise only the refusal guards and never start live adapters.


Vector PDF lettering checks require local `pdftoppm` and `pdftotext` from
`poppler-utils` (installed explicitly in the AI and E2E CI jobs). `make test-ai`
checks serialized geometry, independent raster comparison, Unicode extraction,
subset-font overhead, review notes and legacy streams. `make test-e2e` exercises
both the unchanged baked flow and the opt-in overlay flow through stored manual
placement, PDF export and selectable text. Set `TEST_DATABASE_URL` to a migrated,
seeded loopback PostgreSQL instance to repeat the fake E2E with durable storage.
No PDF test invokes a live provider.
