# SPEC — `platform/persist`

> Status: `DRAFT`
> Leaf level: `L8` (platform — depends only on engine public interfaces; architecture §3/§5 stage 8)  ·  Owner agent: `<filled by implementer>`
> Sub-spec: [`SPEC-world.md`](SPEC-world.md) — **WI-P4** env-state serialization (flora/animals/climate snapshot + Redis render keys + `WorldFrame` SSE projection; `docs/data-contracts.md §10`).

## Purpose

The simulation's **serialization & storage boundary**: it converts the engine's deterministic
`world.WorldState` into an on-the-wire `Snapshot` (data-contracts §1), drives the **Redis live
keyspace** (§2 — decision/render-visible fields only) and the **periodic Postgres backup** (§3 —
the full god-view blob, replay source). It is the only place the engine's state is encoded,
versioned, persisted, and re-loaded for byte-identical resume (D12). It performs **all IO**
(Redis/Postgres clients); the engine never touches storage.

## Public Interface

> The *only* contract callers (`main`, `platform/api`) and the implementer depend on. Module path
> root is `github.com/dogring/bdg` (the `backend/` dir is the module root, not part of the import
> path — e.g. `github.com/dogring/bdg/engine/world`).

```go
package persist

import (
    "context"
    "errors"

    "github.com/dogring/bdg/engine/kernel/core"
    "github.com/dogring/bdg/engine/world"
)

// ── Versioning ───────────────────────────────────────────────────────────────

// SchemaVersion is the current snapshot/contract version (data-contracts §0).
// Bumped +1 on any backward-incompatible change to Snapshot/agent/event shape.
// Load REJECTS a blob whose schema_version != SchemaVersion (no silent migration).
const SchemaVersion int = 1

// ── Snapshot (data-contracts §1) ──────────────────────────────────────────────

// Snapshot is the complete deterministic state for one tick — the unit persist
// serializes/deserializes. Same Snapshot + same seed → byte-identical next tick.
// `World` carries the engine's serializable state (world.WorldState: tick, rng_state,
// agents incl. RealStats, objects, known sets, emerged roles). RealStats live ONLY in
// this blob (Postgres) — never in Redis live keys or events (god-view boundary, below).
type Snapshot struct {
    SchemaVersion int              `json:"schema_version"`
    RunID         core.RunID       `json:"run_id"`
    Tick          core.Tick        `json:"tick"`
    World         world.WorldState `json:"world"` // engine state (incl. rng_state, agents[])
}

// Encode serializes a Snapshot to a deterministic byte blob (JSON for P1; the encoding
// MUST be byte-stable — sorted map-key order, data-contracts §0). It stamps
// s.SchemaVersion = SchemaVersion before encoding.
func Encode(s Snapshot) ([]byte, error)

// Decode parses a blob back into a Snapshot. It REJECTS (returns ErrSchemaMismatch)
// when the decoded schema_version != SchemaVersion — persist refuses to load and
// demands a migration path (data-contracts §5). No partial/lossy load.
func Decode(blob []byte) (Snapshot, error)

// CaptureSnapshot builds a Snapshot from the live world (calls world.State()).
// Pure (no IO); the only place the god-view (RealStats) is read out of the engine.
func CaptureSnapshot(runID core.RunID, w *world.World) Snapshot

// RestoreInto applies a decoded Snapshot back into a constructed (empty) world via
// world.RestoreState — the spatial hash is rebuilt from positions; the root rng_state
// round-trips (resume invariant). Returns an error on schema mismatch.
func RestoreInto(w *world.World, s Snapshot) error

// ErrSchemaMismatch is returned by Decode/RestoreInto when a blob's schema_version
// does not equal SchemaVersion.
var ErrSchemaMismatch = errors.New("persist: snapshot schema_version mismatch")

// ── Redis live store (data-contracts §2) ──────────────────────────────────────

// Keyer builds the exact §2 keyspace for a run. The ONLY source of key strings;
// callers never format keys by hand.
//   sim:{run}:meta        sim:{run}:tick        sim:{run}:snapshot
//   sim:{run}:agent:{id}  sim:{run}:events
//   sim:{run}:animal:{id} sim:{run}:flora  sim:{run}:climate  sim:{run}:terrain  (WI-P4)
type Keyer struct{ Run core.RunID }

func (k Keyer) Meta() string                   // sim:{run}:meta
func (k Keyer) Tick() string                   // sim:{run}:tick
func (k Keyer) SnapshotKey() string            // sim:{run}:snapshot
func (k Keyer) Agent(id core.AgentID) string   // sim:{run}:agent:{id}
func (k Keyer) Events() string                 // sim:{run}:events
func (k Keyer) Animal(id core.ObjectID) string // sim:{run}:animal:{id} (WI-P4)
func (k Keyer) Flora() string                  // sim:{run}:flora       (WI-P4)
func (k Keyer) Climate() string                // sim:{run}:climate     (WI-P4)
func (k Keyer) Terrain() string                // sim:{run}:terrain     (WI-P4)

// LiveStore writes/reads the Redis live keys. It exposes ONLY render/decision-visible
// fields (§2) — it MUST NOT write RealStats to any agent hash (god-view boundary).
type LiveStore interface {
    // WriteTick sets sim:{run}:tick (fast read) and refreshes meta.tick.
    WriteTick(ctx context.Context, run core.RunID, tick core.Tick) error
    // WriteSnapshot stores the latest serialized Snapshot at sim:{run}:snapshot.
    WriteSnapshot(ctx context.Context, run core.RunID, blob []byte) error
    // WriteAgent upserts the per-agent render hash (pos, goal, action, mood — §2).
    // The AgentView it accepts CANNOT carry RealStats (compile-time god-view guard).
    WriteAgent(ctx context.Context, run core.RunID, v AgentView) error
    // ── WI-P4 env render keys (data-contracts §2; see SPEC-world.md) ──
    // WriteAnimal upserts sim:{run}:animal:{id}. AnimalView CANNOT carry
    // Stats/Drives/Vital (god-view guard). Called only when fauna is installed.
    WriteAnimal(ctx context.Context, run core.RunID, v AnimalView) error
    // WriteFlora replaces sim:{run}:flora with the full live plant render set
    // (periodic-full, JSON array; nil ⇒ "[]"). Called only when flora is installed.
    WriteFlora(ctx context.Context, run core.RunID, plants []FloraView) error
    // WriteClimate upserts the sim:{run}:climate ambient hash. Called only when
    // climate is installed.
    WriteClimate(ctx context.Context, run core.RunID, v ClimateView) error
    // WriteTerrain replaces sim:{run}:terrain with the full render terrain grid —
    // the SAME JSON shape GET /api/terrain forwards verbatim (api SPEC). Called
    // only when navmap is installed.
    WriteTerrain(ctx context.Context, run core.RunID, v TerrainView) error
    // InitMeta writes sim:{run}:meta { tick, schema_version, started_at, status }.
    InitMeta(ctx context.Context, run core.RunID, m RunMeta) error
    // ReadSnapshot loads the latest snapshot blob (for resume / hand-off to backup).
    ReadSnapshot(ctx context.Context, run core.RunID) ([]byte, error)
    // Expire applies the TTL/expiry policy for a run (called on completion).
    Expire(ctx context.Context, run core.RunID) error
}

// AgentView is the render/observation-visible subset written to sim:{run}:agent:{id}
// (§2). It deliberately has NO RealStats / ToM field — the type is the god-view boundary.
type AgentView struct {
    ID     core.AgentID `json:"id"`
    Pos    core.Vec2    `json:"pos"`
    Goal   string       `json:"goal"`
    Action string       `json:"action"`
    Mood   float64      `json:"mood"`
}

// ── WI-P4 env render view types (built by engine/world.RenderView(); persist
//    owns the wire shape — see SPEC-world.md for the full serialization spec) ──

// AnimalView → sim:{run}:animal:{id} (§2). NO Stats/Drives/Vital (god-view guard).
type AnimalView struct {
    ID      core.ObjectID `json:"id"`
    Pos     core.Vec2     `json:"pos"`
    Species string        `json:"species"`
    Action  string        `json:"action"`
    Heading float64       `json:"heading"`
    Stamina float64       `json:"stamina"`
}

// FloraView is one row of the sim:{run}:flora JSON array (§2). Stage is DERIVED
// from length (D9) — raw length is not render-visible.
type FloraView struct {
    ID      core.ObjectID `json:"object_id"`
    Species string        `json:"species"`
    Pos     core.Vec2     `json:"pos"`
    Stage   int           `json:"stage"`
    Width   float64       `json:"width"`
}

// ClimateView → sim:{run}:climate ambient hash (§2). ApparentTemp nil ⇒ omitted.
type ClimateView struct {
    Temperature  float64  `json:"temperature"`
    ApparentTemp *float64 `json:"apparent_temp,omitempty"`
    Moisture     float64  `json:"moisture"`
    Raining      bool     `json:"raining"`
    WindDir      float64  `json:"wind_dir"`
    WindMag      float64  `json:"wind_mag"`
    HourOfDay    int      `json:"hour_of_day"`
    DayNight     string   `json:"day_night"`
    YearFraction float64  `json:"year_fraction"`
}

// TerrainView → sim:{run}:terrain (§2) — already shaped exactly as the
// GET /api/terrain response (api forwards the bytes verbatim).
type TerrainSize struct{ Cols, Rows int } // json: cols, rows (flat-top hex offset grid, hex-grid.md)
type TerrainView struct {
    CellSize    float64     `json:"cell_size"`   // hex circumradius
    Orientation string      `json:"orientation"` // "flat" (flat-top hex)
    Size        TerrainSize `json:"size"`
    Terrain     []string    `json:"terrain"`     // offset(col,row) array, i=row*Cols+col
    Wear        []float64   `json:"wear,omitempty"`
}

// RunMeta is the sim:{run}:meta hash payload (§2).
type RunMeta struct {
    Tick          core.Tick `json:"tick"`
    SchemaVersion int       `json:"schema_version"`
    StartedAt     string    `json:"started_at"` // RFC3339; supplied by caller (no wall-clock here)
    Status        string    `json:"status"`     // "running" | "completed" | "failed"
}

// ── Postgres backup (data-contracts §3) ───────────────────────────────────────

// BackupStore persists the periodic full blob + the why-trace event rows (§3 replay/
// analytics source). It is the ONLY tier that stores RealStats (inside the blob).
type BackupStore interface {
    // UpsertRun writes/updates the runs row (run_id, seed, schema_version, started_at,
    // ended_at, status, config_hash).
    UpsertRun(ctx context.Context, r RunRecord) error
    // WriteSnapshot inserts one snapshots row (run_id, tick, blob, created_at). Called
    // every BackupEveryTicks ticks (driven by the SnapshotReady signal / caller).
    WriteSnapshot(ctx context.Context, run core.RunID, tick core.Tick, blob []byte) error
    // WriteEvents appends event rows (run_id, tick, seq, agent_id, type, payload JSONB).
    WriteEvents(ctx context.Context, run core.RunID, evs []core.Event) error
    // LatestSnapshot loads the most recent snapshots blob for a run (replay/resume).
    LatestSnapshot(ctx context.Context, run core.RunID) (tick core.Tick, blob []byte, err error)
}

// RunRecord mirrors the §3 runs table.
type RunRecord struct {
    RunID         core.RunID
    Seed          int64
    SchemaVersion int
    StartedAt     string // RFC3339, caller-supplied
    EndedAt       string // RFC3339, empty while running
    Status        string
    ConfigHash    string // hash of loaded content (reproducibility key, §3)
}

// BackupEveryTicksEnv is the env var (data-contracts §3 backup_every_ticks) that sets
// how often a snapshot row is written to Postgres. Read by the caller/wiring, surfaced
// here as the canonical name.
const BackupEveryTicksEnv = "BACKUP_EVERY_TICKS"

// ── Real infrastructure constructors (this module performs ALL IO) ─────────────

// NewRedisLiveStore returns a LiveStore backed by a Redis-like client. The client is
// injected (an interface), so the run-driver supplies the concrete go-redis client and
// tests supply FakeRedis. ttl is the expiry applied to snapshot/agent keys (0 = none).
func NewRedisLiveStore(client goRedisClient, ttl time.Duration) *RedisLiveStore

// NewPgBackupStorePool returns a BackupStore backed by a live *pgxpool.Pool. Because the
// pgxClient adapter interface is unexported, the real-Postgres constructor MUST live in
// this package — the run-driver cannot satisfy pgxClient from outside. (FakePg is the
// test counterpart.)
func NewPgBackupStorePool(pool *pgxpool.Pool) *PgBackupStore

// EnsureSchema creates the runs/snapshots/events tables (data-contracts §3) if absent —
// a first-run bootstrap, NOT a migration tool (versioned migrations remain Out of Scope).
// Safe to call on every startup (CREATE TABLE IF NOT EXISTS); table shapes mirror the SQL
// in PgBackupStore exactly.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error
```

