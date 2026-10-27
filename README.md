# live-score

A Go multi-sport scores API. It ingests data from sports-data providers
(currently SportScore), normalizes it into canonical models, and serves its own
API with Redis caching, PostgreSQL persistence, and WebSocket updates.

Module path: `github.com/samwilson0745/live-score`.

## Run it

### Quick start (no database)

```bash
docker run -d --name live-score-redis -p 127.0.0.1:6379:6379 redis:7-alpine   # optional
go run ./cmd/api
curl 'localhost:8080/api/v1/matches?sport=football&limit=5'
```

Without `DATABASE_URL` the API calls the provider on demand, behind a 30 s
cache-aside Redis cache. Without Redis it still works (`X-Cache: BYPASS`).

### Full stack (Redis + PostgreSQL + ingestion + WebSockets)

```bash
docker compose -f deployments/compose.yaml up --build
```

With `DATABASE_URL` set, the API runs migrations and serves the **stored** copy;
live requests never wait on the provider. `INGEST_ENABLED=true` runs the worker
inside the API process (needed for WebSocket fan-out). `cmd/ingest` runs the
same worker as a separate process (no WebSocket fan-out, cache is invalidated).

## Endpoints

| Endpoint | Notes |
|---|---|
| `GET /health` | liveness |
| `GET /ready` | readiness (database ping when configured) |
| `GET /metrics` | Prometheus; keep it off the public internet |
| `GET /api/v1/matches?sport=&limit=&status=` | `sport` default `football`, `limit` 1–100 default 5, `status` one of `scheduled,live,finished,postponed,cancelled,unknown`. `X-Cache: HIT\|MISS\|BYPASS`. In provider mode `status` filters after the upstream page is fetched. |
| `GET /api/v1/matches/{id}` | database mode only |
| `GET /api/v1/competitions/{id}/matches?limit=` | database mode only |
| `GET /api/v1/ws?match_id=a&match_id=b` | database mode only; up to 50 IDs |

Errors are `{"error": "..."}`: `400` bad input, `502` provider failure,
`503` load-shed (`Retry-After: 1`), `504` timeout.

Scores are `null` when unknown and `0` when really zero. Times are RFC 3339 UTC.

### WebSocket protocol

On connect the server sends a `snapshot` message per known match, then an
`update` message whenever stored data changes:

```json
{"type":"update","match":{"id":"m_…","version":3,"home_score":1, "…":"…"}}
```

Delivery is in-process and **not durable**. Drop any message whose
`match.version` is not newer than what you hold; after a reconnect the snapshot
recovers anything missed. A client that falls behind is disconnected.
Set `WS_ALLOWED_ORIGINS` (comma-separated host patterns) for browser clients on
other origins.

## Configuration

See [.env.example](.env.example). Everything is optional.

## Layout

```
cmd/api            HTTP server (graceful shutdown on SIGINT/SIGTERM)
cmd/ingest         standalone ingestion worker
cmd/mockprovider   fake provider for load tests
internal/domain    canonical models (Match, Team, Competition, statuses, IDs)
internal/ports     interfaces: provider, cache, repository, publisher
internal/application   list service (cache-aside, singleflight, load shedding), reconciliation
internal/provider/sportscore   HTTP client + mapping to domain
internal/cache     Redis (jittered TTL, timeouts, SCAN-based invalidation) + circuit breaker
internal/storage   memory and postgres repositories, shared contract tests
internal/ingest    ticker worker: bounded retries, backoff+jitter, Retry-After, no overlap
internal/realtime  in-process pub/sub hub
internal/transport/http   router, handlers, middleware (request ID, logging, metrics, recover)
migrations         embedded SQL migrations
tests/k6           load tests
```

## Testing

```bash
gofmt -l .
go vet ./...
go test -race ./...
```

No test calls the real provider. PostgreSQL integration tests are skipped unless
you point them at a **disposable** database (they drop tables):

```bash
docker run -d --name pgtest -e POSTGRES_USER=live -e POSTGRES_PASSWORD=live -e POSTGRES_DB=live_test -p 127.0.0.1:55432:5432 postgres:16.4-alpine
TEST_DATABASE_URL='postgres://live:live@127.0.0.1:55432/live_test?sslmode=disable' go test ./internal/storage/postgres
```

## Load testing

Load-test this API, not the provider. Use the mock:

```bash
go run ./cmd/mockprovider -addr :9090 -delay 100ms &
SPORTSCORE_BASE_URL=http://localhost:9090 go run ./cmd/api &
k6 run -e SCENARIO=warm tests/k6/matches.js   # warm | coldburst | hotkey | manykeys | spike
```

For "provider slow/unavailable" restart the mock with `-delay` or `-fail 0.5`;
for "Redis unavailable" stop Redis. Thresholds in the script are placeholders:
set them from your own baseline and record machine, cache state, duration and
concurrency with every result. Compare `provider_requests_total` in `/metrics`
(or the mock's `/calls`) to the request count to see coalescing work.

## Design notes

- **Ports and adapters.** Handlers and the service depend on `internal/ports`;
  SportScore, Redis and PostgreSQL are adapters.
- **Match identity.** Our match ID is derived from the first provider reference;
  provider IDs live in `match_provider_refs`, never as the public ID.
- **No stale overwrites.** An upsert with an older `provider_updated_at` is
  ignored; only material changes bump `version` and trigger invalidation/publish.
- **Redis failure.** Each cache call has a 500 ms timeout; three consecutive
  failures open a circuit for 10 s so a dead Redis does not slow every request.
  Provider/storage loads are capped (`MAX_PROVIDER_CONCURRENCY`), excess is shed
  with 503, and concurrent misses for one key share a single load.
- **Second provider.** Not added: the roadmap requires verifying coverage, terms,
  quotas and attribution first. `application.Reconcile` and `SameEvent` implement
  the priority/freshness/conflict rules and are tested, but are not yet wired in.
  A new provider only needs to satisfy `ports.SportsProvider`.
- **Not implemented:** stale-while-revalidate, OpenTelemetry tracing, a
  `provider_observations` table (so no retention policy yet), Kubernetes.

## Provider terms

SportScore data is used through its public widget API. Confirm its terms,
attribution requirements, quotas and permitted use before any production or
public deployment.
