.PHONY: all test lint build check guard-hermetic test-contracts test-migrations test-backend test-ai test-frontend test-e2e fmt-backend lint-backend lint-ai lint-frontend build-backend build-frontend dev clean migrate-up migrate-down migrate-seed

all: check

guard-hermetic:
	python3 scripts/check_hermetic_test.py
	python3 scripts/check_hermetic.py

test-contracts:
	uv run --with-requirements contracts/requirements.txt python3 contracts/validate_test.py

test-migrations:
	python3 deploy/validate_migrations_test.py

test-backend:
	( cd backend && go test -v -race ./... )

test-ai:
	( cd ai && uv run --frozen pytest -v )

test-frontend:
	( cd frontend && npm test )

test-e2e:
	( cd ai && uv run --frozen pytest -v -c ../tests/e2e/pytest.ini ../tests/e2e )

test: guard-hermetic test-contracts test-migrations test-backend test-ai test-frontend

fmt-backend:
	( cd backend && test -z "$$(gofmt -l .)" )

lint-backend: fmt-backend
	( cd backend && go vet ./... )

lint-ai:
	( cd ai && uv run --frozen ruff check . && uv run --frozen ruff format --check . )

lint-frontend:
	( cd frontend && npm run check )

lint: lint-backend lint-ai lint-frontend test-contracts test-migrations

build-backend:
	( cd backend && go build -v ./... )

build-frontend:
	( cd frontend && npm run build )

build: build-backend build-frontend

check: lint test build

dev:
	docker compose -f deploy/docker-compose.yml up --build

migrate-up:
	docker compose -f deploy/docker-compose.yml exec -T postgres psql -U icomic -d icomic -f - < deploy/migrations/000001_initial_schema.up.sql

migrate-down:
	docker compose -f deploy/docker-compose.yml exec -T postgres psql -U icomic -d icomic -f - < deploy/migrations/000001_initial_schema.down.sql

migrate-seed:
	docker compose -f deploy/docker-compose.yml exec -T postgres psql -U icomic -d icomic -f - < deploy/seeds.sql

clean:
	rm -rf ai/.pytest_cache ai/.venv frontend/node_modules frontend/.svelte-kit frontend/build
