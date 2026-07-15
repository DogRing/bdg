# SPEC — `platform/api`

> Status: `DRAFT`
> Leaf level: `L8` (platform — depends only on engine public interfaces + platform siblings; architecture §3/§5 stage 9)  ·  Owner agent: `<filled by implementer>`

## Purpose

The simulation's **read-only HTTP boundary**: liveness/readiness probes, the SSE event stream
(tailing the Redis `sim:{run}:events` STREAM), the snapshot/agent query endpoints, and the
**god-view inspection endpoints** (`/api/god/*` — divergence, reputation, relations, why; see the
[godview SPEC](godview/SPEC.md)). It is the only place the engine's live state is exposed over
HTTP. It performs **all HTTP IO**; it owns no simulation state and never advances the tick loop
(D12 single-writer rule — `docs/ops/deployment.md` §1: readers scale freely, only the `sim` writer
mutates). It enforces the **god-view boundary (D8)**: `real_stats` and every `/api/god/*` response
reach a client only when both startup `GodMode` and a per-request `?god=true` are set.

Two **control routes** exist: `POST /api/restart` delivers a restart *signal* (deterministic
rebuild, original seed) and `POST /api/regen?seed=<int64>` a regenerate *signal* (rebuild from a
NEW seed — random when `seed` is absent/0; the test-world/random-terrain path) to the sim writer
via injected callbacks (`Config.Restart` / `Config.Regen`). The handlers mutate nothing themselves
— the world rebuild happens on the sim's tick goroutine (single-writer preserved); api only
forwards the signal. Registered on the writer server (`New`) only, never on the SSE-only server
(`NewSSE`).

## Public Interface

> The *only* contract callers (`main` / `cmd/sim`, `cmd/api`) and the implementer depend on. Module
> path root is `github.com/dogring/bdg` (the `backend/` dir is the module root, **not** part of the
> import path — e.g. `github.com/dogring/bdg/platform/persist`, matching `main.go` and
> `platform/persist/SPEC.md`).

