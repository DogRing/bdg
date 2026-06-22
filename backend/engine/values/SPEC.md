# SPEC — `engine/values`

> Status: `P5`
> Leaf level: `L3`  ·  Owner agent: `<filled by implementer>`

## Purpose

Computes **how much an agent wants** to pursue a given need/action: the pure appraisal layer.
It turns a need's current intensity into a `Standing` (how satisfied), a `Salience` (how urgent),
an `EffValue` (effective value of an action toward a dimension), and a `Priority` (which need to
pursue next, after the per-dimension weight). It is **what-is-wanted only**: it never selects,
orders, or schedules actions — that is the planner (D5). All functions are pure (no IO, no RNG,
deterministic).

P5 extends the appraisal to the three **non-Self referents** (`Other`, `Place`, `Collective`)
without adding new formula functions: it adds **input-derivation** helpers that resolve the
`(currentIntensity, maxIntensity)` pair for any referent so the same four formulas still apply
downstream (D5 — the formulas are unchanged; only the input source differs).

## Public Interface

```go
package values

import (
    "io"
    "github.com/dogring/bdg/backend/engine/core"
    "github.com/dogring/bdg/backend/engine/needs"
    "github.com/dogring/bdg/backend/engine/tom"
)

// Distinct named types so an EffValue is never silently used where a Priority is expected
// (mirrors glossary §Values & goals: Standing, Salience, EffValue). All are bounded scalars.
type Standing float64 // how well a need is currently satisfied, in [0,1] (1 = fully met)
type Salience float64 // momentary urgency of a need, in [0,1] (1 = maximally urgent)
type EffValue float64 // effective value of an action w.r.t. a dimension, ≥ 0
type Priority float64 // weighted urgency used to pick the next need/goal, ≥ 0

// ── Pure appraisal functions (no IO, no RNG, no state) ─────────────────────────

// ComputeStanding returns 1 − (currentIntensity / maxIntensity) clamped to [0,1], where
// maxIntensity is the need's satisfaction threshold (needs.Def.Threshold) — the intensity at
// which the need is "fully unmet". currentIntensity is how much the need has GROWN (higher =
// worse). A maxIntensity ≤ 0 yields Standing 1 (a need that cannot be unmet is always satisfied).
func ComputeStanding(need needs.Def, currentIntensity float64) Standing

// ComputeSalience returns 1 − Standing, clamped to [0,1]. Higher Salience = more urgent.
// (Equivalently currentIntensity / maxIntensity.)
func ComputeSalience(s Standing) Salience

// ComputeEffValue returns Salience × expectedEffect, where expectedEffect is the expected delta
// to the dimension from completing the action, normalized to [0,1] by the max possible delta
// (see Notes for normalization). Result ≥ 0. This is the value of an action TOWARD a need; the
// planner maximizes it when choosing actions for the selected need (it does the maximizing — D5).
func ComputeEffValue(sal Salience, expectedEffect float64) EffValue

// ComputePriority returns Salience × weight, where weight is the per-Dimension tunable from
// content/balance.yaml `values.weights.<Dimension>` (Config.Weight). Result ≥ 0. The planner
// selects the highest-Priority dimension, then finds actions maximizing EffValue for it.
func ComputePriority(sal Salience, weight float64) Priority

// ── Referent-aware appraisal input (P5) ─────────────────────────────────────────

// ReferentInput is the resolved (currentIntensity, maxIntensity) pair that feeds the
// four appraisal functions, for any referent kind. The caller derives it from the referent
// and passes it to ComputeStanding. This keeps the formula functions unchanged (D5).
type ReferentInput struct {
    CurrentIntensity float64 // how much the need/state has grown (higher = worse)
    MaxIntensity     float64 // setpoint / maximum tolerable intensity
}

// DeriveReferentInput resolves the appraisal input for a given referent, dimension, and the
// agent's current knowledge. See the "P5 — Referent-aware appraisal" section for the per-kind
// derivation. It performs NO selection/ordering and reads NO global ToM (the caller passes the
// relevant belief); it is a pure function (D5/D12).
//
// perceivedIntelligence: caller-supplied scalar ∈ [0,1] (ToM[self].EstStats[Intelligence].Mean
//   normalized by max, D8); governs the Other-referent branch.
// moodStatID: the StatID for Mood (resolved from the stats registry by the caller; never a
//   literal here, D7). Only used for the Other low-foresight path.
// members: for Collective kind only; Other/Place/Self ignore it.
func DeriveReferentInput(
    ref       core.Referent,
    dim       core.Dimension,
    selfIntensity float64,         // NeedIntensities[dim], for Self path
    needDef   needs.Def,           // Threshold and Demand helpers
    belief    tom.Belief,          // ToM[target]; used for Other path (zero value for non-Other)
    placeQuality float64,          // [0,1]; used for Place path (ignored for non-Place)
    members   []ReferentInput,     // for Collective; ignored for non-Collective
    perceivedIntelligence float64, // caller-computed ToM[self] Intelligence fraction ∈ [0,1], D8
    moodStatID core.StatID,        // stat id for Mood (registry-resolved, never hardcoded)
    cfg       *Config,             // for OtherLowIntelThreshold
) ReferentInput

// ── Config (the values: block of content/balance.yaml) ─────────────────────────

// Config holds the per-Dimension arbitration weights loaded from content/balance.yaml's
// `values.weights` block. Immutable after Load.
type Config struct {
    /* opaque: map[core.Dimension]float64 + sorted []core.Dimension */

    // OtherLowIntelThreshold: the perceived Intelligence fraction (∈ [0,1]) BELOW which the
    // Other-referent appraisal uses the mood proxy (gross indicator) instead of the unmet-need
    // estimate (ToM stat analysis). Loaded from content/balance.yaml intelligence.other_intel_threshold.
    // Default 0.5 (midpoint). The caller normalizes perceived Intelligence to [0,1] before comparing.
    OtherLowIntelThreshold float64
}

// Load parses ONLY the top-level `values:` block AND the `intelligence.other_intel_threshold`
// scalar from balanceDoc (the bytes of content/balance.yaml — the path is injected by
// platform/config, NEVER a file path here, keeping the engine IO-free, D10). It performs
// SEMANTIC validation (every weight ≥ 0; see Invariants) and returns an error describing the
// first violation. STRUCTURAL JSON-schema validation (content/schema/balance.schema.json) and
// the cross-check that each weight key names a known Dimension are platform/config's job, run
// before this call.
func Load(r io.Reader) (*Config, error)

// Weight returns the arbitration weight for d, DEFAULTING to 1.0 when d is absent from the
// `values.weights` block (so a new Dimension works with no weight authored). Always ≥ 0.
func (c *Config) Weight(d core.Dimension) float64

// Dimensions returns the weighted dimension ids in canonical fixed order (sorted
// lexicographically, D12). The returned slice is a copy.
func (c *Config) Dimensions() []core.Dimension
```

