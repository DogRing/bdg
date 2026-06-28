# SPEC — `engine/world`

> Status: `READY`
> Leaf level: `L6`  ·  Owner agent: `implementer`

## Sub-specs (domain decomposition)

| File | Scope |
|------|-------|
| [`SPEC-tick.md`](SPEC-tick.md) | D12 tick loop · per-agent RNG fork · conflict resolution · outcome resolution · tick ACs · scale/perf ACs |
| [`SPEC-emergent.md`](SPEC-emergent.md) | Emergent reliance-cluster detection (RoleEmerged, D2) · ToM pruning · emergent/prune ACs |
| [`SPEC-world-env.md`](SPEC-world-env.md) | **WI-P1** env orchestration: climate/flora/decay pure-Step driving + cadence · climate→navmap `SetTerrain` bridge · env sampling (flora `SiteInput` / decay env) · flora-shade→perception · env-OFF neutrality (`docs/world-integration.md`) |
| [`SPEC-world-fauna.md`](SPEC-world-fauna.md) | **WI-P2**: animals + `fauna.Step` (plan phase) · combined agent+animal apply (F41) · scent deposit/spread/commit · `EnvSample`/`TerrainSampler` adapters |
| [`SPEC-mine-terrain.md`](SPEC-mine-terrain.md) | **WI-P3**: `Mine` terrain-driven extraction path (resources R1) — stone anywhere / clay on soil from terrain `extract`, coexisting with the `ore_node` node path |

Read the relevant sub-spec for detailed mechanics; this file is the **entry point** — it holds the
public contract, wiring rules, cross-cutting invariants, and out-of-scope boundaries.

---

## Purpose

The **simulation orchestrator** (architecture §2, the top of the engine DAG — imported by no
engine module, no cycles). It owns the global mutable state — every `agent.Agent`, every placed
object, the `spatial.SpatialHash`, the `core.Tick` counter, and the root seeded `*rng.RNG` — and
drives the deterministic tick loop in the mandatory **read → plan → collect → apply** order (D12;
see [`SPEC-tick.md`](SPEC-tick.md)). It spawns agents (sampling `RealStats` from the stat
`GenSpec`), places objects, resolves each intent's outcome **against Real Stats** (D8 — the only
place Real Stats are read), arbitrates conflicts deterministically, advances the clock, and detects
**emergent reliance clusters** (`RoleEmerged`, D2 — detection only, no role type; see
[`SPEC-emergent.md`](SPEC-emergent.md)). It implements `agent.WorldView` so agents perceive and
plan against a frozen per-tick snapshot. It emits observability events through an **injected**
`core.EventEmitter` and never touches IO / Redis / Postgres / SSE.

---

## Public Interface

