# SPEC — `engine/agent` · Politics (P6)
> Scope: emergent reliance, Vote delegation, Influence-weighted gossip
> Status: P6 · Leaf level: L5 · Owner agent: implementer
> Source: split from `engine/agent/SPEC.md` (monolith)

## Purpose

This file owns the **politics / emergent-institutions** slice of the agent decision loop: the
policy layer over `engine/tom`'s reliance / Influence primitives. The agent decides *when* to rely
on another (§G), casts delegation **Vote** signals (§H), and weights incoming social signals by the
source's **Influence** (§I). The world then detects the resulting `RelyOn` cluster as a
`RoleEmerged` statistic (no role type anywhere — D2). All thresholds / ratios are injected (D10).

P6 makes the agent the **policy** layer over `engine/tom`'s reliance/Influence primitives: it
decides *when* to rely on another, casts delegation **Vote** signals, and weights incoming social
signals by the source's **Influence**. The world then detects the resulting `RelyOn` cluster as a
`RoleEmerged` statistic (no role type anywhere — D2). All thresholds/ratios are injected (D10).

> Cross-references:
> - The combined-urgency proxy (`combinedPriority`) consumed by §H is computed in tick phase 3.
>   See `SPEC-core.md` §Phase-3 for the `combinedPriority` computation
>   (`max(selfMax, maxOther, maxPlace, maxCollective)`).
> - The §F Safety-intensity drive that makes member Safety pressure rise (the precondition for the
>   convergence-under-distress contrast) lives in `SPEC-core.md` §F (P6 Blocker Group A).

## FunctionSpec type definition

A **Function** (glossary Reliance edge: `safety`, `judgment`, `knowledge`, …) maps to the goal
Dimension(s) and the capability stat-set it draws on. The mapping is **data-driven** (D7 — no
literal in logic): the agent reads the injected `Config.Functions []FunctionSpec` table to resolve
a goal `Dimension` to its `Function` and the capability `Stats` that Function draws on.

```go
// FunctionSpec maps a Function id to the goal Dimension(s) it serves and the
// capability stat-set required to perform it. Injected via Config.Functions (D7/D10):
// it replaces the hardcoded goalToFunction mapping — the agent resolves a goal Dimension
// to its Function + capability Stats by scanning this table, never a literal.
type FunctionSpec struct {
    ID    core.Function  // e.g. core.FuncSafety
    Dim   core.Dimension // goal dimension this function covers (e.g. "Safety")
    Stats []core.StatID  // capability stats required to provide this function (passed to BestProviderFor)
}
```

## G. Reliance trigger — when a Function is self-unsolvable

A **Function** (glossary Reliance edge: `safety`, `judgment`, `knowledge`, …) maps to the goal
Dimension(s) and the capability stat-set it draws on. The mapping is **data-driven** (D7 — no
literal in logic): the agent reads the injected `Config.Functions []FunctionSpec` table to resolve
a goal `Dimension` to its `Function` and the capability `Stats` that Function draws on. During
phase 5 (plan), when the agent pursues a goal serving Function `f` and **cannot self-solve it**, it
relies on another:

- **Function resolution (gap-closure — replaces the hardcoded `goalToFunction`).** The agent resolves
  `(Function, Stats)` for the current goal Dimension by scanning `Config.Functions` for the entry
  whose `Dim == a.Goal` (iterate the slice in its authored order — it is a small fixed table, D12),
  returning that entry's `ID` (the Function) and `Stats` (the capability set for
  `BestProviderFor`). If no entry matches the goal Dimension, **no Function is resolved and no
  reliance forms** (return early — the hardcoded "Safety → FuncSafety, else FuncKnowledge" fallback
  is REMOVED). The `Stats` come from the matched `FunctionSpec`, NOT from the agent's own
  `EstStats` keys.
