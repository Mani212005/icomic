# Icomic Frontend

Svelte comic editor with an eight-stage, approval-gated workflow.

## Run

```sh
npm ci
PUBLIC_USE_MOCK_API=true npm run dev
```

The mock client is deterministic and needs no backend. Set `PUBLIC_API_URL` to connect to the Go API.

## Verify

```sh
npm test
npm run check
npm run build
```

## Features

- Projects, scripts, characters, outlines, panels, drafts, line art, colouring, and export.
- Approval tracking invalidates downstream work after edits or regeneration.
- Bucket fill, palette suggestions, undo, and outline gap repair.
- PDF and PNG exports with AI disclosure and C2PA provenance.
- Dev identity or bearer authentication; no identity headers without credentials.

## App shell

- The product is tagged **Beta** in the header, the footer and on the bundled demo art (`BetaTag.svelte`). Only the demo banner still says the art is a sample.
- Stage navigation lives in `Sidebar.svelte`, a reusable overlay panel opened by `Hamburger.svelte` or by resting the pointer on the left screen edge. It closes on pointer leave, Escape or an outside click, never while the pointer is inside or a field has focus, and becomes a modal drawer with a scrim on phones. It overlays rather than pushes so the page never shifts when the pointer brushes the edge.
- `AppHeader.svelte` has three slots: `leading` (hamburger), `status` (pills) and `account`. `account` is reserved for a sign-in or account control: it is the last item on the right, vertically centred and at most 44 px tall. It is empty by default.
- `AppFooter.svelte` always ends the page; the layout is a full-height column so short pages push it to the bottom.
- Scroll effects (`design/scroll.js`) fade and lift cards below the fold and drift the header wash. Both use transform and opacity only and switch off under `prefers-reduced-motion`.

## Configuration

| Variable | Default | Use |
|---|---|---|
| `PUBLIC_API_URL` | `http://localhost:8080` | Go API URL |
| `PUBLIC_USE_MOCK_API` | `false` | Use the offline mock |
| `PUBLIC_DEV_MODE` | `false` | Enable local identity |
| `PUBLIC_DEV_USER_ID` | fixed demo UUID | Dev identity |
| `PUBLIC_AUTH_TOKEN` | empty | Bearer token |
