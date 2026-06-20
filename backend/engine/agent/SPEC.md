# SPEC — `engine/agent`

> Status: `NEEDS_FIX` (P1/P2 loop IMPLEMENTED; P3 coping-cascade completion + Stamina/Adrenaline loop drafted below)
> Leaf level: `L5`  ·  Owner agent: `implementer`

## Purpose

Drives one agent through one tick of the decision loop (design §1.1):
`perception → value appraisal → goal mediation → planning (HTN+GOAP) → durative execution →
signal → belief/reputation/reliance update`. It is the **orchestrator** (D5): it owns the
agent's dynamic `Body` state and `ToM`, sequences the upstream pure modules, and emits **intents**
— it never mutates the world directly and never decides *what is wanted* (`values`/`needs`) or
*how to get it* (`planner`). It additionally owns the per-agent dynamics that span the loop: the
**coping cascade** (rebind → longing → latent → apathy), per-stat **self-calibration** (β, D8),
the **Stamina** drain/regen loop, the **Adrenaline** surge/crash, the **Mood** update, and
**Resentment** drift. It feeds the live Body scalars (Stamina, Mood, Adrenaline, Urgency) into the
gate/planner snapshot each tick so the P3 gates (`stamina`, `apathy`, `conscience`-relief,
`adrenaline`-cost) can read them (`engine/gates/SPEC.md`). All rate constants are injected from
`content/balance.yaml` (D10); the package hardcodes none.

## Current state (P1/P2 — IMPLEMENTED, DO NOT regress)

Shipped and tested across `agent.go`, `types.go`, `tick.go`, `execution.go`, `outcome.go`,
`coping.go` (+ `agent_test.go`, `golden_test.go`, `scenario_c_test.go`):

- The 8-phase `Tick` loop (perceive → decay needs → appraise → mediate goal → plan → execute →
  signal → dynamics) and `ApplyOutcome` (durative completion/interruption, β fold, Mood update,
  Effect application, completed-goal reset to Idle).
- The `Agent`/`Intent`/`Signal`/`ActionOutcome`/`Config`/`Services`/`WorldView`/`KnownObject`/
  `LatentGoal` types; `CopingState{Idle,Rebinding,Longing,Latent,Apathy}`.
- **Coping skeleton** (`coping.go`): `enterCopingCascade` advances one step per failed re-plan;
  `canRebind` (budget-derived Intelligence threshold), `rebindSubstitute`, `enterLonging`,
  `enterApathy`. Emits `CopingEntered`.
- **Stamina/Adrenaline skeleton**: `drainStamina` (per-effort drain in `execute`), `updateDynamics`
  (Adrenaline surge/crash + Mood-baseline pull + `updateResentment`).
- β self-calibration via `foldEvidence` (Invisible/Interrupted → no fold, self-sealing D8).
- `mediateGoal` (Stickiness/goal_deadband anti-thrash, owned here per planner OQ).

P3 **completes** the gaps in this skeleton. Below, "Current" marks what exists; "Implement (P3)"
marks the new work. The Public Interface adds four `Agent` fields and threads body scalars into the
snapshot.

## Implement (P3 — what this batch completes)

### A. Coping cascade — full transition logic

```
Idle
  ↓ (plan fail)
Rebinding   ← only if perceived Intelligence ≥ rebind threshold; else SKIP straight to Latent
  ↓ (n consecutive fails)
Longing     ← goal kept, substitute searched
  ↓ (substitute also fails)
Latent      ← goal deferred, alt-goal pursued; Resentment accrual STARTS here
  ↓ (FailStreak ≥ apathy threshold)
Apathy      ← global Mood↓, plan budget↓; recovers on a SINGLE plan success
```

- **`FailStreak`** (new field): incremented each consecutive failed re-plan; reset to 0 on any
  plan success or a completed action. The cascade step is a function of `(failure cause,
  perceived Intelligence, FailStreak, prior CopingState)` — **rng-free, deterministic** (D12).
- **Low-Intelligence shortcut (design §1.4 object-fixation)**: if perceived `Intelligence < 0.4`
  (`coping.rebind_min_intelligence`, see Data Contract) the agent **cannot enter `Rebinding`** and
  on the first plan fail goes **directly to `Latent`** (skipping `Rebinding` AND `Longing`). High-
  Intelligence agents traverse `Rebinding → Longing → Latent`.
  > Current `canRebind` derives the threshold from the budget curve; P3 replaces it with the
  > explicit `coping.rebind_min_intelligence` constant (cleaner, resolves the existing Open
  > Question) and adds the "skip to Latent" shortcut for the low branch.
