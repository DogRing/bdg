# SPEC — `engine/planner`

> Status: `P5`
> Leaf level: `L4`  ·  Owner agent: `<filled by implementer>`
>
> (P6 Blocker Group A — Blocker 3: documents that `owned_by_other` is a **world-state fact**
> injected into `AgentSnapshot.SatisfiedFacts` by the execution layer, NOT an action producer
> [so `Take` becomes reachable], and that `Attack` must produce a **goal-satisfying** predicate
> [`has_Safety`] so a Safety goal can reach it. See "World-state facts" and the new ACs.)

## Purpose

Given an agent's current state and a Priority-ordered set of need/value Dimensions (computed by
`engine/values`), the planner assembles the shortest viable **ordered sequence of atomic actions**
(`[]core.ActionID`) that pursues the highest-Priority goal. It is the **how-to-get-it** module
(D5): it never decides *what* is wanted (that is `values`/`needs`). The plan is assembled by
three composed mechanisms — **GOAP backward chaining**, **HTN forward decomposition**, and
**forward-sim provisioning** (D9) — bounded by a deliberation `Budget`, with action cost derived
from action **Tags** (D4) and a dynamic gate-relaxation step driven by `Urgency`. Plans are
emergent sequences only; this module defines **no** `Method`/`Task`/`Subtask` type (D3).

## Public Interface