- **Self-unsolvable** = the planner returns `ErrUnreachable`/`ErrBudgetExceeded` for `f`'s goal
  (gate-blocked: no visible producer), **OR** the cheapest reachable plan's cost exceeds
  `Config.RelyCostThreshold` (`balance.yaml politics.rely_cost_threshold`). Both are observed at the
  planner boundary — the agent already has the trace, so no gate/cost logic is duplicated (D4/D5).
- **Whom**: among perceived others, pick `ToM.BestProviderFor(f, fnStats, candidates)` (trusted AND
  competent, D6/D8 — reads ToM beliefs, never Real Stats), where `fnStats` is the matched
  `FunctionSpec.Stats`. If none qualifies, no reliance forms.
- **Apply**: `ToM.AdjustRelyOn(provider, f, δ)` with `δ = Config.RelyOnDelta`
  (`balance.yaml politics.relyon_delta`). Reliance thus **accretes** over repeated self-failure —
  the substrate that, once a plurality points at one provider, the world reads as a role.
- Emit a `BeliefUpdated{about: provider, field: "RelyOn["+f+"]", cause: "reliance"}` for the trace.

### `functionForGoal` — the table lookup (replaces the package `goalToFunction`)

```go
// functionForGoal returns the FunctionSpec whose Dim matches the given goal Dimension.
// Scans Config.Functions in order (a small fixed table, D12); returns zero-value
// FunctionSpec and false if none match. Replaces the hardcoded goalToFunction mapping
// (gap-closure §G).
func (a *Agent) functionForGoal(dim core.Dimension) (FunctionSpec, bool)
```

The current code iterates `a.Cfg.Functions` and returns the first entry with `fn.Dim == dim`, else
`(FunctionSpec{}, false)`. This is the **method** that replaces the removed `goalToFunction` package
function.

### G-emit. `handleRelianceTrigger` takes an `emit core.EventEmitter` (gap-closure)

`handleRelianceTrigger` must accept an `emit core.EventEmitter` parameter so it can emit the
`BeliefUpdated{cause:"reliance"}` event on a successful `AdjustRelyOn`. Updated signature:

```go
func (a *Agent) handleRelianceTrigger(world WorldView, planCost float64, planErr error, emit core.EventEmitter)
```

- Both `tick.go` call sites (the plan-failure path and the high-plan-cost path) pass the loop's
  `emit`: `a.handleRelianceTrigger(world, 0, err, emit)` and
  `a.handleRelianceTrigger(world, trace.TotalCost, nil, emit)`.
- **Trigger conditions** (from `p6.go`): if `planErr == nil && planCost <= a.Cfg.RelyCostThreshold`,
  return early (no trigger — the agent can self-solve affordably). Otherwise the Function is
  resolved via `a.functionForGoal(a.Goal)`; if no entry matches, return (no Function registered for
  this goal Dimension).
- **Candidates**: `knownIDs := world.AgentIDs()`; if empty, return. Filter out `a.ID` and sort the
  candidate slice by `string(candidates[i]) < string(candidates[j])` for deterministic evaluation
  (D12).
- **Stat-set**: copy `fnSpec.Stats` into `statSet` and `sortStatIDs(statSet)` (D7 — no hardcoded
  stat ids; the set comes from the matched `FunctionSpec`).
- **Provider**: `target, score := a.ToM.BestProviderFor(fnSpec.ID, statSet, candidates)`; if
  `target == "" || score <= 0`, return (no reliance forms).
- **Apply**: `a.ToM.AdjustRelyOn(target, fnSpec.ID, a.Cfg.RelyOnDelta)`.
- After a successful `a.ToM.AdjustRelyOn(target, f, a.Cfg.RelyOnDelta)`, emit:

  ```go
  emit.Emit(core.Event{
      SchemaVersion: 1,
      Tick:          0,                       // filled by the platform layer
      AgentID:       a.ID,
      Type:          "BeliefUpdated",
      Payload: map[string]any{
          "about": string(target),
          "field": "RelyOn[" + string(f) + "]",
          "cause": "reliance",
          "delta": a.Cfg.RelyOnDelta,
      },
  })
  ```

  The emit is guarded by `emit != nil`. No event is emitted on the no-trigger / no-provider early
  returns (only on an actual reliance shift).

