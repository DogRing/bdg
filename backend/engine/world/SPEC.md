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
// NOT in the `required` array (backward compatibility — see Notes §Scale).
type Config struct {
    SpatialHashCell       float64 // balance.yaml world.spatial_hash_cell — SpatialHash cell edge
    RoleConvergenceThreshold float64 // balance.yaml politics.role_convergence_threshold — share of
                                  //   agents relying on one holder for a Function → emit RoleEmerged
                                  //   on the rising edge (D2). P6: supersedes the retired
                                  //   world.reliance_threshold placeholder; drives the live full scan.
    OutcomeDifficultyBase float64 // balance.yaml world.outcome_difficulty_base — base difficulty an
                                  //   action's used stat is resolved against (vs Real Stat).
    BackupEveryTicks      int     // balance.yaml world.backup_every_ticks — emit SnapshotReady every
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
  - PARALLEL FAN-OUT (D12-safe): the per-agent agent.Tick calls are launched on a goroutine pool
    of size cfg.PlanWorkers (0 → runtime.NumCPU()) over the read-only snapshot. Each goroutine
    reads ONLY the snapshot and its own rng fork; NO goroutine writes world state. Results are
    collected (a per-agent []Intent slot indexed by sorted position, or a channel) and, after ALL
    goroutines complete, gathered into one []Intent. Because each agent's fork is keyed by its
    sorted-id position (not by goroutine scheduling order), the gathered intents are independent of
    the order goroutines happened to run in. cfg.PlanWorkers == 1 degenerates to the sequential
    walk; the two paths MUST be byte-identical (see Acceptance — Scale & performance).
  - TIME-SLICING (round-robin replan): an agent runs the FULL plan (agent.Tick deliberation) on a
    given tick only when it is in this tick's plan slot:
        planSlot(agentID) = sortedIndex(agentID) mod cfg.PlanInterval   (PlanInterval 0/1 ⇒ always)
        replans this tick  iff  w.tick mod cfg.PlanInterval == planSlot(agentID)
    `sortedIndex` is the agent's position in the canonical sorted w.agentIDs slice (D12 — NEVER
    derived from map iteration). An agent NOT in this tick's slot does NOT replan; it instead
    advances its current durative action ONE step (the execute path) and emits the continuation
    intent (IntentContinue) for it. The execute path therefore runs for EVERY agent every tick;
    only the deliberation (goal/plan selection) is time-sliced. An agent mid-durative-action is
    NEVER interrupted when its plan tick arrives — the state machine continues normally and a new
    plan is selected only at the natural action boundary (the agent's own durative policy).

Phase 3 — COLLECT intents:
  - Gather every []Intent from every agent into one []Intent, then STABLE-SORT by Intent.Agent
    (AgentID, lexicographic, D12). Ties (multiple intents from one agent) keep emission order.
    This sorted slice is the authoritative apply order. The sort runs AFTER the parallel fan-out
    has fully joined, so the apply order is independent of goroutine scheduling.

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
  - After all intents: advance the tick counter; run the reliance-cluster scan (below); run the
    ToM prune pass (below) when the tick is on the prune schedule; emit
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

### Full scan (P6 — ACTIVATED)

`engine/tom` formalizes `type Function string` and `Belief.RelyOn map[Function]float64` (see
`engine/tom/SPEC.md` §P6 Reliance & Influence Contract), so the P1 no-op stub is **replaced** by the
scan below. The share threshold is the P6 key `politics.role_convergence_threshold`
(`Config.RoleConvergenceThreshold`), which **supersedes** the P1 placeholder `world.reliance_threshold`
(the old key/field is retired).

```
RelyOn edges live on each agent's ToM ABOUT OTHERS: agent x relies on holder h for f iff
x.ToM[h].RelyOn[f] is the strongest such edge x holds for f (x's chosen provider for f).

per Function f, in sorted Function order over the union of functions referenced across agents (D12):
    for each agent x (AgentIDs() order, D12):
        h* = argmax_h x.ToM[h].RelyOn[f]   (the holder x relies on most for f; skip if max == 0)
        votes[f][h*]++
    holder  = argmax_h votes[f][h]          (ties → lower AgentID, D12)
    share   = votes[f][holder] / agent_count
    if share ≥ cfg.RoleConvergenceThreshold AND (f,holder) was NOT already emerged last scan:
        emit RoleEmerged{function: f, holder: holder, reliance_share: share}   // RISING EDGE only
    if share < cfg.RoleConvergenceThreshold: clear (f,*) from the emerged set   // allow re-emergence
```

- **Emit on the RISING EDGE only.** The world keeps a small `emerged map[Function]AgentID` of the
  currently-emerged (function→holder) pairs and emits `RoleEmerged` only when a (function,holder)
  pair first crosses the threshold — NOT every tick it stays converged (keeps the event stream and
  the Scenario-G golden clean). A holder change for the same function (succession) is a new rising
  edge → a new event. Dropping below the threshold clears the entry so a later re-convergence emits
  again. The `emerged` set is owned state, serialized with the world (so resume stays byte-identical).
- This is **emergent detection ONLY**: the world defines **no** `Role` type, no chief/leader enum,
  no institution (D2). It reads the `RelyOn` distribution that `engine/agent` maintains on
  `tom.Belief` and reports a statistic. The grep/struct guard (Invariants) forbids a `Role` type here.
- Deterministic: functions, agents, and holders are iterated/argmax'd in sorted order; ties break
  by lower `AgentID`; no rng.

Activation checklist (P6 — all met):
- [x] `engine/tom` formalizes `type Function string` and `Belief.RelyOn map[Function]float64` (§P6).
- [x] `engine/agent` populates `RelyOn` edges during deliberation (see `engine/agent/SPEC.md` §P6
  VoteAction & reliance trigger).
- [x] `content/balance.yaml politics.role_convergence_threshold` is present (Config source below).
- [x] Scenario G fixture + golden recorded (`docs/testing.md §4`; AC below).

## ToM pruning (scale — bounded O(N²) ToM memory)

To keep per-agent ToM size bounded as the population and runtime grow, the world runs a periodic
**prune pass** that drops stale subject beliefs. Each `tom.Belief` already tracks `LastSeen` (the
tick of the last direct observation; see `engine/tom/SPEC.md`). The prune pass uses that gap.

```
prune pass — runs after the reliance scan, ONLY on ticks where
    cfg.PruneInterval > 0 AND w.tick mod cfg.PruneInterval == 0:

for each agent a in AgentIDs() (sorted, D12):
    for each subject s in a.ToM.Subjects() (sorted, D12):
        if s == a.ToM.SelfID(): continue                 // never prune ToM[self] (D8)
        if (w.tick - a.ToM[s].LastSeen) > cfg.PruneThreshold:
            decay a.ToM[s].Belief by ×cfg.PruneDecayFactor    // 0.0 ⇒ zero out
            remove subject s from a.ToM                        // then drop the entry entirely
```

- **Decay then remove.** The Belief is multiplied by `cfg.PruneDecayFactor` and then the map entry
  is removed. With the default `PruneDecayFactor = 0.0` the decay zeros the Belief before removal
  (a no-op observable difference vs straight removal, but the hook is in place for a future fade
  feature — see Notes §Scale). A value like `0.5` would let a faint trace persist if a later phase
  chooses to keep decayed-but-nonzero entries; for P1 the entry is always removed after decay.
- **Re-encounter re-initializes (NOT a bug).** If a pruned subject is later perceived again (Sight
  returns them), the next observation calls the SAME first-encounter initialization path
  (`engine/tom` initial-estimate seed via `Observe`/`GossipUpdate` on an unknown subject) — there
  is **no** special "warm-start" branch. The re-seeded Belief uses the engine/tom defaults
  (LastSeen updated to the current tick; EstStats reset to the prior-from-perception seed). This is
  the intended behaviour: forgetting is real, and re-acquaintance starts fresh.
- **Self is never pruned (D8).** `ToM[self]` is exempt — self-perception is calibrated only by
  action, never aged out.
- **Deterministic (D12):** the prune pass iterates agents in `AgentIDs()` (sorted) order and, within
  each agent, subjects in `ToM.Subjects()` (sorted) order; no `map` is ranged for logic. No rng.

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
  at Spawn with the injected rng; read `LastSeen`/`Subjects()` for the prune pass). The world reads
  `Belief.RelyOn`/`LastSeen`; it does not own the update math.
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
  `RealStats` for outcome resolution, `ToM.RelyOn` for the reliance scan, and `ToM` LastSeen for
  the prune pass; the prune pass removes stale ToM subjects via the `engine/tom` API).