- **Apathy entry**: when `Coping == Latent` and `FailStreak ≥ coping.apathy_fail_streak`, enter
  `Apathy`. Apathy lowers planning budget (use a reduced `Budget` next tick) and pulls Mood down
  (the Mood-decay pull plus the adrenaline crash already do this; Apathy additionally suppresses
  the active goal so no positive progress term fires).
- **Apathy recovery (single success)**: in `ApplyOutcome`, a single completed plan success
  (`Status == Succeeded && Completed == true`) while `Coping == Apathy` resets `Coping = Idle`,
  `FailStreak = 0`, and bumps `Mood += coping.apathy_recover_mood` (`+0.15`). A *different* goal
  becoming plannable also exits Apathy via the normal phase-5 success path.

### B. Resentment accrual (Latent residence)

While `Coping == Latent`, each tick a **trigger event** arrives (a rejection without an Offer in
return, or losing a resource conflict — both reported by the world via `ApplyOutcome` /
`WorldView`):

```
Resentment += resentment.per_trigger × Vindictiveness               (resentment.per_trigger = 0.05)
Affinity[toward trigger agent] -= AffinityDrop × Vindictiveness     (balance.yaml resentment.affinity_drop)
```

- `Vindictiveness` is `ToM[self].EstStats[Vindictiveness].Mean` (D8 — read self-belief; the stat id
  resolved from `stats.Registry`, never a `"Vindictiveness"` literal — D7).
- When `Resentment > resentment.threshold` (`balance.yaml`), apply the `aggression_drift` to
  `ToM[self]` (completes the existing `updateResentment`, which today only thins `Affinity` on a
  copy and never persists). The **Affinity drop must persist** on the actual `Belief` toward the
  trigger agent (the current code updates a returned copy — P3 must route the write through
  `a.ToM.Observe`/an Affinity-update method on `engine/tom`; see Open Questions on the tom write
  path). A `BeliefUpdated`/`Interacted` event records the Affinity drop (data-contracts §4).
- Resentment is **emergent, not a hardcoded grudge type** (D2): it is a scalar drift that feeds
  the normal value gradient (avoidance/aggression), not an "enemy" object.

### C. Apathy ↔ gates coupling

- Each tick the agent injects its **live Mood** into the planner/gate `AgentSnapshot` (new
  `Mood`/`Stamina`/`Adrenaline`/`Urgency` fields on `gates.AgentSnapshot`, `engine/gates/SPEC.md`).
  When `Mood ≤ gates.apathy_mood_threshold` (`-0.60`), the `apathy` gate makes `abstraction:med`+
  actions invisible — so a deeply apathetic agent's planner sees a narrowed action set for free
  (no special-case in the agent; the gate does it from the injected Mood).
- A single plan success folds back through `ApplyOutcome` (B above) and bumps Mood, which — once
  Mood climbs above `-0.60` — re-widens the visible action set on the next tick.

### D. Stamina loop (drain / regen / gate)

```
DRAIN  (in execute, per acting tick):
  effortLevel = tag_levels.effort[<the action's effort:* tag>]   (none=0, low=.20, med=.50, high=.90)
  Stamina -= DrainPerEffort × effortLevel                        (DrainPerEffort = stamina.drain_per_effort)

REGEN  (while executing a recovery action):
  if the action's effect_per_minute names Rest (Rest action):  Stamina += RegenRest  × tickMinutes
  if it is Sleep (stronger Rest supply):                        Stamina += RegenSleep × tickMinutes
  clamp: Stamina ∈ [0, StaminaMax]   (StaminaMax = 1.0)

GATE   (visibility, via engine/gates):
  Stamina < gates.stamina_effort_high_threshold (0.20) → effort:high actions INVISIBLE
  (the agent does not branch on this; it injects Stamina into the snapshot and the `stamina`
   gate hides the action — D4/D10).
```

- Current `drainStamina` hardcodes a `0.5` default effort and a literal effort-level table
  (`parseEffortLevel`). **P3 must resolve effort level from the injected `tag_levels.effort`
  config** (no literals — D10) and default `effort:none` to `0`, not `0.5`.