```go
package world

import (
    "github.com/dogring/bdg/engine/mind/actions"
    "github.com/dogring/bdg/engine/agent"
    "github.com/dogring/bdg/engine/kernel/core"
    "github.com/dogring/bdg/engine/mind/needs"
    "github.com/dogring/bdg/engine/mind/perception"
    "github.com/dogring/bdg/engine/mind/planner"
    "github.com/dogring/bdg/engine/kernel/rng"
    "github.com/dogring/bdg/engine/space/spatial"
    "github.com/dogring/bdg/engine/mind/stats"
    "github.com/dogring/bdg/engine/mind/tom"
    "github.com/dogring/bdg/engine/mind/values"
    "github.com/dogring/bdg/engine/kernel/worldtime"
)

// ── Config (every constant from content/balance.yaml world.*; none hardcoded, D10) ──

// Config bundles the world-level tunables, injected by the caller (read from
// content/balance.yaml world.* via platform/config). The world hardcodes NO numeric constant
// (D10). worldtime.Config (tick_minutes/day_minutes/…) is carried separately on Clock.
//
// IMPLEMENTER: before wiring this struct you MUST add the three keys below to BOTH
//   content/balance.yaml  (under the existing `world:` block)
//   content/schema/balance.schema.json  (under the `world` object definition)
// with the following canonical keys and representative defaults:
//   world:
//     reliance_threshold:      0.5     # share ∈ (0,1] — fraction of agents pointing at one holder
//     outcome_difficulty_base: 50.0    # stat level at which an action succeeds with neutral effort
//     backup_every_ticks:      60      # emit SnapshotReady every N ticks
// Add the three entries to the `required` array in the schema and specify their types
// (number / number / integer) and minimum constraints (>0 / >0 / ≥1).
// platform/config performs structural validation against the schema BEFORE calling world.New —
// so a missing key will be caught at startup, not at runtime.
//
// IMPLEMENTER (scale extension): also add the five world.* keys below to BOTH files, with
// these canonical keys and defaults (all OPTIONAL in the schema — a missing key falls back to
// the documented default, so existing content keeps working unchanged):
//   world:
//     plan_workers:       0     # goroutine pool size for the parallel plan phase; 0 → runtime.NumCPU()
//     plan_interval:      1     # round-robin replan stride; 1 → every agent replans every tick
//     prune_interval:     360   # ticks between ToM prune passes
//     prune_threshold:    720   # LastSeen gap (ticks) past which a ToM subject is decayed then removed
//     prune_decay_factor: 0.0   # multiplier applied to a pruned Belief before removal (0.0 = zero out)
// Schema types/constraints: plan_workers integer ≥0; plan_interval integer ≥1; prune_interval
// integer ≥1; prune_threshold integer ≥1; prune_decay_factor number ∈ [0,1]. These keys are
// NOT in the `required` array (backward compatibility — see Open Questions §Scale).
type Config struct {
    SpatialHashCell          float64 // balance.yaml world.spatial_hash_cell — SpatialHash cell edge
    RoleConvergenceThreshold float64 // balance.yaml politics.role_convergence_threshold — share of
                                     //   agents relying on one holder for a Function → emit RoleEmerged
                                     //   on the rising edge (D2). P6: supersedes the retired
                                     //   world.reliance_threshold placeholder; drives the live full scan.
    OutcomeDifficultyBase    float64 // balance.yaml world.outcome_difficulty_base — base difficulty an
                                     //   action's used stat is resolved against (vs Real Stat).
    BackupEveryTicks         int     // balance.yaml world.backup_every_ticks — emit SnapshotReady every
                                     //   N ticks (the world signals; it does NOT write to persist).
    // ── Scale-extension tunables (parallel plan · time-slicing · ToM prune) ──
    // Read from content/balance.yaml world.* by platform/config (same path as the keys above;
    // none hardcoded, D10). The defaults reproduce the prior single-threaded, replan-every-tick,
    // never-prune world byte-for-byte, so adding them changes no existing behaviour.
    PlanWorkers      int     // balance.yaml world.plan_workers — goroutine pool size for the
                             //   parallel plan phase. 0 → runtime.NumCPU(). The fan-out is read-only
                             //   on the snapshot; intents are sorted before apply (D12) so the result
                             //   is byte-identical to PlanWorkers == 1.
    PlanInterval     int     // balance.yaml world.plan_interval — round-robin replan stride.
                             //   0/1 → every agent replans every tick (backward-compatible default).
                             //   N → an agent at sorted-index i replans only on ticks where T mod N
                             //   == (i mod N); off-slot agents only execute their durative step.
    PruneInterval    int     // balance.yaml world.prune_interval — ticks between ToM prune passes.
                             //   The prune pass runs on ticks where T mod PruneInterval == 0.
    PruneThreshold   int     // balance.yaml world.prune_threshold — LastSeen gap (ticks) past which
                             //   a ToM subject is decayed then removed.
    PruneDecayFactor float64 // balance.yaml world.prune_decay_factor — multiplier applied to a
                             //   pruned subject's Belief before removal (0.0 = zero out then remove).
}

// DefaultConfig returns the canonical Config from content/balance.yaml world.* (tests/headless).
func DefaultConfig() Config

// ── Services (the shared, immutable upstream services threaded to every agent Tick) ──

// Services are the read-only, shared services every agent borrows each Tick. The world builds
// them once (from the loaded registries + content config) and passes them to agent.Tick. They
// are never mutated during the loop. This is exactly agent.Services, re-exported as the world's
// construction input so the caller assembles registries once.
type Services = agent.Services

// ── Construction ────────────────────────────────────────────────────────────────

// New builds an empty world: an empty SpatialHash sized by cfg.SpatialHashCell, the root seeded
// RNG, the worldtime.Clock, the injected EventEmitter, the (immutable, shared) Services and the
// actions.Registry used for outcome resolution. tick starts at 0. Agents/objects are added via
// Spawn/PlaceObject. The world borrows emit/clock/svc/actReg read-only and owns the RNG.
func New(
    cfg Config,
    clock worldtime.Clock,
    root *rng.RNG,
    svc Services,
    actReg *actions.Registry,
    emit core.EventEmitter,
) *World

// World is the global mutable simulation state and the tick driver. NOT safe for concurrent
// mutation: the read/plan phase is read-only; the apply phase is single-threaded (D12).
type World struct{ /* opaque: tick, agents map[AgentID]*agent.Agent + sorted ids, objects,
                       spatial *spatial.SpatialHash, root *rng.RNG, clock, cfg, svc, actReg, emit */ }

// ── Generation (deterministic, fixed sorted-id order, D12) ───────────────────────

// Spawn samples RealStats for id from the stat Registry's per-stat GenSpec using rng (iterating
// stats in svc.Stats.IDs() order, D12), builds the calibrated ToM[self] via engine/mind/tom (with the
// same injected rng), constructs the agent via agent.New(id, pos, realStats, selfToM, agentCfg),
// inserts it into the SpatialHash at pos, and registers it. rng is the caller-chosen generator
// (the world's root RNG or a generation fork) — passed in so world-gen ordering is explicit and
// reproducible. Returns the constructed agent (also retained by the world).
func (w *World) Spawn(id core.AgentID, pos core.Vec2, agentCfg agent.Config, rng *rng.RNG) *agent.Agent

// PlaceObject inserts an object of the given kind at pos carrying its supply Effect (D9: supply
// only — objects hold NO "future need" field) and registers it in the SpatialHash. supply keys
// are need Dimensions. Idempotent on id (re-PlaceObject repositions + replaces supply).
func (w *World) PlaceObject(id core.ObjectID, kind core.Tag, pos core.Vec2, supply map[core.Dimension]float64)

// RemoveObject deletes an object (e.g. a foraged-out bush) from the world and the SpatialHash.
// No-op if absent.
func (w *World) RemoveObject(id core.ObjectID)

// ── The tick loop ────────────────────────────────────────────────────────────────

// Tick advances the simulation by exactly ONE tick in the mandatory four-phase order
// (read → plan → collect → apply, D12; see SPEC-tick.md). It is deterministic: the same
// (world state, root rng state, Config, Services, content) run twice yields byte-identical state
// and the same emitted-event sequence. After the apply phase it advances the tick counter, runs
// the reliance-cluster scan (SPEC-emergent.md), and emits TickDone (and SnapshotReady every
// BackupEveryTicks ticks).
func (w *World) Tick()

// CurrentTick returns the world's authoritative tick counter (advanced only by Tick).
func (w *World) CurrentTick() core.Tick

// ── WorldView (engine/agent's contract; the world implements it) ─────────────────

// Snapshot returns the read-only per-tick WorldView agents perceive and plan against (frozen at
// the START of phase 1, valid only for the current tick). For P1 it MAY be a thin read-only
      // wrapper over live state (sequential tick → no concurrent mutation); the type can become a
// true copy-on-write copy when the plan phase parallelizes (see SPEC-tick.md Notes). It is the
// world's own type satisfying agent.WorldView (var _ agent.WorldView = (*WorldSnapshot)(nil)).
func (w *World) Snapshot() *WorldSnapshot

// WorldSnapshot is the world's implementation of agent.WorldView (which embeds
// perception.WorldSnapshot). It exposes, for the tick it was taken on:
//   - EntitiesInRadius(center, radius) []spatial.Entity   (sight/smell candidates; via SpatialHash)
//   - Tags(id core.ObjectID) []core.Tag                   (object/agent kind tags; sorted)
//   - IsOpaque(id core.ObjectID) bool                     (LoS occlusion flag)
//   - SoundEvents() []perception.SoundEvent               (this tick's emitted sounds)
//   - KnownObjects(self core.AgentID) []agent.KnownObject (objects self has ever perceived; ObjectID order, D12)
//   - BeliefOf(self, subject core.AgentID) (tom.Belief, bool) (for gossip folding)
type WorldSnapshot struct{ /* opaque: frozen view bound to a tick */ }

// AgentIDs returns every agent id in canonical sorted order (D12). The ONE ordering used to
// iterate agents anywhere in the world. Copy; safe to retain.
func (w *World) AgentIDs() []core.AgentID

// AgentOf returns the live agent for id and whether it exists (read access for persist/tests;
// callers must NOT mutate the returned agent outside the apply phase).
func (w *World) AgentOf(id core.AgentID) (*agent.Agent, bool)
```