> persist DEFINES no simulation vocabulary. `world.WorldState`/`world.World` are `engine/world`'s
> contract; `core.RunID`/`AgentID`/`Tick`/`Vec2`/`Event` are `engine/kernel/core`'s; `rng.RNGState`
> (carried inside `world.WorldState`) is `engine/kernel/rng`'s. This module encodes & transports them.

## Dependencies

- `engine/world` — `world.WorldState` (the serializable engine state), `world.World`,
  `World.State()` / `World.RestoreState(WorldState)`, `World.CurrentTick()`,
  `World.AgentIDs()` / `World.AgentOf()` (to build per-agent `AgentView`s for Redis). persist
  reads the world's **public** state only; it never reaches into agent internals.
- `engine/kernel/core` — `RunID`, `AgentID`, `Tick`, `Vec2`, `Event` (the event rows it persists).
- `engine/kernel/rng` — only transitively: `rng.RNGState` rides inside `world.WorldState` and round-trips
  through Encode/Decode; persist does not import rng for logic.
- **Contract — NOT imported by the engine**: the engine emits `SnapshotReady`/events through the
  injected `core.EventEmitter`; persist (or the wiring around it) listens and reacts. The engine
  never imports persist (architecture §1 — platform serializes engine state, dependency inversion).
- **External infra**: a Redis client and a Postgres driver (platform tier — the only IO in the
  whole module). The concrete client is injected into the `LiveStore`/`BackupStore`
  implementations so tests use an in-memory fake.