> The formulas below are the contract. Implementations must match them to float tolerance so
> golden snapshots are stable.

### Formulas (authoritative)

```
-- d is a Dimension (a needs.NeedID); currentIntensity = how much the need has GROWN (higher =
-- worse); maxIntensity = the need's satisfaction threshold (needs.Def.Threshold).

Standing(d) = clamp01( 1 - (currentIntensity(d) / maxIntensity(d)) )      -- 1 = fully satisfied

Salience(d) = clamp01( 1 - Standing(d) )                                  -- ≡ currentIntensity / maxIntensity
                                                                         --   (higher = more urgent)

-- ExpectedEffect(action, d) = expected delta to dimension d from completing `action`, taken from
-- the action's Effect / the consumed item's supply (engine/actions, content/objects.yaml),
-- normalized to [0,1] by the max possible delta for d (see Notes).
EffValue(action, d) = Salience(d) × ExpectedEffect(action, d)            -- value of an action toward d

-- weight(d) = per-Dimension tunable from content/balance.yaml values.weights.<d> (default 1.0).
Priority(d) = Salience(d) × weight(d)                                     -- which need to pursue next
```

The planner selects the **highest-`Priority` dimension**, then finds the actions that **maximize
`EffValue`** for that dimension. This module supplies the four scalars; it performs neither the
arg-max over dimensions nor the arg-max over actions (D5).

