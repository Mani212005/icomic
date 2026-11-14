# Known Issues in Backend and AI Service

This document records defects identified during end-to-end integration testing (`tests/e2e/`).
Per the task instructions, defects in existing backend or AI code are documented here with exact reproductions rather than modified in this work package, enabling the orchestrator to dispatch targeted fixes.

---

### Issue 1: `FakeStorageAdapter` Hardcodes Port 8000 & `DownloadExport` Fallback Proxy Fails on Ephemeral Ports

- **Status**: Resolved. No known open issues remain in this file.
- **Fix**: `FakeStorageAdapter` and `LocalStorageAdapter` read `STORAGE_BASE_URL` (default `http://localhost:8000/storage/fake`), and `DeriveStorageKey` in `backend/internal/api/handlers.go` strips everything up to `/storage/fake/` before the Go API proxies a download, so an absolute stored URL no longer produces a doubled path and a 404 that fell back to the stub PDF.
- **Original symptom**: with the AI service on an ephemeral port, `DownloadExport` returned a 3-line stub PDF instead of the assembled comic.
- **Tests**: `backend/internal/api/export_download_test.go` covers the key derivation from an absolute stored URL on an ephemeral port. `tests/e2e/conftest.py` still binds a port 8000 forwarder when that port is free; it is harmless and left alone.
