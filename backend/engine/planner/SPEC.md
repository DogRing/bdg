# SPEC — `engine/planner`

> Status: `DRAFT`
> Leaf level: `L4`  ·  Owner agent: `<filled by implementer>`

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
    NeedIntensities map[core.Dimension]float64 // current GROWN intensity per consumable dimension (higher = worse)
    Known           map[core.ObjectID]struct{} // objects this agent knows of (planner known-check)
    Urgency         float64                  // max Salience across all Dimensions (drives relaxation)
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
// It records the selected goal dimension, the competing candidate actions with their cost, the
// gate verdicts consulted, whether relaxation was applied, and the provisioned dimensions. The
// caller (engine/agent) emits this via core.EventEmitter; the planner does not emit (no IO).
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
> evaluation and the horizon calc; it never mutates it. The planner accepts `tom.Belief`
> directly — `engine/tom` is NOT modified to add an alias (see Open Questions).

## Dependencies

- `engine/core` — `ActionID`, `Tag`, `Pred`, `Dimension`, `Vec2`, `ObjectID`, `Stats`, `Tick`.
- `engine/stats` — `*Registry` (stat `[Min,Max]` for the Intelligence horizon `max_intelligence`;
  `IDs()` order for any stat iteration), `Stats`.
- `engine/gates` — `*Registry`, `Evaluate(Action, AgentSnapshot) Result`, `gates.AgentSnapshot`,
  `gates.Action` (the planner adapts its own `AgentSnapshot` + a candidate `ActionDef` into the
  gate-side shapes; gates do NOT import actions — the planner mediates, architecture §4).
- `engine/needs` — `*Registry`, `Def`, and the pure forward-roll helpers `Def.Demand`,
  `Def.BreachAt`, `Def.Level` (D9 forward-sim provisioning math lives in `needs`; the planner only
  *calls* it and decides whether to insert a subgoal).
- `engine/actions` — `*Registry`, `ActionDef`, `Producers(pred) []ActionID` (GOAP reverse index),
  `Effect` (to estimate a producer's contribution to a dimension during forward-sim).
- `engine/values` — `Priority`, `Salience` (the scalar types carried in `DimensionPriority`).
  The planner consumes the already-computed appraisals; it never recomputes Standing/Salience (D5).
- `engine/tom` — `Belief` (the `SelfModel` shape = `ToM[self]`; the planner reads its
  `EstStats[<Intelligence>].Mean` for gate eval + horizon, D8). No `engine/tom` change required.
- `engine/rng` — `*RNG` (injected, D12) for configured tie-breaking only.
- **Contract**: `content/balance.yaml` `planner:` block (`budget.*`, `base_horizon_ticks`,
  `tag_costs.*`, `urgency_threshold`) supplies `PlannerConfig`, injected by the caller via
  `platform/config`. The planner hardcodes no constant (D10). Schema:
  `content/schema/balance.schema.json` `planner` object.

## Owned Data

- The `Planner`, `Budget`, `PlannerConfig`, `Plan`, `Trace`, `Candidate`, `AgentSnapshot`,
  `DimensionPriority` value types and all search logic.
- The `Planner` holds **borrowed** read-only registry pointers and a **copied** config (with a
  precomputed sorted `[]core.Tag` of the `TagCosts` keys for D12 iteration). It mutates none of
  its inputs. `AgentSnapshot`, `values`, and `rng` are caller-owned; `Plan` never retains them.
- A `Plan`/`Trace` returned to the caller is freshly allocated each call (owned by the caller).

## Planning model

The planner combines three mechanisms in one `Plan` call. The order is: select the goal dimension
→ (provisioning) forward-sim each consumable dimension and prepend provisioning subgoals →
(GOAP) find producers for the goal → (HTN) decompose unmet preconditions of the chosen producer.

### 1. GOAP backward chaining

- Start from the **highest-Priority** `Dim` in `values` (the first row after the defensive
  Priority-desc, ActionID-tiebreak sort).
- Map the goal dimension to its satisfaction predicate(s) and use
  `actions.Registry.Producers(pred) []core.ActionID` (the reverse index, glossary
  `Producers map[Pred][]Action`) to enumerate candidate producer actions, **in `IDs()` order**.
- For each candidate, evaluate its gate visibility via `gates.Registry.Evaluate(act, snap)`,
  where `snap.SelfStats = SelfModel`'s stat means (D8 — the agent acts on its self-model, not
  ground truth). A candidate with `Result.Visible == false` is rejected (unless relaxed — see §
  "Dynamic visibility").
