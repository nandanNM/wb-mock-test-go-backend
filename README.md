# Go Backend Template (Production-Ready)

A production-ready Go HTTP backend with **no framework overkill**: standard
library `net/http` + Go 1.22 routing + `log/slog`, plus `pgx` for PostgreSQL and
a Google OAuth + session authentication system. Third-party dependencies are
kept minimal: the Postgres driver (`pgx`), `golang.org/x/oauth2`, `go-oidc`, and
`golang-jwt`.

## Features

| Concern | Implementation |
|---|---|
| **Structured logging** | `log/slog` — JSON in production, human text in dev |
| **Request IDs** | Generated per request (or propagated from inbound `X-Request-ID`), echoed in the response header, attached to every log line |
| **Request/response logging** | One structured line per request with status, byte count, **latency + latency_ms**, and RFC3339Nano timestamps |
| **Structured errors** | Typed `APIError` → consistent JSON envelope; internal causes logged, never leaked |
| **PostgreSQL** | `pgxpool` connection pool via `DATABASE_URL` — works with Neon, Supabase, RDS, etc. |
| **Migrations** | Embedded SQL migrations, applied on startup, tracked in `schema_migrations` |
| **Panic recovery** | Unhandled panics → logged 500, server stays up |
| **Graceful shutdown** | In-flight requests finish; DB pool closed on SIGINT/SIGTERM |
| **CORS** | Configurable allowed origins + preflight handling |
| **Security headers** | `nosniff`, `X-Frame-Options`, `Referrer-Policy`, COOP |
| **Per-request timeout** | Context deadline propagated to handlers and the DB driver |
| **Health/readiness** | `/healthz` (liveness) + `/readyz` (pings the DB) |
| **Authentication** | Google OAuth (OIDC, PKCE) → EdDSA access tokens + rotating, hashed refresh tokens. See [Authentication](#authentication). |
| **Sessions / devices** | Per-device sessions, list devices, sign out one or all |
| **RBAC** | roles + permissions + role/permission middleware |
| **Audit log** | Append-only auth events (login, refresh, reuse, ban, role change) |
| **Account status** | Ban / suspend / reinstate, enforced within seconds via a status cache |
| **Config** | 12-factor env vars with defaults; auto-loads `.env` for local dev |
| **Docker** | Multistage build → distroless non-root image |
| **CI** | GitHub Actions: fmt check, vet, race tests, build |
| **Tests** | Unit: config, error mapping, token verify/expiry, CSRF, RBAC. Integration (DB-gated): concurrent-refresh CAS + reuse detection |

## Layout

```
backend/
├── cmd/
│   ├── server/main.go              # entrypoint: wiring + graceful shutdown
│   └── genkeys/main.go             # `make gen-keys` — print signing keys
├── internal/
│   ├── config/                     # env config (+ .env loader) and tests
│   ├── logger/                     # slog setup + context helpers
│   ├── httpx/                      # JSON envelopes + structured API errors (+ tests)
│   ├── middleware/                 # request ID, logging, recover, CORS, security, timeout, auth, CSRF
│   ├── database/                   # pgxpool connection + embedded migrations
│   │   └── migrations/*.sql        # 0001 users … 0007 mfa_factors (seam)
│   ├── repository/                 # data-access layer (users, identities, sessions, roles, audit, oauth)
│   ├── auth/                       # tokens, status cache, OAuth/OIDC, login pipeline, transport
│   ├── rbac/                       # role/permission expansion + middleware
│   ├── audit/                      # append-only auth event recorder
│   └── handler/                    # routes + handlers (handler.go, auth.go)
├── Dockerfile  .dockerignore
├── .github/workflows/ci.yml
├── Makefile  .env.example  .gitignore  README.md
```

## Setup

1. Create a database with a hosted provider (e.g. [Neon](https://neon.tech)).
2. Copy the env file and paste your connection string:

   ```bash
   cp .env.example .env
   # then set DATABASE_URL in .env, e.g. for Neon:
   # DATABASE_URL=postgresql://USER:PASS@ep-xxxx-pooler.REGION.aws.neon.tech/DB?sslmode=require
   ```

   > Hosted providers require TLS — keep `sslmode=require` in the URL.

3. Run it:

   ```bash
   make run        # or: go run ./cmd/server
   ```

   On startup the app connects to Postgres, runs migrations, then serves on `:8080`.

## Commands

```bash
make run            # run locally (reads .env)
make build          # build ./bin/server with version stamped in
make test           # go test -race ./...
make vet            # go vet ./...
make gen-keys       # print fresh AUTH_JWT_PRIVATE_KEY + CSRF_HMAC_KEY for .env
make docker-build   # build the Docker image
make docker-run     # run the image with --env-file .env
```

## API

Public health checks:

```bash
curl -i  localhost:8080/healthz
curl -i  localhost:8080/readyz                 # 200 if DB reachable, else 503
```

Authenticated calls use a Bearer access token obtained via [Authentication](#authentication):

```bash
curl -s localhost:8080/v1/me        -H "Authorization: Bearer $ACCESS_TOKEN"
curl -s localhost:8080/v1/auth/sessions -H "Authorization: Bearer $ACCESS_TOKEN"
curl -s localhost:8080/v1/users     -H "Authorization: Bearer $ADMIN_ACCESS_TOKEN"   # needs users:read
```

### Response shape

```jsonc
// success
{ "data": { "id": "…", "name": "Ada Lovelace", "email": "ada@example.com", "created_at": "…" } }

// error (note correlated request_id)
{ "error": { "code": "validation_failed", "message": "Validation failed.",
             "details": {"email": "must be a valid email address"}, "request_id": "a1b2…" } }
```

### Example request log (production JSON)

```json
{"level":"INFO","msg":"request completed","request_id":"a1b2…","method":"POST","path":"/v1/users","status":201,"bytes":210,"latency":"8.1ms","latency_ms":8.123,"timestamp":"2026-06-29T10:00:00.124Z"}
```

Filter by `request_id` in your log aggregator (CloudWatch, Loki, Datadog…) to
trace a single request end to end.

## Authentication

Google OAuth login today; the architecture extends to phone/OTP/TOTP 2FA without
a rewrite (identity providers are decoupled from sessions/tokens via the
`identities` table and the `IdentityProvider` interface; the login pipeline
already models an `mfa_required` step).

### Setup

```bash
make gen-keys     # paste AUTH_JWT_PRIVATE_KEY + CSRF_HMAC_KEY into .env
```

Create OAuth credentials in the [Google Cloud Console](https://console.cloud.google.com/apis/credentials),
add `http://localhost:8080/v1/auth/google/callback` as an authorized redirect URI,
and set `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` in `.env`. To seed the first
admin, set `AUTH_BOOTSTRAP_ADMIN_EMAIL` to your Google email — it's granted the
`admin` role on login.

### How tokens work

- **Access token** — EdDSA (Ed25519) JWT, 10 min, carries `user_id, sid, roles`. Sent as `Authorization: Bearer <token>`.
- **Refresh token** — opaque 256-bit value, stored only as a SHA-256 hash, 30 days, **rotated on every use**. Presenting an old token after the grace window → the session is revoked and a `token_reuse_detected` event is logged.
- **Hybrid delivery** — web clients (`X-Client-Type: web`) get the refresh token in an HttpOnly+Secure+SameSite cookie scoped to `/v1/auth`; native clients (`X-Client-Type: native`) get it in the JSON body.
- **Fast revocation** — ban/suspend/logout take effect within `STATUS_CACHE_TTL` (default 20s) without a per-request DB hit; refresh re-checks status against the DB directly.
- **CSRF** — only the cookie-authenticated `POST /v1/auth/refresh` is guarded (HMAC double-submit: `csrf_token` cookie must equal the `X-CSRF-Token` header). Bearer-token requests are CSRF-immune by construction.

### Endpoints

| Method & path | Auth | Purpose |
|---|---|---|
| `GET /v1/auth/google/start` | public | Begin login (302 to Google for web, `{authorization_url}` for native) |
| `GET /v1/auth/google/callback` | public | Exchange code, issue session |
| `POST /v1/auth/refresh` | cookie/body (+CSRF for web) | Rotate refresh token, get new access token |
| `POST /v1/auth/logout` | bearer | Revoke current session |
| `POST /v1/auth/logout-all` | bearer | Revoke all of the user's sessions |
| `GET /v1/me` | bearer | Current user + roles + permissions |
| `GET /v1/auth/sessions` | bearer | List active devices |
| `DELETE /v1/auth/sessions/{id}` | bearer | Sign out a specific device |
| `POST /v1/admin/users/{id}/ban\|suspend\|reinstate` | `users:ban` | Account status |
| `POST /v1/admin/users/{id}/roles` | `roles:manage` | Grant a role (`{"role":"admin"}`) |
| `DELETE /v1/admin/users/{id}/roles/{role}` | `roles:manage` | Revoke a role |
| `GET /v1/admin/audit` | `audit:read` | View the auth audit log |

> The `/v1/users` directory endpoints require the `users:read` permission (admin by default).

### Flow (web SPA)

1. Browser → `GET /v1/auth/google/start?client=web` → redirected to Google.
2. Google → `GET /v1/auth/google/callback` → server sets the refresh + CSRF cookies, redirects to `OAUTH_SUCCESS_REDIRECT`.
3. SPA → `POST /v1/auth/refresh` (cookie + `X-CSRF-Token`) → receives an access token, held in memory.
4. SPA calls APIs with `Authorization: Bearer <access token>`; silently refreshes when it expires.

## Migrations

Add `internal/database/migrations/000N_name.sql` files — they are embedded into
the binary and applied in filename order on startup, each in its own
transaction, recorded in `schema_migrations`. For advanced needs (down
migrations, etc.) point `golang-migrate` or `goose` at the same directory.

## Extending

- Add routes in `internal/handler/handler.go` (`Routes`); wrap with `protected(...)` for auth or `admin("perm", ...)` for RBAC.
- Add a repository in `internal/repository/`; inject it via `handler.Deps` / `handler.New`.
- Add a new auth method (phone/OTP/TOTP) by implementing the `IdentityProvider` seam in `internal/auth/` and adding `identities` rows — sessions, tokens, RBAC, and audit are unchanged.
- Return `httpx.Err*` sentinels (or `httpx.NewAPIError(...)`) for client errors;
  wrap internal causes with `httpx.Wrap(httpx.ErrInternal, err)`.
- Use `logger.FromContext(r.Context())` in handlers — it's pre-tagged with the request ID.