- The **plan-slot schedule**: the round-robin replan assignment (agentID → slot) derived purely
  from the sorted `w.agentIDs` index mod `cfg.PlanInterval` (no separate stored map is required —
  it is recomputed from the sorted slice each tick; D12), and the **prune-pass scheduler** (the
  tick-mod-`cfg.PruneInterval` gate). Both are derived state, not authored input.
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
  backup interval / plan-worker count / plan interval / prune interval-threshold-decay / stat-or-
  action name appears in logic; every constant flows from `Config` / `worldtime.Config` / the
  injected registries. A grep guard confirms it.
- **No IO (architecture §1)**: imports no `os`/`net`/filesystem package; reads no file; emits only
  through the injected `EventEmitter`; the snapshot WRITE is `platform/persist` (the world emits
  `SnapshotReady`, it does not serialize/transport).
- **Snapshot is read-only & tick-scoped**: agents mutate nothing through the `WorldSnapshot`; it is
  valid only for the tick it was taken on (a stub that panics on write proves the read-only claim).
- **Parallel-plan read-only (D12 extension)**: goroutines in the plan phase read ONLY the
  `WorldSnapshot` (and their own rng fork); world state — agents, objects, the SpatialHash, the
  tick counter, the prune/reliance state — is NEVER written until the serial apply phase. A
  write-guard snapshot + a `-race` run prove no plan-phase goroutine mutates shared state.