- Rest/Sleep detection must be **data-driven** (the action carries `effort:none` and an
  `effect_per_minute` on the `Rest` dimension) — resolve via the actions registry, no `"Rest"`/
  `"Sleep"` action-id literal in logic (D7/D10). The two regen rates distinguish Rest vs Sleep by
  the magnitude of their `Rest` `effect_per_minute` (Sleep > Rest), or by an injected mapping.

### E. Adrenaline loop (surge / crash / stamina debt / cost)

```
Urgency = clamp01( max Salience across Dimensions × UrgencyFromDeficit )

SURGE: if Urgency > AdrTriggerUrgency (0.65):
           Adrenaline = min(AdrMax, Adrenaline + AdrSurge)
CRASH: else:
           Adrenaline = max(0, Adrenaline − AdrDecay)
           Stamina   -= AdrDecay × adrenaline.crash_stamina_penalty   (0.50)   ← NEW stamina debt
           Mood      += Lambda × (−AdrDecay)                          (the post-surge dip, existing)

EFFECT (via engine/gates, NOT a branch here):
  Adrenaline ≥ 0.70 → the `adrenaline` gate sets Result.CostMultiplier = 0.50 for
                      effort:high / risk:high / violent:* actions (the planner applies it).
  After the crash, Stamina < 0.20 → effort:high returns to invisible via the `stamina` gate.
```

- The agent **does not reach into the gate registry**; it only writes `Adrenaline`/`Stamina` into
  the snapshot. The cost discount and the post-crash hiding are pure consequences of the injected
  body scalars + the P3 gates (D2: no hardcoded adrenaline meta-system).
- The crash stamina debt (`Stamina -= AdrDecay × crash_stamina_penalty`) is the P3 addition that
  links the two loops: a long panic drains Stamina, so after the adrenaline fades the agent is too
  exhausted to see high-effort actions until it rests.

## Public Interface

