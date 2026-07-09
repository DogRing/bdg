# SPEC — `engine/world` · Tick Loop, Conflict & Outcome Resolution

> Status: `READY`
> Sub-spec of: `SPEC.md`  ·  Owner agent: `implementer`

## Scope

This sub-spec covers the **D12 deterministic tick loop** (four-phase read → plan → collect →
apply), the **per-agent RNG fork** derivation, **conflict resolution**, and **outcome
resolution**. Emergent-phenomenon mechanics (reliance-cluster detection, ToM pruning) live in
[`SPEC-emergent.md`](SPEC-emergent.md).

---

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
  - TIME-SLICING (round-robin replan) — ⚠ **STATUS: DEFERRED / NOT YET IMPLEMENTED (2026-07-08).**
    The design below is the intended future agent-side planner-LOD; it is **not wired today**. The
    execute-only (no-replan) fast path does not exist, so every agent runs the FULL plan every tick
    regardless of `cfg.PlanInterval`. The `PlanInterval` config is reserved for this future work
    (the analogous cadence LOD already ships for fauna — see `docs/plans/fauna.md` F45 DORMANT/ACTIVE +
    `engine/fauna/cheap.go`). ⚠ **Determinism note for the future implementer:** a real no-replan
    path that skips the planner's RNG draws would DIVERGE the RNG stream from the full-plan path —
    "same fork regardless of slicing" only holds while both paths run full deliberation, so
    completing this is not free. Until implemented, treat this section as design intent, not behavior.
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
  - After all intents: advance the tick counter; run the reliance-cluster scan (SPEC-emergent.md);
    run the ToM prune pass (SPEC-emergent.md) when the tick is on the prune schedule; emit
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

---

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