```go
package planner

import (
    "github.com/dogring/bdg/backend/engine/actions"
    "github.com/dogring/bdg/backend/engine/core"
    "github.com/dogring/bdg/backend/engine/gates"
    "github.com/dogring/bdg/backend/engine/needs"
    "github.com/dogring/bdg/backend/engine/rng"
    "github.com/dogring/bdg/backend/engine/stats"
    "github.com/dogring/bdg/backend/engine/tom"
    "github.com/dogring/bdg/backend/engine/values"
)

// ── Search bounds ──────────────────────────────────────────────────────────────

// Budget caps deliberation cost so a search always terminates (no infinite chaining).
// All three caps are read-only after construction; injected from content/balance.yaml
// planner.budget.* (D10). A breach of any cap aborts the current Plan call with
// ErrBudgetExceeded — it is NOT silently truncated (so a why-trace can record the abort).
type Budget struct {
    MaxDepth   int // max HTN recursion depth (prevents infinite precondition loops)
    MaxActions int // max plan length (actions in the assembled sequence)
    MaxNodes   int // max GOAP nodes expanded per Plan call
}

// ── Configuration (injected from content/balance.yaml planner.*) ────────────────

// PlannerConfig bundles every tunable the planner reads. All fields are injected by the
// caller (read from content/balance.yaml's `planner:` block via platform/config); the planner
// hardcodes NO numeric constant (D10). Immutable for the planner's lifetime.
type PlannerConfig struct {
    Budget           Budget               // search caps (planner.budget.*)
    BaseHorizonTicks int                  // forward-sim base lookahead (planner.base_horizon_ticks)
    TagCosts         map[core.Tag]float64 // per-Tag cost weight (planner.tag_costs.<Tag>)
    UrgencyThreshold float64              // gate-relaxation trigger (planner.urgency_threshold)

    LookaheadThreshold float64 // balance.yaml intelligence.lookahead_threshold — below this,
                               // forward-sim is completely skipped (no provisioning subgoals).
                               // Default 0.4.
}

// ── Inputs (read-only per Plan call) ────────────────────────────────────────────

// AgentSnapshot is the READ-ONLY view of the deliberating agent. The planner MUST NOT mutate it
// and MUST NOT retain a reference past the Plan call. Stat-reading decisions (gates, horizon)
// read SelfModel (ToM[self]) — never Real Stats (D8).
type AgentSnapshot struct {
    ID              core.ObjectID            // the agent's id (used for deterministic apply order, D12)
    Pos             core.Vec2                // current position (for distance-aware cost / MoveTo)
    CurrentStats    core.Stats               // the agent's stat vector (for reference; decisions use SelfModel, D8)
    SelfModel       tom.Belief               // ToM[self]: the self-belief (EstStats means) used for gate eval + horizon (D8)
    NeedIntensities map[core.Dimension]float64 // current GROWN intensity per dimension (higher = worse), incl. driven Safety
    Known           map[core.ObjectID]struct{} // objects this agent knows of (planner known-check)
    Urgency         float64                  // max Priority across referents / max_priority (drives relaxation)

    // SatisfiedFacts are the WORLD-STATE predicates that already hold for this agent THIS TICK,
    // injected by the caller (engine/agent's replan). They seed the GOAP/HTN precondition check:
    // a predicate in SatisfiedFacts needs NO producer action prepended (it is already true). These
    // are facts about the world the agent is standing in — NOT facts an action produced — e.g.
    // `near_other` (an agent is within interaction radius), `tradeOffered` (a pending Offer exists),
    // and `owned_by_other` (a nearby agent holds items the agent could Take). See "World-state
    // facts" below. The slice is caller-owned and never mutated/retained by the planner.
    SatisfiedFacts []core.Pred
}

// DimensionPriority is one row of the Priority-ordered goal list produced by engine/values.
// The caller passes these sorted by Priority DESCENDING; ties are broken by Dim lexicographically
// BEFORE the planner sees them (the planner re-sorts defensively to guarantee D12 order).
type DimensionPriority struct {
    Dim      core.Dimension
    Priority values.Priority
    Salience values.Salience
}

// ── Outputs ──────────────────────────────────────────────────────────────────────

// Plan is the assembled ordered action sequence and its validity window.
type Plan struct {
    Actions []core.ActionID // ordered: prerequisites first, goal action last (HTN order)
    Horizon int             // ticks this plan is forward-valid for (the Intelligence-gated lookahead)
}

// Trace is the why-trace breakdown for one Plan call (data-contracts §4 PlanBuilt / GoalSelected).
type Trace struct {
    GoalDim      core.Dimension
    Candidates   []Candidate     // competing producers considered for the root goal, in ActionID order
    Provisioned  []core.Dimension // dimensions a provisioning subgoal was inserted for (D9)
    Relaxed      bool            // whether Urgency relaxation unlocked a gated action this call
    TotalCost    float64         // Σ tag-derived cost of the chosen Actions
    NodesExpanded int            // GOAP nodes expanded (≤ Budget.MaxNodes)
}

// Candidate is one considered producer action and its evaluation, for the why-trace.
type Candidate struct {
    Action  core.ActionID
    Cost    float64     // tag-derived cost (Σ TagCosts over its tags)
    Visible bool        // gate verdict (after any relaxation)
    Chosen  bool        // selected into the plan
}

// ── Planner ──────────────────────────────────────────────────────────────────────

// Planner is the opaque, immutable-after-New deliberation engine. It holds borrowed read-only
// references to the registries and a copy of the config. Safe to share across goroutines during
// the read/plan phase (each Plan call is a pure function of its arguments + the registries).
type Planner struct { /* opaque: borrowed *Registry refs + PlannerConfig + sorted tag-cost keys */ }

// New constructs a Planner from the already-loaded registries and an injected config. The
// registries are borrowed read-only (never mutated). cfg is copied; TagCosts is snapshotted into
// a sorted-key form for deterministic iteration (D12).
func New(
    actions *actions.Registry,
    gates   *gates.Registry,
    needs   *needs.Registry,
    stats   *stats.Registry,
    cfg     PlannerConfig,
) *Planner

// Plan assembles the action sequence pursuing the highest-Priority dimension in `values`.
// It is a PURE function of (agent, values, the borrowed registries, rng): identical inputs +
// identical rng seed/state → identical Plan and Trace (D12). `rng` is used ONLY for tie-breaking
// AFTER deterministic (Priority → cost → ActionID) comparisons, and only when an explicit
// random tie-break is configured (default: lexicographic ActionID, no rng draw — see Notes).
// Returns ErrBudgetExceeded if any Budget cap is reached; ErrNoGoal if `values` is empty;
// ErrUnreachable if the highest-Priority goal has no visible producer within budget.
func (p *Planner) Plan(
    agent  AgentSnapshot,
    values []DimensionPriority,
    rng    *rng.RNG,
) (Plan, Trace, error)

// Errors returned by Plan (sentinel values, compared with errors.Is).
var (
    ErrBudgetExceeded error // a Budget cap (MaxDepth | MaxActions | MaxNodes) was reached
    ErrNoGoal         error // values is empty — nothing to pursue
    ErrUnreachable    error // the goal has no visible producer reachable within budget
)
```