> The world DEFINES no new vocabulary. `agent.Agent`, `agent.Intent`, `agent.ActionOutcome`,
> `agent.WorldView`, `agent.KnownObject`, `agent.Services`, `agent.Config` are
> `engine/agent`'s contract; `spatial.SpatialHash`/`spatial.Entity` are `engine/space/spatial`'s;
> `actions.Registry`/`actions.ActionDef` are `engine/mind/actions`'; `worldtime.Clock` is
> `engine/kernel/worldtime`'s; `rng.RNG` is `engine/kernel/rng`'s; `core.EventEmitter`/`core.Event` are
> `engine/kernel/core`'s. This module composes them.

---

## Dependencies

- `engine/kernel/core` — `AgentID`, `ObjectID`, `ActionID`, `Tag`, `Dimension`, `StatID`, `Vec2`, `Tick`,
  `EventEmitter`, `Event`. Emits `TickDone`/`RoleEmerged`/`SnapshotReady` via the injected emitter.
- `engine/space/spatial` — `*SpatialHash` (`New`, `Insert`/`Move`/`Remove`, `NearbyEntities`/`NearbyIDs`/
  `PosOf`). The world OWNS one instance; it sizes the cell from `cfg.SpatialHashCell`.
- `engine/kernel/worldtime` — `Clock` (`TicksForMinutes` for durative scaling; `At`/calendar for events).
  The world owns the authoritative `Tick`; worldtime only interprets it.