This is **emergent** (D2): the agent never labels anyone a chief; it just keeps relying on whoever
best covers a need it cannot meet alone.

## H. VoteAction — broadcasting a delegation

`Vote` is an atomic **social signal** (content/actions.yaml; tags `[social, effort:low,
abstraction:med]`, requires `near_other`) that publicly delegates Function `f` to a target — it
lets reliance **converge faster than private accretion alone**. In phase 7 (signal), when the agent
holds a strong private reliance (`ToM[target].RelyOn[f] ≥ Config.VoteRelyThreshold`) **and** its
distributed urgency is high (the combined urgency proxy already computed in phase 3 exceeds
`Config.UrgencyThreshold`), it emits:

```go
Signal{Kind: SignalVote, Toward: target, Function: f, Intensity: relianceStrength}
```

### H-urgency. `emitVoteIfEligible` takes the already-computed `combinedPriority` (gap-closure)

`emitVoteIfEligible` MUST accept the `combinedPriority float64` already computed in phase 3 of
`Tick` (the `max(selfMax, maxOther, maxPlace, maxCollective)` urgency proxy, which already includes
`maxCollective`), instead of recomputing a separate distributed urgency. Updated signature and call
site:

```go
func (a *Agent) emitVoteIfEligible(now core.Tick, world WorldView, combinedPriority float64) Intent
// tick.go phase 7:
voteIntent := a.emitVoteIfEligible(now, world, combinedPriority)
```

- The eligibility test (as implemented in `p6.go`):
  1. Resolve the Function for the `SafetyDim` via `fnSpec, ok := a.functionForGoal(a.Cfg.SafetyDim)`;
     if `!ok`, return `Intent{Kind: IntentNone, Agent: a.ID, Tick: now}`. Let `fn := fnSpec.ID`.
  2. `relyTarget, relyStrength := a.bestRelyOnFor(fn)`; require `relyTarget != "" && relyStrength >
     a.Cfg.VoteRelyThreshold` (the code returns `IntentNone` when
     `relyTarget == "" || relyStrength < a.Cfg.VoteRelyThreshold`).
  3. Use the pre-computed combined urgency proxy from phase 3, normalized by `MaxPossiblePriority`:
     `urgency := clamp01(combinedPriority / a.Cfg.MaxPossiblePriority)`; require
     `urgency > a.Cfg.UrgencyThreshold` (the code returns `IntentNone` when
     `urgency <= a.Cfg.UrgencyThreshold`). This uses `Config.UrgencyThreshold`, the canonical
     distributed-urgency threshold named in §H — if the current code reads
     `Config.VoteUrgencyThreshold`, reconcile to a single field (`UrgencyThreshold`) and document
     it in the Data Contract; do not keep two near-duplicate thresholds.
  4. If both hold, return the `IntentSignal` with the `SignalVote` payload; otherwise `IntentNone`.

- The returned vote `Intent` (from `p6.go`):

  ```go
  Intent{
      Kind:  IntentSignal,
      Agent: a.ID,
      Tick:  now,
      Signal: &Signal{
          Kind:      SignalKind("Vote"),
          Toward:    "",           // broadcast — no specific receiver
          Valence:   0.5,
          Intensity: relyStrength,
          Function:  fn,
          Target:    relyTarget, // the voted holder
      },
  }
  ```

- The `distributedUrgency(world, safetyDim)` helper is **DELETED** — its recomputed mean Safety
  intensity is exactly the `maxCollective` term already folded into `combinedPriority` in phase 3, so
  recomputing it (and re-hardcoding the Safety dimension parameter) is redundant. The Function `f` is
  resolved via the `Config.Functions` table (§G-emit / gap-closure), not the removed
  `goalToFunction`.