```go
package api

import (
    "context"
    "net/http"

    "github.com/dogring/bdg/engine/kernel/core"
    "github.com/dogring/bdg/platform/persist"
)

// Server wires all HTTP handlers onto one mux and exposes a single ListenAndServe.
// It holds no simulation state; it reads Redis (via the injected store + a minimal
// Redis read client) and serializes the result. No global state — every route is
// registered in New.
type Server struct {
    // unexported:
    //   live    persist.LiveStore   // ReadSnapshot + key/shape contracts
    //   rds     RedisReader         // PING, HGETALL agent/meta hash, XREAD events tail (+ XINFO
    //                               //   max-deleted-entry-id for the SSE gap check), GET snapshot
    //   gv      GodViewStore        // QueryEvents — the /api/god/why event source (Postgres); may
    //                               //   be nil when god-view is disabled at wiring time
    //   keyer   persist.Keyer       // sim:{run}:* key strings (never hand-formatted)
    //   runID   core.RunID          // which sim run to tail/query
    //   godMode bool                // startup flag; gates real_stats on /api/agents/{id}?god=true
    //                               //   AND every /api/god/* route (see godview SPEC)
    //   mux     *http.ServeMux
    //   addr    string
}

// GodViewStore is the read-side Postgres surface the /api/god/why endpoint needs (the why-trace
// event source, data-contracts §3 `events` table). It is a narrow read interface over
// persist.BackupStore so api depends on the query method only, not the whole backup writer. The
// concrete persist.BackupStore satisfies it once QueryEvents is added (a persist SPEC change —
// see the godview SPEC Open Questions / backend/platform/persist/SPEC.md). Injected at
// construction; may be nil when GodMode is off (the /why route then 403s before touching it).
type GodViewStore interface {
    // QueryEvents returns all events for (run, agentID, tick) in seq order. agentID may be empty
    // to match all agents at that tick. Used by /api/god/why to reconstruct the decision rationale
    // (GoalSelected + PlanBuilt) for one agent at one game-tick.
    QueryEvents(ctx context.Context, run core.RunID, agentID core.AgentID, tick core.Tick) ([]core.Event, error)
}

// Config holds the API-layer knobs injected at construction (extracted from EnvConfig + flags).
type Config struct {
    Addr    string     // EnvConfig.HTTPAddr (e.g. ":8080")
    RunID   core.RunID // which sim run to tail/query (EnvConfig.RunID)
    GodMode bool       // startup-only: enables real_stats on /api/agents/{id}?god=true AND all /api/god/*
    Restart func()     // POST /api/restart signal sink (non-blocking; the sim writer performs the
                       // actual world rebuild on its tick goroutine). nil ⇒ the route responds 503.
    Regen   func(seed int64) // POST /api/regen signal sink (non-blocking; seed 0 ⇒ the writer draws
                       // a random seed and logs it). nil (e.g. scenario mode) ⇒ the route responds 503.
}

// RedisReader is the minimal Redis surface api needs for the read path. api does NOT own the
// Redis connection — the same client backing the LiveStore is injected here (an interface so
// tests use an in-memory fake). All key strings come from persist.Keyer; api formats none.
type RedisReader interface {
    // Ping is the /readyz liveness check against Redis. Returns nil iff reachable.
    Ping(ctx context.Context) error
    // Get reads a STRING key (sim:{run}:snapshot). Returns (nil, nil) when the key is absent.
    Get(ctx context.Context, key string) ([]byte, error)
    // HGetAll reads a HASH key (sim:{run}:agent:{id}). Empty map ⇒ agent unknown (404).
    HGetAll(ctx context.Context, key string) (map[string]string, error)
    // XRead blocks (BLOCK block) on the events STREAM at key starting after lastID, returning the
    // next batch (entryID, fields) and the new lastID. It MUST honour ctx cancellation: when the
    // client disconnects (ctx.Done), the call unblocks and returns ctx.Err() so the SSE handler
    // returns cleanly with no goroutine leak.
    XRead(ctx context.Context, key, lastID string, block time.Duration) (entries []StreamEntry, newLastID string, err error)
    // StreamMaxDeletedID returns the stream's max-deleted-entry-id (XINFO STREAM) — the highest
    // entry ID ever trimmed/deleted from the stream, "0-0" when nothing was deleted OR the key
    // does not exist (a regen-recreated stream restarts this metadata, which is exactly why a
    // post-regen snapshot cursor referencing deleted construction entries is NOT a gap). The SSE
    // handler compares a client-supplied replay cursor against it: cursor < max-deleted ⇒ entries
    // after the cursor were lost ⇒ StreamGap (routes table).
    StreamMaxDeletedID(ctx context.Context, key string) (string, error)
}

// StreamEntry is one Redis STREAM entry the SSE tail forwards. Fields carries the entry payload;
// the SSE handler reads fields["payload"] (the JSON-serialized core.Event written by
// platform/events, data-contracts §4) and frames it as `data: <JSON>\n\n`.
type StreamEntry struct {
    ID     string
    Fields map[string]string
}

// New wires the mux (all routes registered here; no global state) and returns a ready Server.
// It does not bind a socket — ListenAndServe does. live + rds are injected (rds is the same
// client backing live; api does not dial Redis itself). gv is the god-view event store
// (Postgres) — it MAY be nil when cfg.GodMode is false (every /api/god/* route 403s before it is
// touched). The probe/SSE/snapshot/agent routes do not use gv.
func New(cfg Config, live persist.LiveStore, rds RedisReader, gv GodViewStore) *Server

// NewSSE wires a READ-ONLY Server exposing only GET /healthz, /readyz, /sse — no
// snapshot/agent/god routes, no LiveStore, no Postgres. It backs the standalone bdg-sse
// deployment (sse.dogring.kr, backend/cmd/sse), which connects to valkey with a read-only
// user and never touches the write path. The three handlers read only rds + keyer, so the
// nil live/gv are never dereferenced. Shares ListenAndServe/Handler with New.
func NewSSE(cfg Config, rds RedisReader) *Server

// ListenAndServe binds cfg.Addr and serves until ctx is cancelled (graceful shutdown:
// http.Server.Shutdown is called, in-flight SSE connections are closed) or a fatal listen error
// occurs. Returns nil on clean ctx-cancel shutdown, the error otherwise.
func (s *Server) ListenAndServe(ctx context.Context) error

// Handler returns the underlying http.Handler (the wired mux) for test injection via
// httptest.NewServer / httptest.NewRecorder — no socket bind required to test handlers.
func (s *Server) Handler() http.Handler
```

> api DEFINES no simulation vocabulary. `persist.LiveStore` / `persist.Keyer` / `persist.AgentView`
> / `persist.Snapshot` are `platform/persist`'s contract; `core.Event` / `core.AgentID` /
> `core.RunID` are `engine/kernel/core`'s. This module reads, filters (god-view), and serializes them over
> HTTP. The `time.Duration` in `XRead` is stdlib. The `GodViewResponse` group + the `/api/god/*`
> wire detail live in the [godview SPEC](godview/SPEC.md) (kept out of this file to stay < ~400
> lines, CLAUDE.md §5); only the injected `GodViewStore` interface and the route entries below are
> surfaced here.

## Routes

