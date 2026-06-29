# Go Backend Template (Production-Ready)

A production-ready Go HTTP backend with **no framework overkill**: standard
library `net/http` + Go 1.22 routing + `log/slog`, plus `pgx` for PostgreSQL.
The only third-party dependency is the Postgres driver.

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
| **Config** | 12-factor env vars with defaults; auto-loads `.env` for local dev |
| **Docker** | Multistage build → distroless non-root image |
| **CI** | GitHub Actions: fmt check, vet, race tests, build |
| **Tests** | Config + error-mapping unit tests |

## Layout

```
backend/
├── cmd/server/main.go              # entrypoint: wiring + graceful shutdown
├── internal/
│   ├── config/                     # env config (+ .env loader) and tests
│   ├── logger/                     # slog setup + context helpers
│   ├── httpx/                      # JSON envelopes + structured API errors (+ tests)
│   ├── middleware/                 # request ID, logging, recover, CORS, security, timeout
│   ├── database/                   # pgxpool connection + embedded migrations
│   │   └── migrations/*.sql
│   ├── repository/                 # data-access layer (SQL lives here)
│   └── handler/                    # routes + handlers
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
make docker-build   # build the Docker image
make docker-run     # run the image with --env-file .env
```

## API

```bash
curl -i  localhost:8080/healthz
curl -i  localhost:8080/readyz                 # 200 if DB reachable, else 503

# Create a user
curl -s -X POST localhost:8080/v1/users \
  -H 'Content-Type: application/json' \
  -d '{"name":"Ada Lovelace","email":"ada@example.com"}'

curl -s localhost:8080/v1/users                # list (newest first, ?limit=)
curl -i localhost:8080/v1/users/<id>           # 200, or structured 404
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

## Migrations

Add `internal/database/migrations/000N_name.sql` files — they are embedded into
the binary and applied in filename order on startup, each in its own
transaction, recorded in `schema_migrations`. For advanced needs (down
migrations, etc.) point `golang-migrate` or `goose` at the same directory.

## Extending

- Add routes in `internal/handler/handler.go` (`Routes`).
- Add a repository in `internal/repository/`; inject it via `handler.New`.
- Return `httpx.Err*` sentinels (or `httpx.NewAPIError(...)`) for client errors;
  wrap internal causes with `httpx.Wrap(httpx.ErrInternal, err)`.
- Use `logger.FromContext(r.Context())` in handlers — it's pre-tagged with the request ID.
