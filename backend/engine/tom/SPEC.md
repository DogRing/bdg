# SPEC — `engine/tom`

> Status: `P4` — gossip cluster divergence + ReputationGossip event wiring added
> Leaf level: `L2`  ·  Owner agent: `implementer`

## Purpose

Owns each agent's **Theory of Mind**: a per-subject `Belief` (glossary: `Belief` = `ToM[X]`)
holding a per-stat estimate (mean + uncertainty) of every other agent it knows, **and of
itself** (`ToM[self]`, D8). This is the reputation substrate. **Reputation is never stored as
a single value (D6)** — it is the *distribution* of `ToM[C]` across observers, derived on
demand by `ReputationDist`. The module updates beliefs from direct observation (`Observe`),
from hearsay (`GossipUpdate`), and seeds an initial estimate for an unknown subject from the
observer's own perception capability.

## Public Interface

```go
package tom

import (
    "github.com/dogring/bdg/backend/engine/core"
    "github.com/dogring/bdg/backend/engine/rng"
    "github.com/dogring/bdg/backend/engine/stats"
)

// SubjectID is who a belief is *about*. It aliases core.AgentID; self-belief is keyed by the
// owning agent's own id (ToM[self], D8).
type SubjectID = core.AgentID

// StatDist is the mean/variance summary of a stat estimate (the task's required type).
// Variance ≥ 0 encodes uncertainty: high variance = low confidence (a fresh / hearsay belief);
// it shrinks toward 0 as direct evidence accumulates.
type StatDist struct {
    Mean     float64
    Variance float64
}

// Belief is one subject-model (glossary: Belief = ToM[X], fields EstStats, Trust, Affinity,
// Ledger, RelyOn, LastSeen). This SPEC fixes the reputation-bearing core; relational fields
// (Affinity, Ledger, RelyOn) are carried but updated by their owning modules (see Out of Scope).
type Belief struct {
    EstStats    map[core.StatID]StatDist // per-stat estimate (mean + uncertainty)
    Trust       float64                  // credibility weight applied to THIS subject's claims
                                         // when it gossips (the "ToM[A].credibility" the brief
                                         // names). In [0,1].
    Affinity    float64                  // signed relational stance; updated by engine/agent
    LastSeen    core.Tick                // tick of the last direct observation (glossary)
    // P6: reliance edges — how much THIS observer relies on `subject` for each Function
    // (safety/judgment/knowledge…). The keys are core/content Function ids (D7: never a
    // hardcoded enum here). Updated only via AdjustRelyOn (primitive below); the DECISION
    // of when/whom to rely on lives in engine/agent (see P6 Reliance & Influence Contract).
    RelyOn      map[Function]float64
    // Ledger: reserved, owned/updated by engine/agent + engine/values (see Out of Scope).
}

// ToM is one agent's whole theory-of-mind: subject id → Belief, INCLUDING self.
// It wraps an internal map for storage/lookup; per D12 code MUST NOT range it for logic —
// iterate via Subjects() (sorted) instead. The struct carries the owning agent's id (for D8
// enforcement), the injected rate constants, and a registry reference for stat clamping.
type ToM struct { /* opaque: selfID, map[SubjectID]Belief, rates, *Registry */ }

// Rates bundles the numeric rate constants that govern tom update rules.
// These are injected by the caller (read from content/balance.yaml), never hardcoded (D10).
type Rates struct {
    Alpha              float64 // gossip.alpha (default 0.12)
    Beta               float64 // self_calibration.beta (default 0.08)
    MinTrust           float64 // gossip.min_trust (default 0.05)
    InitialBeliefNoise float64 // generation.initial_belief_noise (default 0.15)
    // P2 trade protocol — content/balance.yaml trade.*
    TradeSuccessTrustDelta  float64 // Trust increase per successful trade (default 0.05)
    TradeRejectAffinityDrop float64 // Affinity drop on rejection, magnitude (default 0.02)
    FraudHonestyDrop        float64 // Honesty mean drop on detected fraud (default 0.10)
    FraudThreshold          float64 // claimedValue−actualEffect above which fraud fires (default 0.20)
    // P6 influence — content/balance.yaml politics.influence_weight
    InfluenceWeight float64 // reflection ratio for influence-amplified gossip; [0,1] (default 0.5)
}

// DefaultRates returns the canonical rate constants from content/balance.yaml.
func DefaultRates() Rates

// NewToM constructs an agent's ToM, seeded with its self-belief (ToM[self], D8). `self` is the
// owning agent's id; `realStats` are the agent's Real Stats (god view) used ONLY to seed the
// self-estimate with calibrated noise; `selfPerception` is the perception capability (typically
// Intelligence from ToM[self], see Notes) governing initial uncertainty; `rng` is the injected
// seeded generator (D12) — no global rand; `reg` is the stat registry for clamping; `rates` are
// the four rate constants injected by the caller. The self-belief's EstStats are NOT exact: they
// are realStats perturbed per rates.InitialBeliefNoise, producing the over/under-estimation
// asymmetry of D8.
func NewToM(self core.AgentID, realStats stats.Stats, selfPerception float64, rng *rng.RNG, reg *stats.Registry, rates Rates) ToM

// Observe folds DIRECT evidence about `subject` into the observer's belief, per stat. Direct
// observation is the ONLY mechanism that updates ToM[self] (D8: calibration by action). Each
// observed stat's StatDist is updated by the self-calibration / precision-weighted rule (see
// Invariants); variance shrinks toward 0. Creates the belief (via the initial-estimate seed) if
// `subject` is unknown. Sets LastSeen = ev.Tick.
func (t ToM) Observe(subject core.AgentID, ev StatEvidence)

// StatEvidence is one direct-observation datum: the outcome the observer attributes to a stat.
type StatEvidence struct {
    Stat     core.StatID
    Observed float64    // the value implied by the observed action/outcome (in stat units)
    Weight   float64    // precision of this evidence in [0,1]; scales the update (β·Weight)
    Tick     core.Tick
}

// GossipUpdate folds HEARSAY about `subject`, told by a source whose model of the subject is
// `source` and whose credibility to this agent is `trustWeight`. Per the glossary gossip rule
// (D6 substrate), each stat's mean shifts toward the source's claimed mean weighted by trust:
//
//     ToM[subject].EstStats[s].Mean += α · trustWeight · (source.EstStats[s].Mean − ToM[subject].EstStats[s].Mean)
//
// where α = balance.gossip.alpha. Hearsay is INFLATIONARY on uncertainty (variance does not
// shrink to zero from gossip; see Invariants). If trustWeight < balance.gossip.min_trust the
// claim is ignored (no-op). Creates the belief from the initial seed if `subject` is unknown.
// GossipUpdate NEVER touches ToM[self] (self is only calibrated by Observe, D8).
//
// Returns the per-stat mean delta it applied (newMean − oldMean), keyed by StatID, for the
// caller (engine/agent) to emit one ReputationGossip event per changed stat (see P4 Gossip
// Propagation Contract + Out of Scope). A no-op call (below min_trust, or self) returns an empty
// map — the caller emits nothing.
func (t ToM) GossipUpdate(subject core.AgentID, source Belief, trustWeight float64) map[core.StatID]float64

// ReputationDist DERIVES the reputation distribution of `subject` across the supplied observer
// models (D6: reputation is NEVER a stored single value — it is this aggregate). It returns,
// per stat, the StatDist whose Mean is the across-observer mean of each observer's estimated
// mean, and whose Variance is the across-observer variance of those means (the spread of
// opinion — the contested-reputation signal). `observers` is each observer's Belief ABOUT the
// subject (the caller gathers ToM_X[subject] for the relevant X set).
func (t ToM) ReputationDist(subject core.AgentID, observers []Belief) map[core.StatID]StatDist

// Subjects returns all subject ids known to this ToM in canonical fixed order (sorted
// lexicographically by AgentID). The ONE ordering for iteration (D12). Returned slice is a copy.
func (t ToM) Subjects() []SubjectID

// Self returns the self-belief (ToM[self]) for owner id `self`, and whether it exists.
func (t ToM) Self(self core.AgentID) (Belief, bool)

// SelfID returns the owning agent's ID (the value passed as `self` to NewToM).
func (t ToM) SelfID() core.AgentID

// ── Trade outcome methods (P2) ───────────────────────────────────────────────

// RecordTradeSuccess increments the Trust for `other` by rates.TradeSuccessTrustDelta,
// clamped to [0, 1]. Must be called on EACH agent's own ToM independently (양측 모두).
// Seeds the belief for `other` if not yet known. Sets LastSeen = tick.
func (t ToM) RecordTradeSuccess(other core.AgentID, tick core.Tick)

// RecordTradeRejection decrements Affinity for `other` by rates.TradeRejectAffinityDrop.
// Called by the REJECTED agent on its own ToM after the counterparty declines.
// Seeds the belief if not yet known. Sets LastSeen = tick.
func (t ToM) RecordTradeRejection(other core.AgentID, tick core.Tick)

// RecordFraud lowers the EstStats[honestyStatID].Mean for `other` by rates.FraudHonestyDrop
// when claimedValue − actualEffect > rates.FraudThreshold (directed: overstating is fraud;
// a pleasant surprise is not). No-op if within threshold or honestyStatID not registered.
// Seeds the belief if not yet known. Sets LastSeen = tick.
func (t ToM) RecordFraud(other core.AgentID, claimedValue, actualEffect float64, honestyStatID core.StatID, tick core.Tick)
```