- `engine/kernel/rng` — `*RNG` (the root seeded generator the world OWNS; per-agent forks derived from it).
- `engine/agent` — `Agent`, `New`, `Tick`, `ApplyOutcome`, `Intent`, `IntentKind`, `Signal`,
  `ActionOutcome`, `OutcomeStatus`, `WorldView`, `KnownObject`, `Config`, `Services`. The world
  CALLS `Tick`/`ApplyOutcome` and IMPLEMENTS `WorldView`. (`engine/agent` never imports `world` —
  the L5→L6 cycle is broken by `WorldView` living in `engine/agent`.)
- `engine/mind/actions` — `*Registry`, `ActionDef` (`Get`, `Tags`/`Producers` as needed). The world
  reads `Tags` (D4) to derive the resolution stat and the action `Effect`/`Duration` to apply.
- `engine/mind/stats` — `*Registry` (via `Services.Stats`): `IDs()`/`Def` to sample `GenSpec` at Spawn
  (D12 order), `Kinds(Capability)` to compose the resolution stat, `Clamp` for sampled values.
  No hardcoded stat name (D7/D10).
- `engine/mind/tom` — `ToM`, `Belief` (read `RelyOn` for the reliance scan; `NewToM` to seed `ToM[self]`
  at Spawn with the injected rng; read `LastSeen`/`Subjects()` for the prune pass). The world reads
  `Belief.RelyOn`/`LastSeen`; it does not own the update math.
- `engine/mind/perception`, `engine/mind/planner`, `engine/mind/needs`, `engine/mind/values` — **borrowed via
  `Services` only** (the world assembles `agent.Services` and threads it to `Tick`; it does not
  call these directly beyond building the snapshot's perception view).
- **Contract — NOT imported**: `platform/events` (the concrete `EventEmitter`), `platform/persist`
  (the snapshot writer), `platform/config` (content loading). The world emits/exposes via
  interfaces and public state only (architecture §1 dependency inversion).
- **Contract**: `content/balance.yaml world.*` supplies `Config`; `worldtime.Config` supplies the
  `Clock`. Injected via `platform/config`. The world hardcodes no constant (D10).

---

## Owned Data

- The authoritative `core.Tick` counter (advanced ONLY by `Tick()`).
- Every live `agent.Agent` (keyed by `AgentID`, with a precomputed sorted-id slice for D12
  iteration). The world mutates an agent ONLY through `agent.Tick`/`agent.ApplyOutcome` in the
  proper phase — it never reaches into an agent's `Body`/`ToM` fields directly (it MAY read
  `RealStats` for outcome resolution, `ToM.RelyOn` for the reliance scan, and `ToM` LastSeen for
  the prune pass; the prune pass removes stale ToM subjects via the `engine/mind/tom` API).
- The **plan-slot schedule**: the round-robin replan assignment (agentID → slot) derived purely
  from the sorted `w.agentIDs` index mod `cfg.PlanInterval` (no separate stored map is required —
  it is recomputed from the sorted slice each tick; D12), and the **prune-pass scheduler** (the
  tick-mod-`cfg.PruneInterval` gate). Both are derived state, not authored input.
- Every placed object: `{id, kind, pos, supply, contents/state}` (the world's own object record;
  data-contracts §1 `world.objects[]`). Objects carry **supply only** (D9 — no future-need field).