- **Intent sort after fan-out (D12)**: intents from the parallel goroutines are gathered and
  stable-sorted by `AgentID` AFTER all goroutines join, BEFORE apply. The result is byte-identical
  to the serial-plan path (`PlanWorkers == 1`) given the same seed — the parallel golden asserts it.
- **Plan-slot determinism (D12)**: `planSlot(agentID) = sortedIndex(agentID) mod cfg.PlanInterval`,
  where `sortedIndex` is the agent's position in the canonical `w.agentIDs` slice — NEVER derived
  from map iteration. An off-slot agent does not replan but its execute path still runs (every
  agent executes its durative step every tick); a mid-durative agent is never interrupted by its
  plan tick. `PlanInterval` 0 or 1 ⇒ every agent replans every tick (backward compatible).
- **ToM prune re-init (engine/tom contract)**: a pruned ToM subject is decayed by
  `cfg.PruneDecayFactor` then removed from the ToM map; a later re-encounter calls the SAME
  first-encounter initialization path (`engine/tom` unknown-subject seed) — there is no special
  "warm-start" branch. `ToM[self]` is never pruned (D8). The prune pass iterates agents and
  subjects in sorted order (D12).

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
- [ ] **No `Role` type exists (D2 guard, all phases)**: a struct/grep guard confirms NO
  `Role`/`Chief`/`Faction`/institution type in `engine/world`; `RoleEmerged` carries only
  `{function, holder, reliance_share}` — a statistic over `RelyOn`, never a stored role object.
- [ ] **Reliance full scan emits RoleEmerged on the rising edge (P6, D2)**: with agents whose
  `ToM[h].RelyOn["safety"]` points a super-threshold share at one holder `h`, `relianceScan`
  emits exactly **one** `RoleEmerged{function:"safety", holder:h, reliance_share:share}` on the tick
  the share first crosses `cfg.RoleConvergenceThreshold`, and **nothing** on subsequent ticks while
  it stays converged (rising-edge debounce via the owned `emerged` set). Share below threshold →
  no emission and the entry clears. Holder change at/above threshold → a new event (succession).
  Deterministic over two runs (D12); tie-break by lower `AgentID`.