- **Contract**: `data-contracts.md` §1/§2/§3/§5 (snapshot shape, keyspace, tables, versioning) and
  `BACKUP_EVERY_TICKS` env. These are frozen here; a change bumps `SchemaVersion`.

## Owned Data

- The `Snapshot` wire type and the `SchemaVersion` constant — the cross-module serialization
  contract (data-contracts §0/§1). persist OWNS the encoding; no other module serializes world state.
- The Redis **keyspace strings** (`Keyer`) — the single source of `sim:{run}:*` formats.
- The Postgres **table mapping** (`runs`/`snapshots`/`events`) and the live/backup tier split.
- The **god-view boundary policy**: `RealStats` is read out of the engine only into the Postgres
  blob; the Redis `AgentView` type structurally cannot carry it.
- persist OWNS no simulation state and mutates no engine state except via `world.RestoreState`
  during a resume.

## Invariants

> A violation here is a bug — these are mechanically checkable.

- **D12 — Resume invariant (HARD).** Resuming from a tick-T `Snapshot` and running k more ticks
  produces state **byte-identical** to running from tick-0 through tick-(T+k) uninterrupted. The
  root `rng_state` round-trips (`world.WorldState.RNGState`); the spatial hash is rebuilt from
  positions on restore; capture/restore iterate sorted ids (no `map`-order dependence). This is the
  same determinism contract as CLAUDE.md D12 and `engine/world` Invariant 8 / testing.md §1.
