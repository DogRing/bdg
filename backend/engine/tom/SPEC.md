# SPEC — `engine/tom`

> Status: `READY`
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
    // RelyOn / Ledger: owned/updated by engine/agent + engine/values (see Out of Scope).
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
func (t ToM) GossipUpdate(subject core.AgentID, source Belief, trustWeight float64)

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

> Structural validation of `content/balance.yaml` (the rate keys) is a **platform/config** AC.
> This module proves only behaviour reachable from injected rates + an injected `*rng.RNG`.

## Out of Scope

- **Deciding to attempt an action** (which is what eventually feeds `Observe` and breaks—or
  fails to break—self-sealing) → `engine/agent` + `engine/planner`.
- **Reading `ToM[self]` for gate/cost decisions** → `engine/gates` consumes the self-stat values
  (this module produces them; gates do not import tom — the agent passes `ToM[self]` into the
  gate `AgentSnapshot`).
- `Affinity` / `Ledger` / `RelyOn` *update* dynamics (resentment, reliance edges, emergent roles
  as reliance clusters) → `engine/agent` + `engine/values` (the fields are carried on `Belief`
  but their update rules live there). `tom` only provides storage + the estimate/gossip core.
- `Signal{Kind, Intent, Valence, ClaimedValue, Truth, Intensity}` construction and the
  truth/deception layer → `engine/agent` interaction (a gossip claim's `Truth` is hidden; this
  module just folds the claimed mean it is handed).
- The `BeliefUpdated` / `ReputationGossip` event emission (data-contracts §4) → emitted by the
  caller (`engine/agent`) via the `core.EventEmitter`; this module returns the deltas it makes
  available for that trace but does not emit.
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
