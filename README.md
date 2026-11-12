# crm-backend

Backend for a multi-tenant mini CRM (contacts, deals, pipeline), written in Go.
Every tenant's data is isolated twice: application queries always filter by `tenant_id`,
and PostgreSQL Row-Level Security blocks other tenants' rows at the database level.

> **Status: week 1 — project skeleton.** Config, logging, health checks, graceful shutdown,
> the first migration (tenants, users, refresh tokens with RLS) and tenant-isolation tests
> are in place. Business features (auth, contacts, deals, dashboard, CSV import) are not
> built yet. See [docs/PRD_SRS.md](docs/PRD_SRS.md) for the full requirements and roadmap.

## Stack

Go 1.27 · Gin · PostgreSQL 17 · pgx v5 · sqlc · goose · log/slog ·
testify · testcontainers-go · golangci-lint · GitHub Actions

No ORM and no DI framework: SQL is written by hand (migrations with goose, queries
compiled to Go by sqlc) and dependencies are wired manually in `cmd/api/main.go`.

## How tenant isolation works

1. Each request runs inside `database.WithTenantTx(ctx, tenantID, fn)`, which opens a
   transaction and runs `set_config('app.tenant_id', <id>, true)` — scoped to that
   transaction only, so a pooled connection never carries a tenant to the next request.
2. Tables have `ENABLE` + `FORCE ROW LEVEL SECURITY` with a `tenant_isolation` policy
   comparing `tenant_id` to that setting. With no tenant set, queries return nothing.
3. The API connects as `app_user`: not the table owner, no `BYPASSRLS`, no `DELETE`.
   Only migrations use the owner role.
4. Login looks users up by email before the tenant is known, through the
   `SECURITY DEFINER` function `auth_find_user_by_email` (pinned `search_path`,
   returns only the columns login needs, executable by `app_user` only).

[test/integration/tenant_isolation_test.go](test/integration/tenant_isolation_test.go)
proves each of these against a real PostgreSQL.

## Project structure

```
cmd/api/              entry point: config, wiring, graceful shutdown
internal/config/      environment variables (+ .env for local dev)
internal/platform/    shared infrastructure: database, httpx (errors, middleware), logger
internal/server/      router and system routes
internal/<feature>/   auth, contact, deal, dashboard, importer (handler → service → repository)
db/migrations/        goose SQL migrations
db/queries/           SQL for sqlc;  db/sqlc/ generated code (do not edit)
test/integration/     integration tests (testcontainers)
api/openapi.yaml      API contract
```

## Getting started

### Prerequisites

- Go 1.27+
- PostgreSQL 17+ (a local install, or Docker with `docker compose up -d --wait postgres`)
- [goose](https://github.com/pressly/goose): `go install github.com/pressly/goose/v3/cmd/goose@v3.30.0`
- Docker, for integration tests and `make sqlc`
- Optional: `make` (on Windows: `winget install ezwinports.make`)

### 1. Create the database

Skip this step when using `docker compose`, which creates the `crm` role and database.
With a local PostgreSQL, as a superuser (e.g. `postgres`):

```sql
CREATE ROLE crm LOGIN PASSWORD 'crm' CREATEROLE;  -- owner, used only by migrations
CREATE DATABASE crm OWNER crm;
```

### 2. Configure

```bash
cp .env.example .env
```

| Variable | Used by | Description |
| --- | --- | --- |
| `PORT` | API | HTTP port (default `8080`) |
| `APP_ENV` | API | `development`, `test` or `production` (default `development`) |
| `DATABASE_URL` | API | Connection as `app_user`; add `&pool_max_conns=10` to size the pool |
| `MIGRATION_DATABASE_URL` | goose | Connection as the owner role, direct (not pooled) |
| `APP_USER_PASSWORD` | first migration | Password given to `app_user`; must match `DATABASE_URL` |
| `JWT_SECRET` | API | At least 32 bytes |
| `FRONTEND_ORIGIN` | API | Frontend URL |

The API and goose both read `.env` automatically; real environment variables take precedence.
`APP_USER_PASSWORD` is read only when the role is first created — to change it later, run
`ALTER ROLE app_user PASSWORD '...'`.

### 3. Migrate and run

```bash
make migrate-up   # goose -dir db/migrations postgres "$MIGRATION_DATABASE_URL" up
make run          # go run ./cmd/api
```

```bash
curl localhost:8080/healthz   # {"status":"ok"}  process is alive
curl localhost:8080/readyz    # {"status":"ok"}  database is reachable (503 otherwise)
```

## Commands

| Command | Description |
| --- | --- |
| `make run` | Start the API |
| `make test` | Unit tests with `-race` (needs cgo; CI runs it on Linux) |
| `make test-integration` | Integration tests in a PostgreSQL 17 container (needs Docker) |
| `make lint` | golangci-lint |
| `make migrate-up` / `make migrate-down` | Apply all / roll back one migration |
| `make sqlc` | Generate Go code from `db/queries` (runs sqlc in Docker) |
| `make db-up` / `make db-down` | Start / stop PostgreSQL in Docker |

## API

| Method | Path | Description |
| --- | --- | --- |
| GET | `/healthz` | Liveness |
| GET | `/readyz` | Readiness (database ping) |

Business endpoints will live under `/api/v1`. Errors use one format:
`{"error":{"code":"UPPER_SNAKE","message":"..."},"request_id":"..."}`, and every response
carries an `X-Request-ID` header. Full contract: [api/openapi.yaml](api/openapi.yaml).