- [ ] **Scenario G — chained theft → RoleEmerged (P6, GOLDEN)**: the integration AC below
  (§Acceptance — Scenario G) records the `RoleEmerged` event and reliance_share as a golden.
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
- [ ] **Scenario G — chained theft → safety↓ → Patrol → RelyOn convergence → RoleEmerged (P6, GOLDEN)**:
  the integration chain end-to-end. Setup (fixture, fixed seed): several villagers + one capable
  guardian near a `village_center`; a low-asset agent commits repeated `Take` (chained theft) so
  each victim's `Safety` intensity rises (collective Safety drops). Per `engine/agent` §P6, victims
  whose **safety Function** is self-unsolvable (gate-blocked / cost > threshold) `AdjustRelyOn` toward
  the guardian (`BestProviderFor("safety", …)`), optionally accelerated by `Vote`; the guardian's
  `defensiveCollectiveGoal` fires → it `Patrol`s. Assertions:
  - direction: mean collective Safety intensity rises across the theft window; the guardian executes
    ≥1 `Patrol`; the guardian's received-RelyOn share for `"safety"` crosses `RoleConvergenceThreshold`.
  - **exactly one** `RoleEmerged{function:"safety", holder:guardian, reliance_share:s}` on the rising
    edge; none on the converged ticks that follow.
  - **golden**: the `RoleEmerged` payload (`function`, `holder`, `reliance_share` to fixed precision)
    and its tick, byte-stable across two runs (D12). Recorded under `testdata/golden/scenario_g_*.json`.

### Scale & performance

- [ ] **Smoke (race-clean) — 200 agents × 1440 ticks**: spawning 200 agents and running 1440 ticks
  with seed 1 completes without OOM or deadlock under `-race` (a -race smoke test; no goroutine
  leak, no data race reported by the plan-phase fan-out).
- [ ] **Parallel plan is byte-identical to serial (D12)**: running with `plan_workers > 1` produces
  byte-identical tick states (state digest per tick) to `plan_workers == 1` at EVERY tick, asserted
  on a 50-agent, 100-tick golden recorded under `testdata/golden/parallel_plan.json`. The fan-out
  + intent sort + serial apply path matches the sequential walk exactly given the same seed.
- [ ] **Plan-slot round-robin (D12, table-driven)**: with `plan_interval = 3`, the agent at
  sorted-index 0 replans on ticks 0,3,6,…; sorted-index 1 on 1,4,7,…; sorted-index 2 on 2,5,8,…
  (table-driven over the (sortedIndex, tick) grid; the slot is `sortedIndex mod plan_interval` and
  the replan condition is `tick mod plan_interval == slot`).
- [ ] **Off-slot agents still execute (every tick)**: an agent NOT in this tick's plan slot still
  executes its current durative action (emits the continuation intent / advances its step). The
  execute phase runs every tick for ALL agents; only deliberation is time-sliced. A test with
  `plan_interval > 1` asserts an off-slot, mid-durative agent still produces an `IntentContinue`
  and is not interrupted.
- [ ] **ToM prune removes after threshold (table-driven)**: after `prune_threshold` ticks of no
  Sight contact with a subject, the subject's ToM entry is removed. Table: a gap JUST BELOW
  threshold → the entry is still present; AT/ABOVE threshold → the entry is decayed (×
  `prune_decay_factor`) then removed. `ToM[self]` is never pruned regardless of gap.
- [ ] **Prune re-encounter re-initializes (NOT a bug)**: after a subject is pruned, re-encountering
  the agent (Sight returns them → next `Observe`) re-initializes the ToM entry to the first-encounter
  defaults (`LastSeen` updated to the current tick; Belief reset to the prior-from-perception seed,
  not the pre-prune values). Asserts the SAME initialization path is taken, with no warm-start branch.
- [ ] **Prune iteration order is sorted (D12)**: the prune pass iterates agent IDs in sorted order
  and, within each agent, ToM subjects in sorted order — verified by logging the iteration order in
  a determinism test and matching it to `AgentIDs()` / `ToM.Subjects()`. Two runs are byte-identical.

> Structural JSON-schema validation of the `content/balance.yaml world.*` block this module reads
> (incl. the three new keys — see Open Questions — and the five scale-extension keys
> plan_workers/plan_interval/prune_interval/prune_threshold/prune_decay_factor) is a
> **platform/config** AC (it owns the file IO + schema). This module proves only behaviour
> reachable from the injected `Config`/`Services`.

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
- **SIMD vectorisation** of the plan/apply hot loops → not done (and not planned for P1); the
  scale work here is goroutine fan-out only.