A receiver folds a heard `Vote` as evidence to **also** `AdjustRelyOn(target, f, δ_vote)` — so under
shared distress (everyone's Safety dropping at once) votes pile onto the same capable holder and the
`RelyOn` share crosses `role_convergence_threshold` in far fewer ticks than independent accretion.
Low distributed urgency ⇒ no votes ⇒ slow/no convergence (the AC contrast). `Vote` produces no need
Effect; it never competes with survival goals (the planner won't pick it for a need goal).

### Receiver-side `processVoteSignal`

```go
func (a *Agent) processVoteSignal(sig core.Signal)
```

Handles an incoming `SignalVote` from another agent (delivered via `WorldView.IncomingSignals`). It
folds a `RelyOn` delta toward the voted holder (`signal.Target`) for the given Function, but only if
the sender has positive Trust (`Trust > 0`) — untrusted senders are ignored. From `p6.go`:

- Guard `sig.Kind != core.SignalVote` → return.
- `fn := sig.Function`; if `fn == ""` → return.
- `votedHolder := sig.Target`; if `votedHolder == ""` → return.
- `sourceID := sig.Source`; if `sourceID == ""` → return.
- `senderBelief, ok := a.ToM.Self(sourceID)`; if `!ok || senderBelief.Trust <= 0` → return (ignore
  votes from untrusted agents).
- `a.ToM.AdjustRelyOn(votedHolder, fn, a.Cfg.VoteRelyOnDelta)`.

> The signal's `Intensity` field carries the voter's own `RelyOn` strength toward the voted holder;
> the delta applied is `Config.VoteRelyOnDelta` (a constant independent of the voter's own strength —
> the vote is a signal, not a transfer of reliance).

## I. Influence — weighting incoming signals

At the signal-fold site, the agent scales the source's credibility by the source's **Influence**
before calling `tom.GossipUpdate`. This is owned by `processIncomingSignals`, which today only
handles `SignalVote` and must now ALSO handle gossip/hearsay signals:

    signalWeight = clamp01( trust · (1 + Config.InfluenceWeight · ToM.Influence(source, f, observers)) )

`Config.InfluenceWeight` = `balance.yaml politics.influence_weight`. A heavily-relied-upon
(high-Influence) source therefore shifts the agent's beliefs **more** for the same claim — the
table AC. `GossipUpdate`'s signature is unchanged (Influence is folded into the weight the agent
passes, per `engine/tom/SPEC.md` §P6).

### Routing — `processIncomingSignals`

```go
func (a *Agent) processIncomingSignals(world WorldView)
```

`processIncomingSignals` iterates `world.IncomingSignals(a.ID)`. From `p6.go`:

- If `len(signals) == 0` → return.
- Pre-compute `observers := a.allBeliefs()` once (sorted observers for Influence weighting, D12).
- For each `sig`:
  - `case core.SignalVote:` → `a.processVoteSignal(sig)` (unchanged).
  - `default:` (gossip / hearsay) → `a.processGossipSignal(sig, observers)`.

### I-fold. Gossip handling in `processGossipSignal` (gap-closure)

```go
func (a *Agent) processGossipSignal(sig core.Signal, observers []tom.Belief)
```

Folds a hearsay/gossip signal with Influence-weighted credibility:
`signalWeight = clamp01(trust · (1 + InfluenceWeight · Influence))`. From `p6.go`:

- `sourceID := sig.Source`; if `sourceID == ""` → return.
- `subjectID := sig.Target`; if `subjectID == ""` then `subjectID = sourceID` (the subject is who
  the gossip is ABOUT, which may differ from the source).
- `sourceBelief, ok := a.ToM.Self(sourceID)`; if `!ok || sourceBelief.Trust <= 0` → return (ignore
  unknown / untrusted sources). `trust := clamp01(sourceBelief.Trust)`.
- `influence := a.ToM.Influence(sourceID, core.FuncKnowledge, observers)` — Influence of the source
  over the **Knowledge** function (the credibility function for generic gossip — the function that
  gossip typically concerns; D7: `FuncKnowledge` is defined in `core` and is the content-canonical
  gossip credibility function).
- `signalWeight := clamp01(trust * (1.0 + a.Cfg.InfluenceWeight*influence))`.
- Resolve the source's belief about the subject for `GossipUpdate`: if `sourceID == subjectID`,
  `subjectBelief = sourceBelief`; else `subjectBelief, _ = a.ToM.Self(subjectID)`.
- `a.ToM.GossipUpdate(subjectID, subjectBelief, signalWeight)`.

> **Verify the exact tom API before coding.** Confirmed against `engine/tom/SPEC.md` §P4/§P6:
> the fold method is `tom.ToM.GossipUpdate(subject core.AgentID, source Belief, trustWeight
> float64) map[core.StatID]float64`, where `source` is the **source's Belief about the subject**
> (not the raw signal), and `Influence` has signature
> `tom.ToM.Influence(subject core.AgentID, function Function, observers []Belief) float64` — note
> it takes a `function` argument (the §I pseudocode's `Influence(source, observers)` shorthand
> omits it). There is no `GossipUpdate` that consumes a raw `Signal`; the agent must convert the
> signal into a `(subject, sourceBeliefAboutSubject, weight)` triple. If a future tom change adds
> a signal-shaped overload, update this section first.

- `GossipUpdate` returns the per-stat mean delta; the agent emits one `ReputationGossip` event per
  changed stat (the existing P4 contract, `engine/tom/SPEC.md` Out of Scope) when `emit` is
  threaded — pass `emit` into `processIncomingSignals` if the gossip-event emission is wired this
  batch (Open Question below).

## Helpers

```go
// bestRelyOnFor iterates the agent's ToM subjects (sorted, D12) and returns the
// subject with the highest RelyOn[fn] value, and that value. Returns ("", 0) if
// no subject has a non-zero RelyOn for fn.
func (a *Agent) bestRelyOnFor(fn core.Function) (core.AgentID, float64)

// allBeliefs returns all beliefs held in the agent's ToM (excluding self) as a
// slice, in sorted SubjectID order (D12). Used to compute Influence distributions.
func (a *Agent) allBeliefs() []tom.Belief
```

- `bestRelyOnFor` skips `a.ToM.SelfID()`, skips subjects with a nil `RelyOn`, tracks the highest
  `b.RelyOn[fn]`; ties break by lower AgentID (the sorted `Subjects()` already handles this). It
  returns `("", 0)` if the best is empty or `bestScore <= 0`.
- `allBeliefs` iterates `a.ToM.Subjects()` (sorted), excludes self, and collects each `Belief`.

## Config fields (politics — injected, D10)

```go
Functions          []FunctionSpec // function id → (goal Dimension, capability stat-set); content/glossary, D7
RelyCostThreshold   float64       // plan cost above which a Function counts self-unsolvable
RelyOnDelta         float64       // δ added to RelyOn on a self-failure (politics.relyon_delta)
VoteRelyThreshold   float64       // private reliance strength that licenses a Vote
UrgencyThreshold    float64       // politics.urgency_threshold — combined-urgency proxy above which a Vote is cast (§H)
VoteRelyOnDelta     float64       // δ a RECEIVER folds toward the voted holder on a heard Vote (politics.vote_relyon_delta)
InfluenceWeight     float64       // politics.influence_weight — Influence→signal-weight ratio
```

Verbatim from the agent `Config` struct (`agent.go`):

```go
// P6: function→dimension→stat-set table (injected, D7/D10).
// Replaces the hardcoded goalToFunction mapping.
Functions []FunctionSpec // content: function id → (goal Dimension, capability stat-set)

// P6: reliance + vote policy thresholds
RelyCostThreshold    float64 // balance.yaml politics.rely_cost_threshold — plan cost above which a Function counts self-unsolvable
RelyOnDelta          float64 // balance.yaml politics.relyon_delta — δ added to RelyOn on self-failure
VoteRelyThreshold    float64 // balance.yaml politics.vote_rely_threshold — private reliance strength that licenses a Vote
UrgencyThreshold     float64 // balance.yaml politics.urgency_threshold — combined-urgency proxy above which a Vote is cast (§H)
VoteRelyOnDelta      float64 // balance.yaml politics.vote_relyon_delta — δ a heard Vote folds into RelyOn

// P6: influence weighting for incoming signals
InfluenceWeight float64 // balance.yaml politics.influence_weight — signalWeight = trust·(1 + influence_weight·Influence)
```

> Note: `emitVoteIfEligible` also reads `Config.MaxPossiblePriority` (the ceiling that normalizes
> `combinedPriority` into the urgency proxy; default `2.5`) and `Config.SafetyDim` (to resolve the
> voted Function via `functionForGoal`). Both are owned/described in `SPEC-core.md`; politics only
> consumes them.

### DefaultConfig values

From `agent.go` `DefaultConfig()`:

```go
// P6: function→dimension→stat-set table (injected defaults for tests; D7/D10).
Functions: []FunctionSpec{
    {ID: core.FuncSafety, Dim: "Safety", Stats: []core.StatID{"Strength"}},
    {ID: core.FuncKnowledge, Dim: "Knowledge", Stats: []core.StatID{"Intelligence"}},
},

// P6: reliance + vote policy thresholds
RelyCostThreshold: 1.2,
RelyOnDelta:       0.15,
VoteRelyThreshold: 0.4,
UrgencyThreshold:  0.65,
VoteRelyOnDelta:   0.10,

// P6: influence weighting for incoming signals
InfluenceWeight: 0.5,
```

The SPEC's documented default `Functions` table (test defaults — platform/config provides the real
ids):

