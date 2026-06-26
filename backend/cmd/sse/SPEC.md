# cmd/sse — standalone read-only SSE server

## Purpose
The public read boundary (`sse.dogring.kr`), split out from the simulation writer (`backend/main.go`)
so it can run with a **read-only** valkey user, **no postgres**, and **no `content/`** — it never
advances a tick or writes a key. It tails the valkey event STREAM the writer appends to and forwards
each entry to clients over `/sse`. Deployed as the `bdg-sse` image (`backend/Dockerfile.sse`),
horizontally scalable (replicas > 1) because it holds no authoritative state.

## Public Interface
A `main` command (no exported API). Configured entirely by environment — no CLI flags:

```
REDIS_ADDR          valkey host:port (required; empty → fatal)
REDIS_DB            logical DB (default 0)
REDIS_RO_USERNAME   read-only valkey user (falls back to REDIS_USERNAME)
REDIS_RO_PASSWORD   password for it       (falls back to REDIS_PASSWORD)
RUN_ID              keyspace prefix — MUST equal the writer's RUN_ID (default "dev")
HTTP_ADDR           listen address (default ":8080")
```

Routes (via `api.NewSSE`): `GET /healthz`, `GET /readyz` (pings valkey), `GET /sse`. Nothing else.

## Dependencies
`platform/api` (`NewSSE`, `Config`, `RedisReader`, `StreamEntry`, `ListenAndServe`), `engine/kernel/core`
(`RunID`), `github.com/redis/go-redis/v9`. It defines its own `redisReadAdapter` (the same shape as
the writer's read adapter; the SSE handler uses only `Ping` + `XRead`).

## Invariants
- **Read-only**: no writes, no postgres, no tick loop, no content load. Credentials default to the
  read-only user (`REDIS_RO_*`); the deployment Secret (`bdg-sse-env`) carries no write/postgres creds.
- **RUN_ID must match the writer** — it tails `sim:{RUN_ID}:events`.
- SIGTERM/SIGINT → graceful `http.Server.Shutdown` (in-flight SSE connections closed).

## Acceptance Criteria (testable)
- With `REDIS_ADDR` unset → exits fatal with a clear message.
- Against a live valkey where the writer is appending events, a `GET /sse` client receives
  `data: {…}\n\n` frames; `/readyz` returns 200 when valkey is reachable, 503 otherwise.
- The SSE forwarding logic itself is covered by `platform/api` tests (`handleSSE`); this command is
  thin wiring over `api.NewSSE`.

## Out of Scope
Snapshot/agent/god-view endpoints (writer's API, `gapi.dogring.kr`); any write path; postgres.
