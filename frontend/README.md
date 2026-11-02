# Icomic Frontend

Svelte SPA frontend for the Icomic comic generation studio (docs/ARCHITECTURE.md section 7).

## Features

- **Multi-Stage Comic Book Editor**:
  - Full approval-gated pipeline: `Script` -> `Characters` -> `Outline` -> `Panels & Pages` -> `Draft Sketches` -> `Final Line Art` -> `Colouring` -> `Export`.
  - Stale approval enforcement upon editing or regenerating assets.
  - Credit cost estimation per stage ($0.01 per attempt for image generation).
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
  - `ApiClient`: Implements the full HTTP API contract specified in `docs/SPEC.md` section 10 and `contracts/schemas/`.
  - `MockApiClient`: Full in-memory deterministic implementation allowing development and testing without running Go backend, Postgres, or Redis.
- **Job Polling & Progress Tracking**:
  - Asynchronous polling utility (`pollJob`) supporting completion, failure, cancellation, timeouts, and `AbortSignal`.
- **Export & Publishing UI**:
  - PDF document and PNG page bundle export options with page size selection (A4, US Letter, Custom comic page).
  - Mandatory Meta AI model disclosure and C2PA content provenance notices.
- **Pluggable Policy Seams (Open Captain Decisions)**:
  - 18+ age gate verification seam with leap-year-safe calendar calculation (`DefaultAgeGatePolicy`).
  - Regional location and image generation restriction seam (`DefaultLocationPolicy`).
  - Image hosting and CDN resolver seam (`DefaultImageHostingPolicy`).

## Running Locally

To start the local development server:

```bash
cd frontend
npm install
npm run dev
```

The application runs on `http://localhost:5173`.

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