- Among visible candidates, select the **shallowest** sequence that satisfies the root goal;
  ties at equal depth are broken by **lower tag-derived cost**, then by **lexicographic
  `ActionID`** (D12). The chosen producer becomes the plan's goal action (appended last).

### 2. HTN forward decomposition

- After choosing the goal action, check its preconditions: `ActionDef.Requires` (ALL must hold)
  and `ActionDef.RequiresAny` (ANY satisfies). A precondition predicate holds if the agent state
  satisfies it; an unmet predicate triggers a recursive GOAP step.
- For each unmet predicate, find a producer (`Producers(pred)`) and recurse — the producer's own
  preconditions are decomposed the same way. Prerequisite actions are **prepended** before the
  goal action, yielding `[…prereqs…, goalAction]`.
- Decomposition is an **emergent ordered sequence**, NOT a hand-defined task network. There is
  **no** `Method`/`Task`/`Subtask` type anywhere in this module (D3) — the only structure is the
  flat `Producers` reverse index from `engine/actions`.
- Recursion is bounded by `Budget.MaxDepth`; expanded GOAP nodes by `Budget.MaxNodes`; final plan
  length by `Budget.MaxActions`. Hitting any cap returns `ErrBudgetExceeded` (§ Budget).

### 3. Forward-sim provisioning (D9)

For each **consumable** dimension `d` (`needs.Registry.Kinds(needs.Consumable)`, in `IDs()`
order), the planner forward-simulates whether `d` will breach its setpoint within the horizon and,
if so, inserts a provisioning subgoal **before** the current goal action. The provisioning need
quantity is **derived, never authored** (D9): it is `need-rate × predicted-time`, computed via the
`engine/needs` helpers — the planner stores no "future need" field.

```
-- Per consumable dimension d, with Def = needs.Registry.Def(d):
current_intensity(d)    = agent.NeedIntensities[d]            -- grown need (higher = worse)
satisfaction_threshold(d) = Def.Threshold                     -- setpoint in [0,1]
current_slack(d)        = satisfaction_threshold(d) - current_intensity(d)
predicted_deficit(d, horizon) = Def.Demand(horizon_ticks) - current_slack(d)
                              -- Def.Demand(t) = Rate × t  (engine/needs; the rate × predicted-time rule)

if predicted_deficit(d, horizon) > 0:
    insert a provisioning subgoal for d  (a producer action satisfying d) BEFORE the goal action
```

- `horizon_ticks` is the **Intelligence-gated lookahead** (next section). A low-foresight agent
  (low perceived Intelligence) provisions little or not at all; a high-foresight agent provisions
  far ahead — this reproduces scenario H (`docs/testing.md` §4) for free.
- The provisioning producer is chosen by the same GOAP+HTN+cost rules as the goal action.
- Multiple dimensions may breach; provisioning subgoals are inserted in `needs.IDs()` order
  (D12), each before the goal action, so the assembled order is deterministic.

### Intelligence-gated lookahead (forward-sim horizon)

The forward-sim horizon is derived from the agent's **self-perceived** Intelligence — the
`ToM[self]` mean estimate for the Intelligence stat (D8: the agent acts on its self-model, so a
self-underestimating agent literally looks less far ahead, and never gets evidence to correct it).

```
perceived_intelligence = SelfModel mean estimate for the Intelligence StatID  (ToM[self], D8)
max_intelligence       = stats.Registry.Def(Intelligence).Max
base_horizon           = PlannerConfig.BaseHorizonTicks   (content/balance.yaml planner.base_horizon_ticks)

horizon_ticks = floor( base_horizon × (perceived_intelligence / max_intelligence) )

if perceived_intelligence == 0:
    horizon_ticks = 1        -- always look at least 1 tick ahead (minimum viable foresight)
```