| Method · Path | Handler behaviour | Status / body |
|---|---|---|
| `GET /healthz` | Liveness — no dependency check; always succeeds. | `200` · `{"status":"ok"}` |
| `GET /readyz` | Readiness — `rds.Ping(ctx)`. | `200` on success · `503` + `{"error":"redis unavailable"}` on PING failure or timeout |
| `GET /sse` | Tails `keyer.Events()` via `XRead` BLOCK with last-id tracking; flushes each event. The stored event JSON is forwarded verbatim without a payload-field whitelist, so additive `WorldFrame` fields such as `snow_cover` are preserved. Every frame carries the Redis entry ID as its standard `id:` line (the transport cursor, data-contracts §4). **Replay**: a client may supply a cursor via the `Last-Event-ID` header or `?cursor=<entry-id>` (header wins; must match `\d+-\d+`, otherwise ignored → live tail) — the handler XREADs strictly AFTER it, so retained entries between a snapshot's `stream_cursor` and the connection are replayed and flow gap-free into the live tail (one loop ⇒ no duplicates, no gap). No cursor ⇒ live tail from `"$"` (previous behaviour; a fresh no-cursor connect never replays the backlog). **Trim/gap**: before replaying, the handler checks `StreamMaxDeletedID` — if entries after the requested cursor were trimmed (cursor < max-deleted-entry-id), it sends ONE `data: {"type":"StreamGap","payload":{...}}` control frame (no `id:` line) and CLOSES: the client must reacquire a fresh snapshot/cursor pair rather than receive a silently-partial sparse history. A regen-recreated stream resets max-deleted-entry-id, so a post-regen cursor referencing deleted old entries replays cleanly (nothing after it was lost). | `200` · `text/event-stream`; frames are `id: <entry-id>\n` + `data: <JSON>\n\n` (StreamGap control frame: `data:` only) |
| `GET /api/meta` | `rds.HGetAll(keyer.Meta())` — forwards the `sim:{run}:meta` hash as a flat JSON object of strings: `{tick, schema_version, started_at, status, world_revision?, terrain?, flora?}`. `world_revision` is the **published** single-world revision marker (data-contracts §2) — written LAST, after the revision's snapshot+terrain+flora baselines are servable, so a poller that sees a new value may immediately load the matching baselines. `terrain` and `flora` (each `"on"`/`"off"`) are the published revision's explicit env availability per subsystem (env-off worlds say `"off"` — clients must not infer env-off from a failed `/api/terrain` or `/api/flora` fetch; `flora` mirrors `terrain`). Registered on `New` only, **not** `NewSSE`. | `200` + meta JSON · `404` + `{"error":"meta not found"}` when the hash is absent/empty |
| `GET /api/snapshot` | `rds.Get(keyer.SnapshotKey())` (the god-view blob; caller does access control). The blob's wrapper self-identifies its world: `world_revision`, `stream_cursor` (the Redis entry ID up to which the state is baked in — SSE replay starts strictly after it), `terrain` (`"on"`/`"off"`) and `flora` (`"on"`/`"off"`) — data-contracts §1/§2. A reader verifies revision consistency across snapshot/terrain/flora/meta by comparing these fields (mismatch ⇒ another regen landed between reads ⇒ refetch); the `flora` flag lets the frontend gate its `/api/flora` load independently of terrain. | `200` + snapshot JSON · `404` + `{"error":"snapshot not found"}` when key absent |
| `GET /api/terrain` | `rds.Get(keyer.Terrain())` — forwards the stored `sim:{run}:terrain` bytes **verbatim** (WI-P4, data-contracts §2; `persist.TerrainView` is written already shaped as this response: `{cell_size, orientation:'flat', size:{cols,rows}, terrain[cols*rows], wear?[cols*rows], world_revision?}` — flat-top hex offset(col,row) array, the frontend `TerrainGrid` contract, `docs/plans/hex-grid.md` / `docs/plans/frontend.md` Q5; `world_revision` tags which published revision the grid belongs to, data-contracts §2). Registered on the writer server (`New`) only, **not** on the SSE-only server (`NewSSE`). | `200` + terrain JSON · `404` + `{"error":"terrain not found"}` when the key is absent/empty (env/navmap OFF) |
| `GET /api/flora` | `rds.Get(keyer.Flora())` — forwards the stored `sim:{run}:flora` bytes **verbatim** (WI-P4, data-contracts §2; `persist.FloraDoc` is written already shaped as this response: `{world_revision, flora:[{object_id, species, pos, stage, width}]}` — the frontend flora baseline). Mirrors `/api/terrain`: `world_revision` tags which published revision the set belongs to, so a reader verifies it against the snapshot (mid-regen guard). The plants that existed **before** the client connected (fixtures + already-propagated — no SSE event replays them); SSE `flora_delta`/`PlantSpawned`/`PlantDied` keep it current after. An installed-but-empty world is a `200` with `flora:[]` (authoritative-empty), distinct from flora-not-installed (`404`). Registered on `New` only, **not** `NewSSE`. | `200` + flora JSON · `404` + `{"error":"flora not found"}` when the key is absent/empty (flora OFF) |
| `GET /api/agents/{id}` | `rds.HGetAll(keyer.Agent(id))` → `AgentView`; `?god=true` AND `GodMode` ⇒ also merge `real_stats` from the snapshot blob. | `200` + agent JSON · `404` + `{"error":"agent not found"}` when hash empty |
| `POST /api/restart` | Invokes `cfg.Restart()` (a non-blocking signal to the sim writer; the writer rebuilds the world from its fixture/scenario with the original seed — deterministic reset of all agent/fauna/flora/terrain state, tick back to 0). A restart is a **debugging rewind**: the writer purges only the swapped-out per-entity live keys and **never deletes Postgres history** (snapshots/events stay append-only). Registered on `New` only, **not** `NewSSE`. No store access, no body. | `202` + `{"status":"restarting"}` · `503` + `{"error":"restart unavailable"}` when `cfg.Restart` is nil |
| `POST /api/regen` | Invokes `cfg.Regen(seed)` (a non-blocking signal; the writer rebuilds the world from its fixture with a NEW seed — random terrain/placements re-rolled, tick back to 0). Optional `?seed=<int64>` pins the seed for reproducibility; absent/0 ⇒ the writer draws one. Regen is "**new map, same run_id**": before flushing the fresh world the writer deletes the run's Postgres snapshots/events rows atomically and refreshes the runs row to the new seed (MANDATORY — on failure the writer ABORTS the regen and keeps the current world), then best-effort deletes the old map's Redis keys (per-entity + snapshot/tick/events/flora/climate/terrain; meta refreshed in place) — persist SPEC "restart vs regen". Deleting the events STREAM is SSE-safe: this route's server tails from `"$"`, a blocked `XREAD` on a deleted key simply keeps waiting, and recreated-stream XADD IDs are wall-clock-based so they stay monotone past any old last-ID (accepted risk: a backwards server-clock step could delay delivery for already-connected clients). **Current single-world development mode**: exactly one world is exposed (no world catalog/selector, no run pointer, no automatic SSE run switching; a multi-world redesign is DEFERRED — `docs/plans/run-generation.md`). After a regen ALL viewers must reload; there is no multi-viewer resync signal. The `202` response means the regen *signal was accepted*; the rebuild, cleanup and fresh flush happen asynchronously on the sim writer, so it does NOT mean the rebuild succeeded (a failed mandatory cleanup keeps the current world) nor that any client has resynchronized. Readiness is the **published `world_revision`** (data-contracts §2): a successful regen bumps the revision and publishes it to `sim:{run}:meta` ONLY AFTER the mandatory Postgres reset, the best-effort Redis cleanup and the regenerated snapshot+terrain baseline writes — restart and every failed/aborted regen leave it unchanged, and concurrent/repeated regen signals coalesce on the single tick goroutine so a revision value is never reused. The initiating frontend therefore does NOT reload on a timer and does NOT infer completion from tick values: it reads `GET /api/meta` `world_revision` BEFORE submitting (unreadable ⇒ report a recoverable error, do not submit), POSTs, polls `/api/meta` (capped backoff, bounded timeout) until a DIFFERENT revision is published, loads the snapshot/terrain whose embedded `world_revision` matches it, and only then reloads. On timeout/abort it surfaces the failure and keeps the current view (`frontend/SPEC.md` §New-map flow). Other concurrently connected viewers keep rendering deltas over their stale baseline until they reload. Registered on `New` only, **not** `NewSSE`. No store access, no body. | `202` + `{"status":"regenerating"}` · `400` + `{"error":"invalid seed"}` on an unparsable `seed` · `503` + `{"error":"regen unavailable"}` when `cfg.Regen` is nil (scenario mode / SSE-only) |
| `GET /api/god/agent/{id}/divergence` | **God-view** (gate below). 3-way real / self-estimate / others-estimate-mean per stat — see [godview SPEC](godview/SPEC.md). | `200` + divergence JSON · `206` + `{"partial":true,...}` if TomDigest absent · `403` if gate fails |
| `GET /api/god/reputation/{id}` | **God-view.** D6 reputation distribution (mean/variance + per-faction) per stat — see [godview SPEC](godview/SPEC.md). | `200` + reputation JSON · `206` if TomDigest absent · `403` if gate fails |
| `GET /api/god/relations` | **God-view.** All agent-pair social edges (affinity/trust/rely_on), sorted by (from,to) — see [godview SPEC](godview/SPEC.md). | `200` + relations JSON · `206` if TomDigest absent · `403` if gate fails |
| `GET /api/god/why/{agent_id}/{tick}` | **God-view.** Decision rationale (GoalSelected + PlanBuilt) for an agent at a tick from Postgres `events` via `gv.QueryEvents` — see [godview SPEC](godview/SPEC.md). | `200` + why JSON · `404` if no events for (agent,tick) · `403` if gate fails |