- **Byte-deterministic encoding (data-contracts §0).** `Encode` is stable across processes and
  runs: sorted map-key order, no `time.Now()` in the payload (timestamps are caller-supplied
  fields, never read from the wall-clock inside encode). `Encode(Decode(b)) == b` for any blob this
  version produced.
- **Schema-version gate (data-contracts §5).** `Decode`/`RestoreInto` REJECT any blob whose
  `schema_version != SchemaVersion` with `ErrSchemaMismatch`. No best-effort partial load, no
  silent field-drop migration.
- **God-view / information-hiding boundary (D8) — `real_stats` is Postgres-only.** `RealStats`
  appears ONLY inside the Postgres `snapshots.blob`. It MUST NOT be written to the Redis
  `sim:{run}:agent:{id}` hash, MUST NOT appear in any `events` row payload, and MUST NOT reach SSE.
  The `AgentView` type carries no stats field, so the Redis path cannot leak it by construction.
  (Agents decide from `ToM[self]`, not god-view — D8.)
- **Live keys hold only what render/observation needs (data-contracts §2).** `sim:{run}:agent:{id}`
  = `{pos, goal, action, mood}` only. Full state lives in `sim:{run}:snapshot` / Postgres.
- **No logic by `map` iteration (D12).** Where ordering is observable (event batches, agent-view
  writes), iterate `world.AgentIDs()` (sorted) — the deterministic order the engine guarantees.