- The Intelligence `StatID` is obtained from `stats.Registry.Kinds(stats.Capability)` /
  `Registry.Has` — **never** a hardcoded `"Intelligence"` literal in planner logic (D7/D10). The
  caller may pass the resolved Intelligence `StatID` in config if a composite is wanted (see Open
  Questions); the default resolves it from the registry's capability set by the glossary id.
- `perceived_intelligence` is read from `SelfModel.EstStats[<Intelligence StatID>].Mean`
  (`tom.Belief.EstStats` — the self-belief means, D8).
- `horizon_ticks` is reported as `Plan.Horizon`.

### Tag-derived cost (absorbed from `engine/gates` schema_version 2)

`engine/gates` (schema_version 2) is boolean-visibility-only and carries **no cost** (see
`engine/gates/SPEC.md` Open Questions — cost was transferred here). The planner computes an
action's cost from its **Tags** (D4), never a bespoke per-action function:

```
cost(action) = Σ  TagCosts[tag]   for tag in action.Tags         (iterate sorted tag keys, D12)
```

- `TagCosts` is `PlannerConfig.TagCosts`, loaded from `content/balance.yaml planner.tag_costs`.
- Cost is the **tie-breaker among equal-Priority, equal-depth candidates**: lower cost wins; a
  remaining tie breaks by lexicographic `ActionID` (D12). Cost never overrides Priority — a
  higher-Priority goal is pursued even if its actions cost more (D5: want dominates how).
- `TagCosts` is used read-only; the sum **must** iterate the precomputed sorted key slice, never
  range the map (D12). A tag absent from `TagCosts` contributes 0.

### Dynamic visibility / conscience loosening (absorbed from `engine/gates`)

`engine/gates` evaluates only stat (ToM[self]) and tag predicates; runtime body/signal-driven
relaxation was transferred here (see `engine/gates/SPEC.md` Open Questions). After the normal gate
verdict, the planner applies a **secondary relaxation step** for actions whose gate failed:

```
Urgency  = agent.Urgency   (= max Salience across all Dimensions, supplied by the caller)
relaxed  = (Urgency > PlannerConfig.UrgencyThreshold)

if a candidate's gate verdict is NOT Visible AND relaxed:
    treat the candidate as visible for this Plan call only (conscience loosening)
```

- Relaxation is a **transient, per-call** override; it never mutates the gate registry or the
  agent. It is recorded in `Trace.Relaxed` and per-candidate `Candidate.Visible` for the why-trace
  (data-contracts §4 — "given enough urgency, theft becomes visible", scenario B).
- Below the threshold, a gated action stays hidden. This is the only place gate visibility is
  loosened; `engine/gates` itself is untouched (it cannot read `Urgency`).

## Invariants

- **Atomic-only plans, no task trees (D3)**: the module defines **no** `Method`, `Task`, or
  `Subtask` type. A `Plan` is a flat `[]core.ActionID`; decomposition is an emergent ordered
  sequence assembled from the `Producers` reverse index. A struct/grep guard confirms no
  method/task/subtask type exists.
- **What-vs-how separation (D5)**: the planner never computes `Standing`/`Salience`/`Priority`
  (it receives them) and never edits a need/value definition. It answers only "given this Priority
  ordering, what sequence of actions maximises it". A higher Priority is never overridden by lower
  cost.
- **Provisioning is derived, never authored (D9)**: provisioning quantity = `need-rate ×
  predicted-time` via `needs.Def.Demand`/`BreachAt`; the planner stores **no** "future need" field
  and reads none from any object/action. Objects carry only their supply `Effect`.
- **Cost is tag-derived (D4)**: action cost = `Σ TagCosts[tag]`; there is **no** per-action cost
  number anywhere (none on `ActionDef`, none here). Adding cost behaviour is a content edit (a tag
  or a `planner.tag_costs` entry), never a Go branch (D10).
- **Decisions read `ToM[self]` (D8)**: gate evaluation and the horizon calc read `SelfModel` stat
  means, never `CurrentStats`/Real Stats. The planner never "corrects" a self-underestimate; a low
  perceived Intelligence yields a short horizon and that is preserved (self-sealing — scenario F).