- **Persistent / long-lived worker pools** → NOT done. The plan-phase goroutines are spun up and
  joined WITHIN a single `Tick()` (per-tick fan-out); there is no pool that outlives a tick (see
  Notes §Scale for why P1 avoids the lifecycle complexity).
- **Cross-machine / distributed simulation** (sharding agents across processes/hosts) → out of
  scope; the world is a single-process orchestrator.

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
  NOT in the schema `required` array — a missing key falls back to its documented default so existing
  content/runs are unaffected (plan_interval default 1 ⇒ replan-every-tick; plan_workers 0 ⇒
  NumCPU; the never-prune path is reachable by a large prune_interval). The implementer adds the
  keys + (optional) schema entries with the type/min constraints noted in the `Config` block.
- **`RelyOn` / `Function` type — RESOLVED (P6); stub REPLACED.** `tom.Belief.RelyOn` and
  `tom.Function` are formalized (`engine/tom/SPEC.md` §P6), and `engine/agent` populates the edges
  (`engine/agent/SPEC.md` §P6). The P1 no-op `relianceScan()` stub is therefore **replaced** by the
  live full scan (§Emergent-reliance). Scenario G is no longer deferred — its golden AC is below.
- **Role-detection threshold & succession (P6).** P6 uses a single share threshold
  `politics.role_convergence_threshold` with **rising-edge** emission (the owned `emerged` set):
  a holder stays emerged silently while its share holds; a shift of the plurality to a new holder
  is a **succession** — a fresh rising edge → a new `RoleEmerged` for the same function. Hysteresis
  (a separate clear-threshold below the emit-threshold to damp flapping) is **deferred**; P6 clears
  on the same threshold. Escalate before tuning if flapping appears in long Scenario-G runs.
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

### Notes — Scale (parallel plan · time-slicing · ToM prune)

- **Why `plan_interval` default = 1 is backward compatible.** With `plan_interval = 1` every
  agent's `planSlot` is `sortedIndex mod 1 == 0` and the replan condition `tick mod 1 == 0` is
  always true — so every agent replans every tick, exactly the pre-time-slicing behaviour. The
  determinism goldens recorded before time-slicing remain valid at the default. Time-slicing only
  changes behaviour when `plan_interval > 1`, and even then it is a performance/throughput trade
  (fewer full plans per tick), not a semantic change to a single agent's durative execution.
- **Why goroutines are per-tick (no persistent pool) for P1.** A `Tick()` spins up the plan-phase
  goroutines and joins them all before phase 3 begins. A long-lived worker pool would add lifecycle
  state (shutdown, backpressure, panic propagation across ticks) that buys little at P1 scale and
  risks leaking goroutines or coupling tick boundaries — both hazards for the determinism/resume
  invariants. The fan-out is cheap relative to per-agent planning, so per-tick creation is the
  simple, safe choice; a persistent pool is an explicit future optimisation (Out of Scope).
- **Why intents are sorted AFTER the fan-out joins.** Goroutine completion order is nondeterministic
  by design, so the gathered intents must be stable-sorted by `AgentID` (and each agent's own forks
  keyed by sorted position) before apply. This is the single line that makes the parallel path
  byte-identical to the serial path — it is asserted by the `parallel_plan.json` golden.
- **`prune_decay_factor = 0.0` means "zero then remove".** The default multiplies a stale Belief by
  0 (zeroing it) and then removes the map entry — observationally the same as a straight removal at
  P1, but the decay hook is deliberately in place. A future value like `0.5` would let a Belief fade
  gradually (a faint, decaying rumor trace) before removal — useful for rumor-persistence scenarios
  — and would be enabled behind a feature flag once the "keep decayed-but-nonzero" path is specified.
  For P1 the entry is always removed after the decay multiply.
- **Re-encounter after prune is intentional forgetting.** Because the prune pass removes the subject
  entirely, a later Sight contact re-seeds the Belief through the same unknown-subject initialization
  `engine/tom` uses on a first encounter. The agent "forgets" and then "re-learns" — this is the
  designed memory bound, not a regression. Tests assert the re-init path is taken (no warm-start).
