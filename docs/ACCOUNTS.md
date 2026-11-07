# Accounts

Icomic has two kinds of account, both stored in the product's own PostgreSQL database. No outside identity provider, email service or paid service is involved.

| | Guest | Registered |
|---|---|---|
| Identity | Browser-bound session cookie, no email or password | Email and password |
| Projects | Exactly 1 | Up to a configurable ceiling, default 100 |
| How to become one | Open the app and create a project | Sign up (a guest becomes the account in place) or sign in |

Any identity without a registered account is a guest. The rule is enforced in the Go backend. The UI only reflects it.

## Project limit

- `account_policy` is a singleton row: `guest_max_projects` (default 1) and `registered_max_projects` (default 100). Operators change it with SQL and it applies on the next request, with no deployment.
- `CreateProjectForAccount` counts and inserts in one transaction under a per-user advisory lock (`pg_advisory_xact_lock(hashtextextended('icomic:projects:' || user_id, 0))`). Many simultaneous creates by one guest produce exactly one project.
- A refused create returns HTTP 403 with `code: project_limit_reached`, a sanitised `message`, and `details: {account_kind, limit}`.
- Deleting a project frees its slot, because the rule counts live projects.
- `Repository.CreateProject` is the unrestricted seeding primitive. Request handlers must call `CreateProjectForAccount`; a test fails if non-test API code calls `CreateProject`. There is no import endpoint today, so any future one must use `CreateProjectForAccount` too.
- The dev identity (`DEV_AUTH_ENABLED=true`, header `X-User-ID`) is mapped to `registered` and is exempt from the limit, so local and e2e runs are never throttled.

### Flipping a user

The kind that counts for a request comes from the authenticator seam (`auth.IdentityAuthenticator`). Cookie sessions read `users.account_kind`. To promote or demote a user by hand:

```sql
UPDATE users SET account_kind = 'registered' WHERE email = 'person@example.com';
```

A registered user must have an email and a password hash (a database constraint). Raise or lower the limits with:

```sql
UPDATE account_policy SET guest_max_projects = 1, registered_max_projects = 100;
```

Migration `000011_accounts` makes every existing user a guest except the seeded dev user (`00000000-0000-0000-0000-000000000001`), which becomes registered. `deploy/seeds.sql` creates the dev user as registered too, for fresh databases where seeds run after the migrations.

## Endpoints

All live under `/api/v1/auth` and are described in `contracts/openapi/ai-service.yaml`.

| Route | Purpose |
|---|---|
| `GET /me` | Current account, or `{"account": null}` when anonymous. Includes `csrf_token` for cookie sessions. |
| `POST /guest` | Start a guest session. Returns the existing session if there is one. |
| `POST /signup` | Create an account. A guest session is upgraded in place, so its project is claimed. |
| `POST /signin` | Sign in. A guest session's projects are attached to the account. |
| `POST /signout` | Revoke the session on the server and clear the cookie. |

Stable error codes: `invalid_request`, `invalid_email`, `weak_password`, `email_taken`, `invalid_credentials`, `login_rate_limited` (with `Retry-After`), `origin_not_allowed`, `csrf_invalid`, and `project_limit_reached`.

### Claiming a guest project

- Sign-up converts the guest user row into the account (`RegisterGuest`), so nothing moves and the claim is atomic.
- Sign-in moves the guest's projects to the account (`ClaimGuestProjects`) in one transaction, oldest first, never beyond the registered ceiling. Both users' locks are taken in a fixed order. Anything that does not fit stays with the guest.
- It never moves projects out of a registered account and is idempotent: a second claim finds nothing to move.
- Credit ledger and image usage rows stay with the guest user. Book budgets follow the project.

## Sessions, cookies and CSRF

