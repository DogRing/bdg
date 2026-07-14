# SPEC — `platform/persist`

> Status: `DRAFT`
> Leaf level: `L8` (platform — depends only on engine public interfaces; architecture §3/§5 stage 8)  ·  Owner agent: `<filled by implementer>`
> Sub-spec: [`SPEC-world.md`](SPEC-world.md) — **WI-P4** env-state serialization (flora/animals/climate snapshot + Redis render keys + `WorldFrame` SSE projection; `docs/core/data-contracts.md §10`).

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
    "time"

    "github.com/dogring/bdg/engine/kernel/core"
    "github.com/dogring/bdg/engine/world"
)

// ── Versioning ───────────────────────────────────────────────────────────────

// SchemaVersion is the current snapshot/contract version (data-contracts §0).
// Bumped +1 on any backward-incompatible change to Snapshot/agent/event shape.
// Load REJECTS a blob whose schema_version != SchemaVersion (no silent migration).
const SchemaVersion int = 1

// ── Snapshot (data-contracts §1) ──────────────────────────────────────────────

// Snapshot carries the complete deterministic state for one tick. The run-driver
// encodes TWO storage views from it: the base CaptureSnapshot result is the
// deterministic Postgres backup blob; a COPY with the publication wrapper stamped
// is the Redis live snapshot. Same base Snapshot + same seed → byte-identical next tick.
// `World` carries the engine's serializable state (world.WorldState: tick, rng_state,
// agents incl. RealStats, objects, known sets, emerged roles). RealStats live ONLY in
// this blob (Postgres) — never in Redis live keys or events (god-view boundary, below).
type Snapshot struct {
    SchemaVersion int              `json:"schema_version"`
    RunID         core.RunID       `json:"run_id"`
    Tick          core.Tick        `json:"tick"`
    // Redis-live publication wrapper (data-contracts §1/§2) — OPERATIONAL metadata the
    // run-driver stamps on a COPY at flush time, NOT deterministic sim state. CaptureSnapshot
    // leaves these zero and the Postgres backup blob MUST omit them via omitempty:
    WorldRevision int64  `json:"world_revision,omitempty"` // single-world publication marker
    StreamCursor  string `json:"stream_cursor,omitempty"`  // Redis events entry ID the state reflects
    TerrainStatus string `json:"terrain,omitempty"`        // "on"|"off" — explicit env-terrain availability
    World         world.WorldState `json:"world"` // engine state (incl. rng_state, agents[])
}

// Encode serializes one Snapshot view to a byte-stable blob (JSON for P1; sorted map-key
// order, data-contracts §0). It stamps s.SchemaVersion = SchemaVersion before encoding.
// Deterministic backup callers pass the untouched CaptureSnapshot result; live Redis
// callers pass the publication-stamped copy.
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
    // InitMeta writes sim:{run}:meta { tick, schema_version, started_at, status }. It
    // deliberately does NOT touch the world_revision/terrain publication fields (HSET of the
    // listed fields only — the meta key is never deleted on regen), so a meta refresh can
    // never un-publish or re-publish a revision.
    InitMeta(ctx context.Context, run core.RunID, m RunMeta) error
    // PublishWorldRevision publishes the single-world revision marker (data-contracts §2):
    // ONE HSET writes {world_revision, terrain:"on"|"off"} onto sim:{run}:meta. The
    // run-driver calls it LAST — only after the revision's snapshot+terrain live baselines
    // were written successfully — so any reader observing the new revision finds matching,
    // revision-tagged baselines already servable. NOT a run generation: one run_id, one
    // active world; the marker only identifies which published map revision the current
    // baselines belong to (multi-world remains DEFERRED, docs/plans/run-generation.md).
    PublishWorldRevision(ctx context.Context, run core.RunID, rev int64, terrainOn bool) error
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
    CoverID core.ObjectID `json:"cover_id,omitempty"`
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
    SnowCover    float64  `json:"snow_cover"`
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
    Elevation   []float64   `json:"elevation,omitempty"` // per-cell relief ∈[0,1], len cols*rows; absent
                                                          // for worlds without generated elevation (the
                                                          // frontend falls back to per-type heights). Static
                                                          // — full grid only, never in terrain_delta.
}

// RunMeta is the sim:{run}:meta hash payload (§2).
type RunMeta struct {
    Tick          core.Tick `json:"tick"`
    SchemaVersion int       `json:"schema_version"`
    StartedAt     string    `json:"started_at"` // RFC3339; supplied by caller (no wall-clock here)
    Status        string    `json:"status"`     // "running" | "completed" | "failed"
}

