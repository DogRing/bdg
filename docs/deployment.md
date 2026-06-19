# Deployment — Topology & Networking (configuration check)

Complements `docs/dev-setup.md` (build/image) and `docs/data-contracts.md` (Redis/PG/event shapes).
This doc verifies the runtime topology you described: backend in a Docker container on k8s,
Redis/Postgres as separate workloads, SSE at `sse.dogring.kr`, other API at `gapi.dogring.kr`,
internal traffic over `*.svc.cluster.local`.

## 1. The single-writer rule (most important — affects everything)
The simulation is **deterministic and stateful with one authoritative timeline** (D12). You **cannot run
N replicas of the tick loop** — each replica would advance its own divergent world.

- **`sim` (writer): exactly one instance.** Runs the tick loop, writes live state to Redis, backs up to Postgres.
  - Deployment `replicas: 1` with **`strategy: Recreate`** (NOT RollingUpdate — a rolling update briefly runs
    two pods = two sims writing the same Redis keyspace). Or a leader-elected setup if you want failover later.
- **Readers (API queries + SSE fan-out): scale freely.** They only *read* Redis (and tail the events stream),
  so they are stateless and horizontally scalable.

> First cut: one binary, `replicas: 1`, does sim + HTTP. Split readers out only when SSE connection load grows.
> When you split, add `backend/cmd/api` (read-mode) alongside `backend/cmd/sim`; both share the image, read the
> same Redis. The sim stays singleton; the readers become a separate scalable Deployment.

## 2. Internal traffic (`cluster.local`)
Already handled by env (see `.env.example`, `docs/dev-setup.md`). On k8s, set via ConfigMap/Secret:
- `REDIS_ADDR = redis.<ns>.svc.cluster.local:6379` (+ `REDIS_PASSWORD` from a Secret)
- `POSTGRES_DSN = postgres://…@postgres.<ns>.svc.cluster.local:5432/bdg?sslmode=…` (Secret)
The backend opens plain in-cluster connections; no public exposure for Redis/Postgres.

## 3. External routing (two hostnames, one backend)
Both hostnames target the same backend Service; the gateway routes by **hostname** (and/or path).
```
sse.dogring.kr   ──HTTPRoute──┐
                              ├──> Service: bdg  (HTTP_ADDR :8080)
gapi.dogring.kr  ──HTTPRoute──┘
```
- `gapi.dogring.kr` → backend API paths (e.g. `/api/…`, `/healthz`).
- `sse.dogring.kr` → backend SSE path (`/sse`) — long-lived `text/event-stream`.
- CORS: the backend allows the frontend origin (`CORS_ORIGINS`) for both hosts.

## 4. SSE through the gateway (verify — common breakage)
Envoy Gateway (and most ingress) **buffer responses and apply idle timeouts that kill SSE.** For the
`sse.dogring.kr` route specifically:
- **Disable response buffering** for that route.
- **Long stream / idle timeout** (SSE connections live for minutes–hours). Default 15–60s timeouts will drop them.
- Backend already sets the right headers (to add when `platform/api` lands): `Content-Type: text/event-stream`,
  `Cache-Control: no-cache`, `Connection: keep-alive`, `X-Accel-Buffering: no`, and **flushes** each event.
- HTTP/2 is fine (multiplexing); ensure the gateway→backend hop keeps the stream unbuffered.

On Envoy Gateway this is a `BackendTrafficPolicy` / `ClientTrafficPolicy` (timeouts) targeting the SSE route.

## 5. Probes & graceful shutdown
- Liveness `/healthz`, readiness `/readyz` (readiness should check Redis reachability).
- Backend handles `SIGTERM`: stop the tick loop cleanly, flush a final snapshot, close SSE connections.
  Set a generous `terminationGracePeriodSeconds`.

## 6. Image & CI
- **Dockerfile only** (per your setup) — the GitHub workflow builds and pushes. Image is multi-stage →
  `distroless/static-debian12:nonroot`, `content/` baked in (`CONTENT_DIR=/app/content`). Static CGO-off binary.
- Optional later: mount `content/` from a ConfigMap so balance tuning redeploys without a rebuild.

## 7. Status note
The HTTP/SSE/API server itself is **not implemented yet** — current `backend/cmd/sim/main.go` is a CLI scaffold.
The serving layer lands as platform modules (`platform/events` → Redis events stream; `platform/api` → health/API/SSE),
built via the spec-first flow. This doc fixes the *topology and contract* those modules implement against.