> **God-view gate (all four `/api/god/*` routes).** Every `/api/god/*` route requires startup
> `GodMode == true` **AND** request `?god=true`. With either missing, the handler responds `403` +
> `{"error":"god mode disabled"}` **before** reading any store. The startup `GodMode` flag is
> authoritative; the query param cannot bypass a disabled `GodMode` (same rule as
> `/api/agents/{id}?god=true`). The full request/response wire detail, response types, invariants,
> and acceptance criteria for these four routes live in **[godview SPEC](godview/SPEC.md)**.

### `GET /sse` — wire detail
- Response headers set **before** the first write: `Content-Type: text/event-stream`,
  `Cache-Control: no-cache`, `X-Accel-Buffering: no`, `Connection: keep-alive`
  (`docs/ops/deployment.md` §4 — disables gateway/nginx buffering).
- Asserts the `ResponseWriter` is an `http.Flusher`; if not, `500` (cannot stream).
- Loop: `XRead(ctx, keyer.Events(), lastID, block)` → for each entry, write
  `data: <fields["payload"]>\n\n`, advance `lastID` to the entry ID, then `Flush()` **per event**.
- The payload is already the JSON-serialized `core.Event` (written by `platform/events`); api does
  **not** re-marshal it — it forwards the bytes inside the `data:` frame. No `id:`/`event:` prefix
  for P1.
- On client disconnect, `r.Context()` cancels → `XRead` unblocks with `ctx.Err()` → the handler
  returns; no background goroutine, no leak.