```go
package agent

import (
    "github.com/dogring/bdg/engine/core"
    "github.com/dogring/bdg/engine/needs"
    "github.com/dogring/bdg/engine/perception"
    "github.com/dogring/bdg/engine/planner"
    "github.com/dogring/bdg/engine/rng"
    "github.com/dogring/bdg/engine/stats"
    "github.com/dogring/bdg/engine/tom"
    "github.com/dogring/bdg/engine/values"
)

// CopingState — unchanged from P1/P2.
type CopingState uint8
const (
    Idle      CopingState = iota
    Rebinding
    Longing
    Latent
    Apathy
)

// IntentKind / Intent / Signal / SignalKind — unchanged from P1/P2 (see types.go).

// Agent is one simulated villager. P3 adds Resentment + FailStreak to the Body/coping state.
type Agent struct {
    ID  core.AgentID
    Pos core.Vec2

    // Body — dynamic state.
    Inventory       map[core.Tag]int
    Stamina         float64                    // consumable effort budget, [0, StaminaMax]
    Mood            float64                    // signed; += λ·(actual−expected), decays to baseline
    Adrenaline      float64                    // urgency-driven surge, [0, AdrMax]
    NeedIntensities map[core.Dimension]float64

    Resentment float64 // NEW (P3): accrues while Latent on trigger events; drives Aggression/Affinity drift (D2)

    RealStats stats.Stats // god view; OUTCOMES only, never read by decisions (D8)
    ToM       tom.ToM     // includes ToM[self]; decisions read ToM[self] (D8)
    Cfg       Config

    // Deliberation state across ticks.
    Goal    core.Dimension
    Plan    planner.Plan
    PlanIdx int
    Elapsed core.GameMinutes

    Coping     CopingState
    FailStreak int          // NEW (P3): consecutive failed re-plans; resets on any plan/action success
    Latent     []LatentGoal
}

// New — unchanged signature; initializes Resentment = 0, FailStreak = 0.
func New(id core.AgentID, pos core.Vec2, realStats stats.Stats, selfToM tom.ToM, cfg Config) *Agent

// Config — P3 adds the coping + Stamina/Adrenaline-coupling constants. Every field from
// content/balance.yaml; no hardcoded constant (D10).
type Config struct {
    // ── existing P1/P2 fields (mood, adrenaline, stamina, urgency, β, resentment.{affinity_drop,
    //    aggression_drift}, stickiness, budget, gossip) — see agent.go ──

    // stamina effort-level resolution (NEW P3): effort tag → level (tag_levels.effort).
    EffortLevels map[core.Tag]float64 // balance.yaml tag_levels.effort, e.g. {effort:none:0,…,effort:high:.90}

    // adrenaline ↔ stamina coupling (NEW P3).
    CrashStaminaPenalty float64 // balance.yaml adrenaline.crash_stamina_penalty (Stamina -= AdrDecay × this on crash)

    // coping cascade (NEW P3 — replaces the budget-derived rebind threshold).
    RebindMinIntelligence float64 // balance.yaml coping.rebind_min_intelligence (< this → skip Rebinding, go Latent)
    ApathyFailStreak      int     // balance.yaml coping.apathy_fail_streak (Latent → Apathy at this streak)
    ApathyRecoverMood     float64 // balance.yaml coping.apathy_recover_mood (Mood += this on Apathy→Idle)
    ApathyBudgetPenalty   float64 // balance.yaml coping.apathy_budget_penalty (planner budget × (1−this) while Apathy)

    // resentment accrual (NEW P3).
    ResentmentPerTrigger float64 // balance.yaml resentment.per_trigger (Resentment += this × Vindictiveness)
    ResentmentThreshold  float64 // balance.yaml resentment.threshold (above → aggression_drift applies)
}

// DefaultConfig — extends the canonical Config with the P3 fields above.
func DefaultConfig() Config

// Services — unchanged (Sensor, Planner, Values, Needs, Stats, Actions).

// Tick — unchanged signature. P3 changes its internals: phase 6 uses data-driven Stamina drain +
// Rest/Sleep regen; phase 8 adds the crash stamina debt + Resentment accrual; the planner/gate
// snapshot it builds now carries the live Stamina/Mood/Adrenaline/Urgency body scalars.
func (a *Agent) Tick(world WorldView, now core.Tick, rng *rng.RNG, svc Services, emit core.EventEmitter) []Intent

// WorldView — unchanged, but the world must now also report resentment-trigger events. P3 adds:
//   ResentmentTriggers(self core.AgentID) []core.AgentID
// (the agents who, this tick, rejected `self` without an Offer or beat `self` in a resource
//  conflict — the world owns conflict resolution, D12). AgentID-stable order.
type WorldView interface {
    perception.WorldSnapshot
    SoundEvents() []perception.SoundEvent
    KnownObjects(self core.AgentID) []KnownObject
    BeliefOf(self, subject core.AgentID) (tom.Belief, bool)
    HasPendingOffer(receiver core.AgentID) bool
    ResentmentTriggers(self core.AgentID) []core.AgentID // NEW (P3)
}

// ApplyOutcome — unchanged signature. P3 adds: on Status==Succeeded && Completed while
// Coping==Apathy → Coping=Idle, FailStreak=0, Mood += ApathyRecoverMood; FailStreak reset on any
// success; FailStreak increment is handled in the Tick coping path (not here).
func (a *Agent) ApplyOutcome(outcome ActionOutcome, rng *rng.RNG, cfg Config, reg *stats.Registry, emit core.EventEmitter)

// ActionOutcome / OutcomeStatus — unchanged from P1/P2.
```

> `tom.*`, `planner.*`, `perception.*`, `gates.*` types are the contracts those SPECs expose. This
> module composes them; it adds **no** vocabulary outside `docs/glossary.md` (Resentment, FailStreak,
> Stamina, Adrenaline, Mood, Urgency, Vindictiveness are all glossary terms).

## Decision-loop model (authoritative ordering)

Unchanged 8-phase order (perceive → decay → appraise → mediate → plan → execute → signal →
dynamics). P3 touches **phase 5** (coping cascade transitions + budget reduction under Apathy),
**phase 6** (data-driven Stamina drain + Rest/Sleep regen), and **phase 8** (Adrenaline crash
stamina debt + Resentment accrual). The planner/gate snapshot built in phase 5 now carries the
four live body scalars.

### Snapshot body-scalar injection (NEW P3 — the gate coupling)

When building the planner `AgentSnapshot` (phase 5), the agent also populates the body scalars the
P3 gates read — `Stamina`, `Mood`, `Adrenaline`, `Urgency` (the same `Urgency` already computed for
adrenaline). The planner forwards them into `gates.AgentSnapshot` when evaluating each candidate.
The agent computes these from its own Body; it never reads them back from the gate registry.