- **persist performs all IO; the engine performs none.** The split is one-directional: the engine
  signals `SnapshotReady` and emits events through the injected emitter; persist reacts. persist
  imports no `engine/*` writer of state beyond `world.RestoreState`.

## Acceptance Criteria (testable)

### §1 Snapshot serialization
- [ ] **Required fields present**: an encoded `Snapshot` round-trips `schema_version`, `rng_state`
  (inside `World`), `world`, and `agents[]` (inside `World`); a golden of the encoded bytes is
  byte-stable across two runs (sorted-key JSON, data-contracts §0).
- [ ] **Schema-version reject on load**: `Decode` of a blob whose `schema_version != SchemaVersion`
  returns `ErrSchemaMismatch` and no `Snapshot` (table-driven: version-1 ok, version-0/2 rejected).
  `RestoreInto` propagates the same error before mutating the world.
- [ ] **Encoding is JSON (P1) and deterministic**: `Encode(Decode(b)) == b` for a captured blob;
  map fields (`NeedIntensities`, `Inventory`, `SelfEstStats`) serialize in sorted-key order.
- [ ] **CaptureSnapshot ↔ world.State()**: `CaptureSnapshot(run, w).World` equals `w.State()`; the
  captured `Tick` equals `w.CurrentTick()` and `RunID` is stamped.

### §2 Redis keyspace
- [ ] **Exact key formats** (`Keyer`): for `run="dev"`, `agent_id="agent_01"` →
  `sim:dev:meta`, `sim:dev:tick`, `sim:dev:snapshot`, `sim:dev:agent:agent_01`, `sim:dev:events`.
  Table-driven over several run/agent ids.
- [ ] **Live agent hash is render-only**: `WriteAgent` persists exactly `{pos, goal, action, mood}`
  for `sim:{run}:agent:{id}`; the `AgentView` type has no stats/ToM field (compile-time + a
  reflection/grep guard that no `real_stats` key is ever written under `:agent:`).
- [ ] **Tick / snapshot / meta keys written**: `WriteTick` sets `sim:{run}:tick`; `WriteSnapshot`
  sets `sim:{run}:snapshot` to the blob; `InitMeta` writes the `{tick, schema_version, started_at,
  status}` hash. Verified against an in-memory fake Redis.
- [ ] **Events stream append**: events are XADD-appended to `sim:{run}:events` in emission order
  (the SSE tail reads them); no event payload contains `real_stats` (god-view guard).
