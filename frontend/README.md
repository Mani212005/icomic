# Icomic Frontend

Svelte SPA frontend for the Icomic comic generation studio (docs/ARCHITECTURE.md section 7).

## Features

- **Multi-Stage Comic Book Editor**:
  - Full approval-gated pipeline: `Script` -> `Characters` -> `Outline` -> `Panels & Pages` -> `Draft Sketches` -> `Final Line Art` -> `Colouring` -> `Export`.
  - Stale approval enforcement upon editing or regenerating assets (`approved_at < updated_at`).
  - Cascading stale invalidations: panel editing invalidates page draft approvals; draft regeneration invalidates line art approvals.
  - Credit cost estimation per stage ($0.01 per attempt for image generation).
- **Project Management**:
  - Project list, creation modal, and switcher UI.
  - Switching projects seamlessly updates the editor and pipeline stages.
- **Client-Side Bucket-Fill Colour Tool**:
  - Visited-on-enqueue flood fill preventing duplicate queue entries and memory blowup.
  - Efficiently handles large canvases (e.g. 1000x1000+ pixels) using zero per-pixel heap object allocations.
  - Fills by configurable tolerance against the clicked pixel colour.
  - Evaluates barriers strictly against the line art ink outline, avoiding false boundaries on dark painted colours in the colour layer.
  - Morphological gap-closing algorithm to bridge anti-aliased line art gaps.
  - Manual bridge brush tool to close visible contour gaps.
  - Client-side canvas undo/redo history (configurable up to 20 actions).
  - AI palette suggestion integration with swatch selection and eyedropper.
- **Contract-Typed API Client & Hermetic Mock**:
  - `ApiClient`: Real HTTP API client connecting to Go backend (`PUBLIC_API_URL`, default `http://localhost:8080`).
  - `MockApiClient`: Full in-memory deterministic implementation selectable only by explicit `PUBLIC_USE_MOCK_API=true` for tests and offline demos.
  - Response unwrapping and normalization across projects, characters, chapters, panels, and pages.
- **Identity & Authentication Handling (SPEC 8.3)**:
  - Supports documented local dev identity via `PUBLIC_DEV_MODE=true` and `PUBLIC_DEV_USER_ID=00000000-0000-0000-0000-000000000001`.
  - When dev mode is configured, sends `X-User-ID: <dev_user_id>`.
  - When bearer token is configured, sends `Authorization: Bearer <token>`.
  - When neither is configured, strictly never sends identity headers and displays a clear unauthenticated state.
- **Robust API Error Handling**:
  - 400 Stale Approval: Clear warnings and actionable re-approval instructions.
  - 503 Kill Switch / Service Unavailable: Alerts users when image generation is halted by platform operator budget rules.
  - 402 / 429 Quota Exceeded: Warns when daily or monthly quotas are reached or credit balance is insufficient.
  - Moderation Rejection: Alerts on AI safety content policy violations.
  - 401 Unauthenticated: Informs the user of missing authentication and provides a one-click dev mode toggle.
- **Job Polling & Progress Tracking**:
  - Asynchronous polling utility (`pollJob`) supporting completion, failure, cancellation, timeouts, and `AbortSignal`.
- **Export & Publishing UI**:
  - PDF document and PNG page bundle export options with page size selection (A4, US Letter, Custom comic page).
  - Mandatory Meta AI model disclosure and C2PA content provenance notices.
- **Policies (Owner Decisions 2026-11-02)**:
  - Global user access: no geo-blocking or regional restriction.
  - Age terms: 18+ advisory notice (enforced gate deferred).
  - Image hosting: configurable CDN/storage URL resolver.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PUBLIC_API_URL` | `http://localhost:8080` | URL of the Go API server |
| `PUBLIC_USE_MOCK_API` | `false` | When `true`, routes all API calls to the in-memory MockApiClient |
| `PUBLIC_DEV_MODE` | `false` | When `true`, enables local development identity matching Go's DevAuthenticator |
| `PUBLIC_DEV_USER_ID` | `00000000-0000-0000-0000-000000000001` | User UUID sent via `X-User-ID` when dev mode is active |
| `PUBLIC_AUTH_TOKEN` | `""` | Bearer token for session authentication |

## Running Locally

To start the local development server:

```bash
cd frontend
npm install
npm run dev
```

The application runs on `http://localhost:5173`.

### Connecting to the Go API in Development

1. Start the Go backend with dev authentication enabled:
   ```bash
   DEV_AUTH_ENABLED=true ./bin/icomic-server
   ```
2. Start the frontend with dev mode configured:
   ```bash
   PUBLIC_DEV_MODE=true npm run dev
   ```
   Or click "Enable Local Dev Identity" directly in the web UI when prompted.

### Running with Offline Mock Client

To run completely hermetically without starting the Go backend:

```bash
PUBLIC_USE_MOCK_API=true npm run dev
```

## Testing & Verification

Run hermetic unit tests without any backend dependencies:

```bash
cd frontend
npm test
```

Run TypeScript and Svelte diagnostics:

```bash
cd frontend
npm run check
```

Build production bundle:

```bash
cd frontend
npm run build
```