- **Determinism (D12)**: `Plan` is a pure function of `(AgentSnapshot, values, registries, rng
  state)`. All iteration uses sorted keys/slices — `actions.IDs()`/`Producers()` order,
  `needs.IDs()` order, the precomputed sorted `TagCosts` keys, and `stats.IDs()` order — **never**
  `map` ranging for logic. The defensive sort of `values` is Priority-desc then `Dim`-lexicographic.
  All tie-breaks are deterministic: equal Priority → lower cost → lexicographic `ActionID`. `rng`
  is consulted **only** after these deterministic comparisons (and, by default, not at all —
  Notes). Same `AgentSnapshot` + `values` + `rng` seed/state → byte-identical `Plan` and `Trace`.
- **Budget always terminates the search**: every recursion is bounded by `MaxDepth`, every
  expansion by `MaxNodes`, every plan by `MaxActions`. A breach returns `ErrBudgetExceeded`; the
  search never loops forever and never silently truncates.
- **Read-only inputs**: `AgentSnapshot`, `values`, and every borrowed registry are never mutated;
  the planner retains no reference to them past the `Plan` call. The gate registry is consulted,
  never relaxed in place (relaxation is a transient per-call decision).
- **No hardcoded ids (D7/D10)**: no `"Intelligence"`, `"Satiety"`, `"Eat"`, … literal in planner
  logic; stat/need/action ids flow from the injected registries and config.
- **No IO (architecture §1)**: imports no `os`/`net`/filesystem package; reads no file; emits no
  event (the caller emits the returned `Trace`). All constants are injected via `PlannerConfig`.

## Acceptance Criteria (testable)

- [ ] **GOAP backward chain selects a producer (D-GOAP)**: given a `Satiety` goal and a stub
  `Producers` index returning `[Eat, Forage]`, the planner selects `Eat` when its gate passes;
  when `Eat`'s gate fails it selects `Forage`. Table-driven against stub registries.
- [ ] **HTN decomposition prepends prerequisites (golden snapshot)**: if `Eat` requires the
  `has_food` predicate and `has_food` is false, the planner prepends a producer of `has_food`
  (`Forage`/`Hunt`) so `Plan.Actions == [Forage, Eat]` (prereq first, goal last). Golden over the
  shipped content + a fixture snapshot (`docs/testing.md` §3).
- [ ] **Forward-sim inserts a provisioning subgoal (D9, scenario H)**: an agent with full current
  slack but a large `predicted_deficit` (high need-rate × long horizon) gets a provisioning action
  inserted before the goal; with a short horizon (low Intelligence) the same agent does **not**
  (ties to testing.md §4 fixture H: high-Intelligence provisions, low-Intelligence does not).
- [ ] **Intelligence horizon = 1 at perceived Intelligence 0**: an agent whose `SelfModel`
  Intelligence mean is 0 yields `horizon_ticks == 1` (minimum viable foresight), regardless of
  `base_horizon`.
- [ ] **Intelligence horizon = base at perceived Intelligence = max**: an agent whose perceived
  Intelligence equals `stats.Registry.Def(Intelligence).Max` yields `horizon_ticks ==
  BaseHorizonTicks`.
- [ ] **Intelligence horizon = floor(base/2) at perceived Intelligence = max/2**: an agent whose
  perceived Intelligence is half of max yields `horizon_ticks == floor(BaseHorizonTicks/2)`
  (table-driven over several perceived-Intelligence / base-horizon pairs, asserting the `floor`).
- [ ] **`Budget.MaxDepth` aborts (no infinite loop)**: a contrived cyclic-precondition content set
  forces decomposition past `MaxDepth`; `Plan` returns `ErrBudgetExceeded` (and likewise for
  `MaxNodes` and `MaxActions`). Table-driven per cap.
- [ ] **Tag-derived cost breaks an equal-Priority tie (D4)**: two equal-Priority producers of the
  same depth, one carrying a tag with a higher `planner.tag_costs` weight — the lower-cost action
  is `Chosen`; a remaining cost tie breaks by lexicographic `ActionID`. Table-driven.
- [ ] **Dynamic visibility relaxation (scenario B)**: a candidate whose gate fails becomes
  `Candidate.Visible == true` and may be `Chosen` when `agent.Urgency > UrgencyThreshold`
  (`Trace.Relaxed == true`); below the threshold it stays hidden and is not chosen. Table-driven.
