# FX Quotes Service

Asynchronous currency exchange rate quotes service written in Go.
A client requests a quote update for a currency pair — the service accepts
the request, returns an identifier, and performs the update in the background
via workers. The result is available by identifier or as the latest quote
for a pair.

Supported pairs: EUR/USD, USD/EUR, EUR/MXN, MXN/EUR, USD/MXN, MXN/USD.

## Requirements

- Go 1.24+
- PostgreSQL 16+
- API key from [exchangeratesapi.io](https://exchangeratesapi.io/)

## Quick start (Docker)

```bash
cp .env.example .env
# Set a real API key in .env (FX_API_KEY)
make docker-up
```

Service: `http://localhost:8080`. PostgreSQL and migration are set up automatically.

## Running locally

```bash
# 1. Create DB and apply migration
createdb quotes
psql quotes < db/init/001_init.sql

# 2. Configure
cp .env.example .env
# Set a real API key in .env (FX_API_KEY)
# Set DATABASE_URL for your local PostgreSQL

# 3. Run
make run
```

`make run` automatically loads variables from `.env`.
`DATABASE_URL` is a standard libpq connection string. If PostgreSQL requires
authentication: `postgres://user:password@localhost:5432/quotes?sslmode=disable`.

## Build and tests

```bash
make build            # binary in dist/
make test             # unit tests
make test-integration # integration tests for repository (requires Docker)
make lint             # golangci-lint (requires installation)
```

## API

### Request a quote update

```bash
curl -X POST http://localhost:8080/api/v1/quotes/update \
  -H 'Content-Type: application/json' \
  -d '{"pair":"EUR/MXN"}'
# → 202 {"update_id":"<uuid>","status":"pending"}
```

With an idempotency key:

```bash
curl -X POST http://localhost:8080/api/v1/quotes/update \
  -H 'Content-Type: application/json' \
  -d '{"pair":"EUR/MXN","request_id":"abc-123"}'
# Reusing the same request_id with a different pair returns 409 Conflict
```

### Get update status

```bash
curl http://localhost:8080/api/v1/quotes/update/<update_id>
# → 200 {"id":"...","pair":"EUR/MXN","status":"done","price":"20.37","created_at":"...","updated_at":"..."}
```

### Get latest quote

```bash
curl 'http://localhost:8080/api/v1/quotes/latest?pair=EUR/MXN'
# → 200 {"pair":"EUR/MXN","price":"20.37","updated_at":"..."}
```

### Health check

```bash
curl http://localhost:8080/health
# → 200 {"status":"ok","db":"up"}
# When DB is unavailable: 503 {"status":"unavailable","db":"down"}
```

## Configuration

| Variable | Description | Default |
|---|---|---|
| `DATABASE_URL` | PostgreSQL connection string | `postgres://user:pass@localhost:5432/quotes?sslmode=disable` |
| `DB_MAX_OPEN_CONNS` | Max open DB connections | `25` |
| `DB_MAX_IDLE_CONNS` | Max idle DB connections | `5` |
| `DB_CONN_MAX_LIFETIME_S` | Connection max lifetime (seconds) | `300` |
| `FX_API_KEY` | API key (required) | — |
| `FX_API_URL` | Base URL for rates API | `https://api.exchangeratesapi.io/v1` |
| `FX_API_TIMEOUT_MS` | HTTP timeout for FX API calls (ms) | `5000` |
| `HTTP_ADDR` | HTTP server address | `:8080` |
| `WORKER_COUNT` | Number of workers | `1` |
| `RETRY_MAX_ATTEMPTS` | Max attempts to fetch a rate | `3` |
| `RETRY_BASE_DELAY_MS` | Base retry delay (ms) | `500` |
| `POLL_INTERVAL_MS` | DB polling interval for workers (ms) | `1000` |
| `BATCH_SIZE` | Number of tasks per worker batch | `10` |
| `JOB_TIMEOUT_MS` | Batch processing timeout (ms) | `30000` |
| `MAX_CLAIM_ATTEMPTS` | Max claim-level retries before failing a task | `5` |
| `STALE_AFTER_MS` | Time before a claimed task becomes reclaimable (ms, 0 = 2×JOB_TIMEOUT_MS) | `0` |

## Documentation

- OpenAPI specification: [`api/openapi.yaml`](api/openapi.yaml)
- Architecture and decisions: [`DESIGN.md`](DESIGN.md)