### Coping cascade (design §3) — completed transitions

```
phase-5 plan fail:
  FailStreak++
  perceived_intel = ToM[self].EstStats[<Intelligence>].Mean         (D8, D7-resolved id)

  if perceived_intel < RebindMinIntelligence:
      enter Latent directly (skip Rebinding + Longing — object-fixation, design §1.4)
  else switch Coping:
      Idle    → Rebinding (substitute the next-Priority goal; success → Idle, fail → Longing)
      Rebinding → Longing  (store the unmet goal as a LatentGoal)
      Longing → Latent     (start Resentment accrual)
      Latent  → if FailStreak ≥ ApathyFailStreak: Apathy   else: stay Latent (accrue Resentment)
      Apathy  → stay (until a different goal becomes plannable, or ApplyOutcome single-success)

phase-5 plan success (any state):
  FailStreak = 0; Coping = Idle
```

- The branch is a **pure function** of (failure cause, perceived Intelligence, FailStreak, prior
  state) — no rng draw (D12).
- While `Coping == Apathy`, phase-5 shapes a **reduced** `planner.Budget` (× `(1 − ApathyBudgetPenalty)`)
  so the apathetic agent deliberates less.

### Stamina / Adrenaline / Mood / Resentment — see "Implement (P3)" §D/§E/§B above

The formulas there are authoritative. Mood and the existing β/forward-sim `expected` term are
unchanged from P1/P2.

## Dependencies

(Unchanged set; P3 adds no new module import.)
- `engine/core` — ids, `Vec2`, `Tick`, `GameMinutes`, `EventEmitter`, `Event`. Emits
  `GoalSelected`/`PlanBuilt`/`ActionStarted`/`ActionDone`/`Interacted`/`BeliefUpdated`/`CopingEntered`.
- `engine/stats` — `*Registry` (capability set for Intelligence; **Vindictiveness** disposition id
  resolved from the registry, never a literal — D7); `Stats`.
- `engine/needs` — `*Registry` (`Kinds(Consumable)`/`IDs()`, forward-roll for decay + Mood expected).
- `engine/values` — appraisal helpers (`ComputeStanding`/`Salience`/`EffValue`/`Priority`).
- `engine/tom` — `ToM` (`Observe` for β + the persisted **Affinity drop** on Resentment; reads).
- `engine/planner` — `*Planner`, `Plan`, `Trace`, `AgentSnapshot` (now carrying the body scalars),
  `Budget` (reduced under Apathy), the sentinel errors. The agent builds the snapshot incl. the
  four body scalars and the reduced budget.
- `engine/perception` — the senses; `WorldView` embeds `WorldSnapshot`.
- `engine/rng` — injected `*RNG` (D12); the coping branch is rng-free.
- `engine/actions` — `*Registry` (effort-tag resolution for Stamina drain; Rest/Sleep detection by
  `effort:none` + `effect_per_minute(Rest)`; all data-driven, no action-id literal).
- **Contract — NOT imported**: `engine/world` implements `WorldView` (dependency inversion).
- **Contract**: `content/balance.yaml` — existing blocks **plus** the new `gates:` thresholds
  (read by the gate registry, not here), `adrenaline.crash_stamina_penalty`, `tag_levels.effort`
  (→ `Config.EffortLevels`), `resentment.{per_trigger,threshold}`, and a new `coping:` block
  (`rebind_min_intelligence`, `apathy_fail_streak`, `apathy_recover_mood`, `apathy_budget_penalty`).
  Injected via `platform/config`. No constant hardcoded (D10).

## Owned Data

- The `Agent` value (Body incl. the new `Resentment`; coping incl. the new `FailStreak`; `Goal`/
  `Plan`/`PlanIdx`/`Elapsed`; `ToM`; `RealStats`). `Tick`/`ApplyOutcome` are the only mutators.
- The `Intent`/`Signal`/`ActionOutcome`/`CopingState`/`LatentGoal`/`Config`/`Services`/`WorldView`/
  `KnownObject` types and the loop/coping/calibration/dynamics logic.
- `RealStats` is held for the world to read at outcome resolution; the decision side never reads it
  (D8).

## Invariants

(All P1/P2 invariants hold unchanged. P3 additions:)

- **Orchestrator only (D5)**: the new coping/Stamina/Adrenaline/Resentment logic sequences and
  threads state; it computes no Standing/Priority and assembles no action sequence of its own.