- [ ] **Cost never overrides Priority (D5)**: a higher-Priority dimension's (costlier) producer is
  chosen over a lower-Priority dimension's cheaper producer.
- [ ] **No task-tree / no per-action cost type (D3/D4)**: struct/grep guard — no
  `Method`/`Task`/`Subtask` type and no numeric cost field on any planner output type; `Plan` is a
  flat `[]core.ActionID`.
- [ ] **Reads `ToM[self]`, not Real Stats (D8)**: a gate evaluation and the horizon calc use
  `SelfModel`; with `CurrentStats` high but `SelfModel` low, the action stays hidden and the
  horizon stays short (self-sealing preserved; scenario F low-Intelligence fixates).
- [ ] **Determinism: 1000 identical calls (D12)**: the same `AgentSnapshot` + `values` + `rng`
  seed produces a byte-identical `Plan` and `Trace` across 1000 calls; a golden test records the
  digest. A second run with a fresh registry from the same content reproduces it (cross-process).
- [ ] **No constant hardcoded (D10)**: grep guard — no numeric budget/horizon/cost/threshold
  literal and no stat/need/action-name literal in `engine/planner` logic; all flow from
  `PlannerConfig` + the injected registries.
- [ ] **`ErrNoGoal` / `ErrUnreachable`**: empty `values` → `ErrNoGoal`; a goal with no visible
  producer within budget → `ErrUnreachable` (not a panic, not an empty plan masquerading as valid).

> Structural JSON-schema validation of the `content/balance.yaml planner:` block against
> `content/schema/balance.schema.json`, and the cross-check that each `tag_costs` key names a tag
> actually used in `content/actions.yaml`, are **platform/config** ACs (it owns the file IO + the
> schema). This module proves only behaviour reachable from the injected `PlannerConfig`.

## Out of Scope

- Reading the file from disk and JSON-schema validation of the `planner:` block →
  `platform/config` (architecture §3).
- **Computing `Standing`/`Salience`/`Priority`/`EffValue`** and arbitrating which dimension is
  wanted → `engine/values` (the planner receives the Priority-ordered `[]DimensionPriority`).
- **Per-need decay, level roll, `Demand`/`BreachAt` math** → `engine/needs` (the planner calls
  these pure helpers; it does not reimplement the forward-roll).
- **Gate predicate evaluation** (the boolean AND-of-matching-gates over ToM[self] + tags) →
  `engine/gates`; the planner only calls `Evaluate` and applies the Urgency relaxation on top.
- **The action catalog, tags, `Producers` reverse index, `Effect`** → `engine/actions`.
- **`ToM[self]` construction, β self-calibration, gossip** → `engine/tom`; the planner reads the
  self-belief's stat means and never updates them.
- **Executing the plan, durative progress, interruption, coping, `Stickiness`/`goal_deadband`
  application, emitting `PlanBuilt`/`GoalSelected` events** → `engine/agent` (it calls `Plan`,
  applies stickiness/deadband to the *goal choice* before/around the call, executes the returned
  actions, and emits the `Trace`). `Stickiness`/`goal_deadband` are anti-thrash on goal *switching*
  across ticks — an agent-loop concern, not a single-plan concern (see Open Questions).
- **Outcome resolution** (does `Hunt` succeed, scaled by Real Stats), intent collection, and
  conflict resolution → `engine/world`.
- **Distance/stat scaling of `Duration`** at execution time → `engine/agent`/`engine/world`; the
  planner uses base `Duration` only for cost estimation.

## Open Questions

- **RNG type: `*rng.RNG` vs `*rand.Rand` (NOT blocking P1; resolved to `*rng.RNG`).** The task
  brief's signature wrote `rng *rand.Rand`, but the project's canonical deterministic generator is
  `engine/rng.*RNG` (D12; `engine/rng/SPEC.md`), which `engine/tom` already injects. This SPEC uses
  `*rng.RNG` to avoid a second RNG abstraction and to satisfy "no global/stdlib rand outside the
  wrapper". Flag if a raw `*rand.Rand` is genuinely wanted — but that would violate the rng-wrapper
  invariant, so `*rng.RNG` is the chosen contract.