- The session token is 256 random bits. Only its SHA-256 is stored (`sessions.token_hash`), so a database read cannot replay a session. Sessions can be revoked server-side (`revoked_at`) and expire (30 days registered, 365 days guest). Sign-up and sign-in rotate the session.
- Cookie `icomic_session`: `HttpOnly`, `SameSite=Lax`, `Path=/`, and `Secure` everywhere except loopback hosts (`localhost`, `127.0.0.1`, `::1`).
- CSRF has three layers. Cookies are `SameSite=Lax`. Account routes refuse a browser `Origin` that is not in `CORS_ALLOWED_ORIGIN` and require `Content-Type: application/json`. Every state-changing request authenticated by the cookie must send the per-session `X-CSRF-Token` returned by `/me`, `/guest`, `/signup` and `/signin`. The client keeps it in memory only. Requests authenticated by headers (the dev identity) carry no cookie, so they are not subject to CSRF.
- Deployment requirement: the app and the API must be same-site (for example `app.example.com` and `api.example.com`), because `SameSite=Lax` cookies are not sent on cross-site fetches. Set `CORS_ALLOWED_ORIGIN` to the exact app origin; credentials are only allowed for an exact match.

## Passwords

- argon2id, PHC-encoded in `users.password_hash`: memory 32 MiB, 3 passes, 1 lane, 16-byte salt, 32-byte key. That is above the OWASP minimum of 19 MiB with 2 passes. The parameters are stored in each hash, so they can be raised later without invalidating old hashes. A rehash-on-login upgrade is a follow-up.
- At most 4 hashes run at once (peak about 128 MiB), so a login burst cannot exhaust memory.
- Policy: at least 10 characters, at most 256 bytes, not equal to the email. No composition rules.
- Sign-in does the same hashing work whether the email is unknown, belongs to a guest, or has the wrong password, and returns the same response, so timing and body reveal nothing. Malformed or foreign hashes never verify.

## Rate limits

Process-local fixed windows with bounded memory. When the table is full of live keys it refuses new keys rather than growing.

| Limit | Window | Counted |
|---|---|---|
| 5 failed sign-ins per submitted email | 15 minutes | Reset by a successful sign-in; applies equally to unknown emails |
| 20 failed sign-ins per client IP | 15 minutes | |
| 10 sign-ups per client IP | 1 hour | Every attempt |
| 20 guest sessions per client IP | 1 hour | Every attempt |

The client IP is the TCP peer address. `X-Forwarded-For` is deliberately untrusted, like the existing API admission limiter.

## Threat notes

- **Credential stuffing and brute force**: per-email and per-IP limits, argon2id cost, uniform failure.
- **Account lockout by an attacker**: five bad attempts lock one email for 15 minutes for everyone. Accepted trade-off while there is no email channel to recover.
- **Account enumeration**: sign-in is uniform. Sign-up returns `email_taken`, which does enumerate registered emails. Without email verification there is no way around it, so it is rate limited per IP.
- **Session theft**: the cookie is `HttpOnly`, so scripts cannot read it. Only a hash is stored. Sign-out revokes server-side.
- **Guest abuse**: every guest row has the normal starting credits and image quotas. Guest creation is limited per IP, and the global kill switch and spend threshold remain the backstop.
- **Behind a reverse proxy** every client shares the proxy address, so the per-IP limits become global. Add a trusted-proxy setting before deploying that way.
- **In-memory limits** are per process. Run one API instance, or move the limiter into PostgreSQL before scaling out.
- **Memory mode** (no `DATABASE_URL`) supports guests and sign-up, but the in-memory credit service gives new users no credits. Use the dev identity there.

## Not implemented (captain decisions)

- Password reset and email verification. No email is sent yet, so a forgotten password cannot be recovered.
- Account deletion and data export.
- Retention: clearing expired and revoked sessions, and abandoned guest rows.
- Moving credit ledger and image usage with a claimed project.
- Listing and revoking other sessions, and revoke-all on password change (there is no password change yet).
- Rehash-on-login when the argon2id parameters are raised.
- A trusted-proxy client IP setting and a shared (PostgreSQL) limiter.
- Plans and billing (`users.plan_id` is unchanged).

## Tests

- Hermetic: `backend/internal/auth` (hashing, policy, identity seam), `backend/internal/accounts` (limiter), `backend/internal/api/accounts_api_test.go` (flows, CSRF, cookies, limits, claim, revoke), `frontend/tests/accounts.test.js` and `frontend/tests/components/loginForm.test.js`.
- Real PostgreSQL (`-tags=realdb`): `backend/internal/db/accounts_realdb_test.go` covers the upgrade, concurrent creates, the advisory lock, claims and sessions.
- Contract: `contracts/validate_test.py` checks that every account error code the Go code emits is in the OpenAPI contract.