```go
//   Functions: []FunctionSpec{
//       {ID: core.FuncSafety,    Dim: "Safety",    Stats: []core.StatID{"Strength"}},
//       {ID: core.FuncKnowledge, Dim: "Knowledge", Stats: []core.StatID{"Intelligence"}},
//   }
```

## Loop wiring (where politics runs in `Tick`)

P6 politics hooks into the 8-phase loop at three points (see `SPEC-core.md` for the full ordering):

- **phase 0** — `a.processIncomingSignals(world)` folds incoming gossip (Influence-weighted) + votes
  before perception.
- **phase 5a** — `a.handleRelianceTrigger(world, …, emit)` runs the reliance trigger; both call
  sites pass the loop's `emit`:
  - plan-failure path: `a.handleRelianceTrigger(world, 0, err, emit)`
  - high-plan-cost path: `a.handleRelianceTrigger(world, trace.TotalCost, nil, emit)`
- **phase 7** — `voteIntent := a.emitVoteIfEligible(now, world, combinedPriority)` emits a Vote when
  the phase-3 `combinedPriority` (normalized by `MaxPossiblePriority`) exceeds `UrgencyThreshold` and
  private reliance exceeds `VoteRelyThreshold`.

## Dependencies (politics-relevant)

- `engine/core` — `Function` (`FuncSafety`/`FuncKnowledge`), `Signal`/`SignalVote`, `AgentID`,
  `StatID`, `Dimension`, `EventEmitter`, `Event`. Emits `BeliefUpdated` (reliance) and
  `ReputationGossip` (Influence-weighted gossip).