### `GET /api/agents/{id}` — god-view gate (D8)
- **Default** (no `?god=true`, or `GodMode=false`): respond with the `persist.AgentView` shape
  `{id, pos, goal, action, mood}` decoded from the Redis hash. The `real_stats` field is **absent**
  from the JSON — not `null`, not zero, the key is omitted.
- **`?god=true` AND startup `GodMode=true`**: additionally read the snapshot blob
  (`keyer.SnapshotKey()`), locate this agent's `real_stats` block, and include it under a
  `"real_stats"` key in the response.
- **`?god=true` but `GodMode=false`**: behaves exactly as default — `real_stats` absent. The
  startup flag is authoritative; the query param **cannot** override a disabled `GodMode`.

### `GET /api/god/*` — see the godview SPEC
The four god-view inspection endpoints (divergence, reputation, relations, why) share the gate
above and a common data-availability model (they read the snapshot blob's `TomDigest`, or fall
back to `206 Partial Content`). Their wire detail is delegated to **[godview SPEC](godview/SPEC.md)**
so this file stays under ~400 lines (CLAUDE.md §5). The parent surfaces only the route table rows,
the `GodViewStore` injection, and the gate invariant; everything else (response shapes, faction
derivation, D6/D8/D12 compliance, ACs) is in the child.

## SSE event wire format

Each SSE message is exactly:
```
data: {"schema_version":1,"tick":42,"seq":0,"agent_id":"farmer_1","type":"ActionDone","payload":{...}}\n\n
```
The `data:` value is the `core.Event` JSON exactly as `platform/events` wrote it to the stream
(`fields["payload"]`). api adds only the `data: ` prefix and the terminating `\n\n`. No `id:` or
`event:` line for P1 (data-contracts §4).

## Dependencies

- `platform/persist` — `persist.LiveStore` (`ReadSnapshot`; the live-store contract and the same
  Redis client that backs it), `persist.Keyer` (the **only** source of `sim:{run}:*` key strings —
  `Events()`, `SnapshotKey()`, `Agent(id)`), `persist.AgentView` (the render-visible agent shape;
  by construction carries no `real_stats`), `persist.Snapshot` (the god-view blob shape decoded for
  the `real_stats` merge **and** the `/api/god/*` `TomDigest` reads), and `persist.BackupStore`
  (satisfies the `GodViewStore` `QueryEvents` read used by `/api/god/why` — a persist SPEC change
  flagged in the godview SPEC). api reads persist's **public** contract only.
- `platform/config` — `config.EnvConfig` supplies `HTTPAddr` → `Config.Addr`, `RunID` →
  `Config.RunID`, and `RedisAddr` (used by the wiring to dial the client injected as `RedisReader`;
  api does not read env itself).
- `engine/kernel/core` — `core.Event` (the SSE payload type + the `/api/god/why` event source),
  `core.AgentID`, `core.RunID`, `core.Tick`. Read-only; api never mutates engine state.
- Standard library — `net/http`, `context`, `encoding/json`, `time` (the `XRead` BLOCK duration).
- **External infra** — a Redis client injected via the `RedisReader` interface (the **same**
  client backing the `LiveStore`) and a Postgres-backed `GodViewStore` for the `/why` endpoint;
  api does not own or dial either connection. Tests use in-memory fakes satisfying both interfaces.
- **Contract** — `data-contracts.md` §2 (keyspace: `sim:{run}:events` STREAM, `sim:{run}:agent:{id}`
  hash, `sim:{run}:snapshot` STRING), §3 (Postgres `events` table — the `/why` source), §4
  (event/SSE shape + the `real_stats` SSE policy); `docs/ops/deployment.md` §3/§4 (SSE headers + no
  buffering), §5 (probe semantics). The `/api/god/*` endpoints depend additionally on a `TomDigest`
  on the snapshot blob and a `competing_candidates` field on `GoalSelected` — both flagged as
  prerequisite contract changes in the godview SPEC Open Questions.

## Owned Data

- The HTTP route table and the wired `http.ServeMux` (including the four `/api/god/*` registrations).
- The SSE framing (`data: <JSON>\n\n`) and the per-event `Flush()` policy.
- The **god-view gate**: the `(GodMode && god=true)` predicate that decides whether `real_stats` is
  merged into `/api/agents/{id}` **and** whether any `/api/god/*` route serves at all. This mirrors
  the persist-tier boundary (`persist.AgentView` cannot carry `real_stats`; only the `Snapshot` blob
  does). The `/api/god/*` response shaping (the `GodViewResponse` group) is owned by the
  [godview SPEC](godview/SPEC.md).
- The `RedisReader` / `StreamEntry` read-surface types and the `GodViewStore` read interface (api's
  view of Redis/Postgres; the concrete clients are injected).
- api OWNS **no** simulation state, **no** key strings (those are `persist.Keyer`), and mutates no
  engine or Redis write key. It is read-only over HTTP.

## Invariants

> A violation here is a bug — these are mechanically checkable.

- **God-view boundary (D8) — `real_stats` is gated.** `real_stats` appears in an
  `/api/agents/{id}` response **iff** startup `GodMode == true` AND the request carries `?god=true`.
  In every other case the `real_stats` key is **absent** (not `null`, not zero — omitted). The
  default `AgentView` path is structurally incapable of carrying it (`persist.AgentView` has no
  stats field); `real_stats` is only ever read from the snapshot blob under the gate. This mirrors
  `platform/persist`'s "Redis agent hash never carries RealStats" invariant — api must not
  re-introduce it from the blob unless both conditions hold.