> `tom.Belief` is the self/other belief model the `engine/tom` SPEC exposes (its `Belief` type,
> `= ToM[X]`, with `EstStats map[core.StatID]tom.StatDist`). The planner reads ONLY the
> per-stat estimate means from `SelfModel` (the agent's self-belief, `ToM[self]`) for gate
> evaluation and the horizon calc; it never mutates it.

## Dependencies

- `engine/core` — `ActionID`, `Tag`, `Pred`, `Dimension`, `Vec2`, `ObjectID`, `Stats`, `Tick`.
- `engine/stats` — `*Registry` (stat `[Min,Max]` for the Intelligence horizon; `IDs()` order),
  `Stats`.
- `engine/gates` — `*Registry`, `Evaluate(Action, AgentSnapshot) Result`, `gates.AgentSnapshot`,
  `gates.Action`.
- `engine/needs` — `*Registry`, `Def`, and the pure forward-roll helpers `Def.Demand`,
  `Def.BreachAt`, `Def.Level` (D9 forward-sim provisioning math lives in `needs`).
- `engine/actions` — `*Registry`, `ActionDef`, `Producers(pred) []ActionID` (GOAP reverse index),
  `Effect`.
- `engine/values` — `Priority`, `Salience` (the scalar types carried in `DimensionPriority`).
- `engine/tom` — `Belief` (the `SelfModel` shape = `ToM[self]`). No `engine/tom` change required.
- `engine/rng` — `*RNG` (injected, D12) for configured tie-breaking only.
- **Contract**: `content/balance.yaml` `planner:` block (`budget.*`, `base_horizon_ticks`,
  `tag_costs.*`, `urgency_threshold`) plus the `intelligence.lookahead_threshold` scalar (P5)
  supplies `PlannerConfig`. **`content/actions.yaml`** supplies the action catalog incl. the
  `requires`/`produces` predicate wiring this SPEC's Blocker-3 notes reference (`Take` requires
  `owned_by_other`; `Attack` produces `has_Safety` — see "World-state facts" + "Transgressive
  reachability"). The planner hardcodes no constant (D10).

## Owned Data

- The `Planner`, `Budget`, `PlannerConfig`, `Plan`, `Trace`, `Candidate`, `AgentSnapshot`,
  `DimensionPriority` value types and all search logic.
- The `Planner` holds **borrowed** read-only registry pointers and a **copied** config (with a
  precomputed sorted `[]core.Tag` of the `TagCosts` keys for D12 iteration). `AgentSnapshot`
  (incl. `SatisfiedFacts`), `values`, and `rng` are caller-owned; `Plan` never retains them.

## Planning model

The planner combines three mechanisms in one `Plan` call. The order is: select the goal dimension
→ (provisioning) forward-sim each consumable dimension and prepend provisioning subgoals →
(GOAP) find producers for the goal → (HTN) decompose unmet preconditions of the chosen producer.

### 1. GOAP backward chaining

- Start from the **highest-Priority** `Dim` in `values`.
- Map the goal dimension to its satisfaction predicate(s) and use
  `actions.Registry.Producers(pred) []core.ActionID` to enumerate candidate producer actions, **in
  `IDs()` order**. The dimension→predicate mapping is the convention `has_<Dimension>` (e.g.
  `Satiety` → `has_Satiety`, `Safety` → `has_Safety`); a producer of that predicate satisfies the
  goal.
- For each candidate, evaluate its gate visibility via `gates.Registry.Evaluate(act, snap)`. A
  candidate with `Result.Visible == false` is rejected (unless relaxed — see § "Dynamic
  visibility").
- Among visible candidates, select the **shallowest** sequence; ties at equal depth break by
  **lower tag-derived cost**, then by **lexicographic `ActionID`** (D12).

### 2. HTN forward decomposition

- After choosing the goal action, check its preconditions: `ActionDef.Requires` (ALL must hold)
  and `ActionDef.RequiresAny` (ANY satisfies). A precondition predicate **holds** if it is in
  `AgentSnapshot.SatisfiedFacts` (a world-state fact, § "World-state facts") OR a producer for it
  is found and recursively decomposed; an unmet predicate with no producer makes the candidate
  unreachable.
- For each unmet predicate, find a producer (`Producers(pred)`) and recurse. Prerequisite actions
  are **prepended** before the goal action, yielding `[…prereqs…, goalAction]`.
- Decomposition is an **emergent ordered sequence**, NOT a hand-defined task network (D3).
- Recursion is bounded by `Budget.MaxDepth`; expanded GOAP nodes by `Budget.MaxNodes`; final plan
  length by `Budget.MaxActions`.

### World-state facts (`SatisfiedFacts`, incl. `owned_by_other`) — Blocker 3

Some preconditions are not produced by any action — they are **facts about the world the agent is
currently standing in**. The caller (`engine/agent`'s `replan`) injects these into
`AgentSnapshot.SatisfiedFacts` each tick by reading the world snapshot; the planner then treats any
predicate in that slice as already satisfied (no producer needed). The injected world-state facts
are:

| Predicate        | Injected when … (world condition the caller checks)                                  |
|------------------|--------------------------------------------------------------------------------------|
| `near_other`     | an entity tagged `agent` is within `interactionRadius` of the agent.                 |
| `tradeOffered`   | `world.HasPendingOffer(self)` is true.                                               |
| `owned_by_other` | a nearby agent (within `interactionRadius`) has a **non-empty Inventory** (it holds items the agent could Take). |

- **`owned_by_other` is a world-state fact, NOT an action producer.** `content/actions.yaml`'s
  `Take` requires `[at_target, owned_by_other]`, and **no action anywhere `produces:
  owned_by_other`** — so without this injection the predicate can never enter the fact set and
  `Take` is permanently unreachable (the diagnosed blocker). The fix is in the **execution layer**
  (`engine/agent` `replan`, the same place `near_other`/`tradeOffered` are already injected from the
  world snapshot): when a nearby agent's Inventory is non-empty, append `owned_by_other` to
  `SatisfiedFacts`. The planner change is purely that `SatisfiedFacts` short-circuits the
  precondition check (which it already must do for `near_other`/`tradeOffered`).
- This keeps D2/D3 intact: `owned_by_other` is not a hardcoded "theft" system — it is an ordinary
  precondition fact derived from world state, and `Take` is an ordinary atomic action the conscience
  gate hides until Urgency relaxes it (§ "Dynamic visibility").
- **`at_target`** for `Take` is reached the normal way (a `MoveTo` producer prepended by HTN), same
  as `Forage`'s `at_target` — it is NOT a world-state fact.

### Transgressive reachability — `Attack` produces `has_Safety` (Blocker 3, design choice)

`content/actions.yaml`'s `Attack` previously produced only `struck`, which satisfies **no** goal
predicate — so no Priority-ordered goal could ever resolve to `Attack` and the planner had no reason
to plan it (the diagnosed blocker). **Preferred design choice: option (b) — `Attack` produces
`has_Safety` (active deterrence).** Attack's `produces` list becomes `[struck, has_Safety]`: striking
a perceived threat is a way to satisfy the Safety dimension (remove the threat), so a Safety goal
(raised by the §F threat-perception drive in `engine/agent`) can resolve to `Attack` as one
high-cost, gated, `norm:transgressive` producer — competing with `Patrol`/`TakeShelter`/`Sleep`
elsewhere. The conscience + risk gates keep it hidden until Urgency is high enough to relax them
(scenario B mechanism), so a calm agent never attacks; only a cornered, high-Safety-urgency agent
sees `Attack` become visible.

- **Why (b) over (a)?** Option (a) (rename `struck` to a new `intimidated` predicate and author an
  `intimidated`→Safety producer) adds a second predicate and a content indirection for no behavioral
  gain; (b) reuses the existing `has_<Dimension>` convention the GOAP mapping already understands
  (`Patrol` already produces `has_Safety`), so `Attack` slots into the Safety producer set with a
  one-line `content/actions.yaml` edit and no new predicate vocabulary (glossary-clean). `struck`
  is retained as the world-facing combat-resolution marker (the world reads it to apply damage).
- **This is a `content/actions.yaml` edit**, not engine code: add `has_Safety` to `Attack.produces`.
  The planner needs no change — it already enumerates `Producers(has_Safety)`; once `Attack` is in
  that index it is considered (and gated) like any other Safety producer. (D2/D4: no hardcoded
  combat system; visibility/cost flow from `Attack`'s tags `violent:high`, `risk:high`,
  `norm:transgressive`, `effort:high`.)

### 3. Forward-sim provisioning (D9)

(Unchanged — per consumable dimension `d`, forward-sim whether `d` breaches within the horizon and
prepend a provisioning subgoal if `predicted_deficit > 0`; quantity = `need-rate × predicted-time`
via the `engine/needs` helpers; iterate `needs.IDs()` order, D12. When `horizon_ticks == 0` (the
hard skip below `LookaheadThreshold`) the provisioning loop is not entered.)

### Intelligence-gated lookahead (forward-sim horizon)

(Unchanged — `perceived_intelligence = SelfModel mean for Intelligence`; below
`LookaheadThreshold` → `horizon_ticks = 0` HARD SKIP; else `max(1, floor(base × pi/max))`. The
threshold and the Intelligence `StatID` are injected/registry-resolved, no literal — D7/D10.)

### Tag-derived cost (absorbed from `engine/gates` schema_version 2)

(Unchanged — `cost(action) = Σ TagCosts[tag]` over sorted tag keys, D12; cost is the tie-breaker
among equal-Priority, equal-depth candidates; cost never overrides Priority, D5.)

### Dynamic visibility / conscience loosening (absorbed from `engine/gates`)

(Unchanged — `relaxed = (Urgency > UrgencyThreshold)`; a gated candidate is treated visible for the
call only when relaxed; recorded in `Trace.Relaxed`/`Candidate.Visible`. This is what makes `Take`
and the transgressive `Attack` visible under high Safety urgency — scenario B / the §F threat
reflex.)

## Invariants

- **Atomic-only plans, no task trees (D3)**: no `Method`/`Task`/`Subtask` type; a `Plan` is a flat
  `[]core.ActionID`; decomposition is emergent from the `Producers` reverse index + `SatisfiedFacts`.
- **What-vs-how separation (D5)**: the planner never computes `Standing`/`Salience`/`Priority` and
  never edits a need/value definition. A higher Priority is never overridden by lower cost.
- **Provisioning is derived, never authored (D9)**: provisioning quantity = `need-rate ×
  predicted-time` via `needs.Def`; no "future need" field anywhere.
- **Cost is tag-derived (D4)**: action cost = `Σ TagCosts[tag]`; no per-action cost number.
- **World-state facts are inputs, not products (D2/D3)**: `owned_by_other`/`near_other`/
  `tradeOffered` are read from `AgentSnapshot.SatisfiedFacts` (caller-injected from the world
  snapshot); the planner never invents them and no action `produces` them. They short-circuit the
  precondition check only — they do not add actions to the plan.
- **Decisions read `ToM[self]` (D8)**: gate evaluation + horizon read `SelfModel` means, never Real
  Stats. A low perceived Intelligence yields a short horizon / hard skip, preserved (self-sealing).
- **Determinism (D12)**: `Plan` is pure over `(AgentSnapshot, values, registries, rng)`; all
  iteration uses sorted keys/slices, incl. iterating `SatisfiedFacts` membership via a set/sorted
  check (never relying on slice order for logic). Same inputs → byte-identical `Plan` + `Trace`.
- **Budget always terminates**; **read-only inputs** (incl. `SatisfiedFacts` never mutated);
  **no hardcoded ids (D7/D10)**; **no IO**.

## Acceptance Criteria (testable)

- [ ] **GOAP backward chain selects a producer (D-GOAP)**: given a `Satiety` goal and a stub
  `Producers` index returning `[Eat, Forage]`, the planner selects `Eat` when its gate passes; when
  `Eat`'s gate fails it selects `Forage`. Table-driven.
- [ ] **HTN decomposition prepends prerequisites (golden snapshot)**: if `Eat` requires `has_food`
  and `has_food` is false, the planner prepends a producer of `has_food` so `Plan.Actions ==
  [Forage, Eat]`.
- [ ] **`SatisfiedFacts` short-circuits a precondition (Blocker 3)**: a candidate requiring
  `near_other` is reachable with NO prepended producer when `AgentSnapshot.SatisfiedFacts` contains
  `near_other`, and unreachable (or requiring a producer that does not exist → `ErrUnreachable`)
  when it does not. Table-driven over `{present, absent}`.
- [ ] **`owned_by_other` injection makes `Take` reachable (Blocker 3, scenario)**: with the shipped
  `content/actions.yaml`, a `Take` candidate (requires `[at_target, owned_by_other]`) is
  **unreachable** when `SatisfiedFacts` lacks `owned_by_other` (no action produces it →
  `ErrUnreachable` for a goal whose only producer is `Take`); when `SatisfiedFacts` includes
  `owned_by_other` (and `at_target` is reached via a `MoveTo` producer), `Take` appears in the
  planner `Candidate`s and, under relaxation, is `Chosen`. Asserts the diagnosed dead predicate is
  now live ONLY via the world-state-fact channel.
- [ ] **Execution-layer injects `owned_by_other` near a holder (Blocker 3, `engine/agent`)**: a
  `replan` AC (in `engine/agent`) — given an agent positioned within `interactionRadius` of another
  agent whose `Inventory` is non-empty, the built `AgentSnapshot.SatisfiedFacts` includes
  `owned_by_other`; when the nearby agent's `Inventory` is empty (or no agent is near), it does NOT.
  (Implemented in `engine/agent`; cross-referenced here because the planner contract relies on it.)
- [ ] **`Attack` is a Safety producer (Blocker 3, content + planner)**: with `Attack.produces`
  including `has_Safety` (the content edit), `actions.Registry.Producers("has_Safety")` includes
  `Attack`; a Safety goal therefore lists `Attack` among its `Candidate`s. `Attack` is `Visible ==
  false` (gated by `norm:transgressive`/`risk:high`) at low Urgency and becomes `Visible == true`
  (and selectable) only when `Urgency > UrgencyThreshold` (relaxation). A Safety goal with `struck`
  alone (the old content) would have **no** Safety producer from `Attack` — regression-guard the
  predicate set.
- [ ] **Forward-sim inserts a provisioning subgoal (D9, scenario H)**; **Intelligence horizon**
  table (floor at threshold = 1, max → base, max/2 → floor(base/2)); **Lookahead hard-skip below
  threshold** (`Plan.Horizon == 0`, `Trace.Provisioned` empty). (Unchanged.)
- [ ] **`Budget.MaxDepth/MaxNodes/MaxActions` aborts (no infinite loop)** → `ErrBudgetExceeded`.
- [ ] **Tag-derived cost breaks an equal-Priority tie (D4)**; **Dynamic visibility relaxation
  (scenario B)**; **Cost never overrides Priority (D5)**. (Unchanged.)
- [ ] **No task-tree / no per-action cost type (D3/D4)**; **Reads `ToM[self]`, not Real Stats
  (D8)**; **Determinism: 1000 identical calls (D12)**; **No constant hardcoded (D10)**;
  **`ErrNoGoal`/`ErrUnreachable`**. (Unchanged.)

> Structural JSON-schema validation of the `content/balance.yaml planner:` block, the
> `intelligence.lookahead_threshold` scalar, and the **cross-check that `owned_by_other` has no
> action producer (it is a world-state fact)** and that `has_Safety` appears in `Attack.produces`
> are **platform/config** ACs (it owns the file IO + the schema). This module proves only behaviour
> reachable from the injected `PlannerConfig` and the `SatisfiedFacts` it is handed.

## Out of Scope

- Reading the file from disk and JSON-schema validation → `platform/config`.
- **Computing `Standing`/`Salience`/`Priority`/`EffValue`** → `engine/values`.
- **Per-need decay, level roll, `Demand`/`BreachAt`, the conditional Safety driver** → `engine/needs`.
- **Gate predicate evaluation** → `engine/gates`; the planner only calls `Evaluate` + Urgency relax.
- **The action catalog, tags, `Producers` reverse index, `Effect`** → `engine/actions`. The
  `Attack.produces += has_Safety` edit is a `content/actions.yaml` change (D10), not planner code.
- **Constructing `AgentSnapshot.SatisfiedFacts`** (reading the world snapshot for `near_other`/
  `tradeOffered`/`owned_by_other`) → `engine/agent`'s `replan` (execution layer). The planner only
  consumes the slice it is given; it never queries the world.
- **`ToM[self]` construction, β self-calibration, gossip** → `engine/tom`.
- **Executing the plan, durative progress, interruption, coping, emitting events** → `engine/agent`.
- **Outcome resolution** (does `Attack`/`Hunt` succeed, scaled by Real Stats; what `struck` does to
  the target), intent collection, and conflict resolution → `engine/world`.
- **The starvation-mid-journey end-to-end (scenario H full)** → `engine/world`/`engine/agent`.

## Open Questions

- **RNG type: `*rng.RNG` (RESOLVED).** Uses `engine/rng.*RNG`, not stdlib rand.
- **`SelfModel` type — RESOLVED: planner uses `tom.Belief` directly.**
- **Goal-satisfaction predicate mapping (RESOLVED for the `has_<Dimension>` convention).** GOAP
  maps a goal `Dimension` to the predicate `has_<Dimension>` and enumerates `Producers(has_<Dim>)`.
  The Blocker-3 `Attack.produces += has_Safety` edit relies on this convention (it makes `Attack` a
  `has_Safety` producer). If a content-authored `needs.yaml satisfied_by:` map is later preferred
  over the convention, update this SPEC + the schema first; the convention is the default.
- **`SatisfiedFacts` ownership (RESOLVED for Blocker 3).** World-state facts (`near_other`,
  `tradeOffered`, `owned_by_other`) are injected by `engine/agent`'s `replan` from the world
  snapshot — the planner never reads the world. `owned_by_other` joins the existing two; the only
  planner-side requirement is that `SatisfiedFacts` membership short-circuits the precondition check
  (already required for the existing two). Confirm with the agent SPEC author that `replan` adds the
  Inventory check (it already builds `near_other`/`tradeOffered` there).
- **`Stickiness`/`goal_deadband` ownership (NOT blocking).** Placed in `engine/agent`'s cross-tick
  loop, not single-call `Plan`.

## Notes

- **`planner:` block in `content/balance.yaml`** with `budget.*`, `base_horizon_ticks`,
  `tag_costs.*`, `urgency_threshold`; `intelligence.lookahead_threshold` lives in the
  `intelligence:` block.
- **Blocker 3 is two content/execution edits + one contract clarification, not a planner-algorithm
  change.** (1) `engine/agent` `replan` appends `owned_by_other` to `SatisfiedFacts` when a nearby
  agent holds items; (2) `content/actions.yaml` adds `has_Safety` to `Attack.produces`; (3) this
  SPEC documents that `owned_by_other` is a world-state fact and `has_Safety` makes `Attack` a Safety
  producer. The planner's existing `SatisfiedFacts` short-circuit and `Producers(has_Safety)`
  enumeration already do the rest.
- **RNG default: no draw.** Lexicographic `ActionID` final tie-break; `*rng.RNG` threaded for a
  future configurable random tie-break, deterministic per seed (D12).
- **Why-trace (NFR-3):** the returned `Trace` carries the competing `Candidate`s, provisioned
  dimensions, and relaxation flag so `engine/agent` emits `GoalSelected`/`PlanBuilt`.
- **`gates.AgentSnapshot` vs the planner `AgentSnapshot`** are two different structs; the planner
  adapts its snapshot into `gates.AgentSnapshot{SelfStats: <SelfModel means>, Known: agent.Known}`.
- **Intelligence `StatID` resolution** via `stats.Registry` (glossary capability id), never a literal.
</content>