- `engine/tom` — `ToM`: `AdjustRelyOn`/`BestProviderFor`/`Influence`/`GossipUpdate` (P6 reliance +
  Influence-weighted gossip), `Self`/`Subjects`/`SelfID`. `Belief`.
- `engine/perception` — the senses; the candidate-gathering for `BestProviderFor` draws on perceived
  others (the agent set surfaced via `WorldView.AgentIDs()` / `IncomingSignals`).
- **Contract — NOT imported**: `engine/world` implements `WorldView` (dependency inversion):
  `AgentIDs()` and `IncomingSignals(self)` (votes + hearsay/gossip) are the P6 additions politics
  reads.
- **Contract**: `content/balance.yaml politics.*` (`rely_cost_threshold`, `relyon_delta`,
  `vote_rely_threshold`, `urgency_threshold`, `vote_relyon_delta`, `influence_weight` →
  `Config.RelyCostThreshold`/`RelyOnDelta`/`VoteRelyThreshold`/`UrgencyThreshold`/`VoteRelyOnDelta`/
  `InfluenceWeight`); `Config.Functions` from `content/glossary` (the Reliance edges) via
  platform/config. Injected via `platform/config` and wired in `backend/main.go`
  (`agentConfigFromBalance`). No constant hardcoded (D10).

