# Icomic Frontend

Svelte SPA for Icomic.

## Features

- Multi-stage comic book editor (Script -> Characters -> Chapters -> Draft -> Line Art -> Colour -> Export).
- Client-side Canvas colour editor with paint-bucket flood-fill tool and undo history.
- API client interfacing with the Go backend API.

## Running Locally

To start the local development server:

```bash
cd frontend
npm run dev
```

The application runs on `http://localhost:5173`.

## Testing

Run unit tests for client-side components:

```bash
cd frontend
npm test
```