- **Decisions read `ToM[self]`, never Real Stats (D8)**: the rebind Intelligence threshold reads
  `ToM[self]` Intelligence; Resentment's `Vindictiveness` factor reads `ToM[self]` Vindictiveness;
  the body scalars (Stamina/Mood/Adrenaline/Urgency) injected into the gate snapshot are objective
  Body facts, NOT beliefs — that split is intentional (`engine/gates/SPEC.md` Body-scalar invariant).
  A guard confirms `Tick` never reads `a.RealStats`.
- **Coping branch is deterministic (D12)**: the cascade step is a pure function of (failure cause,
  perceived Intelligence, FailStreak, prior state). No rng draw selects the branch.
- **Resentment / roles are emergent, not hardcoded (D2)**: Resentment is a scalar drift feeding the
  normal value gradient; there is no "grudge"/"enemy" type. Affinity drops persist on the actual
  `Belief` (routed through `engine/tom`), driving avoidance/aggression through ordinary appraisal.
- **All rates injected (D10)**: no literal for the rebind threshold, apathy streak/recover/budget,
  crash stamina penalty, effort levels, or resentment per-trigger/threshold. The current
  `parseEffortLevel` literal table and the `0.5` default effort are **removed** — resolved from
  `Config.EffortLevels`. A grep guard confirms no balance-constant literal and no stat/need/action
  id literal in logic (`Intelligence`/`Vindictiveness`/`Rest`/`Sleep` all registry-resolved).
- **Bounds**: `Stamina ∈ [0, StaminaMax]` (clamped after drain, regen, and crash debt);
  `Adrenaline ∈ [0, AdrMax]`; `Resentment ≥ 0`; `FailStreak ≥ 0`.
- **Determinism (D12)**: `Tick`/`ApplyOutcome` are pure functions of (Agent state, world snapshot,
  rng state, Config, Services); all iteration uses sorted keys/slices; `ResentmentTriggers`/
  `Affinity` writes apply in AgentID-stable order.

## Acceptance Criteria (testable)

(P1/P2 ACs remain. P3 additions — each maps to a unit/golden/scenario:)

- [ ] **Low-Intelligence skips Rebinding to Latent (scenario F, golden)**: an agent with perceived
  `Intelligence = 0.3` (< `RebindMinIntelligence = 0.4`) on a plan fail emits **one**
  `CopingEntered(Latent)` with **no** `CopingEntered(Rebinding)` and no `Longing` — it goes straight
  to Latent. A perceived-`Intelligence = 0.7` agent emits `CopingEntered(Rebinding)` first and (on
  repeated fail) `Longing → Latent`. Golden over the event sequence.
- [ ] **Cascade order + FailStreak (design §3)**: a contrived `ErrUnreachable` sequence drives a
  high-Intelligence agent `Idle → Rebinding → Longing → Latent → Apathy`; `FailStreak` increments
  per fail and resets to 0 on a success; `Apathy` is entered only at `FailStreak ≥ ApathyFailStreak`.
  Table-driven; the branch is rng-free.