## Acceptance Criteria (testable)

### Scenario G (P6) — Reliance convergence, Vote, and Influence

- [ ] **Function resolved from `Config.Functions` (gap-closure)**: with a stub `Config.Functions =
  [{ID: FuncSafety, Dim: SafetyDim, Stats: [StrengthID]}, {ID: FuncKnowledge, Dim: KnowledgeDim,
  Stats: [IntelligenceID]}]`, `handleRelianceTrigger` pursuing a `SafetyDim` goal resolves Function
  `FuncSafety` and passes `[StrengthID]` to `BestProviderFor`; pursuing `KnowledgeDim` resolves
  `FuncKnowledge` with `[IntelligenceID]`; pursuing a Dimension absent from the table resolves no
  Function and forms NO reliance. Asserts the removed `goalToFunction` fallback is gone.
- [ ] **Reliance forms on a self-unsolvable Function (P6)**: an agent pursuing a `Safety`-Function
  goal that the planner reports unreachable OR whose cheapest plan cost > `Config.RelyCostThreshold`
  calls `ToM.AdjustRelyOn(provider, FuncSafety, RelyOnDelta)` toward `BestProviderFor`, and emits one
  `BeliefUpdated{cause:"reliance"}` (via the `emit` now passed into `handleRelianceTrigger`). When
  the agent CAN self-solve cheaply, no reliance forms and no event is emitted.
- [ ] **Vote uses the phase-3 combined urgency (gap-closure)**: `emitVoteIfEligible(now, world,
  combinedPriority)` emits a `SignalVote` iff `bestRelyOnFor(f).strength > VoteRelyThreshold` AND
  `combinedPriority > Config.UrgencyThreshold`; it does NOT recompute a separate distributed urgency
  (the `distributedUrgency` helper is removed). Table-driven over {high, low} `combinedPriority` and
  {above, below} reliance strength.
- [ ] **Vote accelerates convergence under distributed urgency (P6, CONTRAST)**: two runs of the
  same fixture. (a) HIGH distributed urgency (all members' Safety low at once → high
  `combinedPriority`) → agents emit `Vote` and the guardian's received-RelyOn share for `Safety`
  crosses `role_convergence_threshold` in markedly fewer ticks than (b) LOW urgency (no votes).
  Assert `ticks_to_converge(high) < ticks_to_converge(low)`.
- [ ] **Influence-weighted gossip moves ToM more (P6, TABLE)**: a receiver folds the SAME claim
  about C from two sources with equal `Trust` but different Influence in `processIncomingSignals`,
  using `signalWeight = clamp01(trust·(1 + InfluenceWeight·Influence(source, f, observers)))` and
  then `GossipUpdate(C, sourceBeliefAboutC, signalWeight)`. Assert the per-stat mean delta is
  monotonically non-decreasing in Influence for each `InfluenceWeight > 0`, and the
  `InfluenceWeight = 0` row is flat (Influence has no effect). Asserts `processIncomingSignals`
  handles non-Vote gossip signals (not only votes).