---

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
   Action, Status, Completed (locomotion: within cfg.ArrivalEpsilon of Intent.Move; else: a.Elapsed reached the scaled Duration),
   StatsUsed (= statsUsed, drives which β-calibration runs),
   Expected (the agent's pre-action expected progress, carried on the Intent / read from agent),
   Actual   (the realized progress this tick, from realLevel — reflects Real Stats),
   Effect   (the realized need deltas applied: def.Effect or the consumed item's supply, D9),
   Evidence (per-stat direct-observation evidence the world attributes for ToM[self] folding, D8).
```

- **Movement & locomotion completion**: a *locomotion* action is one whose effect is a positional
  predicate — `MoveTo` (`produces: at_target`) or `Approach` (`produces: near_other`); identified by
  `isLocomotion(Produces)`, the same signal `engine/agent` uses to bind `Intent.Move`. For these the
  world steps `Pos` a `MoveSpeedPerTick` fraction toward `Intent.Move`, `SpatialHash.Move`s it, and
  reports `Completed` on **arrival** — when the pre-move `Pos` is within `cfg.ArrivalEpsilon` of
  `Intent.Move`. Travel time therefore scales with distance (D11). A distance-derived cap
  (`⌈dist⌉ + Duration + 1` game-minutes) guarantees termination so a moving/unreachable target can
  never freeze the agent. Non-locomotion actions ignore `Intent.Move`.
- **Durative scaling** (non-locomotion): the base `ActionDef.Duration` (game-minutes) is scaled by
  stats at execution **here** (the planner uses the base only — see `engine/agent` §Durative). The
  `worldtime.Clock.TicksForMinutes` converts the scaled duration to ticks.
- `real_stats` are **never** placed on an emitted event payload (god-view; data-contracts §4).

---

## Invariants (D12 tick-loop scope)

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
   sorted `ObjectID`.
7. **No wall-clock, no uncontrolled parallelism**: no `time.Now()`; the only time source is the
   `Tick` counter. No OS-level parallelism that can interleave phase-4 mutations (apply is serial).
8. **Byte-identical from the seed**: same seed + same content (`config_hash`) → byte-identical
   simulation state and event sequence from tick 0; resuming from a tick-T snapshot is
   byte-identical to running 0→T+k (testing.md §1 resume invariant; the root `rng_state`
   round-trips).
9. **Snapshot serialization contract (`WorldState` JSON)**: field names are the snake_case
   data-contracts §1 shape (`agents`/`id`/`real_stats`/`self_est_stats`/`emerged_roles`) so the
   read API parses the live blob directly. `State()` additionally emits `tom_digest`
   (observer→subject→{`est_stats`,`rely_on`}) — the **capture-only** cross-agent ToM projection
   for the god-view (D6/D8). `RestoreState` ignores `tom_digest` (the running sim rebuilds
   beliefs), so it does not affect the resume invariant.

Plus:

- **Read/plan/collect/apply ordering is mandatory** (the four phases above, in order). Phase 2 is
  read-only on shared state; phase 4 is the only mutator. A test instruments the phase boundaries.
- **Real Stats read ONLY at outcome resolution / conflict (D8)**: the decision path (agent `Tick`)
  reads `ToM[self]`; the world reads `RealStats` only when resolving an applied intent and when
  composing the conflict stat. A guard confirms `Tick`-phase code never reads `RealStats`.
- **Objects carry supply only (D9)**: an object record has a `supply map[Dimension]float64` and no
  "future need"/demand/schedule field; provisioning is the planner's forward-sim, not stored here.
- **Capability composition, no individual skill (D7)**: the resolution stat is composed from
  `Stats.Kinds(Capability)` per the action's `uses:<StatID>` tags — never a per-action skill.
- **Tag-derived resolution & cost (D4)**: the stat an action resolves against is derived from its
  `Tags`, not a bespoke per-action function or field.
- **All constants injected (D10)**: no literal for cell size / reliance threshold / difficulty /
  backup interval / plan-worker count / plan interval / stat-or-action name appears in logic;
  every constant flows from `Config` / `worldtime.Config` / the injected registries.
- **No IO (architecture §1)**: imports no `os`/`net`/filesystem package; reads no file; emits only
  through the injected `EventEmitter`; the snapshot WRITE is `platform/persist`.
- **Snapshot is read-only & tick-scoped**: agents mutate nothing through the `WorldSnapshot`; it is
  valid only for the tick it was taken on (a stub that panics on write proves the read-only claim).
- **Parallel-plan read-only (D12 extension)**: goroutines in the plan phase read ONLY the
  `WorldSnapshot` (and their own rng fork); world state — agents, objects, the SpatialHash, the
  tick counter — is NEVER written until the serial apply phase. A write-guard snapshot + a `-race`
  run prove no plan-phase goroutine mutates shared state.
- **Intent sort after fan-out (D12)**: intents from the parallel goroutines are gathered and
  stable-sorted by `AgentID` AFTER all goroutines join, BEFORE apply. The result is byte-identical
  to the serial-plan path (`PlanWorkers == 1`) given the same seed — the parallel golden asserts it.
- **Plan-slot determinism (D12)**: `planSlot(agentID) = sortedIndex(agentID) mod cfg.PlanInterval`,
  where `sortedIndex` is the agent's position in the canonical `w.agentIDs` slice — NEVER derived
  from map iteration. An off-slot agent does not replan but its execute path still runs (every
  agent executes its durative step every tick); a mid-durative agent is never interrupted by its
  plan tick. `PlanInterval` 0 or 1 ⇒ every agent replans every tick (backward compatible).

---

## Acceptance Criteria (tick loop · conflict · outcome · spawn · scale)

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
- [ ] **TickDone / SnapshotReady events**: every `Tick()` emits `TickDone{tick, agent_count,
  intent_count}`; `SnapshotReady` is emitted exactly every `BackupEveryTicks` ticks and never
  performs IO (the world only signals). `real_stats` appears on no event payload (god-view).
- [ ] **No hardcoded constant / id (D10/D7 guard)**: grep guard — no cell-size /
  threshold / difficulty / backup-interval literal and no stat/action-name string literal in
  `engine/world` logic; all flow from `Config`/registries.
- [ ] **No IO (architecture §1 guard)**: grep/import guard — no `os`/`net`/filesystem import;
  events leave only through the injected `EventEmitter`.
- [ ] **Determinism golden (testing.md §1/§3)**: run a fixed seed with 1–3 spawned agents + a few
  objects for N ticks → serialize state digest → matches the golden; a second run from a fresh
  registry built from the same content reproduces it byte-for-byte (cross-process).
- [ ] **Resume invariant (testing.md §1)**: capturing the world (incl. `root.State()`) at tick T
  and resuming yields the same tick-T+k state as running 0→T+k uninterrupted (the SpatialHash is
  rebuilt from positions; the root `rng_state` round-trips).
- [ ] **Scenario B (need-driven intent through one tick)**: agent Ticks, world resolves, agent's
  `ApplyOutcome` folds back — direction/existence assertions, not exact numbers (testing.md §4).

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

---

## Notes

- **Why per-agent RNG forks, not a shared root draw in phase 2.** If agents drew from the shared
  root during planning, the *order* of agent evaluation would change every agent's draws — making
  parallelization impossible and the sequential order load-bearing. Forking by sorted-id position
  decouples each agent's randomness from evaluation order (D12), which is the prerequisite for the
  future parallel plan phase. The apply phase (serial) may draw from forks too (e.g. stochastic
  outcomes) using the SAME fork derivation.
- **Why the snapshot can stay thin for P1.** The tick is sequential, so phase 2 never races phase
  4 (no agent reads while another mutates). A read-only wrapper over live state is therefore safe.
  The SPEC commits to the `WorldSnapshot` *interface* (frozen, tick-scoped) so the implementation
  can switch to a true COW copy when phase 2 parallelizes — agents never depend on the wrapper vs
  copy distinction (they only see `agent.WorldView`).
- **Durative scaling lives here.** Per `engine/agent` §Durative, the base `ActionDef.Duration` is
  scaled by stats/distance at execution — the world (apply phase) owns that scaling and uses
  `worldtime.Clock.TicksForMinutes` to convert; the planner uses the base duration only.

### Notes — Scale (parallel plan)

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