- The single `spatial.SpatialHash` (derived state; rebuilt from positions on resume — never
  serialized, per `engine/space/spatial` Notes).
- The root `*rng.RNG` (owned; its state is the snapshot's `rng_state`, data-contracts §1).
- The small `emerged map[Function]AgentID` (owned; serialized for resume byte-exactness; see
  [`SPEC-emergent.md`](SPEC-emergent.md) §Emergent reliance-cluster detection).
- The `WorldSnapshot`/`World`/`Config` types and the tick-loop/conflict/resolution/cluster logic.
- Borrowed read-only: `worldtime.Clock`, `Services`, `*actions.Registry`, the `EventEmitter`.

---

## Invariants (cross-cutting)

The domain-specific invariants live in the sub-specs. These cross-cutting invariants apply across
the entire module:

- **D12 enforced end-to-end** — see [`SPEC-tick.md`](SPEC-tick.md) §Invariants for the eight
  concrete D12 checkpoints. Summary: sorted-id iteration only; deterministic per-agent RNG forks;
  sorted apply order; ID-stable tie-break; no wall-clock; byte-identical from seed.
- **D2 — no Role type**: `engine/world` defines **no** `Role`/`Chief`/`Faction`/institution type;
  `RoleEmerged` is a derived statistic only. Struct/grep guard (see
  [`SPEC-emergent.md`](SPEC-emergent.md) §Invariants).
- **D8 — Real Stats read ONLY at outcome resolution**: the decision path reads `ToM[self]`; the
  world reads `RealStats` only in the apply phase (outcome resolution + conflict). A guard confirms
  no `Tick`-phase code reads `RealStats`.
- **D9 — Objects carry supply only**: object record has `supply map[Dimension]float64`; no
  future-need/demand/schedule field.
- **D7 — Capability composition, no individual skill**: resolution stat composed from
  `Stats.Kinds(Capability)` per the action's `uses:<StatID>` tags; never a per-action skill value.
- **D4 — Tag-derived resolution**: the stat an action resolves against is derived from its `Tags`,
  not a bespoke per-action function.
- **D10 — All constants injected**: no literal for cell size / threshold / difficulty / backup
  interval / plan-worker count / stat-or-action name in logic; all flow from `Config`/registries.
- **No IO (architecture §1)**: imports no `os`/`net`/filesystem package; emits only through the
  injected `EventEmitter`.

---

## Out of Scope

- **IO / Redis / Postgres / SSE** → `platform/persist` (snapshot write/read, Redis live keys,
  Postgres backup) and `platform/api` (SSE). The world emits `SnapshotReady`; it never serializes
  or transports state itself (data-contracts §1–§3).
- **Event serialization & the concrete emitter** → `platform/events` (implements `core.EventEmitter`,
  the why-trace + SSE stream, data-contracts §4). The world only constructs `core.Event`s and calls
  `Emit`.
- **The snapshot write/encoding itself** → `platform/persist` (it reads the world's public state +
  `root.State()`; the world signals readiness via `SnapshotReady`).
- **Content loading & schema validation** (`stats.yaml`/`actions.yaml`/`gates.yaml`/`needs.yaml`/
  `objects.yaml`/`balance.yaml` → registries + referential integrity) → `platform/config`
  (architecture §3). The world receives already-built registries + `Config`.
- **The agent decision loop** (perceive → appraise → mediate → plan → execute → signal → dynamics,
  coping, β self-calibration, Mood/Adrenaline) → `engine/agent`. The world only calls
  `Tick`/`ApplyOutcome`.
- **Appraisal / planning / need / ToM update math** → `engine/mind/values` / `engine/mind/planner` /
  `engine/mind/needs` / `engine/mind/tom` (threaded via `Services`); the world composes contracts, it does
  not reimplement them. The `RelyOn` **update** math is the agent's/values'; the world only
  **reads** the distribution for the cluster scan.
- **Sense modeling** (LoS occlusion, smell gradient, hearing falloff) → `engine/mind/perception`; the
  world supplies the proximity candidates (`SpatialHash`) + the object tags/opacity to the sensor.
- **Frontend / API** → `platform/api` (later, architecture §3).
- **SIMD vectorisation** of the plan/apply hot loops → not done (and not planned for P1); the
  scale work here is goroutine fan-out only.
- **Persistent / long-lived worker pools** → NOT done. The plan-phase goroutines are spun up and
  joined WITHIN a single `Tick()` (per-tick fan-out); there is no pool that outlives a tick (see
  [`SPEC-tick.md`](SPEC-tick.md) Notes §Scale).
- **Cross-machine / distributed simulation** (sharding agents across processes/hosts) → out of
  scope; the world is a single-process orchestrator.

---

## Open Questions

- **New `balance.yaml world.*` keys — RESOLVED, implementer action required.** The three keys
  (`reliance_threshold`, `outcome_difficulty_base`, `backup_every_ticks`) are now formally specified
  in `Config` with defaults and schema constraints (see §Public Interface `Config` block). The
  implementer MUST add them to `content/balance.yaml` and `content/schema/balance.schema.json`
  **as the first step before writing any Go code** for this module; `platform/config` structural
  validation will catch any missing key at startup. `DefaultConfig()` may carry test-only fallbacks
  but all logic paths read the injected `Config` value. This unblocks P1 wiring.
- **Scale-extension `balance.yaml world.*` keys — RESOLVED, OPTIONAL (backward compatible).** The
  five keys (`plan_workers`, `plan_interval`, `prune_interval`, `prune_threshold`,
  `prune_decay_factor`) are specified in `Config` with defaults (see §Public Interface). They are
  NOT in the schema `required` array — a missing key falls back to its documented default so
  existing content/runs are unaffected. The implementer adds the keys + (optional) schema entries
  with the type/min constraints noted in the `Config` block.
- **`RelyOn` / `Function` type — RESOLVED (P6); stub REPLACED.** `tom.Belief.RelyOn` and
  `tom.Function` are formalized (`engine/mind/tom/SPEC.md` §P6), and `engine/agent` populates the edges
  (`engine/agent/SPEC.md` §P6). The P1 no-op `relianceScan()` stub is therefore **replaced** by
  the live full scan (see [`SPEC-emergent.md`](SPEC-emergent.md)). Scenario G is no longer deferred.
- **Role-detection threshold & succession (P6).** P6 uses a single share threshold
  `politics.role_convergence_threshold` with **rising-edge** emission. Hysteresis (a separate
  clear-threshold to damp flapping) is **deferred**; P6 clears on the same threshold. Escalate
  before tuning if flapping appears in long Scenario-G runs.
- **Multi-stat outcome composition (NOT blocking P1).** P1 uses a deterministic, tag-order-stable
  mean (or min) of the capability composition; the exact rule belongs in a tuned `balance.yaml`
  term later. Flag before tuning combat/contest outcomes.
- **Outcome difficulty model (NOT blocking P1).** P1 resolves `realLevel` vs a single
  `outcome_difficulty_base`. Any rng draw in stochastic outcomes MUST use the per-agent fork (D12).
  Escalate before adding stochastic outcomes.
- **Resource regrowth / animal behaviour (NOT blocking P1).** Out of the P1 batch. Recorded so the
  object record's `contents/state` shape leaves room for them.

---

## Notes

- **Events emitted (data-contracts §4).** The world emits `TickDone{tick, agent_count,
  intent_count}` and `RoleEmerged{function, holder, reliance_share}`; `SnapshotReady{tick}` signals
  persist (the world does no IO). Agent-scoped events originate in `engine/agent` through the SAME
  injected emitter — the world threads the emitter to `Tick`. No `real_stats` on any payload.
- **Snapshot field mapping (data-contracts §1).** `tick` ← the world's counter; `rng_state` ←
  `root.State()`; `world.objects[]` ← the object records (`id, kind, pos, contents`);
  `agents[]` ← each agent's public state (`id, pos, real_stats, body, goal, plan_summary,
  tom_digest, known_digest`). The world EXPOSES these via `AgentIDs`/`AgentOf`/`Snapshot` and the
  object accessor; `platform/persist` reads and serializes them. The SpatialHash is derived and
  rebuilt from positions on resume — never serialized (`engine/space/spatial` Notes).
- **`Services` re-export.** `type Services = agent.Services` (a type alias) keeps the caller from
  assembling the bundle twice; the world threads the exact same value to every `agent.Tick`.
- **Object record shape.** Minimal P1 record: `{ID core.ObjectID, Kind core.Tag, Pos core.Vec2,
  Supply map[core.Dimension]float64, /* contents/state reserved */}`. Supply only (D9). The
  `KnownObject` the agent sees mirrors `{ID, Pos, Kind, Supply}` — the world builds it in
  `WorldSnapshot.KnownObjects` (sorted by `ObjectID`, D12).
