# SPEC — `engine/values`

> Status: `DRAFT`
> Leaf level: `L3`  ·  Owner agent: `<filled by implementer>`

## Purpose

Computes **how much an agent wants** to pursue a given need/action: the pure appraisal layer.
It turns a need's current intensity into a `Standing` (how satisfied), a `Salience` (how urgent),
an `EffValue` (effective value of an action toward a dimension), and a `Priority` (which need to
pursue next, after the per-dimension weight). It is **what-is-wanted only**: it never selects,
orders, or schedules actions — that is the planner (D5). All functions are pure (no IO, no RNG,
deterministic).

## Public Interface

```go
package values

import (
    "io"
    "github.com/dogring/bdg/backend/engine/core"
    "github.com/dogring/bdg/backend/engine/needs"
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

// ── Config (the values: block of content/balance.yaml) ─────────────────────────

// Config holds the per-Dimension arbitration weights loaded from content/balance.yaml's
// `values.weights` block. Immutable after Load.
type Config struct{ /* opaque: map[core.Dimension]float64 + sorted []core.Dimension */ }

// Load parses ONLY the top-level `values:` block from balanceDoc (the bytes of
// content/balance.yaml — the path is injected by platform/config, NEVER a file path here,
// keeping the engine IO-free, D10). It performs SEMANTIC validation (every weight ≥ 0;
// see Invariants) and returns an error describing the first violation. STRUCTURAL JSON-schema
// validation (content/schema/balance.schema.json) and the cross-check that each weight key
# names a known Dimension are platform/config's job, run before this call.
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

## Dependencies

- `engine/core` — `Dimension`. (Plus a YAML decoder and stdlib `sort` for `Load`.)
- `engine/needs` — `Def` (reads `Threshold` as `maxIntensity`; `ComputeStanding` takes a
  `needs.Def`). Per architecture §2 `values` depends on `needs`. No mutation of the need registry.
- `engine/stats`, `engine/tom` — **per architecture §2/§4** `values → tom` is a one-way dependency
  for Other-referent appraisal (reading `ToM[X]` of a target). The P1 appraisal formulas in this
  SPEC are Self-referent and do not yet call `tom`; the Other-referent appraisal that reads
  `ToM[X]` is reserved (see Out of Scope / Open Questions). `tom` never imports `values`.
- **Contract**: `content/schema/balance.schema.json` (`values.weights` block) defines the on-disk
  shape; `content/balance.yaml` is the data. `platform/config` bridges file → schema-validate →
  cross-check (weight keys name known Dimensions) → `Load`.

## Owned Data

- The `Standing`/`Salience`/`EffValue`/`Priority` scalar types and the pure appraisal functions.
- `Config` (the per-Dimension weight map), **immutable after `Load`**; owns its internal map +
  precomputed sorted dimension slice. Returned slices are copies. The dynamic per-agent need
  **levels/intensities** fed into these functions are owned by `engine/agent`/`engine/world`.

## Invariants

- **Values module never selects or orders actions (D5)**: this module exposes **no** function
  that returns "the chosen action", "the next goal", a sorted action list, or an arg-max over
  dimensions/actions. It returns only scalar appraisals. A struct/API guard confirms there is no
  selection/sort/plan method. The planner consumes these scalars and does the arbitration.
- **Pure, deterministic functions (D12)**: `ComputeStanding`/`ComputeSalience`/`ComputeEffValue`/
  `ComputePriority` are pure — no `time.Now()`, no RNG, no global or package state, no map
  iteration for logic. Identical inputs → identical outputs (golden-stable). `Load` is the only
  function that touches input bytes, and it is deterministic.
- **No hardcoded dimension names (D10)**: this package never references `"Satiety"`, `"Safety"`,
  etc. as literals in logic; dimensions flow in as `core.Dimension` arguments and weights load
  from the injected `values.weights` block. `Weight` defaults an unknown dimension to `1.0`.
- **Bounds**: `Standing, Salience ∈ [0,1]` (clamped); `EffValue, Priority ≥ 0`; `expectedEffect`
  is expected in `[0,1]` (normalized) and any out-of-range input is treated per the clamp rule in
  Notes. Every authored `values.weights` value is `≥ 0` (`Load` rejects a negative weight).
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
- [ ] **`Weight` defaults to 1.0 for an unknown dimension**: `Weight("NotADimension") == 1.0`.
- [ ] **`Load` rejects a negative weight (semantic check)**: a synthetic `values.weights` with a
  negative value yields a non-nil error naming the offending dimension.
- [ ] **No selection/ordering API (D5)**: a struct/API guard confirms the package exposes no
  function returning a chosen action, a goal, a sorted list, or an arg-max — only the four scalar
  appraisals + `Config` accessors.
- [ ] **Determinism (D12)**: `Dimensions()` is lexicographically sorted and identical across
  repeated calls and a second `Load` of the same bytes; the four appraisal functions are pure
  (property test: same inputs → same outputs; no state between calls).
- [ ] **Integration with the appraisal chain (scenario)**: for a fixture agent whose `Satiety`
  intensity exceeds its threshold while `Rest` is comfortably met, `Priority(Satiety) >
  Priority(Rest)` — so the planner (downstream) would pick Satiety. Asserts the chain direction
  (ties to testing.md §4 scenario fixtures B/H, which exercise need-driven goal selection).

> Structural JSON-schema validation of `content/balance.yaml` `values.weights`, and the
> cross-check that each weight key names a known Dimension in `content/needs.yaml`, are
> **platform/config** ACs — it owns the file IO + the schema. This module proves only semantic
> checks reachable from the injected reader.

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
- **Other-referent appraisal reading `ToM[X]`** (valuing an action's effect on another agent —
  the `values → tom` edge) → reserved; the P1 formulas are Self-referent. Adding it does not
  change the four scalar signatures (it supplies a different `currentIntensity`/`expectedEffect`).
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