// ── Postgres backup (data-contracts §3) ───────────────────────────────────────

// BackupStore persists the periodic deterministic base snapshot blob + the why-trace
// event rows (§3 replay/analytics source). The blob omits Redis-live publication fields.
// It is the ONLY tier that stores RealStats (inside the blob).
type BackupStore interface {
    // UpsertRun writes/updates the runs row (run_id, seed, schema_version, started_at,
    // ended_at, status, config_hash).
    UpsertRun(ctx context.Context, r RunRecord) error
    // WriteBackup persists ONE backup flush atomically — blob is the untouched,
    // deterministically encoded CaptureSnapshot result (no world_revision,
    // stream_cursor or terrain wrapper fields), and a single Postgres
    // transaction inserts the drained why-trace event rows (events: run_id,
    // tick, seq, agent_id, type, payload JSONB, created_at) AND the snapshots row (run_id,
    // tick, blob, created_at, last_event_seq), committing together or rolling
    // back together. last_event_seq is computed INSIDE the store as the max
    // Event.Seq of evs (the rows written in the same transaction — the boundary
    // can never reference rows that failed to persist); evs empty ⇒ NULL.
    // High-frequency render/operational events are excluded upstream and never
    // count. On error NOTHING was written: the caller may re-buffer evs and
    // retry on the next flush cadence (no partial batch). The run-driver's
    // diagnostic buffer is bounded at 5,000 events; a prolonged outage may
    // discard the oldest why-trace to preserve backend memory.
    // The pair (snapshot at tick T, events with seq ≤ last_event_seq) is a
    // consistent replay cut WITHIN one simulation process lifetime — seq is a
    // process-local counter and repeats across process restarts of the same
    // run (Notes "Seq scope"); cross-restart replay is not supported yet.
    // Called every BackupEveryTicks ticks.
    WriteBackup(ctx context.Context, run core.RunID, tick core.Tick, blob []byte, evs []core.Event) error
    // LatestSnapshot loads the most recently PERSISTED snapshots blob for a run
    // (replay/resume). Recency is storage order — newest created_at, ties by row
    // id — NOT the highest tick: POST /api/restart is a debugging rewind that
    // resets tick to 0 while preserving history, so a pre-restart high-tick row
    // must never shadow a newer post-restart low-tick row. Deliberately still
    // (tick, blob) WITHOUT last_event_seq: NO production caller exists yet —
    // the semantics are specified ahead of the future resume path, which will
    // need the blob alone; no consumer of the replay boundary exists either.
    // Extend the return when a replay tool lands.
    LatestSnapshot(ctx context.Context, run core.RunID) (tick core.Tick, blob []byte, err error)
    // PruneSnapshots applies the §3 Postgres retention/downsample policy to a run
    // (legacy method name retained): snapshots and events older than 3 days are
    // deleted by created_at; surviving snapshots are bucketed by created_at
    // (wall-clock; now is caller-supplied):
    //   · newer than 1h            → keep all (full backup cadence)
    //   · 1h–24h old               → keep the newest row per 10-minute bucket
    //   · 24h–3d old               → keep the newest row per 1-day bucket
    //   · older than 3d            → delete snapshots and events
    // Buckets are epoch-aligned (floor(epoch/bucket)); "newest" = latest
    // created_at, ties by tick, then row id. Called by the run-driver after each
    // successful backup; SQL maintenance runs immediately on the first request
    // and then at most once per run per 6 hours. Compression and incremental
    // snapshot chains are Out of Scope.
    PruneSnapshots(ctx context.Context, run core.RunID, now time.Time) error
    // ResetRunData atomically resets a run for POST /api/regen ("new map",
    // same run_id — the current single-world development mode; a multi-world
    // redesign is DEFERRED, docs/plans/run-generation.md): ONE Postgres
    // transaction deletes the
    // run's events + snapshots rows AND upserts the runs row to the
    // regenerated world's record (new seed/started_at/status/schema_version/
    // config_hash). All three steps commit together or roll back together, so
    // a failed reset leaves the old history AND the old runs metadata fully
    // intact and the run-driver ABORTS the regen (Notes "restart vs regen").
    // Other runs are untouched. NOT called on POST /api/restart: a restart is
    // a debugging rewind and never deletes Postgres history.
    ResetRunData(ctx context.Context, r RunRecord) error
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
// in PgBackupStore exactly. One additive exception rides along: an idempotent
// ALTER TABLE snapshots ADD COLUMN IF NOT EXISTS last_event_seq BIGINT upgrades tables
// created before the column existed (backfilled NULL = "no boundary recorded", correct).
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
- [ ] **Revision publication (`PublishWorldRevision`)**: one call HSETs
  `{world_revision, terrain}` onto `sim:{run}:meta`; `InitMeta` before/after it never clears
  those fields. Run-driver ordering (verified in `backend/main.go` tests): the revision is
  published only AFTER the same revision's snapshot (and terrain, when env is on) live writes
  succeeded; rebuild/reset failures and restarts never bump it; a failed baseline write leaves
  it unpublished until the next flush retries.

### §3 Postgres backup
- [ ] **Three tables mapped**: `UpsertRun` → `runs(run_id, seed, schema_version, started_at,
  ended_at, status, config_hash)`; `WriteBackup` → one transaction over
  `events(run_id, tick, seq, agent_id, type, payload JSONB, created_at)` +
  `snapshots(run_id, tick, blob, created_at, last_event_seq)`.
  Verified against a fake/embedded store.
- [ ] **Backup flush is atomic**: `WriteBackup` inserts the event batch and the snapshot row in one
  transaction — on failure NOTHING is stored (no partial event batch, no snapshot without its
  events, no events without their snapshot); the run-driver re-buffers the drained batch and the
  next flush persists it. Verified via failure injection on the fake (all-or-nothing) and the
  transaction plumbing on the SQL client, including a mid-batch event-INSERT failure and a payload
  marshal failure — both end in ROLLBACK with no COMMIT and no snapshot row.
- [ ] **`last_event_seq` semantics**: a flush that writes why-trace events stamps the snapshot row
  with the batch's max `Event.Seq` (computed in-store from the rows of the same transaction); a
  flush with no drained events stamps NULL. Excluded high-frequency events
  (`TickDone`/`AgentFrame`/`WorldFrame`/`SnapshotReady`) never count — their seqs appear as gaps.
- [ ] **`LatestSnapshot` = storage recency, not tick**: after a restart rewind writes a
  lower-tick row later, `LatestSnapshot` returns that newest row (`created_at DESC, id DESC`),
  never the pre-restart high-tick row.
- [ ] **Retention/downsample (`PruneSnapshots`)**: with rows spread over the `created_at`
  bands, pruning keeps all rows younger than 1h, exactly one (the newest; tick tie-break) per
  10-minute bucket for 1h–24h, and one per 1-day bucket for 24h–3d; this run's snapshots and
  events older than 3d are deleted. Other runs' rows are untouched and a second prune with the
  same `now` is a no-op. Verified against FakePg (policy) and the fake pgx client (SQL band
  bounds + bucket widths).
- [ ] **Prune cadence**: the first prune request runs immediately; a request less than 6 hours
  later performs no SQL, and the boundary request at 6 hours runs.
- [ ] **Regen reset (`ResetRunData`) is atomic**: one transaction deletes the run's `snapshots` +
  `events` rows AND refreshes the `runs` row to the new record; other runs are untouched. A
  failure at ANY step (including the runs-row upsert AFTER the deletes) rolls the whole reset
  back — old history and old runs metadata both survive, so the run-driver can abort the regen
  cleanly. Verified via failure injection on the fake (all-or-nothing) and a rollback test on the
  SQL client (upsert fails after the deletes ⇒ ROLLBACK).
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
- **Why-trace exclusion + one shared seq (run-driver policy, data-contracts §3/§4).** The
  run-driver (`backend/main.go`) stamps `Event.Seq` ONCE at fan-out (its Redis emitter runs
  `events.WithCallerSeq`), buffers only why-trace events for Postgres (high-frequency
  `TickDone`/`AgentFrame`/`WorldFrame`/`SnapshotReady` stay on the Redis stream only), and on the
  backup cadence hands the drained batch to `WriteBackup` — one transaction, so `last_event_seq`
  can never reference rows that failed to persist. On a failed flush the run-driver re-buffers the
  batch and retries on the next cadence. A transient outage within the 5,000-event buffer loses
  no why-trace; a longer outage drops oldest diagnostic events and logs the count.
  **Seq scope = one simulation PROCESS lifetime.** The fan-out counter starts at 0 when the
  process starts; `POST /api/restart` does not restart the process, so the sequence stays monotone
  across that debugging rewind. A REAL process restart begins a new process-local seq epoch at 0
  while the run_id and its (restart-preserved) Postgres history persist — seq values therefore
  REPEAT within one run across process lifetimes, and high-frequency events consume seqs that
  never reach Postgres, so no stored maximum can reconstruct the shared namespace.
  `(snapshot at tick T, events with seq ≤ last_event_seq)` is a consistent replay cut only among
  events written during the SAME process lifetime; replay across process restarts is NOT currently
  supported. Durable cross-process sequence identity belongs to the deferred world/run lifecycle
  design (`docs/plans/run-generation.md`).
- **restart vs regen (control-flow contract with `platform/api`) + regen failure policy.**
  `POST /api/restart` is a debugging rewind: same seed, live entity keys purged, Postgres history
  APPENDED to (never deleted). `POST /api/regen` is — **INTERIM, see below** — "new map, same
  run_id", in this order:
  1. the run-driver SEPARATES the old world's buffered why-trace from the rebuild: the buffer is
     drained before `ctl.rebuild`, so everything buffered during the rebuild belongs to the
     CANDIDATE world. A rebuild failure aborts: the candidate's partial construction events are
     DISCARDED and the old batch is restored exactly once (original seq order) — candidate events
     never leak into the continuing world's why-trace. The same drain/discard/restore discipline
     applies to a restart rebuild; on a SUCCESSFUL restart (append-only history) the old batch is
     re-queued AHEAD of the candidate's construction events, preserving seq order, while a
     successful regen DROPS the old batch (its Postgres rows were just deleted);
  2. **Postgres reset — MANDATORY, abort-on-failure**: `ResetRunData` (ONE transaction: delete
     events + delete snapshots + upsert the runs row to the new seed). If it fails NOTHING was
     changed and the regen is ABORTED: the rebuilt world is discarded, its construction events
     are dropped, the old buffer is restored, and the CURRENT world keeps running — a
     half-cleaned run is never presented as a new map;
  3. **Redis cleanup — best-effort (ACCEPTED interim limitation)**: deletion of the per-entity +
     snapshot/tick/events/flora/climate/terrain keys is ATTEMPTED; meta refreshed via `InitMeta`
     (which never touches the publication fields). Failures here are logged, not fatal: the
     immediate fresh flush + per-tick writes overwrite every fixed key, so stale data self-heals
     — residual risk: a stale per-entity hash whose DEL failed lingers until the run ends (see
     api SPEC);
  4. **fresh baseline flush**: the regenerated world's snapshot (wrapper stamped with the NEXT
     `world_revision` + the emitter's `stream_cursor` + the `terrain` flag) and, when env is on,
     the revision-tagged terrain blob are written to the live keys;
  5. **revision publication — LAST**: only after step 4's live snapshot (and terrain, when env is
     on) writes SUCCEED does the run-driver call `PublishWorldRevision` — a reader observing the
     new revision in meta is guaranteed matching baselines are servable. A transient step-4/5
     failure leaves the revision UNPUBLISHED (pending) and the next backup-cadence flush retries
     flush+publication; restart and every failed/aborted regen never bump the revision, and the
     serial tick goroutine makes concurrent regen signals coalesce (no revision reuse).
     **Process restart**: the run-driver reads the stored `world_revision` at boot and publishes
     `stored+1` with its first flush — never backwards, never reused (a restarted process
     rebuilds from the fixture and may publish a different map, so the stored value must not be
     re-claimed). Until that first flush, meta and baselines still describe the previous
     process's world self-consistently.
  Redis and Postgres are NOT one distributed transaction; the ordering above (abortable Postgres
  step BEFORE any Redis mutation, world swap only after the mandatory step, revision publication
  only after the baseline) is the consistency mechanism. Wall-clock (`time.Now`) for
  prune/refresh lives in the run-driver — never inside persist encode paths (D12 holds; the
  Redis-live snapshot WRAPPER's stream_cursor/world_revision are operational flush-time metadata.
  The run-driver encodes that wrapper separately from the untouched deterministic base snapshot
  passed to Postgres, so transport/publication changes cannot alter backup bytes (data-contracts §1).
  **Scope (human, 2026-07-11): current single-world development mode.** Destructive same-run
  regen IS the supported contract for this phase — exactly one world is exposed, all viewers
  reload after a regen (no resync signal), and there is no run pointer or automatic SSE run
  switching. A multi-world redesign (regen creates a NEW run and preserves the old one,
  dissolving the cross-store deletion problem) was chosen as the preferred future direction but
  is **DEFERRED** to be designed together with the world-selection layer —
  `docs/plans/run-generation.md` (design notes, not contracts). Do not harden the interim Redis
  deletion further (mandatory-with-compensation was considered and declined), and do not replace
  this path with generation-based storage now.
