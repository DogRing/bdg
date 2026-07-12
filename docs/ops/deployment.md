# Deployment — Topology & Networking (configuration check)

Complements `docs/ops/dev-setup.md` (build/image) and `docs/core/data-contracts.md` (Redis/PG/event shapes).
This doc verifies the runtime topology you described: backend in a Docker container on k8s,
Redis/Postgres as separate workloads, SSE at `sse.dogring.kr`, other API at `gapi.dogring.kr`,
internal traffic over `*.svc.cluster.local`.

## 1. The single-writer rule (most important — affects everything)
The simulation is **deterministic and stateful with one authoritative timeline** (D12). You **cannot run
N replicas of the tick loop** — each replica would advance its own divergent world.

The backend ships as **two binaries / two images** (one Go module, `backend/`):

| Binary | Source | Image / Dockerfile | Role | Replicas |
|--------|--------|--------------------|------|----------|
| **bdg-backend** (writer + API) | `backend/` (root `main.go`) | `bdg-backend` / `backend/Dockerfile.api` | tick loop, writes live state to valkey, backs up to postgres, serves `/api/*` (`gapi.dogring.kr`) | **1** (`strategy: Recreate`) |
| **bdg-sse** (read-only SSE) | `backend/cmd/sse` | `bdg-sse` / `backend/Dockerfile.sse` | tails the valkey event stream with a **read-only** user, serves `/sse` (`sse.dogring.kr`) | N (`RollingUpdate`) |

- **bdg-backend is the single writer:** `replicas: 1`, **`strategy: Recreate`** (NOT RollingUpdate — a rolling
  update briefly runs two pods = two sims writing the same `RUN_ID` keyspace).
- **bdg-sse only reads** (XREAD on the events stream): stateless, no postgres, no `content/`, no write grants —
  scale it horizontally with the SSE connection load. It gets ONLY the read-only valkey credentials.

## 2. Internal traffic (`cluster.local`)
Already handled by env (see `.env.example`, `docs/ops/dev-setup.md`). On k8s, set via ConfigMap/Secret:
- `REDIS_ADDR = redis.<ns>.svc.cluster.local:6379` (+ `REDIS_PASSWORD` from a Secret)
- `POSTGRES_DSN = postgres://…@postgres.<ns>.svc.cluster.local:5432/bdg?sslmode=…` (Secret)
The backend opens plain in-cluster connections; no public exposure for Redis/Postgres.

## 3. External routing (two hostnames, two services)
Each hostname targets its own Service; the ingress routes by **hostname**.
```
gapi.dogring.kr  ──Ingress──> Service: bdg-backend  (writer+API,  :8080)
sse.dogring.kr   ──Ingress──> Service: bdg-sse      (read-only SSE, :8080)
```
- `gapi.dogring.kr` → `bdg-backend`: API paths (`/api/…`, `/healthz`, `/readyz`).
- `sse.dogring.kr` → `bdg-sse`: SSE path (`/sse`) — long-lived `text/event-stream`.
- Manifests: `deploy/k8s/{deployment-backend,deployment-sse,service,ingress}.yaml`.

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
Two multi-stage Dockerfiles, both built from the **repo root** and pushed to `registry.dogring.kr` by
GitHub Actions (`.github/workflows/{backend,sse}.yaml`, Zot login via `ZOT_USERNAME/PASSWORD`):
- `backend/Dockerfile.api` → `bdg-backend`: ships `content/` (`CONTENT_DIR=/app/content`).
- `backend/Dockerfile.sse` → `bdg-sse`: no content (read-only stream tailer).

`backend.yaml` gates the image build on a `test` job (`go test -race ./...`, `needs: test`) — the
race detector needs CGO/gcc (absent from the dev sandbox), so CI is the only place it runs and a
race-failing commit never produces a `:latest` image Flux would auto-deploy.
Both: static CGO-off binary on `distroless/static-debian12:nonroot`. Health via k8s httpGet probes
(`/healthz`, `/readyz`) — no in-image HEALTHCHECK (distroless has no shell).

## 7. Status note
The HTTP/SSE/API serving layer is **implemented**: `platform/events` (valkey events stream),
`platform/persist` (live store + postgres backup), and `platform/api` (health/API/SSE + god-view). The
writer entrypoint is `backend/main.go`; the read-only SSE entrypoint is `backend/cmd/sse` (via
`api.NewSSE`). See `deploy/README.md` for the end-to-end local/docker/k8s runbook.
