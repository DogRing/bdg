# Dev Setup — Build, Run, Containerize

Physical setup that complements `docs/architecture.md` (logical module deps).

## Prerequisites
- Go **1.26+** (`go.mod` pins `go 1.26`; match your installed `go version`).
- Docker + Compose (local Redis/Postgres; backend image).
- `make` (task shortcuts).

## Module layout
- `go.mod` is at the **repo root**. Module path: `github.com/dogring/bdg`.
- All Go code lives under `backend/`. Import paths are
  `github.com/dogring/bdg/backend/engine/<module>` and `.../backend/platform/<module>`.
- Entry point: `backend/cmd/sim/main.go` (scaffold; boots flags/config, no engine wired yet).
- Engine/platform modules are implemented **leaf-first via the spec-first agent flow** — see
  `docs/architecture.md` (order) and each `backend/**/SPEC.md`.

```
repo/
  go.mod                      # module github.com/dogring/bdg
  Makefile  Dockerfile  docker-compose.yml  .env.example
  backend/
    cmd/sim/main.go           # entrypoint
    engine/<m>/   SPEC.md + code   # pure deterministic sim (no IO)
    platform/<m>/ SPEC.md + code   # redis · postgres · sse · config
  content/                    # data-driven stats/actions/gates/balance (loaded at runtime)
  docs/  .claude/             # specs, agent prompts (see CLAUDE.md)
```

## First-time setup
```bash
# from repo root
go mod tidy            # no external deps yet; safe
make build             # -> bin/sim
make test              # unit + golden (none yet; passes empty)
make run               # scaffold no-op (prints seed/ticks/run, exits)
```

## Day-to-day
```bash
make fmt vet test      # before committing
make build && bin/sim -seed=1 -ticks=0 -run=dev
make test-update       # regenerate golden snapshots — REVIEW the diff (docs/testing.md)
```

## Runtime config (env, 12-factor)
Infra config comes from the environment (see `.env.example`). Engine reads it via `platform/config`.
| Var | Purpose | k8s source |
|-----|---------|-----------|
| `SEED`, `RUN_ID` | determinism, keyspace | ConfigMap |
| `CONTENT_DIR` | content data dir (image: `/app/content`) | image / ConfigMap |
| `TICKS_PER_SECOND`, `BACKUP_EVERY_TICKS` | pacing, backup cadence | ConfigMap |
| `REDIS_ADDR` | live-state Redis | ConfigMap |
| `REDIS_PASSWORD` | — | **Secret** |
| `POSTGRES_DSN` | backup Postgres | **Secret** |
| `HTTP_ADDR`, `LOG_LEVEL` | health/SSE, logging | ConfigMap |

## Local infra (Redis + Postgres)
```bash
make dev-up      # docker compose: redis:7-alpine + postgres:16-alpine
make dev-down
```
Defaults in `.env.example` match the compose creds.

## Container image (for k8s)
```bash
docker build -t bdg:dev .     # context = repo root
```
- Multi-stage → `gcr.io/distroless/static-debian12:nonroot` (small, no shell, runs as nonroot).
- `content/` is baked into the image; `CONTENT_DIR=/app/content`.
- CGO disabled, static binary — drops straight into a scratch/distroless k8s pod.

## Deploying on k8s (later)
- **backend**: a `Deployment` running the image. Inject config via a `ConfigMap` (non-secret) and a `Secret`
  (`REDIS_PASSWORD`, `POSTGRES_DSN`). Add liveness/readiness probes on `HTTP_ADDR` once the health endpoint lands.
- **Redis** and **Postgres**: separate workloads (operator/Helm chart or StatefulSet+Service+PVC, your call).
  Their in-cluster Service DNS becomes `REDIS_ADDR` / the host in `POSTGRES_DSN`.
- **Content tuning without rebuild** (optional): mount `content/` from a `ConfigMap` and point `CONTENT_DIR` at the mount,
  so balance changes redeploy without a new image.
- SSE/frontend is added later as its own Service; it tails the Redis events stream (`docs/data-contracts.md` §4).