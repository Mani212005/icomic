.PHONY: all test lint test-contracts test-backend test-ai test-frontend dev clean

all: lint test

test-contracts:
	uv run --with pyyaml --with jsonschema python3 contracts/validate_test.py

test-backend:
	( cd backend && go test -v -race ./... )

test-ai:
	( cd ai && uv run pytest -v )

test-frontend:
	( cd frontend && npm test )

test: test-contracts test-backend test-ai test-frontend

lint-backend:
	( cd backend && go vet ./... )

lint-ai:
	( cd ai && uv run ruff check . && uv run ruff format --check . )

lint: lint-backend lint-ai test-contracts

dev:
	docker compose -f deploy/docker-compose.yml up --build

clean:
	rm -rf ai/.pytest_cache ai/.venv frontend/node_modules frontend/.svelte-kit frontend/build
