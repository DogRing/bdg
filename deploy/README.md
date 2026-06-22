# Deploying the bdg backend

The backend ships as **two images / two deployments** from one Go module (`backend/`):

| Service | Source | Dockerfile | Image | Host | Role |
|---------|--------|-----------|-------|------|------|
| **bdg-backend** | `backend/` (root `main.go`) | `backend/Dockerfile.api` | `bdg-backend` | `gapi.dogring.kr` | simulation **writer** + read/write API. Single replica. |
| **bdg-sse** | `backend/cmd/sse` | `backend/Dockerfile.sse` | `bdg-sse` | `sse.dogring.kr` | **read-only** SSE stream tailer. Horizontally scalable. |

All infra config comes from **environment variables** — you edit one file
([`.env.example`](.env.example) → copy to `.env`) and feed it to the shell, Docker, or
Kubernetes Secrets.

| File | Purpose |
|------|---------|
| `.env.example` | Config template — the one file you fill in. Copy to `.env` (git-ignored). |
| `../backend/Dockerfile.api` | bdg-backend image (multi-stage, ships `content/`). Build from repo root. |
| `../backend/Dockerfile.sse` | bdg-sse image (multi-stage, no content). Build from repo root. |
| `../.dockerignore` | Keeps the build context small and secret-free. |
| `k8s/secret.example.yaml` | `bdg-backend-env` — **full** creds (primary valkey + postgres + RO). |
| `k8s/secret-sse.example.yaml` | `bdg-sse-env` — **read-only** creds only (no postgres, no write). |
| `k8s/deployment-backend.yaml` | bdg-backend Deployment (replicas: 1, Recreate). |
| `k8s/deployment-sse.yaml` | bdg-sse Deployment (replicas: 2+, RollingUpdate). |
| `k8s/service.yaml` | ClusterIP services `bdg-backend` + `bdg-sse`. |
| `k8s/ingress.yaml` | `gapi.dogring.kr`→bdg-backend, `sse.dogring.kr`→bdg-sse. |

## Environment variables

| Var | Used by | What |
|-----|---------|------|
| `REDIS_ADDR` | both | valkey host:port. Empty = batch-only (writer). |
| `REDIS_USERNAME` / `REDIS_PASSWORD` | backend | primary (read+write) valkey user — the writer. |
| `REDIS_DB` | both | logical DB (default 0). |
| `REDIS_RO_USERNAME` / `REDIS_RO_PASSWORD` | both | read-only valkey user. **bdg-sse uses this exclusively** (falls back to the primary vars if unset). |
| `POSTGRES_DSN` | backend | full DSN, internal only. Empty = no backup. |
| `HTTP_ADDR` | both | listen addr (default `:8080`). |
| `GOD_MODE` | backend | `true` enables `/api/god/*`. Keep `false` if public. |
| `BACKUP_EVERY_TICKS` | backend | snapshot cadence (0 = balance.yaml default). |
| `RUN_ID` | both | keyspace prefix. **bdg-sse's RUN_ID must match the writer's.** |
| `SEED` | backend | RNG seed. |
| `CONTENT_DIR` | backend | content path (`/app/content` in the image; `./content` for local). |

`-ticks` / `-agents` are CLI flags on bdg-backend only (container args, e.g. `["-ticks=0","-agents=200"]`).
bdg-sse takes no flags — it is fully env-configured.

### The read-only boundary
Clients never talk to valkey directly. **bdg-backend** (the writer) appends events to the valkey
stream; **bdg-sse** tails that stream with a read-only valkey user and forwards it over `/sse`. The
public SSE pods hold only read-only credentials (separate `bdg-sse-env` Secret) — a compromised SSE
pod cannot write state or reach postgres.

## Local testing (against the in-cluster valkey/postgres)

valkey/postgres are `ClusterIP`, so from a dev box port-forward them first:
```bash
kubectl -n valkey   port-forward svc/valkey-primary 6379:6379 &
kubectl -n postgres port-forward svc/postgres       5432:5432 &
```
Then point `.env` at localhost (`REDIS_ADDR=127.0.0.1:6379`, host `127.0.0.1` in the DSN) and:
```bash
cp deploy/.env.example deploy/.env        # fill in passwords
( cd backend && go build -o /tmp/bdg . && go build -o /tmp/bdg-sse ./cmd/sse )

set -a; . deploy/.env; set +a
# writer + API (run from repo root so ./content resolves; the .env CONTENT_DIR is the in-container path)
CONTENT_DIR=./content /tmp/bdg -ticks=0 -agents=50 &

# read-only SSE, in the same env (uses REDIS_RO_* automatically)
HTTP_ADDR=:8081 /tmp/bdg-sse &
```
Verify (curl, or any HTTP client):
```bash
curl -s  localhost:8080/healthz                 # bdg-backend
curl -N  localhost:8081/sse                      # bdg-sse — streams "data: {...}" as the sim emits
curl -s 'localhost:8080/api/god/agent/agent_00/divergence?god=true' | jq .per_stat
```
> agent ids are `agent_00`, `agent_01`, … ; the divergence per-stat triple is `real` / `self_estimate` / `others_estimate_mean`.

## Docker
```bash
docker build -f backend/Dockerfile.api -t bdg-backend:dev .   # from repo root
docker build -f backend/Dockerfile.sse -t bdg-sse:dev .
docker run --rm --env-file deploy/.env -p 8080:8080 bdg-backend:dev -ticks=0 -agents=200
docker run --rm --env-file deploy/.env -p 8081:8080 bdg-sse:dev
```

## Kubernetes
```bash
kubectl create namespace bdg

# Two secrets: full creds for the writer, read-only-only for the SSE pods.
kubectl -n bdg create secret generic bdg-backend-env --from-env-file=deploy/.env
kubectl -n bdg apply -f deploy/k8s/secret-sse.example.yaml   # edit the RO password first

kubectl -n bdg apply -f deploy/k8s/deployment-backend.yaml
kubectl -n bdg apply -f deploy/k8s/deployment-sse.yaml
kubectl -n bdg apply -f deploy/k8s/service.yaml
kubectl -n bdg apply -f deploy/k8s/ingress.yaml
```
Set `image:` in both deployments to your pushed tags. Ingress annotations are `ingress-nginx`;
TLS assumes cert-manager (`cluster-issuer: letsencrypt-prod` — change to your issuer).

### ⚠️ Single writer
bdg-backend runs the simulation; two replicas would corrupt the shared `RUN_ID` keyspace. It stays
`replicas: 1` / `Recreate`. Scale the read side via **bdg-sse** instead (it only reads).
