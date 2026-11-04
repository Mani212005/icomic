.PHONY: all test lint build check guard-hermetic test-contracts test-migrations test-backend test-ai test-frontend test-e2e e2e-comic generate-contract-models check-contract-models install-contract-codegen fmt-backend lint-backend lint-ai lint-frontend build-backend build-frontend dev clean migrate-up migrate-down migrate-seed

OAPI_CODEGEN_VERSION := v2.6.0
DATAMODEL_CODEGEN_VERSION := 0.89.0
RUFF_VERSION := 0.17.3
GO_BIN_DIR := $(shell go env GOBIN)
ifeq ($(GO_BIN_DIR),)
GO_BIN_DIR := $(shell go env GOPATH)/bin
endif
OAPI_CODEGEN := $(GO_BIN_DIR)/oapi-codegen
OPENAPI_SPEC := contracts/openapi/ai-service.yaml
DATAMODEL_CODEGEN_ARGS := --input $(OPENAPI_SPEC) --input-file-type openapi --output-model-type pydantic_v2.BaseModel --target-python-version 3.10 --use-default --field-constraints --use-schema-description --strict-nullable --enum-field-as-literal all --disable-timestamp --formatters ruff-format ruff-check

all: check

guard-hermetic:
	python3 scripts/check_hermetic_test.py
	python3 scripts/check_hermetic.py
	python3 scripts/check_generated_test.py

install-contract-codegen:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)
	uv tool install --with ruff==$(RUFF_VERSION) datamodel-code-generator==$(DATAMODEL_CODEGEN_VERSION)

generate-contract-models:
	$(OAPI_CODEGEN) -config backend/oapi-codegen.yaml -o backend/gen/types.go $(OPENAPI_SPEC)
	datamodel-codegen $(DATAMODEL_CODEGEN_ARGS) --output ai/app/gen/models.py

check-contract-models:
	@tmp=$$(mktemp -d ai/.contract-codegen.XXXXXX); trap 'rm -rf "$$tmp"' EXIT; \
	  $(OAPI_CODEGEN) -config backend/oapi-codegen.yaml -o "$$tmp/types.go" $(OPENAPI_SPEC) && \
	  datamodel-codegen $(DATAMODEL_CODEGEN_ARGS) --output "$$tmp/models.py" && \
	  python3 scripts/check_generated.py --generated-dir "$$tmp"

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

e2e-comic:
	python3 scripts/e2e/comic_e2e.py $(E2E_ARGS)

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