- [ ] **TTL / expiry policy (data-contracts §2)**: `Expire` removes (or sets TTL on) all
  `sim:{run}:*` keys for a completed run; active runs keep their keys. (Live keys hold only active
  runs; on completion → back up to Postgres, then expire.)

### §3 Postgres backup
- [ ] **Three tables mapped**: `UpsertRun` → `runs(run_id, seed, schema_version, started_at,
  ended_at, status, config_hash)`; `WriteSnapshot` → `snapshots(run_id, tick, blob, created_at)`;
  `WriteEvents` → `events(run_id, tick, seq, agent_id, type, payload JSONB)`. Verified against a
  fake/embedded store.
- [ ] **Backup cadence from `BACKUP_EVERY_TICKS`**: with `BACKUP_EVERY_TICKS=N`, a snapshot row is
  written exactly once every N ticks (driven by the `SnapshotReady` signal the world emits every
  `BackupEveryTicks` ticks); off-cadence ticks write no `snapshots` row. Table-driven over N.
- [ ] **Replay key persisted (data-contracts §3)**: the `runs` row stores `seed + config_hash` so a
  run is reproducible from `seed + config_hash + last snapshot`.
- [ ] **Postgres blob is the only RealStats tier**: the `snapshots.blob` decodes to a `Snapshot`
  carrying `RealStats`; the Redis agent hash and every `events.payload` for the same run carry none
  (cross-tier god-view assertion).

### Resume & determinism (HARD invariant — testing.md §1)
- [ ] **Resume invariant (D12)**: capture a `Snapshot` at tick T (`CaptureSnapshot`), `Encode` →
  `Decode` → `RestoreInto` a fresh world, run k ticks; the resulting tick-(T+k) state digest is
  **byte-identical** to a world run 0→(T+k) uninterrupted. The root `rng_state` round-trips and the
  spatial hash is rebuilt from positions. Golden under `testdata/golden/resume_*.json`.
- [ ] **Cross-process determinism**: `Encode` of the same world produced in two separate process
  runs (same seed + content) is byte-identical (sorted-key encoding; no wall-clock in the payload).

### Boundary guards
- [ ] **No `real_stats` outside the Postgres blob (D8 guard)**: a grep/reflection guard asserts the
  `AgentView` type and the `events` payload path expose no `RealStats`/`ToM` field; only the
  `Snapshot.World` blob (→ Postgres) does.
- [ ] **No engine import of persist (architecture §1 guard)**: `engine/*` does not import
  `platform/persist`; persist reacts to the world's emitted `SnapshotReady`/events.

## Out of Scope

- **The SSE endpoint / HTTP transport** → `platform/api` (architecture §3; consumes
  `sim:{run}:events` + the latest snapshot). persist only writes the stream/keys.
- **Event construction & the concrete `core.EventEmitter`** → `platform/events` (why-trace + SSE
  serialization, data-contracts §4). persist persists `core.Event` rows; it does not build events
  or decide the SSE/god-view filter for the live stream (that flag is `platform/events`/`api`).
- **Content loading / `config_hash` computation** → `platform/config` (loads `content/` → registries
  + a content hash). persist receives the hash in `RunRecord.ConfigHash`; it does not hash content.
- **The tick loop, `SnapshotReady` emission cadence, and what state is captured** → `engine/world`
  (it owns `World.State()`/`RestoreState` and emits `SnapshotReady` every `BackupEveryTicks`).
  persist serializes/transports; it does not decide capture cadence (the world signals).
- **Migration tooling for a bumped `SchemaVersion`** → a future `platform/persist/migrate` sub-tool
  (data-contracts §5 demands a migration path; persist only REFUSES a mismatched load here).

## Open Questions

- **Encoding swap (NOT blocking P1).** data-contracts §0 allows switching JSON → a deterministic
  dense encoding (gob/protobuf) "after it stabilizes," provided byte-determinism holds. P1 ships
  JSON; revisit once snapshot size becomes a problem. Escalate before changing the wire format
  (it bumps `SchemaVersion`).