## P5 — Referent-aware appraisal

The four appraisal functions above are written in Self-referent terms: they operate on a
`(currentIntensity, maxIntensity)` pair drawn from the agent's OWN need state. P5 generalizes the
**source** of that pair to the three non-Self referents (`core.Referent.Kind ∈ {Self, Other,
Place, Collective}`) via **input derivation** — `DeriveReferentInput` returns a `ReferentInput`,
which the caller then feeds into the SAME `ComputeStanding → ComputeSalience → ComputePriority`
chain. **No new formula function is added** (D5): the formulas are unchanged; only the input
resolution differs by referent kind.

`DeriveReferentInput` reads NO global ToM — the caller passes the relevant `belief` (its
`ToM[target]`). The module keeps the `values → tom` edge one-way: it imports `engine/tom` for the
`tom.Belief` type only and never writes to it (architecture §4).

### Per-kind derivation

```
Self  (ref.Kind == core.Self):
  CurrentIntensity = selfIntensity            (the agent's own NeedIntensities[dim])
  MaxIntensity     = needDef.Threshold        (the satisfaction setpoint)
  -- this is the existing Self-referent path; identical to the P1 input.

Other (ref.Kind == core.Other, ref.ID = target agent):
  belief = caller's ToM[target] (passed in; values never reads the global ToM).
  Low-foresight  (perceivedIntelligence <  cfg.OtherLowIntelThreshold):
      -- mood proxy: read the target's perceived Mood as a gross welfare indicator.
      -- a deeply negative mood → high intensity ("they are suffering").
      moodMean         = belief.EstStats[moodStatID].Mean          (D7 — id passed in, not a literal)
      CurrentIntensity = 1 - clamp01( moodMean / maxMood )
  High-foresight (perceivedIntelligence >= cfg.OtherLowIntelThreshold):
      -- unmet-need proxy: estimate the target's unmet need from ToM stat estimates.
      -- a mean stat level below max → higher estimated suffering intensity.
      CurrentIntensity = mean over s in belief.EstStats of ( 1 - clamp01( mean(s) / max(s) ) )
                         (iterate belief.EstStats in sorted StatID order, D12)
  MaxIntensity = needDef.Threshold            (same setpoint as Self).

Place (ref.Kind == core.Place, ref.ID = place/object id):
  CurrentIntensity = 1 - placeQuality         (placeQuality ∈ [0,1], supplied by caller)
  MaxIntensity     = 1.0
  -- a "good" place (quality 1) → intensity 0 (no threat); a "bad" place (quality 0) →
  -- intensity 1 (maximum threat/urgency).
  -- POSTURE: the SPEC does NOT encode posture internally. The caller maps ref.Posture
  --   (Maximize | MaintainAbove | PreventBelow) to the placeQuality transformation BEFORE the
  --   call: e.g. Maximize → caller wants quality HIGH (passes the deficit 1-quality);
  --   MaintainAbove → caller passes the deviation below the setpoint. See Out of Scope.

Collective (ref.Kind == core.Collective):
  CurrentIntensity = mean CurrentIntensity across `members` (per-member ReferentInputs —
                     typically the Other-referent inputs for each member).
  MaxIntensity     = mean MaxIntensity across `members` (or the first member's if homogeneous).
  -- members are summed in the order the caller supplies (the caller sorts in AgentID order, D12).
  -- empty members → CurrentIntensity 0, MaxIntensity 0.
```

Notes on the derivation:
- `maxMood` / `max(s)` are the stat `[Min,Max]` bounds; the caller resolves them from the stats
  registry (the helper takes the normalized fraction — see the caller convention). `clamp01`
  bounds every quotient to `[0,1]` so `CurrentIntensity ∈ [0,1]`.
- The **branch cutoff is `>=`**: at exactly `perceivedIntelligence == OtherLowIntelThreshold`, the
  HIGH-foresight (unmet-need) branch is used; the LOW-foresight (mood) branch is strictly `<`.