> Numeric constants (`α = balance.gossip.alpha`, `min_trust = balance.gossip.min_trust`,
> `β = balance.self_calibration.beta`, `initial_belief_noise = balance.generation.initial_belief_noise`)
> are **injected** by the caller (`engine/agent`), read from `content/balance.yaml` via
> `platform/config`. This module hardcodes no rate (D10).

## Dependencies

- `engine/core` — `AgentID`, `StatID`, `Tick`.
- `engine/stats` — `Stats`, `*Registry` (to range over stat ids in `IDs()` order, seed/clamp
  estimates against each stat's range).
- `engine/rng` — `*RNG` (injected) for the calibrated self-belief noise at construction (D12).
  No global rand, no `time.Now()`.
- **Contract**: `content/balance.yaml` (`gossip.alpha`, `gossip.min_trust`,
  `self_calibration.beta`, `generation.initial_belief_noise`) supplies the rates, injected by
  the caller. The gossip formula is fixed in `docs/glossary.md` §Social.

## Owned Data

- `ToM`, `Belief`, `StatDist`, `StatEvidence` value types and the update logic. A `ToM` map is
  owned by the agent that holds it (`engine/agent` / `engine/world` state); this module defines
  the shape and the mutating methods. `Observe`/`GossipUpdate` mutate the **receiver's own** ToM
  only; they never reach into another agent's ToM.
- `EstStats` `StatDist`s are owned by the belief; `ReputationDist` returns a **fresh** derived
  map (never a stored value, D6).

## Invariants

- **Reputation is a distribution, never a stored scalar (D6)**: there is **no** `Reputation
  float64` field anywhere. The only way to get "reputation" is `ReputationDist`, which derives
  the across-observer mean/variance on demand. The *spread* (variance across observers) is a
  first-class output — contradictory faction reputations are the core emergent signal and must
  not be averaged away into a single number at rest.
- **Self-perception is `ToM[self]`, calibrated only by action (D8)**: `ToM[self]` is seeded at
  construction from Real Stats + calibrated noise and is updated **only** by `Observe` (direct
  attempt/outcome evidence), never by `GossipUpdate`. **Underestimation is self-sealing and MUST
  NOT be corrected**: this module provides **no** routine that nudges `ToM[self]` toward Real
  Stats. If an agent underestimates a stat, it acts on the lower value, may not attempt the
  action that would generate disconfirming evidence, and so the estimate never rises — that
  asymmetry is intentional (it generates impostors / timidity for free). The only path to
  correction is the agent actually attempting and succeeding, which is `engine/agent`'s decision,
  not a self-correction here.
- **Gossip update formula (deterministic, float64)**: exactly the glossary rule
  `mean += α · trustWeight · (claim − mean)` per stat, with `α = balance.gossip.alpha`. Ignored
  when `trustWeight < balance.gossip.min_trust`. Order-independent per stat; the receiver
  iterates stats in `stats.Registry.IDs()` order (D12). Means are clamped to the stat's
  `[Min,Max]` range after update.
- **Observe update rule (precision-weighted, float64)**: each observed stat's
  `mean += β · Weight · (Observed − mean)` with `β = balance.self_calibration.beta`, and its
  `Variance` shrinks by a factor of `(1 − Weight)` (or an equivalent precision-blend) toward 0 —
  direct evidence increases confidence. Means clamped to range.
- **Gossip does not manufacture confidence**: `GossipUpdate` moves the mean but does **not**
  drive `Variance` to 0 (hearsay stays uncertain) — variance is held or mildly increased, never
  collapsed. This keeps the observer-spread in `ReputationDist` meaningful.
- **Initial estimate seeded from observer's own perception**: a previously-unknown subject `X`
  gets `EstStats[s] = StatDist{Mean: priorMean(s), Variance: f(selfPerception)}` where higher
  `selfPerception` (Intelligence) → **lower** initial variance (more confident first guess). The
  exact form: `Variance = baseVar · (1 − clamp(selfPerception,0,1))` (so perfect perception →
  near-zero prior variance; zero perception → `baseVar`), with `baseVar` derived from
  `initial_belief_noise²`. `priorMean` defaults to the stat's range midpoint (no information).
  Specified precisely so two implementations agree.
- **Determinism (D12)**: all stat/subject iteration uses sorted keys (`stats.Registry.IDs()`,
  `Subjects()`), never `map` ranging for logic. The only randomness is the injected `*rng.RNG`
  used at construction; given the same seed and call order the constructed ToM is byte-identical.
  `Observe`/`GossipUpdate`/`ReputationDist` use **no** RNG.
- **Mutation locality**: a `ToM` method mutates only its receiver. It never reads or writes
  another agent's ToM; `GossipUpdate` takes the source's `Belief` *by value*.
- **Bounds**: every `StatDist.Mean` stays within the subject stat's `[Min,Max]`; `Variance ≥ 0`;
  `Trust`, `Affinity` ∈ their declared ranges.
- **No values dependency (architecture §4)**: `tom` does NOT import `engine/values`
  (`values → tom` is one-way). This module knows nothing about goals or EffValue.

## P4 Gossip Propagation Contract

Specifies how hearsay propagates through a **trust graph** so that disconnected trust clusters
hold divergent reputations indefinitely (the D6 factional-contradiction signal). This module
provides only the *fold* (`GossipUpdate`); the *who-tells-whom* sequencing is owned by
`engine/agent` (signal emission, tick phase 6) and `engine/world` (signal delivery, phase 4e).
The contract below fixes the joint behaviour so all three modules agree.

- **Source → receiver fold (one call site).** When an agent `A` delivers a Signal to agent `B`
  (engine/agent tick phase 6 → engine/world phase 4e), `B` folds `A`'s model of each subject `C`
  carried in the Signal context by calling, on `B`'s own ToM:

  ```
  delta := B.ToM.GossipUpdate(C, A_belief_of_C, trustWeight = B.ToM[A].Trust)
  ```

  where `A_belief_of_C` is obtained by the caller via `WorldSnapshot.BeliefOf(A, C)` (the
  source's `Belief` about `C`), and `B.ToM[A].Trust` is the receiver's credibility in the source.
  The folding math is `tom`'s; the gather of the source belief and the trust weight is the
  caller's (`engine/agent` / `engine/world`).

- **Trust bridge (cluster-isolation corollary of the existing min_trust no-op).** If there is no
  direct trust edge between source and receiver — `B.ToM[A].Trust < balance.gossip.min_trust` —
  `GossipUpdate` is a no-op and returns an empty delta map. Gossip therefore **cannot cross a
  zero-trust gap**: the existing min_trust no-op invariant, read at the graph level, *is* the
  cluster boundary. No special-case code is required to isolate clusters; the boundary falls out
  of the per-edge trust gate.

- **Cluster divergence (D6 corollary — MUST PERSIST).** Two agents in disconnected trust clusters
  (no chain of `≥ min_trust` trust edges between them) will hold **divergent** `ToM[C]` estimates
  indefinitely. This divergence **MUST NOT be collapsed, averaged, reconciled, or "corrected"**
  anywhere — not in `tom`, not in `engine/agent`, not in `engine/world`. It is the mechanism that
  produces factional reputation contradiction. The only place the divergence surfaces as a single
  number is `ReputationDist`, and there it surfaces as **variance** (the spread of opinion), never
  as a collapsed mean. There is no anti-divergence routine in this module by design.

- **Propagation is one-hop per tick.** `A` gossips to `B` in tick `T`; `B`'s updated `ToM[C]`
  becomes part of tick `T+1`'s snapshot, from which `B` may in turn gossip onward. There is **no
  transitive multi-hop fold within a single tick** — a claim cannot travel `A → B → D` in one
  tick. This bounds per-tick work and keeps the propagation order independent of agent-apply order
  within the tick (D12: only the snapshot read at tick start feeds gossip; mid-tick belief changes
  are not visible to other folders until the next snapshot).

- **`GossipUpdate` never touches `ToM[self]` (reinforced).** Self-belief is calibrated only by
  `Observe` (D8). A `GossipUpdate` whose `subject == receiver.SelfID()` is a no-op and returns an
  empty delta map — hearsay about oneself never edits `ToM[self]`. This is checked before the
  min_trust gate so the self-protection holds regardless of trust weight.

## P6 Reliance & Influence Contract (emergent institutions)

P6 turns the carried-but-inert `RelyOn` field into the substrate for emergent roles and political
weight. `tom` owns the **storage and the two pure primitives** (the reliance increment and the
Influence aggregate); it does NOT decide *when* to rely or *how* signals are weighted — those
triggers read gates/costs and live in `engine/agent` (mirrors the Affinity / GossipUpdate split).

```go
// Function names a service one agent can rely on another to provide (glossary: Reliance edge).
// D7/D2: these are content/glossary ids (e.g. "safety", "judgment", "knowledge"), NEVER a
// hardcoded enum or role type — a role is the EMERGENT cluster of RelyOn edges, not a Function.
// Canonical declaration is core.Function (the L0 leaf); tom aliases it so map keys interoperate.
type Function = core.Function

// AdjustRelyOn folds a reliance increment toward `target` for `function` into the observer's
// belief about `target`: RelyOn[function] += δ, clamped to [0,1]. Seeds the belief (initial
// estimate) if `target` is unknown. δ may be negative (reliance decays / shifts away). This is
// the ONLY mutator of RelyOn; the WHEN/WHOM decision is engine/agent's (see below). Mirrors
// AdjustAffinity: a storage primitive, no policy.
func (t ToM) AdjustRelyOn(target core.AgentID, function Function, delta float64)

// BestProviderFor ranks `candidates` as reliance targets for `function` and returns the best
// (trusted AND competent) plus its score, or ("", 0) if none qualifies. Score combines this
// observer's Trust in the candidate with the candidate's perceived competence for the function's
// stat set (EstStats; D7 — the caller passes which stats the function draws on, no literal here).
// Deterministic: candidates are evaluated in sorted AgentID order; ties break by lower id (D12).
func (t ToM) BestProviderFor(function Function, statSet []core.StatID, candidates []core.AgentID) (core.AgentID, float64)

// Influence DERIVES the social weight of `subject` for `function`: the mean of
// RelyOn[function] across the supplied observer Beliefs about subject (D6-style:
// Influence is NEVER a stored scalar — it is this aggregate over the RelyOn
// distribution, exactly as reputation is an aggregate over EstStats). `observers`
// is each agent's Belief ABOUT `subject` (the caller gathers ToM_X[subject]).
// If observers is empty, returns 0. Per-function only (caller iterates functions).
// The caller scales it by balance influence_weight when folding a signal (below).
func (t ToM) Influence(subject core.AgentID, function Function, observers []Belief) float64
```

**How Influence reaches signal weight (kept out of `GossipUpdate`'s signature).** `GossipUpdate`
already weights a hearsay claim by `trustWeight`. P6 does NOT change that signature: the **caller**
(`engine/agent`, at the signal-fold site) pre-scales the weight by the source's Influence —

    signalWeight = clamp01( trustWeight · (1 + influence_weight · Influence(source, observers)) )

then calls `GossipUpdate(subject, sourceModel, signalWeight)`. `influence_weight`
(`content/balance.yaml politics.influence_weight`) is the reflection ratio. Consequence (the table
AC): with equal `trustWeight` and claim, a higher-Influence source yields a larger per-stat mean
delta. `tom` owns the `Influence` derivation; the agent owns the application — no new GossipUpdate
parameter, no hidden global.

**Invariants (P6).**
- `Influence` and "role" are **derived, never stored** (D6 generalized): no `Influence` field on
  `Belief`, no `Role`/`Chief` type anywhere in `tom`. A struct/grep guard confirms it.
- `AdjustRelyOn` is the sole RelyOn mutator; `RelyOn` values stay in `[0,1]`; all folds and the
  Influence sum iterate `Subjects()` / sorted Function order (D12 — no map ranged for logic).
- `Function` carries no behavior — it is an opaque id. No code branches on a specific Function
  literal (D2/D7); the safety/judgment/knowledge split is content, surfaced via the stat set the
  caller passes to `BestProviderFor`.

## Acceptance Criteria (testable)

- [ ] **Self-belief seeded with calibrated noise (D8)**: `NewToM(self, realStats, …, rng, reg)`
  creates `ToM[self]` whose `EstStats` means differ from `realStats` by noise scaled to
  `initial_belief_noise`; with a fixed seed the noise is reproducible (golden). The self-belief
  is keyed by `self`.
- [ ] **No self-correction routine exists (D8 invariant)**: a struct/API guard confirms there is
  **no** exported method (and no internal call from `GossipUpdate`) that moves `ToM[self]` toward
  `realStats`. A scenario test: an agent that underestimates a capability and never "attempts"
  (no `Observe` call) keeps the low estimate indefinitely (self-sealing preserved).
- [ ] **Observe raises confidence and calibrates the mean (D8)**: repeated `Observe` of a stat
  with `Observed` above the current mean moves the mean toward `Observed` by `β·Weight` per
  step and monotonically shrinks `Variance` toward 0 (table-driven + golden on a fixed sequence).
- [ ] **Gossip formula exact (D6 substrate)**: `GossipUpdate` shifts the subject mean by
  `α·trustWeight·(claim − mean)` per stat — assert to float tolerance against a hand-computed
  table. `trustWeight < min_trust` is a no-op. `GossipUpdate` never touches `ToM[self]`.
- [ ] **Gossip does not collapse variance**: after `GossipUpdate`, the subject's `Variance` is
  unchanged or higher, never driven to 0 (distinguishes hearsay from direct evidence).
- [ ] **Reputation is derived, never stored (D6)**: there is no reputation scalar field
  (struct-shape guard). `ReputationDist(subject, observers)` returns per-stat `StatDist` whose
  `Mean` is the across-observer mean of means and `Variance` is the across-observer variance —
  a table with 3 observers holding divergent means yields a high-variance (contested) reputation;
  identical observers yield ~0 variance. Golden over a fixture set.
- [ ] **Initial estimate vs perception**: a higher `selfPerception` yields a strictly lower
  initial `Variance` for an unknown subject (table-driven: perception 0.0, 0.5, 1.0). `priorMean`
  defaults to the stat range midpoint when there is no information.
- [ ] **Means clamped to range**: after `Observe`/`GossipUpdate`, no `StatDist.Mean` exceeds the
  stat's `[Min,Max]` (property test against a stub `stats.Registry`).
- [ ] **Determinism (D12)**: `Subjects()` and all per-stat folds use sorted order; a golden test
  builds a ToM, applies a fixed sequence of `Observe`/`GossipUpdate`, and reproduces a recorded
  digest (ties to data-contracts §1 `tom_digest`). Two runs with the same seed/inputs are
  byte-identical.
- [ ] **Mutation locality**: `GossipUpdate(source)` does not mutate `source` (passed by value);
  a method on ToM A never mutates ToM B (property test).
- [ ] **Trade success raises Trust (P2)**: after `RecordTradeSuccess(other)` on both ToM A and ToM B,
  `ToM_A[B].Trust` and `ToM_B[A].Trust` are each strictly greater than their pre-call values;
  delta equals `rates.TradeSuccessTrustDelta`; clamped to [0, 1].
- [ ] **Trade rejection drops Affinity (P2)**: after `RecordTradeRejection(other)`, the rejecting
  agent's `ToM[other].Affinity` is strictly less than before; delta equals `rates.TradeRejectAffinityDrop`.
- [ ] **Fraud detection lowers Honesty estimate (P2)**: when `claimedValue − actualEffect > rates.FraudThreshold`,
  `ToM[other].EstStats[honestyStatID].Mean` is strictly less than before; within threshold is a no-op.
- [ ] **No fraud below threshold**: `RecordFraud` with `claimedValue − actualEffect ≤ FraudThreshold`
  leaves EstStats unchanged.
- [ ] **Gossip trust-cluster isolation (P4)**: three agents A, B, C. A witnesses an event that sets
  `ToM_A[X][s].Mean` to a non-midpoint value. A gossips to B (`Trust_B[A] = 0.8 ≥ min_trust`): B's
  `ToM[X][s].Mean` shifts toward A's claim. A does **not** interact with C
  (`Trust_C[A] < min_trust`): C's `ToM[X][s].Mean` is **unchanged**. After the gossip the two
  observers disagree. Verified by a table-driven scenario test with a golden on the per-observer
  delta. (This is **Scenario C, gossip extension** — the end-to-end propagation/event assertions
  live in `engine/agent/SPEC.md` "Scenario C Extension — Gossip Trust-Cluster Propagation (P4)".)
- [ ] **Variance preserved across cluster boundary (P4)**: `ReputationDist(X, []Belief{B_of_X,
  C_of_X})` over the post-gossip beliefs returns a **HIGH** variance for stat `s` — the spread
  between B's updated mean and C's unchanged mean. Threshold: `Variance > |delta_B| / 2` (the
  cross-observer spread is at least half the shift B took). Confirms the factional-disagreement
  signal is measurable and is **not** collapsed (D6).

> Structural validation of `content/balance.yaml` (the rate keys) is a **platform/config** AC.
> This module proves only behaviour reachable from injected rates + an injected `*rng.RNG`.

- [ ] **`AdjustRelyOn` folds and clamps (P6)**: `AdjustRelyOn(target, "safety", δ)` on an unknown
  target seeds the belief and sets `RelyOn["safety"] = clamp01(δ)`; a second call accumulates and
  clamps at 1.0; a negative δ decays it (floor 0). `RelyOn` is untouched on other functions/targets.
- [ ] **`Influence` is the mean received-reliance per function (P6)**: given observer beliefs about
  `subject` with `RelyOn["Safety"]` edges {A→subject:0.6, B→subject:0.3, C→subject:0.0},
  `Influence(subject, "Safety", observers)` returns (0.6+0.3+0.0)/3 = 0.3. A set of observers with no
  RelyOn for a function returns 0. Iteration order does not change the result (D12).
- [ ] **Influence-weighted gossip moves beliefs more (P6, TABLE)**: holding `trustWeight`, the source
  claim, and α fixed, fold the SAME claim about C from a HIGH-Influence source vs a LOW-Influence
  source using `signalWeight = clamp01(trustWeight·(1 + influence_weight·Influence))`. Assert
  `|Δmean_high| > |Δmean_low|` across rows {Influence ∈ 0, 0.5, 2.0} × {influence_weight ∈ 0.25, 0.5}.
  At `influence_weight = 0` the two collapse to the plain trust-weighted delta (Influence has no
  effect) — the control row.
- [ ] **No stored Influence / role type (P6, D6/D2 guard)**: grep/struct guard — no `Influence`
  field on `Belief`, no `Role`/`Chief`/`Faction` type in `engine/tom`; Influence exists only as the
  `Influence(...)` derivation.

## Out of Scope

- **Deciding to attempt an action** (which is what eventually feeds `Observe` and breaks—or
  fails to break—self-sealing) → `engine/agent` + `engine/planner`.
- **Reading `ToM[self]` for gate/cost decisions** → `engine/gates` consumes the self-stat values
  (this module produces them; gates do not import tom — the agent passes `ToM[self]` into the
  gate `AgentSnapshot`).
- `Affinity` / `Ledger` *update* dynamics (resentment, ledgers) → `engine/agent` + `engine/values`.
- **The reliance *trigger* (P6)** — deciding that a Function is hard to self-solve (gate-blocked or
  cost > threshold) and therefore calling `AdjustRelyOn` toward `BestProviderFor(...)` → `engine/agent`
  (it alone sees gates/costs). `tom` owns RelyOn **storage** (`AdjustRelyOn`), provider ranking
  (`BestProviderFor`), and the `Influence` aggregate — the primitives, not the policy. Likewise
  emergent **roles** as reliance clusters are detected in `engine/world` over the `RelyOn`
  distribution `tom` exposes; `tom` defines no role type (D2).
- `Signal{Kind, Intent, Valence, ClaimedValue, Truth, Intensity}` construction and the
  truth/deception layer → `engine/agent` interaction (a gossip claim's `Truth` is hidden; this
  module just folds the claimed mean it is handed).
- **`BeliefUpdated` event emission** (data-contracts §4) → emitted by the caller (`engine/agent`)
  via the `core.EventEmitter`; this module returns the per-stat deltas it makes available for that
  trace but does not emit.
- **`ReputationGossip` event emission** (data-contracts §4) → the emission spec lives in
  `platform/events/SPEC.md`; `engine/agent` emits it via the injected `core.EventEmitter` after
  each `GossipUpdate` call, passing `{about: C, from: A, trust: trustWeight, stat: s, delta:
  (newMean − oldMean)}` **per stat that changed** (the deltas `GossipUpdate` returns). A no-op
  gossip (below `min_trust`, or self) returns an empty delta map and emits nothing. `tom` itself
  never emits.
- The compact `tom_digest` serialization (top-K relationships; data-contracts §1/§3) →
  `platform/persist` reads `Subjects()`/`Belief` to build it.

## Open Questions

- **`selfPerception` source (NOT blocking P1).** The brief's `NewToM` signature passes `stats`;
  this SPEC threads an explicit `selfPerception float64` (the perception capability governing
  initial uncertainty) plus the `stats.Registry` for clamping, rather than re-deriving "which
  stat is perception" inside `tom` (D7: no hardcoded stat names). The caller (`engine/agent`)
  computes `selfPerception` from `ToM[self][Intelligence]` (glossary: Intelligence = ToM modeling
  depth) and passes it in. Confirm Intelligence is the intended perception axis; if a composite
  is wanted, the agent composes it and still passes a scalar — no `tom` change.
- **`StatDist.Variance` aggregation in `ReputationDist`.** This SPEC defines reputation variance
  as the **across-observer variance of observer means** (spread of opinion). An alternative is to
  also fold each observer's own per-stat `Variance` (total variance = between + within). The
  between-observer spread is the D6-critical signal (factional disagreement), so it is the
  specified default; folding within-observer variance can be added later without breaking the
  signature (same return type). Flag if the within term is wanted for P1.
- **`GossipUpdate` return type addition (NOT blocking; P4).** P4 changes `GossipUpdate` to return
  `map[core.StatID]float64` (the per-stat mean deltas) so `engine/agent` can emit one
  `ReputationGossip` event per changed stat without re-reading the belief before/after. This is
  additive for the engine math (the fold is unchanged) but is a signature change any existing
  caller must adopt. Confirm before the implementer touches existing `GossipUpdate` call sites.

## Notes

- `Belief` mirrors glossary `Belief` (= `ToM[X]`): `EstStats, Trust, Affinity, Ledger, RelyOn,
  LastSeen`. This SPEC implements `EstStats`, `Trust`, `Affinity`, `LastSeen` now; `Ledger` and
  `RelyOn` are reserved fields populated by `engine/agent`/`engine/values` (kept on the struct so
  the type is the single carrier, but their update logic is out of scope here).
- The `Trust` field is the "credibility" the brief refers to ("B's trust in A" =
  `ToM_B[A].Trust`): when A gossips to B about C, B calls `GossipUpdate(C, A_model_of_C,
  trustWeight = B.ToM[A].Trust)`. Trust itself is updated by `engine/agent` from interaction
  outcomes (signal truthfulness over time) — out of scope here.
- Estimates reuse the `stats.Stats` shape (per-stat `float64` mean) for serialization
  compatibility with `real_stats` (data-contracts §1); the per-stat `Variance` is the extra
  uncertainty channel `Stats` does not carry.
- All folds iterate `stats.Registry.IDs()` order so a `tom_digest` is byte-deterministic
  (data-contracts §0 sorted-key rule, D12).
</content>
</invoke>