- **Snapshot digest vs full ToM/Known (data-contracts §1/§3).** §1 says the snapshot stores a
  *digest* (top-K ToM relationships, strong values) and the full O(N²) set is a Postgres-only
  option. `world.WorldState` currently carries `SelfEstStats` + per-agent `Known` sets but not the
  full per-observer ToM. Confirm with the architect whether the Postgres blob should additionally
  persist the full ToM matrix (a separate `BackupStore` method) or whether the world digest suffices
  for P1 replay. Does NOT block the resume invariant (that round-trips exactly what `world.State()`
  captures).
- **`world.WorldState` is exported but its inner digest types are unexported.** persist serializes
  `world.WorldState` as an opaque whole (its JSON tags are owned by `engine/world`). If persist must
  reshape agents into the data-contracts §1 `agents[]{ real_stats, body, tom_digest, known_digest }`
  layout explicitly, `engine/world` must export the field accessors. P1: serialize `WorldState`
  whole (the resume contract only needs round-trip fidelity). Flag if `platform/api` needs a
  different on-wire agent shape than the engine's internal digest.
- **Redis/Postgres client choice & connection wiring (NOT blocking the pure layer).** `Encode`/
  `Decode`/`Keyer`/`CaptureSnapshot`/`RestoreInto` are pure and testable with no infra. The
  concrete `LiveStore`/`BackupStore` clients (and the `SnapshotReady` listener loop) are the IO
  layer — implement against an in-memory fake first; pick the driver at wiring time.

## Notes

- **The serialization boundary is `world.WorldState`** (`engine/world/state.go` —
  `World.State()` / `World.RestoreState`). It already captures `Tick`, `RNGState`, per-agent public
  state (incl. `RealStats`, `SelfEstStats`, `NeedIntensities`, `Inventory`, plan, coping, latents),
  objects, per-agent `Known` sets, and the P6 `EmergedRoles` set — all in sorted-id order (D12).
  persist wraps it in `Snapshot{schema_version, run_id, tick, world}` and is the resume contract.
  Do **not** reimplement capture/restore — call `World.State()`/`RestoreState`.
- **Snapshot field mapping (data-contracts §1).** `schema_version` ← `SchemaVersion`; `run_id` ←
  caller; `tick` ← `World.CurrentTick()`; `rng_state` ← `WorldState.RNGState` (`rng.RNGState`, a
  base64 PCG blob — `engine/kernel/rng/SPEC.md`); `world.objects[]` ← `WorldState.Objects`; `agents[]` ←
  `WorldState.Agents` (carries the god-view `RealStats` → Postgres only).
- **Two tiers, one boundary.** Redis (`LiveStore`) = live, render/decision-visible, TTL'd, lossy.
  Postgres (`BackupStore`) = periodic, full, durable, the replay/why-trace source. The full
  `Snapshot` blob is identical in both `sim:{run}:snapshot` and `snapshots.blob`; the difference is
  the **per-agent live hash** (`AgentView`, no god-view) vs the **blob** (full, god-view).
- **`real_stats` boundary (data-contracts §4, D8).** The §4 SSE policy and §1 comment both flag
  `real_stats` as god-view "backup only." The cleanest enforcement is structural: the Redis-facing
  `AgentView` has no stats field, so the live path can never leak it; only the encoded `Snapshot`
  blob (→ Postgres) carries `RealStats`.
- **No wall-clock inside encode (D12).** `started_at`/`created_at`/`ended_at` are caller-supplied
  RFC3339 strings (or DB `now()` on the Postgres side, which is outside the deterministic payload).
  The encoded `Snapshot` blob contains no timestamp, so two runs at different real times produce the
  identical blob — the prerequisite for the cross-process determinism AC.
- **`BackupEveryTicks` lives in content** (`content/balance.yaml world.backup_every_ticks` →
  `world.Config.BackupEveryTicks`); the env var `BACKUP_EVERY_TICKS` (data-contracts §3) is the
  deploy-time override the wiring reads. The world emits `SnapshotReady` on that cadence; persist
  reacts — it does not own the counter.