- `DeriveReferentInput` is pure: no RNG, no global state, no map ranging for logic (it iterates
  `belief.EstStats` by sorted StatID, and `members` in the caller-supplied order). Identical inputs
  → identical `ReferentInput` (D12).
- Downstream the caller calls `ComputeStanding(needDef, ri.CurrentIntensity)` (it may construct a
  `needs.Def` whose `Threshold == ri.MaxIntensity` for non-Self referents, or pass a Def whose
  Threshold already matches). The four formulas are unchanged.

## Dependencies

- `engine/core` — `Dimension`, `Referent`, `Posture`, `StatID`. (Plus a YAML decoder and stdlib
  `sort` for `Load`.)
- `engine/needs` — `Def` (reads `Threshold` as `maxIntensity`; `ComputeStanding` and
  `DeriveReferentInput` take a `needs.Def`). Per architecture §2 `values` depends on `needs`. No
  mutation of the need registry.
- `engine/tom` — `Belief` (the `ToM[target]` shape, `= ToM[X]`, with `EstStats map[core.StatID]
  tom.StatDist`). **Per architecture §2/§4** `values → tom` is a ONE-WAY dependency: this module
  imports `engine/tom` for the `tom.Belief` type and reads its `EstStats` means for the
  Other-referent derivation; it NEVER writes to `tom` and `tom` never imports `values`.
- `engine/stats` — the stat `[Min,Max]` bounds used to normalize the Other-referent stat estimates
  are resolved by the **caller** (engine/agent), which passes the normalized fractions / the
  `moodStatID`; this module references no stat id literal (D7).
- **Contract**: `content/schema/balance.schema.json` (`values.weights` block +
  `intelligence.other_intel_threshold`) defines the on-disk shape; `content/balance.yaml` is the
  data. `platform/config` bridges file → schema-validate → cross-check (weight keys name known
  Dimensions) → `Load`.

## Owned Data

- The `Standing`/`Salience`/`EffValue`/`Priority` scalar types, the `ReferentInput` type, the pure
  appraisal functions, and `DeriveReferentInput`.
- `Config` (the per-Dimension weight map + `OtherLowIntelThreshold`), **immutable after `Load`**;
  owns its internal map + precomputed sorted dimension slice. Returned slices are copies. The
  dynamic per-agent need **levels/intensities** fed into these functions are owned by
  `engine/agent`/`engine/world`.

## Invariants

- **Values module never selects or orders actions (D5)**: this module exposes **no** function
  that returns "the chosen action", "the next goal", a sorted action list, or an arg-max over
  dimensions/actions. It returns only scalar appraisals + a `ReferentInput`. A struct/API guard
  confirms there is no selection/sort/plan method. The planner consumes these scalars and does the
  arbitration.
- **One-way `values → tom` (architecture §4)**: `DeriveReferentInput` READS `tom.Belief.EstStats`
  for the Other path and NEVER writes to `tom`; `engine/tom` never imports `engine/values`. A
  guard confirms no `tom` mutation call from this package.
- **Pure, deterministic functions (D12)**: `ComputeStanding`/`ComputeSalience`/`ComputeEffValue`/
  `ComputePriority`/`DeriveReferentInput` are pure — no `time.Now()`, no RNG, no global or package
  state, no map iteration for logic (Other-path iterates `EstStats` by sorted StatID; Collective
  sums `members` in the caller-supplied order). Identical inputs → identical outputs
  (golden-stable). `Load` is the only function that touches input bytes, and it is deterministic.
- **No hardcoded dimension/stat names (D10/D7)**: this package never references `"Satiety"`,
  `"Safety"`, `"Mood"`, `"Intelligence"`, etc. as literals in logic; dimensions/stat ids flow in
  as `core.Dimension`/`core.StatID` arguments (`moodStatID` is passed, never a literal) and weights
  load from the injected `values.weights` block. `Weight` defaults an unknown dimension to `1.0`.
