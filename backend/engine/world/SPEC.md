# SPEC — `engine/world`

> Status: `READY`
> Leaf level: `L6`  ·  Owner agent: `implementer`

## Purpose

The **simulation orchestrator** (architecture §2, the top of the engine DAG — imported by no
engine module, no cycles). It owns the global mutable state — every `agent.Agent`, every placed
object, the `spatial.SpatialHash`, the `core.Tick` counter, and the root seeded `*rng.RNG` — and
drives the deterministic tick loop in the mandatory **read → plan → collect → apply** order (D12).
It spawns agents (sampling `RealStats` from the stat `GenSpec`), places objects, resolves each
intent's outcome **against Real Stats** (D8 — the only place Real Stats are read), arbitrates
conflicts deterministically, advances the clock, and detects **emergent reliance clusters**
(`RoleEmerged`, D2 — detection only, no role type). It implements `agent.WorldView` so agents
perceive and plan against a frozen per-tick snapshot. It emits observability events through an
**injected** `core.EventEmitter` and never touches IO / Redis / Postgres / SSE.

## Public Interface

```go
package world

import (
    "github.com/dogring/bdg/backend/engine/actions"
    "github.com/dogring/bdg/backend/engine/agent"
    "github.com/dogring/bdg/backend/engine/core"
    "github.com/dogring/bdg/backend/engine/needs"
    "github.com/dogring/bdg/backend/engine/perception"
    "github.com/dogring/bdg/backend/engine/planner"
    "github.com/dogring/bdg/backend/engine/rng"
    "github.com/dogring/bdg/backend/engine/spatial"
    "github.com/dogring/bdg/backend/engine/stats"
    "github.com/dogring/bdg/backend/engine/tom"
    "github.com/dogring/bdg/backend/engine/values"
    "github.com/dogring/bdg/backend/engine/worldtime"
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
type Config struct {
    SpatialHashCell       float64 // balance.yaml world.spatial_hash_cell — SpatialHash cell edge
    RelianceThreshold     float64 // balance.yaml world.reliance_threshold — share of agents relying
                                  //   on one holder for a Function → emit RoleEmerged (D2).
                                  //   Stub: until tom.Belief.RelyOn is formalized this path is a
                                  //   no-op; threshold is still injected so the stub compiles.
    OutcomeDifficultyBase float64 // balance.yaml world.outcome_difficulty_base — base difficulty an
                                  //   action's used stat is resolved against (vs Real Stat).
    BackupEveryTicks      int     // balance.yaml world.backup_every_ticks — emit SnapshotReady every
                                  //   N ticks (the world signals; it does NOT write to persist).
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
// stats in svc.Stats.IDs() order, D12), builds the calibrated ToM[self] via engine/tom (with the
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
// (read → plan → collect → apply, D12; see "Tick loop" below). It is deterministic: the same
// (world state, root rng state, Config, Services, content) run twice yields byte-identical state
// and the same emitted-event sequence. After the apply phase it advances the tick counter, runs
// the reliance-cluster scan, and emits TickDone (and SnapshotReady every BackupEveryTicks ticks).
func (w *World) Tick()

// CurrentTick returns the world's authoritative tick counter (advanced only by Tick).
func (w *World) CurrentTick() core.Tick

// ── WorldView (engine/agent's contract; the world implements it) ─────────────────

// Snapshot returns the read-only per-tick WorldView agents perceive and plan against (frozen at
// the START of phase 1, valid only for the current tick). For P1 it MAY be a thin read-only
// wrapper over live state (sequential tick → no concurrent mutation); the type can become a
// true copy-on-write copy when the plan phase parallelizes (see Notes / Invariants). It is the
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
> `engine/agent`'s contract; `spatial.SpatialHash`/`spatial.Entity` are `engine/spatial`'s;
> `actions.Registry`/`actions.ActionDef` are `engine/actions`'; `worldtime.Clock` is
> `engine/worldtime`'s; `rng.RNG` is `engine/rng`'s; `core.EventEmitter`/`core.Event` are
> `engine/core`'s. This module composes them.

## Tick loop — D12 read → plan → collect → apply (MANDATORY ORDER)

`Tick()` runs these four phases **in exactly this order**. No mutation of shared world state
happens before phase 4.

```
Phase 1 — READ (snapshot):
  - Take a read-only WorldSnapshot (implements agent.WorldView) of the current live state at the
    start of the tick. NO mutation occurs in this phase.
  - For P1 this is a thin wrapper over live state (sequential tick); it MUST be replaceable by a
    copy-on-write/frozen copy when the plan phase parallelizes — agents read ONLY through it.

