# Icomic Contracts

This directory contains the shared, versioned source of truth for inter-service communication and event records in Icomic.

## Contents

- `openapi/ai-service.yaml`: Versioned OpenAPI 3.1 specification for the internal HTTP interface between the Go API backend and the Python FastAPI AI service.
- `schemas/job.schema.json`: JSON Schema (Draft 2020-12) for generation job lifecycle events and state records.
- `schemas/credit.schema.json`: JSON Schema (Draft 2020-12) for credit ledger debit, refund, and balance events.

## Validation

Contract validation verifies that all schemas and OpenAPI specifications are valid and syntactically sound.

You can validate the contracts locally using Python or Node tooling:

```bash
# Validate OpenAPI and JSON schemas with python
python3 -m unittest contracts/validate_test.py
```