- **Bounds**: `Standing, Salience ∈ [0,1]` (clamped); `EffValue, Priority ≥ 0`; `expectedEffect`
  is expected in `[0,1]` (normalized) and any out-of-range input is treated per the clamp rule in
  Notes. `ReferentInput.CurrentIntensity ∈ [0,1]` (each derivation clamps). Every authored
  `values.weights` value is `≥ 0` (`Load` rejects a negative weight).
- **Separation of want vs how (D5)**: `engine/values` answers "how much is this wanted"; it never
  answers "how to get it" (no planning, no GOAP, no provisioning, no cost). That is `engine/planner`.
- **Immutable after init**: `Config` exposes no setter/writable field; returned slices are copies.
- **No IO**: imports no `os`/`net`/filesystem package; `Load` reads from an injected `io.Reader`
  only. No JSON-schema validation here (that lives in `platform/config`).

## Acceptance Criteria (testable)

- [ ] **`ComputeStanding` formula**: table-driven — `currentIntensity = 0` → Standing 1;
  `currentIntensity = maxIntensity` → Standing 0; halfway → 0.5; `currentIntensity > maxIntensity`
  → clamped to 0; `maxIntensity ≤ 0` → Standing 1. Matches `1 − intensity/threshold` to tolerance.
- [ ] **`ComputeSalience` formula**: `Salience == 1 − Standing` clamped to `[0,1]` across the
  same table; equals `currentIntensity/maxIntensity` (cross-check).
- [ ] **`ComputeEffValue` formula**: `EffValue == Salience × expectedEffect`; with `Salience = 0`
  → 0; with `expectedEffect = 0` → 0; monotone increasing in each factor (table-driven). Result ≥ 0.
- [ ] **`ComputePriority` formula**: `Priority == Salience × weight`; doubling the weight doubles
  Priority; `weight = 0` → 0 (table-driven). Result ≥ 0.
- [ ] **`Load` reads `values.weights` from injected bytes (D10, path not hardcoded)**:
  `Load(balanceDoc)` returns a `Config` where `Weight("Safety") == 1.40`, `Weight("Hydration")
  == 1.05`, `Weight("Satiety") == 1.00`, `Weight("Rest") == 0.85`, `Weight("Standing") == 0.60`,
  `Weight("Openness") == 0.40` for the shipped content. No file path in the call. Grep guard: no
  numeric weight literal and no dimension-name literal in `engine/values` source.
- [ ] **`Load` reads `OtherLowIntelThreshold` (P5)**: `Load(balanceDoc).OtherLowIntelThreshold ==
  0.5` (from `intelligence.other_intel_threshold` in the shipped content); a doc missing the key
  defaults it to `0.5`. No file path in the call.
- [ ] **`Weight` defaults to 1.0 for an unknown dimension**: `Weight("NotADimension") == 1.0`.
- [ ] **`Load` rejects a negative weight (semantic check)**: a synthetic `values.weights` with a
  negative value yields a non-nil error naming the offending dimension.
- [ ] **No selection/ordering API (D5)**: a struct/API guard confirms the package exposes no
  function returning a chosen action, a goal, a sorted list, or an arg-max — only the four scalar
  appraisals, `DeriveReferentInput`, + `Config` accessors.
- [ ] **Determinism (D12)**: `Dimensions()` is lexicographically sorted and identical across
  repeated calls and a second `Load` of the same bytes; the appraisal functions +
  `DeriveReferentInput` are pure (property test: same inputs → same outputs; no state between
  calls).
- [ ] **Integration with the appraisal chain (scenario)**: for a fixture agent whose `Satiety`
  intensity exceeds its threshold while `Rest` is comfortably met, `Priority(Satiety) >
  Priority(Rest)` — so the planner (downstream) would pick Satiety. Asserts the chain direction
  (ties to testing.md §4 scenario fixtures B/H, which exercise need-driven goal selection).
- [ ] **Other-referent low-Intel (mood proxy, P5)**: with perceivedIntelligence < OtherLowIntelThreshold,
  DeriveReferentInput(Other, ...) uses belief.EstStats[moodStatID].Mean as the intensity proxy.
  Table-driven: three (perceivedIntelligence, belief.Mood) pairs → expected CurrentIntensity values.