Phase 2 — PLAN (parallel-safe, read-only):
  - For each agent in AgentIDs() (sorted, D12): derive a deterministic per-agent rng FORK from the
    root RNG keyed by the agent's sorted-id position (forkRNG below), then call
    agent.Tick(snapshot, now, fork, svc, emit). The agent reads the snapshot and returns []Intent.
  - The snapshot is NEVER mutated. Each agent gets its OWN rng fork (no shared RNG draws across
    agents in this phase) so the order of agent evaluation cannot change any agent's draws.
  - P1: sequential over sorted ids. Future: parallelizable because every Tick is read-only on
    shared state and uses a private rng fork (the fork derivation is the determinism anchor).

Phase 3 — COLLECT intents:
  - Gather every []Intent from every agent into one []Intent, then STABLE-SORT by Intent.Agent
    (AgentID, lexicographic, D12). Ties (multiple intents from one agent) keep emission order.
    This sorted slice is the authoritative apply order.

Phase 4 — APPLY (serial, fixed AgentID order, D12):
  - Iterate the sorted intents serially (no parallelism that can interleave mutations):
      a. Resolve the action OUTCOME against the acting agent's RealStats (NOT ToM[self], D8):
         the used stat is derived from the action's Tags (D4 — uses:<StatID>); success/fail/
         interrupt is RealStat vs OutcomeDifficultyBase (or the competing agent's RealStat).
      b. Detect & resolve CONFLICTS: if two+ intents target the same resource this tick, the
         higher relevant Real Stat wins; tie-break = lower AgentID (lexicographic, D12). Losers
         get ActionOutcome{Status: Interrupted}. (See "Conflict resolution".)
      c. Mutate world state for the winner: update object inventories / object state, apply the
         action Effect / consumed item supply (D9) as need deltas, and for a move intent update
         the agent's Pos in the SpatialHash (IntentStart/IntentContinue).
      d. Call agent.ApplyOutcome(outcome, fork, agentCfg, statsReg, emit) for the OWNING agent —
         in this same sorted serial phase, so no two agents' belief writes interleave.
      e. Deliver any IntentSignal: hand the Signal to the receiver via its ApplyOutcome/gossip
         hook (the world decides who-receives-what; folding math is the agent's, see OoS).
  - After all intents: advance the tick counter; run the reliance-cluster scan (below); emit
    TickDone{tick, agent_count, intent_count}; emit SnapshotReady every BackupEveryTicks ticks.
```

### Per-agent RNG fork (the determinism anchor, D12)

```
forkRNG(root *rng.RNG, sortedAgents []AgentID, i int) *rng.RNG
  // a pure, deterministic derivation of a child RNG from the root seed + the agent's POSITION i
  // in the sorted id slice (e.g. seed = mix(rootSeed, i) or a counter-stream split). It MUST NOT
  // depend on iteration/wall-clock order: two runs that evaluate agents in any goroutine order
  // produce the identical fork for a given (sorted id, tick). The fork — not the root — is what
  // agent.Tick/ApplyOutcome draw from in that tick.
```

The root RNG is advanced (or split) in a fixed, tick-deterministic way; no agent's draws depend
on another agent's draw count. This is what makes phase 2 parallelizable later without breaking
byte-determinism.

## Conflict resolution — D12

When two or more intents in the same tick target the **same resource** (same `ObjectID` /
exclusive interaction target):

1. **Relevant stat**: derived from the contested action's `Tags` (D4) — the `uses:<StatID>` tag
   names the stat; the world reads each contender's **Real Stat** for that id (D8). Multi-stat
   actions use a fixed, tag-order-deterministic composition (documented at implementation; no
   `map` ranging).
2. **Winner**: the contender with the higher relevant **Real Stat**.
3. **Tie-break**: the lower `AgentID` (lexicographic) wins (D12 — ID-stable, reproducible).
4. **Losers**: receive `agent.ActionOutcome{Status: Interrupted}` (the agent discards partial
   progress per its durative policy). Winners are resolved normally (Succeeded/Failed).
5. Conflict groups are formed by iterating the **sorted** intent slice and grouping by target
   id (no map-order dependence).

## Outcome resolution

Resolution reads the acting agent's **Real Stats** (the only place in the engine that does, D8):

```
def := actReg.Get(intent.Action)                 // the ActionDef (Tags, Effect, Duration, …)
statsUsed := statsFromTags(def.Tags)             // the uses:<StatID> tags → []StatID (sorted, D12)
realLevel := compose(agent.RealStats, statsUsed) // capability composition (D7: no individual skill)
Status:
   - Invisible  → never reached here (a gate blocked it at PLAN time; the agent never emits it)
   - Interrupted→ lost a conflict (above)
   - Succeeded  → realLevel meets/clears the difficulty (cfg.OutcomeDifficultyBase, scaled by tags)
   - Failed     → realLevel below the difficulty (drives the agent's β overclaim correction, D8)
Outcome fields populated for ApplyOutcome:
   Action, Status, Completed (a.Elapsed reached the stat/distance-scaled Duration),
   StatsUsed (= statsUsed, drives which β-calibration runs),
   Expected (the agent's pre-action expected progress, carried on the Intent / read from agent),
   Actual   (the realized progress this tick, from realLevel — reflects Real Stats),
   Effect   (the realized need deltas applied: def.Effect or the consumed item's supply, D9),
   Evidence (per-stat direct-observation evidence the world attributes for ToM[self] folding, D8).
```

- **Movement** (`IntentStart`/`IntentContinue` for a move action): the world advances the agent
  toward `Intent.Move`, updates `Pos`, and `SpatialHash.Move`s it; `Completed` when it arrives.
- **Durative scaling**: the base `ActionDef.Duration` (game-minutes) is scaled by stats/distance
  at execution **here** (the planner uses the base only — see `engine/agent` §Durative). The
  `worldtime.Clock.TicksForMinutes` converts the scaled duration to ticks.
- `real_stats` are **never** placed on an emitted event payload (god-view; data-contracts §4).

## Emergent reliance-cluster detection (D2 — detection only, no role type)

After the apply phase, scan reliance edges across agents to detect emergent functional roles
(design §1.6 "창발 제도": 역할은 자료형이 아니다).

### P1 stub (implement this first)

`tom.Belief.RelyOn` and a `Function` type are not yet formalized in `engine/tom`. Until that
contract is settled, the implementer **MUST** ship a no-op stub:

```go
// relianceScan is the cluster-detection step run after every apply phase.
// P1 STUB: tom.Belief.RelyOn has no concrete shape yet; this is intentionally a no-op.
// Replace with the full scan below once engine/tom formalizes RelyOn + Function.
// Scenario G is deferred — all other P1 acceptance criteria are unaffected.
func (w *World) relianceScan() { /* no-op stub — see Open Questions */ }
```

The stub must still compile against `engine/tom`; it MUST NOT reach into `Belief.RelyOn` until
that field is formalized. The `cfg.RelianceThreshold` is injected and carried but not read here.

### Full scan (activate once tom.Belief.RelyOn is formalized)

```
for each Function f referenced across agents' ToM RelyOn edges (iterate agents in AgentIDs() order, D12):
    share[holder] = (count of agents whose RelyOn[f] points strongest at holder) / agent_count
    if max(share) ≥ cfg.RelianceThreshold:
        emit RoleEmerged{function: f, holder: argmax, reliance_share: share[holder]}
```

- This is **emergent detection ONLY**: the world defines **no** `Role` type, no chief/leader
  enum, no institution (D2). It reads the `RelyOn` distribution that `engine/agent`/`engine/values`
  maintain on `tom.Belief` and reports a statistic.
- The scan is deterministic: agents and Functions are iterated in sorted order; the argmax
  tie-breaks by lower holder `AgentID` (D12). No rng.

Activation checklist (do NOT activate before all are done):
- [ ] `engine/tom` SPEC formalizes `type Function string` and `Belief.RelyOn map[Function]float64`
- [ ] `engine/agent` or `engine/values` populates `RelyOn` edges during deliberation
- [ ] `content/balance.yaml world.reliance_threshold` is present (already required for the stub Config)
- [ ] Scenario G fixture in `docs/testing.md §4` is written and a golden recorded

## Dependencies

- `engine/core` — `AgentID`, `ObjectID`, `ActionID`, `Tag`, `Dimension`, `StatID`, `Vec2`, `Tick`,
  `EventEmitter`, `Event`. Emits `TickDone`/`RoleEmerged`/`SnapshotReady` via the injected emitter.
- `engine/spatial` — `*SpatialHash` (`New`, `Insert`/`Move`/`Remove`, `NearbyEntities`/`NearbyIDs`/
  `PosOf`). The world OWNS one instance; it sizes the cell from `cfg.SpatialHashCell`.
- `engine/worldtime` — `Clock` (`TicksForMinutes` for durative scaling; `At`/calendar for events).
  The world owns the authoritative `Tick`; worldtime only interprets it.
- `engine/rng` — `*RNG` (the root seeded generator the world OWNS; per-agent forks derived from it).
- `engine/agent` — `Agent`, `New`, `Tick`, `ApplyOutcome`, `Intent`, `IntentKind`, `Signal`,
  `ActionOutcome`, `OutcomeStatus`, `WorldView`, `KnownObject`, `Config`, `Services`. The world
  CALLS `Tick`/`ApplyOutcome` and IMPLEMENTS `WorldView`. (`engine/agent` never imports `world` —
  the L5→L6 cycle is broken by `WorldView` living in `engine/agent`.)
- `engine/actions` — `*Registry`, `ActionDef` (`Get`, `Tags`/`Producers` as needed). The world
  reads `Tags` (D4) to derive the resolution stat and the action `Effect`/`Duration` to apply.
- `engine/stats` — `*Registry` (via `Services.Stats`): `IDs()`/`Def` to sample `GenSpec` at Spawn
  (D12 order), `Kinds(Capability)` to compose the resolution stat, `Clamp` for sampled values.
  No hardcoded stat name (D7/D10).
- `engine/tom` — `ToM`, `Belief` (read `RelyOn` for the reliance scan; `NewToM` to seed `ToM[self]`
  at Spawn with the injected rng). The world reads `Belief.RelyOn`; it does not own the update math.
- `engine/perception`, `engine/planner`, `engine/needs`, `engine/values` — **borrowed via
  `Services` only** (the world assembles `agent.Services` and threads it to `Tick`; it does not
  call these directly beyond building the snapshot's perception view).
- **Contract — NOT imported**: `platform/events` (the concrete `EventEmitter`), `platform/persist`
  (the snapshot writer), `platform/config` (content loading). The world emits/exposes via
  interfaces and public state only (architecture §1 dependency inversion).
- **Contract**: `content/balance.yaml world.*` supplies `Config`; `worldtime.Config` supplies the
  `Clock`. Injected via `platform/config`. The world hardcodes no constant (D10).

## Owned Data

- The authoritative `core.Tick` counter (advanced ONLY by `Tick()`).
- Every live `agent.Agent` (keyed by `AgentID`, with a precomputed sorted-id slice for D12
  iteration). The world mutates an agent ONLY through `agent.Tick`/`agent.ApplyOutcome` in the
  proper phase — it never reaches into an agent's `Body`/`ToM` fields directly (it MAY read
  `RealStats` for outcome resolution and `ToM.RelyOn` for the reliance scan).
- Every placed object: `{id, kind, pos, supply, contents/state}` (the world's own object record;
  data-contracts §1 `world.objects[]`). Objects carry **supply only** (D9 — no future-need field).
- The single `spatial.SpatialHash` (derived state; rebuilt from positions on resume — never
  serialized, per `engine/spatial` Notes).
- The root `*rng.RNG` (owned; its state is the snapshot's `rng_state`, data-contracts §1).
- The `WorldSnapshot`/`World`/`Config` types and the tick-loop/conflict/resolution/cluster logic.
- Borrowed read-only: `worldtime.Clock`, `Services`, `*actions.Registry`, the `EventEmitter`.

## Invariants

D12 is guaranteed by these eight, each independently checkable:

1. **Sorted-id iteration only**: agents are iterated via `AgentIDs()` (sorted), objects via sorted
   `ObjectID`. **No `map` is ranged for logic anywhere** (intents, conflicts, reliance, snapshot).
2. **Deterministic per-agent RNG forks**: each agent's tick RNG is a pure function of (root seed,
   sorted-id position, tick) — never of evaluation/goroutine order. No agent's draws depend on
   another's draw count.
3. **Sorted apply order**: intents are stable-sorted by `AgentID` before the apply phase; agents
   are applied serially in that order; `ApplyOutcome` runs in the same serial phase.
4. **ID-stable conflict tie-break**: conflicts break ties by lower lexicographic `AgentID`.
5. **Sorted object/resource ops**: object inventory/state mutations and conflict grouping iterate
   sorted `ObjectID`; the reliance scan iterates sorted `AgentID` and sorted `Function`.
6. **Reliance scan is deterministic**: argmax holder ties break by lower `AgentID`; no rng.
7. **No wall-clock, no uncontrolled parallelism**: no `time.Now()`; the only time source is the
   `Tick` counter. No OS-level parallelism that can interleave phase-4 mutations (apply is serial).
8. **Byte-identical from the seed**: same seed + same content (`config_hash`) → byte-identical
   simulation state and event sequence from tick 0; resuming from a tick-T snapshot is
   byte-identical to running 0→T+k (testing.md §1 resume invariant; the root `rng_state` round-trips).

Plus:

- **Read/plan/collect/apply ordering is mandatory** (the four phases above, in order). Phase 2 is
  read-only on shared state; phase 4 is the only mutator. A test instruments the phase boundaries.
- **Real Stats read ONLY at outcome resolution / conflict (D8)**: the decision path (agent `Tick`)
  reads `ToM[self]`; the world reads `RealStats` only when resolving an applied intent and when
  composing the conflict stat. A guard confirms `Tick`-phase code never reads `RealStats`.
- **No hardcoded meta-system / role type (D2)**: there is **no** `Role`/`Chief`/`Faction`/`Crime`
  type in this package. `RoleEmerged` is a derived statistic over `RelyOn`; a struct/grep guard
  confirms no institution type exists.
- **Objects carry supply only (D9)**: an object record has a `supply map[Dimension]float64` and no
  "future need"/demand/schedule field; provisioning is the planner's forward-sim, not stored here.
- **Capability composition, no individual skill (D7)**: the resolution stat is composed from
  `Stats.Kinds(Capability)` per the action's `uses:<StatID>` tags — never a per-action skill value.
- **Tag-derived resolution & cost (D4)**: the stat an action resolves against is derived from its
  `Tags`, not a bespoke per-action function or field.
- **All constants injected (D10)**: no literal for cell size / reliance threshold / difficulty /
  backup interval / stat-or-action name appears in logic; every constant flows from `Config` /
  `worldtime.Config` / the injected registries. A grep guard confirms it.
- **No IO (architecture §1)**: imports no `os`/`net`/filesystem package; reads no file; emits only
  through the injected `EventEmitter`; the snapshot WRITE is `platform/persist` (the world emits
  `SnapshotReady`, it does not serialize/transport).
- **Snapshot is read-only & tick-scoped**: agents mutate nothing through the `WorldSnapshot`; it is
  valid only for the tick it was taken on (a stub that panics on write proves the read-only claim).

## Acceptance Criteria (testable)

- [ ] **Four-phase order (D12)**: a tick with instrumented phase hooks asserts the strict order
  read → plan → collect → apply; no shared-state mutation is observed before phase 4 (a recording
  `EventEmitter` + a write-guard snapshot prove plan-phase reads only).
- [ ] **Apply in sorted AgentID order (D12)**: with three agents `["a3","a1","a2"]` each emitting
  one intent, `ApplyOutcome` is called in the order `a1, a2, a3` regardless of spawn/emit order
  (a stub agent records its apply order). Table-driven over shuffled spawn orders.
- [ ] **Per-agent RNG fork is order-independent (D12)**: evaluating the plan phase over agents in
  two different iteration orders produces the IDENTICAL fork (and identical intents) per agent
  id; a digest of all forks' first draws matches a golden.
- [ ] **Conflict: higher Real Stat wins, ties by AgentID (D12)**: two agents target the same
  object in one tick; the one with the higher Real Stat for the contested `uses:<StatID>` tag gets
  `Succeeded`, the other `Interrupted`. With EQUAL Real Stats, the lower `AgentID` wins
  (lexicographic). Table-driven; the relevant stat is derived from the action `Tags` (D4).
- [ ] **Outcome resolved against Real Stats, not ToM (D8)**: an agent with HIGH `RealStats` but
  LOW `ToM[self]` for the used stat SUCCEEDS at resolution (the world reads `RealStats`); a guard
  asserts no `ToM[self]` read on the resolution path. Conversely LOW `RealStats` → `Failed`,
  feeding the agent's β overclaim correction (verified via the `ActionOutcome.StatsUsed`/`Status`).
- [ ] **Movement updates the SpatialHash**: an `IntentStart`/`IntentContinue` move advances the
  agent's `Pos` and `SpatialHash.Move`s it; after the move a radius query at the old position no
  longer returns the agent and one at the new position does. `Completed` set on arrival.
- [ ] **Effect / consumed-item supply applied (D9)**: applying an `Eat`-style intent applies the
  consumed item's supply (or the action `Effect`) as need deltas in `ActionOutcome.Effect`; an
  object record carries supply only (struct guard — no future-need field).
- [ ] **Spawn samples RealStats deterministically (D12)**: `Spawn(id, pos, cfg, rng)` samples
  per-stat from `GenSpec` in `stats.IDs()` order; the same (id, seed) yields byte-identical
  `RealStats` and a calibrated `ToM[self]` (golden). The agent is inserted in the SpatialHash at
  `pos`. Two agents spawned in either source order get identical stats for their own seed/position.
- [ ] **PlaceObject / RemoveObject + SpatialHash registration**: a placed object is returned by a
  radius query at its pos and appears in `KnownObjects` once perceived; `RemoveObject` drops it
  from both the world and the index. Idempotent re-`PlaceObject` repositions + replaces supply.
- [ ] **WorldView satisfies agent.WorldView (compile + behaviour)**: `var _ agent.WorldView =
  (*WorldSnapshot)(nil)` compiles; `EntitiesInRadius`/`Tags`/`IsOpaque`/`SoundEvents`/
  `KnownObjects`/`BeliefOf` return tick-scoped data; `KnownObjects` is in `ObjectID` order (D12).
  A write-attempt on the snapshot is impossible (read-only API; no mutator exposed).
- [ ] **RoleEmerged stub compiles and is a no-op (D2, P1)**: the `relianceScan()` stub compiles,
  does not reach into `tom.Belief.RelyOn`, emits nothing, and a struct/grep guard confirms NO
  `Role`/institution type exists in `engine/world`. The full scan (threshold-gated `RoleEmerged`
  emission, tie-break by AgentID, scenario G fixture) is a **deferred AC** — it activates only
  after the `engine/tom` + `engine/agent` pre-conditions in §Emergent-reliance are all checked off.
  Document the stub's presence in the implementer note with a `// TODO(scenario-G)` tag.
- [ ] **TickDone / SnapshotReady events**: every `Tick()` emits `TickDone{tick, agent_count,
  intent_count}`; `SnapshotReady` is emitted exactly every `BackupEveryTicks` ticks and never
  performs IO (the world only signals). `real_stats` appears on no event payload (god-view).
- [ ] **No hardcoded constant / id / role (D10/D7/D2 guard)**: grep guard — no cell-size /
  threshold / difficulty / backup-interval literal and no stat/action-name string literal and no
  role/institution type in `engine/world` logic; all flow from `Config`/registries.
- [ ] **No IO (architecture §1 guard)**: grep/import guard — no `os`/`net`/filesystem import; events
  leave only through the injected `EventEmitter`.
- [ ] **Determinism golden (testing.md §1/§3)**: run a fixed seed with 1–3 spawned agents + a few
  objects for N ticks → serialize state digest → matches the golden; a second run from a fresh
  registry built from the same content reproduces it byte-for-byte (cross-process).
- [ ] **Resume invariant (testing.md §1)**: capturing the world (incl. `root.State()`) at tick T
  and resuming yields the same tick-T+k state as running 0→T+k uninterrupted (the SpatialHash is
  rebuilt from positions; the root `rng_state` round-trips).
- [ ] **Scenario fixtures (testing.md §4)**: at minimum scenario **G** (chief emerges:
  distributed Safety drop → `RelyOn` converges → `RoleEmerged`) and scenario **B** (need-driven
  intent selection end-to-end through one full tick: agent Ticks, world resolves, agent's
  `ApplyOutcome` folds back). Assertions test direction/existence, not exact numbers (§4).

> Structural JSON-schema validation of the `content/balance.yaml world.*` block this module reads
> (incl. the three new keys — see Open Questions) is a **platform/config** AC (it owns the file IO
> + schema). This module proves only behaviour reachable from the injected `Config`/`Services`.

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
- **Appraisal / planning / need / ToM update math** → `engine/values` / `engine/planner` /
  `engine/needs` / `engine/tom` (threaded via `Services`); the world composes contracts, it does
  not reimplement them. The `RelyOn` **update** math is the agent's/values'; the world only
  **reads** the distribution for the cluster scan.
- **Sense modeling** (LoS occlusion, smell gradient, hearing falloff) → `engine/perception`; the
  world supplies the proximity candidates (`SpatialHash`) + the object tags/opacity to the sensor.
- **Frontend / API** → `platform/api` (later, architecture §3).

## Open Questions

- **New `balance.yaml world.*` keys — RESOLVED, implementer action required.** The three keys
  (`reliance_threshold`, `outcome_difficulty_base`, `backup_every_ticks`) are now formally specified
  in `Config` with defaults and schema constraints (see §Public Interface `Config` block). The
  implementer MUST add them to `content/balance.yaml` and `content/schema/balance.schema.json`
  **as the first step before writing any Go code** for this module; `platform/config` structural
  validation will catch any missing key at startup. `DefaultConfig()` may carry test-only fallbacks
  but all logic paths read the injected `Config` value. This unblocks P1 wiring.
- **`RelyOn` / `Function` type — RESOLVED as P1 stub; scenario G deferred.** `tom.Belief.RelyOn`
  and a `Function` type are not yet formalized in `engine/tom`; the world's `RoleEmerged` scan
  depends on them. **Decision**: the implementer ships `relianceScan()` as an explicit no-op stub
  (see §Emergent-reliance "P1 stub"). The stub compiles, does not reach `Belief.RelyOn`, and carries
  a `// TODO(scenario-G)` tag. The `RoleEmerged` acceptance criterion is deferred (also noted in
  §Acceptance Criteria). Scenario G is the only P1 item this affects; all other tick-loop,
  conflict, outcome, spawn, and snapshot ACs are fully unblocked. The activation checklist in
  §Emergent-reliance defines the exact pre-conditions for replacing the stub.
- **Role-detection threshold & succession (design §4 open item; NOT blocking the basic emit).**
  design.md lists "역할 검출 임계·승계" as unresolved. P1 uses a single `reliance_threshold` share
  with no succession/decay (a holder stays "emerged" while the share holds). Succession (what
  happens when reliance shifts to a new holder) and hysteresis are deferred. Escalate before tuning G.
- **Multi-stat outcome composition (NOT blocking P1).** An action with several `uses:<StatID>`
  tags (e.g. `Hunt` uses Strength AND Agility) needs a fixed composition for the resolution stat
  and the conflict stat. P1 uses a deterministic, tag-order-stable mean (or min) of the
  capability composition; the exact rule (mean vs weakest-link) belongs in a tuned `balance.yaml`
  term later. Flag before tuning combat/contest outcomes.
- **Outcome difficulty model (NOT blocking P1).** P1 resolves `realLevel` vs a single
  `outcome_difficulty_base` (optionally tag-scaled by `effort:`/`risk:` levels). A richer
  per-action difficulty curve or a probabilistic (rng-drawn) success roll is reserved — note that
  any rng draw here MUST use the per-agent fork (D12). Escalate before adding stochastic outcomes.
- **Resource regrowth / animal behaviour (NOT blocking P1).** Seasonal regrowth of foraged
  objects and animal (prey) movement are world-state dynamics that belong here eventually
  (architecture §2 "resource regen in engine/world") but are out of the P1 batch. Recorded so the
  object record's `contents/state` shape leaves room for them.

## Notes

- **Why the snapshot can stay thin for P1.** The tick is sequential, so phase 2 never races phase
  4 (no agent reads while another mutates). A read-only wrapper over live state is therefore safe.
  The SPEC commits to the `WorldSnapshot` *interface* (frozen, tick-scoped) so the implementation
  can switch to a true COW copy when phase 2 parallelizes — agents never depend on the wrapper vs
  copy distinction (they only see `agent.WorldView`).
- **Why per-agent RNG forks, not a shared root draw in phase 2.** If agents drew from the shared
  root during planning, the *order* of agent evaluation would change every agent's draws — making
  parallelization impossible and the sequential order load-bearing. Forking by sorted-id position
  decouples each agent's randomness from evaluation order (D12), which is the prerequisite for the
  future parallel plan phase. The apply phase (serial) may draw from forks too (e.g. stochastic
  outcomes) using the SAME fork derivation.
- **Events emitted (data-contracts §4).** The world emits `TickDone{tick, agent_count,
  intent_count}` and `RoleEmerged{function, holder, reliance_share}`; `SnapshotReady{tick}` signals
  persist (the world does no IO). Agent-scoped events (`GoalSelected`/`PlanBuilt`/`ActionStarted`/
  `ActionDone`/`Interacted`/`BeliefUpdated`/`CopingEntered`/`Perceived`) originate in
  `engine/agent` through the SAME injected emitter — the world threads the emitter to `Tick`. No
  `real_stats` on any payload (data-contracts §4 SSE policy).
- **Snapshot field mapping (data-contracts §1).** `tick` ← the world's counter; `rng_state` ←
  `root.State()`; `world.objects[]` ← the object records (`id, kind, pos, contents`);
  `agents[]` ← each agent's public state (`id, pos, real_stats, body, goal, plan_summary,
  tom_digest, known_digest`). The world EXPOSES these via `AgentIDs`/`AgentOf`/`Snapshot` and the
  object accessor; `platform/persist` reads and serializes them. The SpatialHash is derived and
  rebuilt from positions on resume — never serialized (`engine/spatial` Notes).
- **`Services` re-export.** `type Services = agent.Services` (a type alias) keeps the caller from
  assembling the bundle twice; the world threads the exact same value to every `agent.Tick`.
- **Object record shape.** Minimal P1 record: `{ID core.ObjectID, Kind core.Tag, Pos core.Vec2,
  Supply map[core.Dimension]float64, /* contents/state reserved */}`. Supply only (D9). The
  `KnownObject` the agent sees mirrors `{ID, Pos, Kind, Supply}` — the world builds it in
  `WorldSnapshot.KnownObjects` (sorted by `ObjectID`, D12).
- **Durative scaling lives here.** Per `engine/agent` §Durative, the base `ActionDef.Duration` is
  scaled by stats/distance at execution — the world (apply phase) owns that scaling and uses
  `worldtime.Clock.TicksForMinutes` to convert; the planner uses the base duration only.
