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

## Configuration

| Variable | Default | Use |
|---|---|---|
| `PUBLIC_API_URL` | `http://localhost:8080` | Go API URL |
| `PUBLIC_USE_MOCK_API` | `false` | Use the offline mock |
| `PUBLIC_DEV_MODE` | `false` | Enable local identity |
| `PUBLIC_DEV_USER_ID` | fixed demo UUID | Dev identity |
| `PUBLIC_AUTH_TOKEN` | empty | Bearer token |