- [ ] **Other-referent high-Intel (unmet-need, P5)**: with perceivedIntelligence >= OtherLowIntelThreshold,
  DeriveReferentInput(Other, ...) returns CurrentIntensity = mean of (1 - stat/max) across EstStats.
  Table-driven: a belief with all stats at max → intensity 0; all at min → intensity 1.
- [ ] **Intel branch cutoff (P5)**: at exactly perceivedIntelligence == OtherLowIntelThreshold, the
  HIGH-foresight branch is used (>=, not >). The low-foresight branch is strictly <.
- [ ] **Place referent (P5)**: DeriveReferentInput(Place, ..., placeQuality=0.8) → CurrentIntensity 0.2.
  With Maximize posture the caller passes (1-placeQuality) as placeQuality → intensity 0.8. Table-driven.
- [ ] **Collective referent (P5)**: DeriveReferentInput(Collective, ..., members=[{0.3,1.0},{0.7,1.0}])
  → CurrentIntensity 0.5, MaxIntensity 1.0. Empty members → CurrentIntensity 0. Iteration in
  sorted AgentID order (D12 — the caller sorts; DeriveReferentInput sums the passed slice in order).
- [ ] **No hardcoded stat name (D7, P5)**: moodStatID is passed in; a stub with moodStatID="X" uses X.
  Grep guard: no "Mood"/"Intelligence"/"Social" literal in engine/values source.
- [ ] **Scenario D integration (P5, golden)**: agent A cares about child C (Other referent, high Intel
  branch, perceivedIntelligence 0.8 >= 0.5). C's perceived welfare is low (belief stats near min →
  CurrentIntensity 0.9). Safety weight for Self path = 1.40. Other-referent Safety weight for C =
  2.0 (a higher per-agent weight the caller applies, reflecting the Social/Affinity bond). After
  computing Priority for both Self.Safety and Other.Safety(C): Other.Priority > Self.Priority, so the
  conscience threshold effectively lowers — the planner's goal selection picks Safety(C) first.
  Assert: Priority(Other.Safety, C) > Priority(Self.Safety, A). Golden on the delta.
  NOTE: this scenario AC is at the VALUES module level (it tests DeriveReferentInput + ComputePriority
  composition); the full end-to-end with conscience threshold is an engine/agent concern (P5-2).

> Structural JSON-schema validation of `content/balance.yaml` `values.weights` /
> `intelligence.other_intel_threshold`, and the cross-check that each weight key names a known
> Dimension in `content/needs.yaml`, are **platform/config** ACs — it owns the file IO + the
> schema. This module proves only semantic checks reachable from the injected reader.

## Out of Scope

- Reading the file from disk, JSON-schema validation, and the weight-key↔Dimension cross-check →
  `platform/config` (architecture §3).
- **Selecting the goal dimension (arg-max over Priority) and the action (arg-max over EffValue),
  goal arbitration, `Stickiness`/`Budget`/`goal_deadband`, and any planning** → `engine/planner`
  (it consumes these scalars; this module never selects, D5).
