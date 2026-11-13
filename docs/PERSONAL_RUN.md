# Running Icomic on your own machine

Icomic is a personal project: one person, one machine, no hosting. This page is what a
local run needs, what survives a restart, how paid calls are capped, and how to back up.

## What it needs

- Docker with the Compose plugin. Nothing else is installed on the host.
- Free ports: `5173` (app), `8080` (API), `8000` (AI service, loopback only), `5433`
  (PostgreSQL) and `6379` (Redis).
- No credentials for the default run. Adapters default to the fakes: deterministic text
  and fixed test images, no network and no cost.

```sh
make dev            # docker compose -f deploy/docker-compose.yml up --build
```

Open `http://localhost:5173`. Dev sign-in is on in this stack, so keep it on a machine only
you use. Stop with Ctrl-C; `docker compose -f deploy/docker-compose.yml down` keeps your data.

## Using the real Meta models

Keep the settings in a file outside the repository, or in `deploy/.env` (ignored by git),
and start Compose with `--env-file`:

```
ADAPTER_LANGUAGE=meta
ADAPTER_VISION=meta
ADAPTER_IMAGE=meta
META_API_KEY=...
```

The adapter switch, pricing flags and rollback are in [META_SWAP.md](META_SWAP.md).
Paid calls are bounded three ways, so a mistake costs a known amount:

| Limit | Value | Where it is enforced |
| --- | --- | --- |
| Images per book | 40 | Go API, per project |
| Images per day | 80 | Go API, per user |
| Provider spend cap | 2 USD, lifetime | AI service, before each call is sent |

The stop switch (`KILL_SWITCH=true` on the API) halts new paid generation only. Finished
pages, downloads and exports still work. Cancel is available while a job is pending.

### The spend cap

`PROVIDER_SPEND_CAP_USD` (default `2`, the most it accepts) turns on the provider cost
ledger. It reserves the worst-case price of a call *before* the request leaves, replaces
the reservation with the observed usage afterwards, and keeps a call whose outcome is
unknown at its full reservation. When the next call would pass the cap it is refused
without being sent, and the editor says "Spend cap reached".

- The header shows the running total against the cap, for example `$0.37 / $2.00`, and the
  amount left on hover. It is hidden when the cap is off. The numbers come from
  `provider_spend` in `GET /api/v1/me/credit-usage`.
- The ledger is `/var/lib/icomic/provider-costs.json` on the `ai_data` volume: every
  call's stage, job id, status and tokens or image count, and never a prompt.
- To lower or raise the cap, change the variable and restart. Spend so far is kept.
- To start a fresh budget, stop the stack and move the ledger file aside. Keep the old
  file as the record of what was spent.
- Set `PROVIDER_SPEND_CAP_USD=` (empty) to turn the cap off.
- A capped E2E run (`ICOMIC_E2E_LIVE=1`) keeps its own per-run ledger and ignores this.

## What survives a restart

| Data | Where | Survives `down` / restart |
| --- | --- | --- |
| Projects, pages, credits, jobs | PostgreSQL, `postgres_data` volume | Yes |
| Generated images, PDFs | AI service disk, `ai_data` volume (`ADAPTER_STORAGE=local`) | Yes |
| Spend ledger | `ai_data` volume | Yes |
| Queue (only with `ADAPTER_QUEUE=redis`) | Redis, `redis_data` volume | Yes |
| Anything under `down -v` | all of the above | **No**, `-v` deletes the volumes |

Before `ADAPTER_STORAGE=local`, images lived in the AI service's memory and an AI restart
left database rows pointing at images that no longer existed. If your database still holds
such rows, those pages need regenerating. New images are safe.

## Readiness and logs

- `GET /healthz` says the process is up. `GET /readyz` says it can work: the API checks
  PostgreSQL and the AI service, the AI service checks its storage volume and its queue
  (Redis when `ADAPTER_QUEUE=redis`). Compose
  waits on `/readyz`, so a stack that reports healthy can take a request.
- `docker compose -f deploy/docker-compose.yml logs ai backend` shows failed generations as
  one line each, with the same `job_id` in both services and a stable `code`, for example
  `generation failed job_id=... code=SPEND_CAP_REACHED`. Prompts and provider bodies are
  never logged. A failed refund or status write names itself on that line.

## Backing up

Volume names are prefixed with the Compose project, `deploy` for this stack. List them with
`docker volume ls`. With the stack running (PostgreSQL) or stopped (files):

```sh
# Database: a plain SQL dump
docker compose -f deploy/docker-compose.yml exec -T postgres pg_dump -U icomic icomic > icomic-db.sql

# Images, PDFs and the spend ledger: a tarball of the ai_data volume
docker run --rm -v deploy_ai_data:/data:ro -v "$PWD":/backup alpine \
  tar czf /backup/icomic-ai-data.tgz -C /data .
```

Back both up together: the database holds URLs, and the volume holds the files behind them.

Restore into a stopped, empty stack:

```sh
docker compose -f deploy/docker-compose.yml up -d postgres
docker compose -f deploy/docker-compose.yml exec -T postgres psql -U icomic icomic < icomic-db.sql
docker run --rm -v deploy_ai_data:/data -v "$PWD":/backup alpine \
  tar xzf /backup/icomic-ai-data.tgz -C /data
docker compose -f deploy/docker-compose.yml up -d
```

## Checking your changes

`make check` runs the lint, test and build checks. `make check-ci` runs everything CI runs
on top of it. See [CI.md](CI.md) for what each covers.