- **`SelfModel` type — RESOLVED: planner uses `tom.Belief` directly.** The task brief named the
  self-model field `tom.AgentModel`; `engine/tom/SPEC.md` exposes no such alias (it has `tom.ToM` /
  `tom.Belief` with `EstStats map[core.StatID]tom.StatDist`). Per human decision, the planner
  accepts `tom.Belief` directly for `AgentSnapshot.SelfModel` and reads
  `SelfModel.EstStats[<Intelligence StatID>].Mean` for the horizon and `SelfModel.EstStats` means
  for gate evaluation. **`engine/tom` is NOT modified** — no `AgentModel` alias is added on the tom
  side. The planner contract is stable: it depends only on `tom.Belief.EstStats`.
- **Goal-satisfaction predicate mapping (NOT blocking P1).** GOAP needs to map a goal `Dimension`
  to the predicate(s) a producer must `Produces`. Today the link is implicit (an action's `Effect`
  names the dimension; its `Produces` names predicates like `has_food`). The planner resolves a
  dimension's producers by querying `actions.Producers(pred)` over the predicate(s) whose producers
  carry an `Effect` on that dimension. Confirm whether a dimension→predicate map should be authored
  in content (e.g. `needs.yaml satisfied_by:`) or derived from action `Effect`s at `New`. Derived
  is the default; flag if content authoring is preferred (D10 would favour content, but it adds a
  schema field — escalate before any feature needing it).
- **`Stickiness`/`goal_deadband` ownership (NOT blocking P1).** `content/balance.yaml planning.*`
  carries `stickiness`/`goal_deadband` (anti-thrash). This SPEC places them in `engine/agent`'s
  cross-tick goal-switching loop, not in single-call `Plan` (which is stateless). Confirm with the
  agent SPEC author so neither double-applies them.

## Notes

- **`planner:` block added to `content/balance.yaml`** (this batch) with `budget.{max_depth,
  max_actions, max_nodes}`, `base_horizon_ticks`, `tag_costs.{Tag: weight}`, `urgency_threshold`;
  the schema (`content/schema/balance.schema.json`) `planner` object was added to match. The
  pre-existing `planning:` / `forward_sim:` / `tag_levels:` / `cost_terms:` blocks remain (they
  serve `engine/agent` budget scaling, the needs roll granularity, and the gate-tag library). The
  new `planner.tag_costs` is the planner's authoritative per-tag cost weight; it is distinct from
  `cost_terms`×`tag_levels` (which is the finer additive model the agent may later compose) — for
  P1 the planner uses the flat `planner.tag_costs` sum specified above. If the richer `cost_terms`
  model supersedes it, fold `tag_costs` into that and update this SPEC + the schema first.
- **RNG default: no draw.** With lexicographic `ActionID` as the final tie-break, a well-formed
  content set never needs `rng`. It is threaded in so a future configurable random tie-break (e.g.
  to diversify identical agents) stays deterministic per seed (D12). For P1 the `*rng.RNG` argument
  is accepted but, by default, not drawn from — keep it in the signature so the contract is stable.
- **Why-trace (NFR-3):** the returned `Trace` carries the competing `Candidate`s (with cost + gate
  verdict), the provisioned dimensions, and the relaxation flag so `engine/agent` can emit
  `GoalSelected` / `PlanBuilt` with the selection rationale (data-contracts §4 `steps[],
  total_cost, provisioned[]`). The planner computes the rationale; the agent emits it.
- **`gates.AgentSnapshot` vs the planner `AgentSnapshot`:** these are two different structs. The
  planner adapts its own snapshot into `gates.AgentSnapshot{SelfStats: <SelfModel.EstStats means>,
  Known: agent.Known}` per candidate (gates read ToM[self] only, D8). Keep them distinct. Note the
  adaptation projects each `tom.StatDist.Mean` from `SelfModel.EstStats` into the gate-side
  `stats.Stats` (`map[StatID]float64`) shape.
- **Intelligence `StatID` resolution:** resolve via `stats.Registry` (the glossary capability id),
  never a string literal in logic (D7). The implementer obtains it once in `New` and stores the
  resolved `StatID`, failing fast if the registry has no Intelligence capability.