- **Computing `ExpectedEffect` for a concrete action** (reading the action's `Effect`/the consumed
  item's supply, forward-simulating need satisfaction) → `engine/planner` + `engine/actions`
  (this module takes the already-computed, normalized `expectedEffect` as an argument).
- **Per-agent need intensities/levels and their decay over time** → `engine/agent`/`engine/world`
  state (they feed `currentIntensity` in here) + `engine/needs` forward-roll helpers.
- **Per-agent setpoint/weight perturbation by disposition** (Greed, Sociability, … shifting the
  effective weight for an individual) → reserved for a later pass; the `values.weights` block is
  the population default. Flag to architect when the disposition-modulated appraisal lands.
- **Per-agent weight perturbation by Affinity/bond (P5)**: the caller (engine/agent) multiplies
  `weight(d)` by an Affinity-based bond multiplier before calling `ComputePriority`; this module
  provides only the base weight. The multiplication lives in the agent loop.
- **Posture-to-input mapping (P5)**: `Maximize`/`MaintainAbove`/`PreventBelow` differ only in HOW
  the caller computes `placeQuality` before calling `DeriveReferentInput`. The posture enum is
  `core.Referent.Posture`; this module accepts the pre-mapped intensity.
- **Standing value over known objects (`Known map[ObjectID]Valuation`)** → the per-agent value
  map is built by `engine/agent`; this module provides the per-dimension appraisal primitives it
  composes. (Glossary `Standing value` = weighted sum of an object's dimension contributions; the
  weighting uses these primitives.)

## Open Questions

- **`Standing` name collision (NOT blocking P1).** Glossary uses `Standing` for two distinct
  things: (1) the appraisal scalar "how satisfied a need is" (this SPEC's `type Standing`), and
  (2) `Standing` the social Dimension (a need id in `content/needs.yaml`). They are different
  namespaces (a Go type vs a `core.Dimension` string value), so they do not collide at the type
  level, but reviewers should not conflate them. Recorded for clarity; no change requested.
- **`ExpectedEffect` normalization owner (NOT blocking P1).** This SPEC takes a pre-normalized
  `expectedEffect ∈ [0,1]`. The normalization (delta ÷ max-possible-delta for the dimension)
  needs a per-dimension max; whether that max comes from `needs.Def`, the largest object `supply`,
  or a balance constant is a planner concern. Flagged so the planner SPEC fixes the normalization
  source; this module's signature is unaffected either way.
- **`needs.Def` for non-Self referents (NOT blocking P5).** For Other/Place/Collective the
  downstream `ComputeStanding` needs a `needs.Def` whose `Threshold == ReferentInput.MaxIntensity`.
  The caller either reuses the dimension's own `Def` (Other uses `needDef.Threshold` so this holds)
  or constructs a synthetic Def for Place/Collective (Threshold = the returned `MaxIntensity`).
  Confirm whether a small `ComputeStandingFromInput(ri ReferentInput) Standing` convenience wrapper
  is wanted to avoid the synthetic-Def construction at the call site; the current contract keeps the
  four formulas unchanged and lets the caller adapt. Non-blocking; flag if the wrapper is preferred.

## Notes

- **Normalization of `expectedEffect`** (Notes, not a hard contract here): the caller passes
  `expectedEffect = clamp01(rawDelta / maxDelta(d))`. `ComputeEffValue` itself does **not**
  re-normalize — it multiplies the two factors — so the caller owns the normalization choice
  (see Open Questions). `ComputeEffValue` clamps a negative `expectedEffect` to 0 (an action that
  worsens a need has no positive value toward it; aversion/cost is the planner's channel, not here).
- **Why `Standing` ≠ `Salience` as separate types**: keeping them distinct named types prevents
  a planner bug where urgency and satisfaction are swapped; the compiler catches the mix-up.
- The `values:` block lives in `content/balance.yaml` (the single auto-tuning file, testing.md
  §5), NOT in `content/needs.yaml` — the catalog stays in needs.yaml, every tunable scalar stays
  in balance.yaml, mirroring the needs-rate split (`content/README.md §Need definitions split`).
- These appraisals feed the why-trace: the planner surfaces the competing `Priority`/`EffValue`
  candidates in the `GoalSelected` event payload (data-contracts §4 `dimension, priority,
  eff_value`) so "why this goal" is reconstructable (NFR-3). This module computes the numbers; the
  planner emits them.
- `currentIntensity` is the *grown* need (higher = worse), the complement of the satisfaction
  *level* used by `engine/needs.Def.Level` (level = 1 − intensity-fraction). The caller converts
  between the two; this module's formulas are written in intensity terms to match the brief.
- **P5 referent derivation keeps the formulas pristine.** `DeriveReferentInput` is the ONLY P5
  addition that touches referent kinds; the four appraisal functions never branch on `Referent.Kind`.
  This preserves D5 (one place computes "how much is wanted" regardless of who the referent is) and
  keeps the golden formulas stable across the Self/Other/Place/Collective extension.
