# Deploy the mock frontend to Vercel

The frontend is a browser-only SvelteKit SPA: it has no server routes or server-side data requirements, and all editor stages use the in-browser mock API. The static adapter prerenders the app shell and supplies a `200.html` fallback for client-side routing. It needs no backend, API keys, or environment variables.

Live demo: https://icomic.vercel.app

## Deploy

From the frontend directory on the Mac where you are signed in to Vercel:

```sh
cd frontend
vercel link
vercel --prod
```

Select the existing Vercel project when linking, or create the project in the Vercel dashboard first. Set its Root Directory to `frontend` and leave environment variables unset. The checked-in `vercel.json` sets the install command, demo build command, static output directory, SPA rewrite, security headers, CSP, and immutable asset caching.

## Verify

After deployment, open the production URL and confirm the `Mock API` indicator appears, create or select a comic, and move through all editor stages. The mock state is local to the page session. In browser developer tools, confirm there are no failed requests or console errors. The only network requests should be same-origin page and asset requests.

To verify the same demo build locally:

```sh
cd frontend
npm ci
npm run build:demo
python3 -m http.server 4173 --directory build
```

Open `http://127.0.0.1:4173` to exercise the production static files. The demo build check scans the deploy output and fails if it contains the configured backend URL or API credential variable names.