- [ ] **Latent Resentment trigger drops Affinity (golden)**: an agent in `Latent` that receives a
  `ResentmentTriggers == [B]` event accrues `Resentment += per_trigger × Vindictiveness` and the
  persisted `Affinity` toward `B` drops by `AffinityDrop × Vindictiveness`; an empty trigger list
  causes no drift; a non-Latent agent does not accrue. A `BeliefUpdated`/`Interacted` event records
  the Affinity drop. Golden over a fixed sequence (task AC: "Latent agent + trigger → Affinity drop
  event").
- [ ] **Resentment threshold drives Aggression drift**: once `Resentment > ResentmentThreshold`,
  the `aggression_drift` is folded into `ToM[self]` Aggression (table-driven over the boundary).
- [ ] **Apathy single-success recovery (golden)**: an `Apathy` agent receiving
  `ApplyOutcome{Status: Succeeded, Completed: true}` resets `Coping = Idle`, `FailStreak = 0`,
  `Mood += ApathyRecoverMood`, and emits `CopingEntered(Idle)`. Golden (task AC: "Apathy +
  ActionDone(succeeded) → CopingEntered(Idle)").
- [ ] **Apathy narrows cognition via the gate**: an `Apathy` agent whose `Mood ≤ -0.60` builds a
  snapshot with `Mood ≤ -0.60`; the planner (calling the `apathy` gate) does not select an
  `abstraction:med`+ action (verified via the planner stub / a scenario that has only an abstract
  producer available). Once Mood recovers above `-0.60`, the abstract action is visible again.
- [ ] **Stamina drain is data-driven (no literal)**: executing an `effort:high` action drains
  `DrainPerEffort × tag_levels.effort[effort:high]` per tick; `effort:none` drains 0 (not the old
  `0.5` default). Table-driven over the effort levels from `Config.EffortLevels`.
- [ ] **Rest/Sleep regen (data-driven)**: executing `Rest` adds `RegenRest × tickMinutes`;
  executing `Sleep` adds `RegenSleep × tickMinutes`; Stamina clamps at `StaminaMax`. The Rest/Sleep
  actions are detected from `effort:none` + `effect_per_minute(Rest)`, not by id literal.
- [ ] **Stamina gate hides effort:high (scenario, golden)**: an agent with `Stamina = 0.10` builds
  a snapshot with `Stamina = 0.10`; the planner's `PlanBuilt` contains **no** `effort:high` action
  (the `stamina` gate hides it). At `Stamina = 0.30` the high-effort action can appear. Golden over
  the snapshot (task AC: "Stamina 0.10 agent: no effort:high in PlanBuilt").
- [ ] **Adrenaline surge then crash with stamina debt**: `Urgency > AdrTriggerUrgency (0.65)` raises
  `Adrenaline` by `AdrSurge` (clamped `AdrMax`); once urgency falls, `Adrenaline` drains by `AdrDecay`
  per tick AND `Stamina` drops by `AdrDecay × CrashStaminaPenalty` (the crash debt); after ~3 crash
  ticks `Stamina < previous value`. Table/golden (task AC: "Urgency>0.65 → surge → 3 ticks → crash →
  Stamina < prev").
- [ ] **Adrenaline cost discount reaches the planner (scenario)**: with `Adrenaline = 0.75` injected,
  the `adrenaline` gate returns `CostMultiplier = 0.50` for an `effort:high`/`violent:*` candidate,
  so the planner's cost for that action halves (verified at the planner via the passed snapshot;
  the agent only injects `Adrenaline`).
- [ ] **Conscience urgency relief end-to-end (scenario B, golden)**: a starving agent whose
  `Urgency > conscience_urgency_threshold (0.70)` injects `Urgency` into the snapshot; the
  `conscience` gate's relief branch makes `Take` (`norm:transgressive`) visible, and `PlanBuilt`
  steps include `Take`. At low urgency `Take` is absent. Golden (task AC: scenario B).
- [ ] **Scenario D design intent recorded (NOT a P3 golden)**: a SPEC/test comment marks that the
  "others' wellbeing lowers the conscience threshold" branch (Social/Standing rise for a cared-for
  Other) is reserved (see Open Questions). P3 asserts only the urgency path; no D golden.
- [ ] **No constant / id hardcoded (D10/D7 guard)**: grep guard — no rebind/apathy/crash/effort/
  resentment literal and no `Intelligence`/`Vindictiveness`/`Rest`/`Sleep` string literal in logic.
- [ ] **Determinism: identical tick reproduces (D12)**: same (Agent state, `WorldView` snapshot,
  rng seed, Config, Services) twice → byte-identical `[]Intent` + identical receiver mutations
  (incl. `Resentment`/`FailStreak`/`Stamina`/`Adrenaline`). Golden digest.

> Structural JSON-schema validation of the `content/balance.yaml` blocks this module reads is a
> **platform/config** AC.

## Out of Scope

(Unchanged from P1/P2.) Additionally for P3:
- **The four P3 gate predicates themselves** (the `stamina`/`apathy`/`conscience`-relief/
  `adrenaline` `expr` trees + the `CostMultiplier` emission) → `engine/gates` (`backend/engine/gates/SPEC.md`).
  This module only *feeds* the body scalars and *consumes* the resulting visibility/cost via the
  planner. It does NOT branch on Stamina/Mood/Adrenaline for visibility itself.
- **Applying the `CostMultiplier` to the tag-cost sum** → `engine/planner` (it reads
  `gates.Result.CostMultiplier`). This module does not compute cost.
- **Resource-conflict resolution and detecting a "rejection without Offer"** → `engine/world`
  (it reports these via `WorldView.ResentmentTriggers`). This module only *reacts* to the reported
  triggers.
- **The persisted-Affinity write mechanism on `Belief`** → `engine/tom` owns the `Belief` storage +
  the Affinity-update method; this module calls it (see Open Questions on the write path).

## Open Questions

- **`engine/tom` Affinity write path (BLOCKS the Resentment golden — flag to architect).** The
  current `updateResentment` mutates a *copy* returned by `a.ToM.Self(...)` / `BeliefOf`, so the
  Affinity drop does not persist. P3 needs a `engine/tom` method to **persist** an Affinity delta on
  the belief toward a subject (e.g. `ToM.AdjustAffinity(subject, delta)` or fold it through
  `Observe`). If `engine/tom` exposes no such writer, the Resentment AC cannot pass. Confirm the tom
  contract (the planner SPEC notes `engine/tom` is otherwise unchanged) — adding an Affinity writer
  is a tom-side change. Escalate before implementing the Resentment golden.
- **`coping:` block in `balance.yaml` (NOT blocking; resolves the prior Rebind OQ).** P1/P2 derived
  the rebind threshold from the budget curve and flagged adding a `coping:` block. P3 adopts the
  explicit block (`rebind_min_intelligence`, `apathy_fail_streak`, `apathy_recover_mood`,
  `apathy_budget_penalty`). Confirm the schema addition (`content/schema/balance.schema.json`) and
  the initial values (rebind 0.4 per the task brief) before tuning.
- **`tag_levels.effort` → `Config.EffortLevels` plumbing (NOT blocking).** Stamina drain must read
  the effort levels from `balance.yaml tag_levels.effort` rather than the hardcoded
  `parseEffortLevel` table. Confirm `platform/config` projects `tag_levels.effort` into the agent
  `Config` (it already loads `tag_levels` for the planner cost path). Removing the literal table is
  required for the D10 guard to pass.
- **Scenario D conscience relief (NOT blocking P3; mirrors `engine/gates` OQ).** The "others'
  wellbeing lowers the conscience threshold" branch is reserved — it needs a per-referent wellbeing
  term the boolean gate grammar lacks today. P3 ships the urgency relief only. Coordinate with the
  gates author so D lands in ONE place (gate input vs planner value-gradient), not two. Do not
  hardcode a "love"/"wellbeing" meta-system (D2).
- **Double-relaxation of conscience (overlap with the planner, NOT blocking).** The planner already
  has a blanket `Urgency > urgency_threshold` relaxation; the P3 `conscience` gate now also relaxes
  on urgency. Pick ONE owner so theft is not unlocked twice (mirrored in `engine/gates/SPEC.md`).

## Notes

- **Why the body scalars are injected, not gate-side state.** Gates are stateless and immutable
  after `Load` (`engine/gates/SPEC.md`); the live Stamina/Mood/Adrenaline/Urgency are this module's
  Body state. The agent writes them into the per-tick snapshot so the gate can read fresh values
  without the gate registry holding mutable per-agent state — keeping the gate a pure function and
  preserving D12.
- **`CopingEntered` event mode strings (data-contracts §4).** Emit `mode` as the canonical string
  `rebind|longing|latent|apathy` (plus `idle` on recovery), not the `int(state)` the current
  `emitCopingEntered` uses — align the payload with data-contracts §4 (`mode(rebind|longing|latent|
  apathy)`). Fix the existing helper as part of P3.
- **Stamina/Adrenaline coupling is the drama link.** The crash stamina debt (§E) ties a long panic
  to subsequent exhaustion: surge cheapens hard actions (cost discount), but the crash drains
  Stamina, so once Adrenaline fades the `stamina` gate hides high-effort actions until the agent
  rests — a self-limiting loop, all from injected rates (D10) and the P3 gates (D2: no hardcoded
  meta-system).
- **Resentment feeds the gradient, not a new system (D2).** `Resentment`/`Affinity↓`/`Aggression
  drift` change the agent's ordinary value appraisal toward avoidance/aggression of the trigger
  agent; there is no separate enemy/grudge subsystem. Reliance edges (`RelyOn`) still crystallize
  into roles in `engine/world`, not here.
- **Intelligence / Vindictiveness id resolution (D7).** Both are resolved once from
  `svc.Stats` (capability set / disposition set per the glossary ids), never a string literal in
  logic — matching the planner's Intelligence resolution.