- **`/api/god/*` gate (D8, all four routes).** Every `/api/god/*` route requires
  `GodMode == true` AND request `?god=true`. With either missing the handler responds `403` +
  `{"error":"god mode disabled"}` **before** reading any store (no Redis/Postgres call on the 403
  path). The startup flag is authoritative; `?god=true` only *requests* god-view, it never *grants*
  it. (Detailed per-endpoint compliance — D6 for `/reputation`, D8 for `/divergence`, D12 for
  `/relations` — is asserted in the [godview SPEC](godview/SPEC.md).)
- **Read-only HTTP (deployment §1).** No handler advances the tick, writes a live key, or mutates
  engine/world state. api is a reader; the `sim` writer is the sole authority (D12 single-writer).
  The `/api/god/why` Postgres access is a **read** (`QueryEvents`); api never writes events.
  `POST /api/restart` and `POST /api/regen` are the only control routes and keep this invariant:
  they call the injected `cfg.Restart`/`cfg.Regen` signal and return — they never touch the world,
  a store, or a Redis key; the rebuild is executed by the sim writer on its own tick goroutine.
- **No key strings formatted by hand.** Every Redis key comes from `persist.Keyer`
  (`Events`/`SnapshotKey`/`Agent`). api never concatenates `"sim:"`/`":agent:"` itself — the
  keyspace contract lives in persist (data-contracts §2).
- **SSE flushes per event, no buffering.** Each event is written and `http.Flusher.Flush()`-ed
  immediately; the handler sets `X-Accel-Buffering: no` and `Cache-Control: no-cache` so no
  intermediary buffers (deployment §4). A client sees a tick's event within the same tick.
- **Clean disconnect, no goroutine leak.** The SSE loop is bound to `r.Context()`; on
  cancellation `XRead` unblocks and the handler returns. No detached goroutine outlives the request.
- **No wall-clock for logic; no global rand.** api reads no `time.Now()` for control flow (the
  `XRead` BLOCK duration is a config constant, not derived from the clock). It introduces no
  determinism-affecting source — it is downstream of the deterministic engine (D12). The
  `/api/god/relations` output is byte-deterministic for a fixed snapshot (D12; asserted in the
  godview SPEC).
- **No naming drift.** Identifiers use the glossary canonical names; api introduces no new
  vocabulary. `real_stats`, `pos`, `goal`, `action`, `mood` mirror `persist.AgentView` / §1 / §4;
  the `faction_id` label in `/api/god/reputation` is a *derived* cluster label (D2 — no Faction
  type), specified in the [godview SPEC](godview/SPEC.md).

## Acceptance Criteria (testable)

### Probes
- [ ] `GET /healthz` always returns `200` with body `{"status":"ok"}` — table test over Redis up,
  Redis down, and a no-op `RedisReader` (the handler runs no dependency check).
- [ ] `GET /readyz` returns `200` when `rds.Ping` succeeds; `503` + body `{"error":"redis
  unavailable"}` when `Ping` returns an error or times out (table test with a fake `RedisReader`
  whose `Ping` is toggled).

### SSE
- [ ] `GET /sse` response carries headers `Content-Type: text/event-stream`,
  `Cache-Control: no-cache`, and `X-Accel-Buffering: no` (set before any body write).
- [ ] The SSE handler `Flush()`-es each event immediately — verified with a fake `ResponseWriter`
  that implements `http.Flusher` and counts `Flush` calls (one per forwarded event).
- [ ] The SSE handler returns cleanly when the client disconnects: cancelling `r.Context()` unblocks
  `XRead` and the handler returns with no leaked goroutine (asserted via `runtime.NumGoroutine`
  delta or a leak detector).
- [ ] After a tick event is XADD-ed to the events stream, a connected `/sse` client receives the
  `data: <JSON>\n\n` frame within the same tick — integration test against an in-memory Redis fake;
  the forwarded `data:` value equals the stream entry's `payload` field (the `core.Event` JSON).

### Snapshot & agents
- [ ] `GET /api/snapshot` returns the latest snapshot JSON (the `sim:{run}:snapshot` blob) with
  `200`; returns `404` + `{"error":"snapshot not found"}` when the key is absent.
- [ ] `GET /api/terrain` returns the `sim:{run}:terrain` bytes **verbatim** with `200` when the
  key exists (WI-P4); returns `404` + `{"error":"terrain not found"}` when the key is
  absent/empty (env-off neutrality); the SSE-only server (`NewSSE`) does **not** register the
  route (404 there).
- [ ] `GET /api/flora` returns the `sim:{run}:flora` bytes **verbatim** with `200` when the key
  exists (WI-P4) — the `FloraDoc` `{world_revision, flora:[{object_id, species, pos, stage,
  width}]}` baseline, no god-view field (`length`/`death_streak`) present; an installed-but-empty
  set is a `200` with `flora:[]`; returns `404` + `{"error":"flora not found"}` when the key is
  absent/empty (flora OFF); the SSE-only server (`NewSSE`) does **not** register the route (404 there).