### Scenario C Extension — Gossip Trust-Cluster Propagation (P4)

(unchanged from the prior batch — gossip propagates within the trust cluster, non-witness cluster
unchanged, cluster reputation divergence variance > 0, `ReputationGossip` events emitted, golden at
seed 303.)

> The Influence-weighting fold (§I) is the politics-side hook into this propagation: a heard gossip
> signal routed through `processGossipSignal` scales credibility by the source's Influence before
> `GossipUpdate`, per the "Influence-weighted gossip moves ToM more" table AC above.

> Structural JSON-schema validation of the `content/balance.yaml politics:` block this module reads
> is a **platform/config** AC.

## Invariants (politics-relevant)

- **Roles are emergent, not hardcoded (D2)**: reliance is `RelyOn` accretion with **no
  role/chief/faction type** anywhere; the agent never labels anyone a chief — convergence is a
  cluster of `RelyOn` edges the world reads as a `RoleEmerged` statistic. `Vote` is the ordinary
  social-signal acceleration of that accretion, not a special institution.
- **No hardcoded Function mapping (D7/D10)**: the goal→Function resolution reads `Config.Functions`
  via `functionForGoal`, never the removed `goalToFunction` "Safety→FuncSafety else FuncKnowledge"
  literal; the capability stat-set passed to `BestProviderFor` is the matched `FunctionSpec.Stats`,
  not a literal. `core.FuncKnowledge` is used as the content-canonical gossip-credibility function in
  `processGossipSignal` (a core-defined id, not a string literal in logic).
- **All thresholds / ratios injected (D10)**: no literal for the politics ratios
  (`RelyCostThreshold`/`RelyOnDelta`/`VoteRelyThreshold`/`UrgencyThreshold`/`VoteRelyOnDelta`/
  `InfluenceWeight`); each is read from `balance.yaml politics.*` and consumed off `a.Cfg`.
- **Decisions read `ToM[self]` / ToM beliefs, never Real Stats (D8)**: reliance reads ToM beliefs
  (`BestProviderFor`/`Influence`/`Self`/`Subjects`), never Real Stats; `bestRelyOnFor` and
  `allBeliefs` iterate `ToM.Subjects()`.
- **Determinism (D12)**: reliance candidates are sorted by AgentID before `BestProviderFor`; the
  `FunctionSpec.Stats` set passed to `BestProviderFor` is sorted (`sortStatIDs`); `Config.Functions`
  is scanned in its authored (fixed-table) order; `allBeliefs` / `bestRelyOnFor` iterate sorted
  `Subjects()`; observers for Influence weighting are the sorted `allBeliefs()` slice. No rng draw
  selects the Function, the provider, the vote, or the gossip weight.
- **Bounds**: `signalWeight ∈ [0, 1]` (clamp01); `RelyOn` values stay in `[0,1]` (enforced by
  `tom.AdjustRelyOn`).

## Open Questions (politics-relevant)

- **Gossip-event emission threading (gap-closure follow-up).** `processIncomingSignals` does not
  currently take an `emit core.EventEmitter`; the Influence-weighted gossip fold (§I-fold) should
  emit one `ReputationGossip` event per changed stat (the P4 contract). If that emission is in scope
  this batch, add `emit` to `processIncomingSignals`' signature and thread it from `Tick`'s phase 0;
  otherwise fold silently and flag to the reviewer. Flag to architect.
- **Vote urgency threshold field name (gap-closure).** §H standardizes on `Config.UrgencyThreshold`.
  If the current code carries `Config.VoteUrgencyThreshold`, reconcile to the single
  `UrgencyThreshold` field (and the `politics.urgency_threshold` balance key) so there are not two
  near-duplicate thresholds. Non-blocking; document the chosen name in the Data Contract.
