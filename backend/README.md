# Icomic Backend API

Go API service for Icomic.

## Responsibilities

- Authentication and session management.
- 18+ age verification gate policy.
- Country geo-blocking policy (blocking India per Meta image API terms).
- Project, chapter, character, panel, and page CRUD.
- Credit ledger tracking and transactional debit/refunds.
- Approval gates preventing premature image generation.
- Dispatching generation jobs to the internal Python AI service.

## Running Locally

To run the Go API server locally:

```bash
cd backend
go run cmd/server/main.go
```

The server listens on `http://localhost:8080`.

## Testing

Hermetic unit tests run with standard Go tooling and require no external services:

```bash
cd backend
go test -v ./...
```