- [ ] `GET /api/agents/{id}` **without** `?god=true` → the response JSON, decoded into
  `map[string]any`, has **no** `real_stats` key (field absent, not `null`).
- [ ] `GET /api/agents/{id}?god=true` **with** startup `GodMode=true` → the response includes a
  `real_stats` block (merged from the snapshot blob) plus the `AgentView` fields.
- [ ] `GET /api/agents/{id}?god=true` **with** startup `GodMode=false` → `real_stats` is still
  **absent** (the startup flag overrides the query param; the param cannot bypass a disabled
  `GodMode`).
- [ ] `GET /api/agents/{id}` for an unknown id (empty `HGetAll` result) → `404` + body
  `{"error":"agent not found"}`.

### Restart control
- [ ] `POST /api/restart` with `Config.Restart` set → `202` + `{"status":"restarting"}` and the
  injected callback is invoked exactly once (counting fake); no store/Redis call is made.
- [ ] `POST /api/restart` with `Config.Restart == nil` → `503` + `{"error":"restart unavailable"}`
  (no panic).
- [ ] The SSE-only server (`NewSSE`) does **not** register the route (404 there).

### Regen control
- [ ] `POST /api/regen` with `Config.Regen` set → `202` + `{"status":"regenerating"}` and the
  callback is invoked exactly once with seed `0`; `?seed=42` invokes it with `42`; no store call.
- [ ] `POST /api/regen?seed=abc` → `400` + `{"error":"invalid seed"}`; the callback is NOT invoked.
- [ ] `POST /api/regen` with `Config.Regen == nil` → `503` + `{"error":"regen unavailable"}`
  (no panic); the SSE-only server (`NewSSE`) does **not** register the route (404 there).

### God-view inspection endpoints
> The full per-endpoint ACs (response-shape, faction-variance, competing-candidates, partial-content
> fallback, determinism) live in the [godview SPEC](godview/SPEC.md). The two cross-cutting guards
> the parent owns:
- [ ] **`/api/god/*` gate — table over `(GodMode, god)`**: for each of the four routes, a
  table-driven test over `(GodMode=false, god=true)`, `(GodMode=true, god=false)`,
  `(GodMode=true, god=true)` yields `403 / 403 / 200` respectively. The two `403` cases respond
  `{"error":"god mode disabled"}` and make **no** store call (a fake store/`gv` that records calls
  asserts zero calls on the 403 path).
- [ ] **All four `/api/god/*` routes are registered on the mux** (a route-table guard asserting
  `divergence`, `reputation`, `relations`, `why` are wired and the gate wraps each).

### Boundary / wiring guards
- [ ] **No hand-formatted keys**: a grep/reflection guard asserts api builds every Redis key through
  `persist.Keyer`, never a `"sim:"`/`":agent:"` literal.
- [ ] **God-view structural guard**: with `GodMode=false`, no code path reads `real_stats` from the
  snapshot blob for the agent endpoint (the merge branch is unreachable) — table-driven over the
  four `(GodMode, god)` combinations, asserting `real_stats` presence only for `(true, true)`.

## Out of Scope

- **The four `/api/god/*` endpoints' wire detail, response types, faction derivation, and per-route
  D6/D8/D12 ACs** → the [godview SPEC](godview/SPEC.md) (kept in a child to stay < ~400 lines,
  CLAUDE.md §5). The parent owns only the route-table rows, the `GodViewStore` injection, and the
  gate invariant.
- **Authentication / TLS / rate limiting** → future `platform/auth` (the brief flags `/api/snapshot`
  as caller-access-controlled; api itself does no auth for P1 beyond the god-view gate).
- **Write endpoints / admin API** — the sim is read-only over HTTP for P1; the `sim` writer is the
  sole mutator (deployment §1).
- **WebSocket upgrade** — SSE suffices for P1.
- **CORS headers** — added at the `platform/auth` or gateway/nginx layer (deployment §3
  `CORS_ORIGINS`); api sets none for P1.
- **Event construction / serialization to the stream** → `platform/events` (writes
  `sim:{run}:events`; api only tails and forwards the bytes — `backend/platform/events/SPEC.md`).
- **Snapshot serialization, the Redis keyspace strings, the `TomDigest` capture, the
  `QueryEvents` implementation, and the `LiveStore`/`RedisReader`/`BackupStore` concrete clients** →
  `platform/persist` (`backend/platform/persist/SPEC.md`); api consumes `Keyer` / `AgentView` /
  `Snapshot` / `GodViewStore` and injected clients. The `TomDigest` + `QueryEvents` additions are
  persist SPEC changes flagged in the godview SPEC.
- **Env parsing** → `platform/config` (`config.EnvConfig`); api receives a `Config` assembled by the
  wiring.
