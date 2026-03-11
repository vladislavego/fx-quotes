# FX Quotes Service — Design Document

## 1. Overview

Asynchronous currency exchange rate quotes service. A client requests a
quote update for a currency pair — the service accepts the request,
returns an identifier, and performs the update in the background. The
result is available by update identifier or as the latest quote for a
pair.

### Requirements

- HTTP JSON API
- Three endpoints: request update, get by ID, latest quote by pair
- Asynchronous processing — the HTTP handler does not perform the update
- Results stored in PostgreSQL
- Limited set of currencies (USD, EUR, MXN — 6 pairs)
- Rate source — external API (exchangeratesapi.io)

---

## 2. Architecture

```
                        ┌──────────────────────────┐
                        │        Client            │
                        └────────────┬─────────────┘
                                     │ HTTP
                        ┌────────────▼─────────────┐
                        │   httpserver (transport)  │
                        └────────────┬─────────────┘
                                     │
                        ┌────────────▼─────────────┐
                        │  QuoteService (app logic) │
                        └────────────┬─────────────┘
                                     │
              ┌──────────────────────▼──────────────────────┐
              │               PostgreSQL                    │
              │  ┌──────────────┐    ┌───────────────────┐  │
              │  │quote_updates │    │      outbox       │  │
              │  └──────────────┘    └────────┬──────────┘  │
              └───────────────────────────────┼─────────────┘
                                              │ poll
                        ┌─────────────────────▼────────────┐
                        │     Worker (background loop)     │
                        └─────────────────────┬────────────┘
                                              │ HTTP
                        ┌─────────────────────▼────────────┐
                        │   exchangeratesapi.io (FX API)   │
                        └──────────────────────────────────┘
```

### Layers

```
cmd/server/main.go          — entry point and dependency wiring
internal/httpserver          — HTTP handlers (transport layer)
internal/service             — QuoteService and Worker (application logic)
internal/repository          — PostgreSQL implementation
internal/fxclient            — external rates API client
internal/domain              — domain types and errors
internal/config              — configuration from environment variables
```

Interfaces follow the Go idiom **"accept interfaces, return structs"**.
Interfaces are defined at the consumer side.

All dependencies are wired in `main.go`.

---

## 3. Data Flow

```
POST /api/v1/quote-updates
  → HTTP handler parses request and validates pair
  → service.RequestUpdate
  → repo.FindOrCreatePending (atomic INSERT + outbox enqueue)
    ↳ with request_id: idempotent (returns existing record for same pair)
    ↳ without request_id: always creates a new update
  → HTTP 202 response with update_id
```

Background processing (Worker.Run loop):

1. `repo.ClaimBatch(limit, staleAfter)`
2. `collectSymbols(pairs)`
3. `fx.GetRates(symbols)`
4. `domain.CrossRate(from, to)`
5. `repo.MarkDone` / `repo.MarkFailed`

`GET /api/v1/quote-updates/{id}` returns the current status of the quote update.

`GET /api/v1/quote-updates/latest?pair=` returns the most recently completed quote (by `updated_at`, not `created_at`).

---

## 4. Design Decisions

### Task Queue: Database Polling with Outbox

The system uses an **outbox table with polling** for task processing.

Advantages:

- Crash-safe
- Atomic enqueue with domain write
- Horizontal worker scaling using `FOR UPDATE SKIP LOCKED`
- No additional infrastructure required

Alternative approaches considered:

| Approach | Pros | Cons |
|---|---|---|
| Go channel | Simple | No crash recovery |
| Redis queue | Scalable | Additional dependency |
| DB polling | Durable | Small polling latency |

DB polling was chosen because it provides durability and simplicity
without introducing external infrastructure.

---

### Domain / Queue Separation

Two tables are used:

- **quote_updates** — domain state
- **outbox** — queue mechanics

Queue metadata (attempts, claimed_at) does not pollute the domain model.
This allows queue mechanics to evolve independently of the domain layer.

---

### Retry Model

Two retry levels are used:

1. **In-process retry** — handles transient network errors with exponential backoff
2. **Claim-level retry** — handles worker crashes or restarts, controlled by `MaxClaimAttempts`

---

### Error Classification

Permanent errors:
- HTTP 4xx (except 429)
- API `success=false`

Transient errors:
- Network failures
- HTTP 5xx
- HTTP 429

Permanent errors fail tasks immediately. Transient errors trigger retries.

---

## 5. Asynchronous Processing

Workers poll the outbox table periodically.

Processing cycle:

1. Claim tasks using `ClaimBatch`
2. Fetch exchange rates in a single API call
3. Compute cross rates
4. Persist results

Context handling:

- `jobCtx` is used for external API calls and computation.
- `dbCtx` is used for database writes (`MarkDone`, `MarkFailed`).

Separating contexts prevents long API timeouts from interfering with
database consistency operations.

Graceful shutdown policy: DB writes use `context.WithoutCancel` and
continue for up to 5 s per task after SIGTERM. FX API calls are
cancelled immediately; their tasks remain in outbox for re-claim.

---

## 6. Database Schema

**quote_updates**

Fields: id, request_id, pair, status, price, error, created_at, updated_at

UNIQUE constraint on `request_id` (when provided) ensures idempotency.

CHECK constraint ensures valid lifecycle: `pending → done | failed`

**outbox**

Fields: update_id, pair, attempts, claimed_at, created_at

The outbox row is deleted only after successful completion of `MarkDone`
or `MarkFailed`.

---

## 7. Invariants

- A quote lifecycle is `pending → done | failed`
- A single pending task can only be claimed by one worker
- No tasks are lost after crashes
- `/latest` returns the most recent completed quote

---

## 8. Operational Considerations

Workers can scale horizontally across service instances.

Polling interval affects queue latency.

Long-running deployments should implement retention or partitioning for
the `quote_updates` table.

Operational monitoring should include metrics such as:

- claimed tasks
- completed updates
- failed updates
- retry counts

---

## 9. Known Limitations

| Limitation | Explanation |
|---|---|
| Worker latency | Depends on polling interval |
| Table growth | quote_updates table grows indefinitely |
| Single service binary | API and workers share the same process |
| External API limits | Rate provider may impose limits |

These limitations are acceptable for the current system scale.
