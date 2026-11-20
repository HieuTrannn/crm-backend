# Load test (NFR-PERF-01)

`load.js` ramps from 10 to 200 requests/second over 60 seconds, holds 200 req/s for
60 seconds, and fails if the 95th percentile of read requests is 100 ms or more.
Reads are spread evenly over: list contacts, search contacts (`q=nguyen`), get one
deal, and the pipeline dashboard.

Every piece runs in Docker Compose, on a private network (nothing is published on
the host, so a local PostgreSQL on port 5432 is not affected):

| Service | Role |
| --- | --- |
| `lt-db` | PostgreSQL 17 |
| `lt-migrate` | goose: applies `db/migrations` |
| `lt-seed` | `seed -users 200 -contacts 100000`: 2 demo tenants, 200 member accounts, 100,000 contacts in Acme Demo |
| `lt-api` | the API image built from the `Dockerfile` (distroless) |
| `k6` | runs `load.js` |

## Run

```bash
docker build -t crm-backend:local .        # Compose v5 builds through Bake, which asks for extra privileges
docker compose --profile loadtest run --rm k6   # starts db, migrate, seed, api in order, then k6
docker compose --profile loadtest down -v       # remove containers and data
```

Or all three steps: `make loadtest`. Use `run`, not `up --exit-code-from k6`: that flag
stops every container as soon as one exits, including the migration and seed steps
that are meant to finish.

The exit code is 0 when every threshold passes. The summary at the end lists
`http_req_duration{kind:read}` with its p(95).

## Why 200 accounts

The API allows 100 requests per minute per user (NFR-SEC-02). With one account
per virtual user, 200 req/s over 200 users is about 60 requests per minute each,
so the test measures the API with the rate limit enabled, as in production.
Logins (bcrypt, slow by design) are tagged `kind:login` and excluded from the
latency threshold.
