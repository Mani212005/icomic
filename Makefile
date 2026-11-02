.PHONY: all test lint test-contracts test-migrations test-backend test-ai test-frontend lint-frontend dev clean migrate-up migrate-down migrate-seed

all: lint test

test-contracts:
	uv run --with pyyaml --with jsonschema python3 contracts/validate_test.py

test-migrations:
	python3 deploy/validate_migrations_test.py

test-backend:
	( cd backend && go test -v -race ./... )

test-ai:
	( cd ai && uv run pytest -v )

test-frontend:
	( cd frontend && npm test && npm run build )

test: test-contracts test-migrations test-backend test-ai test-frontend

lint-backend:
	( cd backend && go vet ./... )

lint-ai:
	( cd ai && uv run ruff check . && uv run ruff format --check . )

lint-frontend:
	( cd frontend && npm run check )

lint: lint-backend lint-ai lint-frontend test-contracts test-migrations

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