- **Splitting readers into a separate `cmd/api` Deployment** → deployment §1 (a wiring/topology
  concern; api's handlers are already stateless and reader-safe).

## Open Questions

- **God-view data availability (BLOCKER for `/api/god/*` full responses) — see the
  [godview SPEC](godview/SPEC.md).** The divergence / reputation / relations endpoints need the full
  cross-agent ToM, which the current snapshot does NOT carry (`world.WorldState` stores only
  `SelfEstStats`). The recommended fix is a `TomDigest` on `world.WorldState` + `persist.Snapshot`
  (an `engine/world` + `platform/persist` change — escalates to the architect). The `/why` endpoint
  needs a `competing_candidates` field on the `GoalSelected` event payload (a data-contracts §4
  change + schema-version bump) and a `QueryEvents` method on `persist.BackupStore`. The full
  proposal, the two options (TomDigest digest vs per-agent Redis key), the recommendation, and the
  graceful-degradation `206` fallback are specified in the [godview SPEC](godview/SPEC.md). These
  block the *full* `/api/god/*` responses but **not** the gate/403 behaviour, the SSE/probe/agent
  endpoints, or the `206`-partial path.
- **`persist.LiveStore` lacks a per-agent read + an events-tail + a PING (NOT blocking — resolved by
  the `RedisReader` interface).** The frozen `persist.LiveStore` exposes `ReadSnapshot` but **no**
  `ReadAgent`, **no** events `XREAD`, and **no** `Ping`. Rather than widen `LiveStore` (a persist
  contract change), this SPEC defines a minimal read-side `RedisReader` interface (Ping / Get /
  HGetAll / XRead) satisfied by the same injected client. Flag to the architect whether these reads
  should instead live on `persist.LiveStore` (one tier owning all Redis access) — if so, persist's
  Public Interface gains `ReadAgent`/`TailEvents`/`Ping` and api drops `RedisReader`. P1 ships
  `RedisReader` to avoid editing the frozen persist SPEC.
- **`real_stats` shape inside the snapshot blob (NOT blocking).** data-contracts §1 nests
  `agents[].real_stats { StatID: float }` inside the `Snapshot.World` blob, but
  `persist.Snapshot.World` is `world.WorldState` serialized whole (its inner agent layout is
  `engine/world`-owned, and persist's own Open Question flags that the digest fields are unexported).
  To merge `real_stats` for `/api/agents/{id}?god=true`, api must locate this agent's stats inside
  that blob. Confirm with the architect whether (a) `engine/world` exports an accessor for a single
  agent's `RealStats`, or (b) persist exposes a `ReadAgentRealStats(run, id)` helper. P1 god-view
  endpoint depends on one of these; **does not block** the probe/SSE/snapshot ACs. Until resolved,
  the god-view merge is the only AC at risk. (The same unexported-digest problem blocks the
  `/api/god/*` reads of `TomDigest` — tracked in the godview SPEC.)
- **Graceful-shutdown SSE drain (NOT blocking).** On SIGTERM the wiring cancels `ctx`;
  `ListenAndServe` calls `http.Server.Shutdown`, which waits for handlers to return. Confirm the SSE
  loop checks `ctx.Done()` promptly enough that `Shutdown` does not hang past
  `terminationGracePeriodSeconds` (deployment §5). The `XRead` BLOCK duration bounds the worst-case
  drain; keep it short (e.g. a few seconds).

## Notes

- **Import path** is `github.com/dogring/bdg/platform/api` (module `github.com/dogring/bdg`; the
  `backend/` dir is the module root, **not** part of the path — matching `main.go`,
  `platform/persist/SPEC.md`, and the actual source under `backend/engine/*`). `platform/events/SPEC.md`
  shows `backend/engine/kernel/core`, which is a stale typo — the canonical engine import is
  `github.com/dogring/bdg/engine/kernel/core`.
- **api reuses persist's contract, not its implementation.** Keys come from `persist.Keyer`; the
  agent shape is `persist.AgentView`; the blob is `persist.Snapshot`. api never re-derives the
  keyspace or the god-view boundary policy — it inherits both from persist (single source,
  data-contracts §2).
- **SSE last-id tracking.** Start the tail from `"$"` (only new entries) or `"0"` (replay from the
  start) — P1 starts from `"$"` so a fresh connection sees only events emitted after it connected;
  advance `lastID` to each consumed entry ID so reconnects can resume (the resume-from-id refinement
  is a later concern). The `XRead` BLOCK keeps the goroutine parked between ticks without busy-wait.
- **Why a `RedisReader` interface, not `*redis.Client`.** The minimal surface (Ping/Get/HGetAll/
  XRead) lets every AC run against an in-memory fake with no live Redis — the same testability
  pattern `platform/events` uses for its `RedisClient`. The concrete go-redis client (the same one
  backing `LiveStore`) is wired at startup and invisible to api's tests. The `GodViewStore` follows
  the same pattern: a narrow read interface so `/api/god/why` tests run against an in-memory event
  fake with no live Postgres.
- **The god-view gate is startup-pinned (D8).** `GodMode` is set once in `Config` at construction
  (from a deploy flag), never per-request. The `?god=true` param only **requests** god-view; the
  startup flag **grants** it. This makes the boundary a deploy-time decision, not a client-toggleable
  bypass — the same separation persist enforces structurally with `AgentView`. The same gate guards
  all four `/api/god/*` routes (godview SPEC).
- **Probe semantics (deployment §5).** `/healthz` is liveness (process up — no dependency check, so
  a Redis blip does not restart the pod); `/readyz` is readiness (gated on Redis reachability, so a
  not-ready reader is pulled from the load-balancer rotation). Keep them distinct.
</content>
</invoke>
