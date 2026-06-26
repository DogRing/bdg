# SPEC — `engine/mind/needs`

> Status: `DRAFT`
> Leaf level: `L2`  ·  Owner agent: `<filled by implementer>`

## Purpose

Owns the immutable **`Registry`** of need / value **`Dimension`** definitions and the pure
forward-roll helpers that project a consumable need's satisfaction level over time. The
registry is assembled from two injected sources (D10: needs are data, not code):
**`content/needs.yaml`** supplies the dimension *catalog* (which dimensions exist, their
`kind`, default posture / setpoint / referent, salience curve), and **`content/balance.yaml`'s
`needs:` block** supplies the per-need *rate* constants (`decay_per_tick`,
`satisfaction_threshold`) for consumable needs. A `Dimension` declares *what need exists* and
*at what rate it intensifies* — it carries **no information about how the need is satisfied**
(D5: satisfaction is the planner's job) and **no "future need" field** (D9: demand =
`rate × predicted-time` is derived by the planner, never authored). The registry is the single
authority on which `NeedID`s exist and their decay rate, posture, setpoint, and salience curve.

## Public Interface

```go
package needs

import (
    "io"
    "github.com/dogring/bdg/engine/kernel/core"
)

// NeedID names a need / value Dimension (glossary §"Values & goals" Dimension:
// Satiety, Hydration, Rest, Safety, Standing, Openness, …). It is an ALIAS of the canonical
// core.Dimension type (engine/kernel/core exports `type Dimension string`), so a NeedID and a
// values-module Dimension are the exact same type — no conversion, no naming drift. Canonical
// ids live in content/needs.yaml.
type NeedID = core.Dimension

// Kind classifies how a need's level changes over time (content/needs.yaml kind).
type Kind uint8

const (
    Consumable  Kind = iota // level decays by time at Rate; planner forward-rolls + provisions.
                            // Its Rate/Threshold come from content/balance.yaml needs:<id>.
    Conditional             // not time-driven; set by world/social state; Rate == 0; NOT listed
                            // in the balance.yaml needs: block.
)

// Posture is the goal posture for a Value on this dimension (glossary: Posture).
type Posture uint8

const (
    Maximize      Posture = iota // push as high as possible (Standing, Openness)
    MaintainAbove                // keep at/above setpoint (Satiety, Hydration, Rest)
    PreventBelow                 // act when threatened to fall below setpoint (Safety)
)

// SalienceCurve names how a need's gap maps to momentary salience.
type SalienceCurve uint8

const (
    Deficit  SalienceCurve = iota // salience ~ max(0, setpoint − level)
    GapToMax                       // salience ~ (1 − level)
)

// Def is one immutable need/value Dimension definition. Catalog fields (Kind, Posture,
// Referent, Curve, Gain, Setpoint default) come from content/needs.yaml; rate fields (Rate,
// Threshold) come from content/balance.yaml's needs:<id> block for consumable needs.
type Def struct {
    ID        NeedID            // canonical Dimension id (docs/glossary.md)
    Kind      Kind              // consumable | conditional
    Rate      float64           // satisfaction lost per tick (= per game-minute), D9.
                                // From balance.yaml needs:<id>.decay_per_tick. 0 for conditional.
    Threshold float64           // satisfaction_threshold (setpoint) in [0,1]. From balance.yaml
                                // needs:<id>.satisfaction_threshold for consumable needs;
                                // for conditional needs, the needs.yaml default.setpoint.
    Posture   Posture           // default goal posture for a fresh Value on this dimension
    Setpoint  float64           // default setpoint in [0,1] (needs.yaml default.setpoint;
                                // for consumable needs equals Threshold)
    Referent  core.ReferentKind // default referent kind (Self | Other | Place | Collective)
    Curve     SalienceCurve     // salience curve
    Gain      float64           // salience gain (≥ 0)
}

// Registry is the immutable, read-only set of Dimension definitions. After Load it never
// changes (no setters, no exported mutable fields). Safe to share across goroutines.
type Registry struct{ /* opaque: defs map[NeedID]Def + precomputed sorted []NeedID */ }

// Load parses the dimension catalog from needsDoc (the bytes of content/needs.yaml) and the
// per-need rate constants from balanceDoc (the bytes of content/balance.yaml, from which only
// the top-level `needs:` block is read). BOTH readers are injected by platform/config — the
// engine opens NO file and hardcodes NO path (D10, architecture §1, engine is IO-free). Load
// MERGES the two: a consumable dimension's Rate/Threshold are taken from balanceDoc's
// needs:<id> entry; its catalog metadata from needsDoc. It performs SEMANTIC validation (see
// Invariants) and returns an error describing the FIRST violation, with a DESCRIPTIVE message
// when: the top-level `needs:` block in balanceDoc is missing/empty/malformed; a consumable
// dimension in needsDoc has no matching needs:<id> rate in balanceDoc (or vice-versa); or any
// field is out of bounds. STRUCTURAL JSON-schema validation (content/schema/needs.schema.json
// and content/schema/balance.schema.json) is NOT done here — that is platform/config's job,
// run before this call.
func Load(needsDoc, balanceDoc io.Reader) (*Registry, error)

// IDs returns ALL need ids in canonical fixed order: sorted lexicographically by NeedID.
// This is the ONE ordering every consumer uses to iterate needs (D12). The returned slice is
// a copy; identical across calls and across processes for the same content.
func (reg *Registry) IDs() []NeedID

// Def returns the definition for id and whether it exists.
func (reg *Registry) Def(id NeedID) (Def, bool)

// Has reports whether id is a known need. Used to reject unknown NeedIDs referenced elsewhere
// (the config referential-integrity check; objects/actions effects).
func (reg *Registry) Has(id NeedID) bool

// Len returns the number of defined needs.
func (reg *Registry) Len() int

// Kinds returns the lexicographically-sorted ids of all needs of the given Kind (e.g. the
// consumable needs the planner must forward-roll).
func (reg *Registry) Kinds(k Kind) []NeedID

// ── Pure forward-roll helpers (D9: demand is DERIVED, never authored) ──────────
//
// These compute the *trajectory* of a consumable need from its current level; they do NOT
// decide how to satisfy it (that is the planner, D5). They are pure functions of (Def, inputs)
// — no clock, no RNG, no state owned here.

// Level returns the satisfaction level after `minutes` of pure decay from `level0`, clamped to
// [0,1]: level0 − Rate·minutes for a Consumable; level0 unchanged for a Conditional (rate 0).
func (d Def) Level(level0 float64, minutes core.GameMinutes) float64

// Demand returns the predicted shortfall over a horizon: rate × predicted-time (glossary:
// "Demand = need-rate × predicted-time"). This is the value the planner uses to size a
// provisioning subgoal; it is computed here, NEVER stored on an object (D9).
func (d Def) Demand(minutes core.GameMinutes) float64

// BreachAt returns the game-minute offset at which a Consumable's level would first cross below
// `setpoint` given `level0`, and ok=false if it never does within the horizon or for a
// Conditional need. The planner uses this to decide WHEN provisioning is needed (forward-sim);
// it does not insert the subgoal — that is the planner.
func (d Def) BreachAt(level0, setpoint float64, horizon core.GameMinutes) (at core.GameMinutes, ok bool)

// Salience returns the momentary salience of this dimension given the current level and
// setpoint, per the dimension's Curve and Gain. Read by engine/mind/values arbitration; bounded ≥ 0.
func (d Def) Salience(level, setpoint float64) float64

// ── Conditional-need driver (BLOCKER-1: ACTIVATED — was previously undriven) ──────
//
// A Conditional dimension (Safety, Standing, Openness) is NOT clock-driven (Rate == 0), so the
// consumable decay loop never touches it. Its INTENSITY (the grown need pressure the agent stores
// in NeedIntensities[id], where higher = worse) must instead be driven by world/social STATE.
// For Safety, the driving state is the set of currently-perceived threatening agents, supplied by
// the agent loop (engine/agent phase 1 hostile-tag scan → the threat AgentIDs). This module owns
// only the PURE intensity update; the per-agent stored level lives on the Agent (Out of Scope).
//
// UpdateConditionalNeeds is a PURE function (no clock, no RNG, no stored state): given the current
// stored intensity for this Conditional dimension and the list of perceived threats this tick, it
// returns the NEW intensity. The caller (engine/agent) stores the result into NeedIntensities[id].
// The driver is parameterised by two injected constants (D10 — from content/balance.yaml's
// `threats:` block, threaded in by the caller; this package hardcodes neither):
//
//   perThreatGain — intensity added per perceived threat this tick (balance.yaml threats.per_threat_intensity)
//   decayPerTick  — intensity removed per tick when NO threat is perceived (balance.yaml threats.safety_decay)
//
// Formula (deterministic; clamped to [0,1]):
//
//   if len(threats) == 0:                 // no threat in perception radius this tick
//       new = clamp01( cur − decayPerTick )            // decays toward 0 over ~ceil(cur/decayPerTick) ticks
//   else:                                 // at least one threat perceived
//       new = clamp01( cur + perThreatGain × float64(len(threats)) )  // rises ∝ threat count
//
// Returns `cur` unchanged for a non-Conditional Def (Safety/Standing/Openness only; consumable
// needs decay via Level, not here). `threats` is iterated only for its LENGTH (count), so its
// element order is irrelevant — but callers MUST pass a count-stable (deduplicated, sorted-id)
// slice so the same world state yields the same count (D12). The method reads NO field naming an
// action/object/effect (D5: needs do not encode satisfaction).
func (d Def) UpdateConditionalNeeds(cur float64, threats []core.AgentID, perThreatGain, decayPerTick float64) float64
```

> `Load` takes two `io.Reader`s, not paths — the engine performs **no filesystem IO**
> (architecture §1). `platform/config` opens `content/needs.yaml` and `content/balance.yaml`,
> runs the JSON-schema validation on each, and passes the readers/bytes here.

> **`UpdateConditionalNeeds` signature note (BLOCKER-1).** The task brief names the entry
> `UpdateConditionalNeeds(threats []core.AgentID)`. Because `engine/mind/needs` is **stateless** (the
> registry is immutable; per-agent need *levels* live on the Agent — Out of Scope), the method
> cannot store the intensity itself; it is a PURE function that *takes the current intensity and the
> tuning constants and returns the new intensity*. The two injected scalars keep it D10-clean (no
> rate literal in this package). The agent's wrapper (`engine/agent`, §F below) supplies
> `a.NeedIntensities[cfg.SafetyDim]` as `cur`, the phase-1 threat AgentIDs as `threats`, and the two
> constants from `cfg`, then writes the result back. If a future caller wants the bare
> `UpdateConditionalNeeds(threats []core.AgentID)` shape, it must close over `(cur, gain, decay)` —
> the constants stay injected either way.

## Dependencies

- `engine/kernel/core` — `GameMinutes`, `ReferentKind`, `AgentID`, and `Dimension` (the `NeedID` alias;
  `engine/kernel/core/SPEC.md` now exports `type Dimension string`). (Plus a YAML decoder and stdlib
  `sort`.)
- `engine/mind/stats` — referenced for type alignment only (need-level vectors reuse the `Stats`
  shape conceptually); no behavioural dependency. May be dropped if unused at implementation.
- **Contract**: `content/schema/needs.schema.json` defines the on-disk shape of the dimension
  catalog (`content/needs.yaml`). `content/schema/balance.schema.json` defines the `needs:`
  rate block (`content/balance.yaml`) **and the new `threats.per_threat_intensity` /
  `threats.safety_decay` scalars** that parameterise the conditional driver. The per-need
  `decay_per_tick` is the **only** authored *consumable* demand input (D9); the two conditional
  scalars are the **only** authored Safety-pressure inputs. `platform/config` bridges file →
  schema-validate → `Load` for both, and threads the two `threats.*` scalars into the agent
  `Config` (BLOCKER-2 wiring).

## Owned Data

- `Registry` and `Def` value types. The `Registry` is **immutable after `Load`** and owns its
  internal map + precomputed sorted id slice — no other module mutates it.
- Per-agent need **levels** (the dynamic satisfaction/intensity state, incl. the Conditional
  Safety intensity) are owned by the agent/world state, not here; this module provides only the
  shape, the pure consumable roll helpers, and the pure conditional driver (`UpdateConditionalNeeds`).

## Invariants

- **Rate-only authoring; demand is derived (D9)**: a `Def` carries only `Rate` (per-tick decay,
  from `balance.yaml needs:<id>.decay_per_tick`) plus posture/setpoint/salience metadata. There
  is **no "future need" / quantity / schedule field**. `Demand`, `BreachAt`, and provisioning
  are *computed* by `forward-sim`, never read from a field. Objects carry only their supply
  `Effect` (see `engine/mind/actions` + `content/objects.yaml`), never a need.
- **Need rate is loaded, never hardcoded (D10)**: the consumable decay/threshold constants come
  from the injected `balance.yaml` `needs:` block; the conditional driver's `perThreatGain` /
  `decayPerTick` come from the injected `balance.yaml threats:` block (passed in by the caller).
  This package contains **no** numeric rate literal and **no** file path.
- **Conditional needs are driven by STATE, not the clock (D9, design §"conditional")**: a
  `Conditional` dimension has `Rate == 0` and is **never** touched by the consumable decay loop.
  Its intensity changes ONLY through the pure `UpdateConditionalNeeds` driver, fed by world/social
  state (for Safety: the perceived-threat count). `UpdateConditionalNeeds` returns `cur` unchanged
  for a non-Conditional `Def` (it never substitutes for consumable decay).
- **Needs do not encode satisfaction (D5)**: the registry says *what* needs exist and *how fast*
  they intensify; it says **nothing** about which action/object satisfies them. `UpdateConditionalNeeds`
  reads no action/object/effect field — only a threat count and two scalars.
- **Consumable ⟺ has a balance rate; conditional ⟺ none**: every `Consumable` dimension in
  `needs.yaml` MUST have a `balance.yaml needs:<id>` entry (with `decay_per_tick > 0`), and
  every key in the balance `needs:` block MUST name a `Consumable` dimension. A `Conditional`
  dimension is event-driven (threats, standing, terrain), has `Rate == 0`, and MUST NOT appear
  in the balance `needs:` block. `Load` rejects any mismatch with a descriptive error.
- **Canonical ordering (D12)**: the `Registry` precomputes a single fixed-order `[]NeedID`
  (sorted lexicographically) via `IDs()`. **All need iteration — here and in every consumer —
  MUST use this slice; ranging over the backing map for logic is forbidden.** `Kinds()` iterates
  in `IDs()` order.
- **Bounds well-formed (semantic check)**: every `Def` has `Rate ≥ 0`, `Threshold ∈ [0,1]`,
  `Setpoint ∈ [0,1]`, `Gain ≥ 0`, and a recognized `Posture`/`Curve`/`Referent`. Consumable
  needs additionally require `Rate > 0`. `Load` rejects violations and duplicate ids.
- **Pure, clock-free helpers (D12)**: `Level`/`Demand`/`BreachAt`/`Salience`/`UpdateConditionalNeeds`
  are pure functions — no `time.Now()`, no RNG, no global state. Given identical inputs they return
  identical outputs (golden-stable). `UpdateConditionalNeeds`'s result is clamped to `[0,1]`.
- **Immutable after init**: `Registry` exposes no setter and no writable field; returned slices
  are copies.
- **No hardcoded need names (D10)**: this package never references `"Satiety"`, `"Safety"`, etc.
  as literals in logic; all dimensions come from the loaded data.
- **No IO**: imports no `os`/`net`/filesystem package; reads from injected `io.Reader`s only.
  No JSON-schema validation here (that lives in `platform/config`).

## Acceptance Criteria (testable)

- [ ] **Need-rate constants are loaded from `content/balance.yaml`'s `needs:` block, path
  injected, not hardcoded**: `Load(needsDoc, balanceDoc)` reads each consumable dimension's
  `Rate` from `balanceDoc`'s `needs:<id>.decay_per_tick` and `Threshold` from
  `satisfaction_threshold` (no file path, no literal in the call). For the shipped content,
  `Def("Satiety").Rate == 0.00070` & `Threshold == 0.55`, `Hydration == 0.00110/0.50`,
  `Rest == 0.00045/0.45` (table-driven). A grep guard confirms no numeric rate literal in
  `engine/mind/needs` source.
- [ ] **Missing / malformed `needs:` block → descriptive error**: `Load` returns a non-nil error
  whose message names the problem when (a) `balanceDoc` has no top-level `needs:` key, (b) the
  `needs:` block is empty, (c) an entry is malformed (`decay_per_tick ≤ 0`,
  `satisfaction_threshold` outside `[0,1]`, an unknown sub-key). Table-driven; each case asserts
  the error text identifies the offending field/entry.
- [ ] **Catalog ↔ rate cross-consistency**: `Load` errors when (a) a `Consumable` dimension in
  `needsDoc` has no matching `balanceDoc needs:<id>` rate, (b) a `balanceDoc needs:` key names a
  dimension absent from `needsDoc`, or (c) a `balanceDoc needs:` key names a `Conditional`
  dimension. Each error message identifies the offending id.
- [ ] **Catalog loaded from `content/needs.yaml`, path injected**: each `Def.Posture`,
  `Referent`, `Curve`, `Gain`, `Kind` equals the `needsDoc` entry. The shipped content yields
  exactly the glossary dimensions `Satiety, Hydration, Rest` (consumable) and `Safety, Standing,
  Openness` (conditional).
- [ ] **`IDs()` deterministic order (D12)**: `IDs()` returns the same lexicographically sorted
  order across repeated calls in a process AND yields the identical order in a second
  freshly-`Load`-ed registry from the same bytes (cross-process stability).
- [ ] **Need does NOT encode satisfaction (D5)**: the `Def` struct exposes no field naming an
  action, object, or effect; a grep/struct-shape guard confirms the registry carries only
  rate/posture/setpoint/salience metadata.
- [ ] **No "future need" field (D9)**: `Def` exposes no quantity/schedule/future-need field;
  `Demand(t)` is a pure function returning `Rate · t` (table-driven), proving demand is computed
  not stored.
- [ ] **Conditional needs have `Rate == 0` and no balance entry**: every `Conditional` `Def` has
  `Rate == 0`; `Kinds(Conditional)` returns `Safety, Standing, Openness` for the shipped content.
- [ ] **Forward-roll helpers are pure & clamped (D12)**: `Level(level0, m)` decays a Consumable
  by `Rate·m` clamped to `[0,1]`, leaves a Conditional unchanged; `BreachAt` finds the first
  sub-setpoint crossing or returns `ok=false` past the horizon. Golden table over the shipped
  rates (e.g. `Satiety` from full crosses its 0.55 threshold at the expected minute).
- [ ] **`Salience` per curve**: `Deficit` → `max(0, setpoint − level)·Gain`; `GapToMax` →
  `(1 − level)·Gain`; both ≥ 0 (table-driven).
- [ ] **Conditional driver: no threat → intensity unchanged-then-decays toward 0 (BLOCKER-1)**:
  for the `Safety` `Def`, `UpdateConditionalNeeds(cur, nil, gain, decay)` and
  `UpdateConditionalNeeds(cur, []core.AgentID{}, gain, decay)` both return
  `max(0, cur − decay)` — strictly: starting from `cur == 0` it stays `0` (never goes negative);
  starting from `cur == 0.50` with `decay == 0.10` it returns `0.40`, and N=⌈0.50/0.10⌉=5 repeated
  empty-threat calls drive it to `0`. Table-driven over `(cur, decay)` pairs incl. the `cur==0` /
  `cur<decay` clamp-at-0 case.
- [ ] **Conditional driver: ≥ 1 threat → intensity rises, ∝ count, clamped (BLOCKER-1)**:
  `UpdateConditionalNeeds(0, []core.AgentID{"B"}, gain, decay)` returns `gain` (> 0);
  `UpdateConditionalNeeds(0, []core.AgentID{"B","C"}, gain, decay)` returns `2·gain`;
  the result is clamped to `1.0` once `cur + gain·count ≥ 1` (e.g. `cur==0.9, count==3, gain==0.1`
  → `1.0`, not `1.2`). Result is **monotonically non-decreasing in `len(threats)`**. Table-driven.
- [ ] **Conditional driver: count-only, order-independent, pure (D12)**:
  `UpdateConditionalNeeds(cur, []core.AgentID{"B","C"}, …)` equals
  `UpdateConditionalNeeds(cur, []core.AgentID{"C","B"}, …)` (depends only on count); calling it
  with identical inputs twice returns identical floats; it draws no RNG and reads no clock.
- [ ] **Conditional driver is a no-op on consumable Defs**: `UpdateConditionalNeeds(cur, threats,
  gain, decay)` returns `cur` unchanged for a `Consumable` `Def` (e.g. `Satiety`) regardless of
  the threat list — consumable pressure flows through `Level`/decay only.
- [ ] **Conditional driver constants are injected, not literal (D10)**: a grep guard confirms the
  `perThreatGain`/`decayPerTick` magnitudes appear nowhere as literals in `engine/mind/needs` logic;
  they arrive only as parameters.
- [ ] **Immutable after init**: the public API exposes no mutator; mutating a returned id slice
  does not change the registry.
- [ ] **No literal need name in source (D10)**: grep guard — no `"Satiety"`/`"Safety"`/… literal
  in `engine/mind/needs` logic.

> Structural JSON-schema validation of `content/needs.yaml` and `content/balance.yaml` against
> their schemas, and referential integrity (need ids used by `objects.yaml`/`actions.yaml`
> exist here; balance `needs:` keys match `needs.yaml` ids; the `threats.per_threat_intensity` /
> `threats.safety_decay` scalars exist and are ≥ 0), are **platform/config** ACs — they own the
> file IO and the schemas. This module proves only semantic checks reachable from the injected
> readers and the pure driver behaviour.

## Out of Scope

- Reading the files from disk and JSON-schema validation → `platform/config` (architecture §3).
- Deciding **how** to satisfy a need, inserting provisioning subgoals, and the forward-sim loop
  itself → `engine/mind/planner` (this module supplies only the pure roll helpers it calls).
- Composing a `Value{Dimension, Ref, Posture, Setpoint}`, the per-agent value map (`Known`),
  `Standing`/`Salience` arbitration, and EffValue → `engine/mind/values`.
- Per-agent dynamic need **levels** and their decay during a tick → `engine/agent` / `engine/world`
  state (they call `Def.Level` for consumables and `Def.UpdateConditionalNeeds` for conditionals).
- **Detecting the perceived threats** (scanning perceived entities/signals for a threat tag and
  producing the threat `[]core.AgentID`) → `engine/agent` phase 1 (it then passes the list here).
  This module owns only the intensity arithmetic, not the perception.
- The forward-sim **horizon** and step size (`forward_sim.*`) and the urgency mapping
  (`urgency.from_deficit`) → `content/balance.yaml`, read by `engine/mind/planner` / `engine/agent`.

## Open Questions

- None blocking. **Prior escalations resolved** by human-confirmed decisions:
  (1) `NeedID` is an alias of `core.Dimension` (added to `engine/kernel/core/SPEC.md`), using glossary
  Dimension names — never `hunger`/`social`. (2) Per-need **rate** constants load from the
  `needs:` block in `content/balance.yaml`; the dimension *catalog* remains in `content/needs.yaml`.
  `Load` merges the two injected readers and cross-checks consumable-need rate coverage.
  (3) **BLOCKER-1 (conditional Safety driver)** is resolved by the pure `UpdateConditionalNeeds`
  helper above; its two tuning scalars (`threats.per_threat_intensity`, `threats.safety_decay`)
  are added to `content/balance.yaml`'s `threats:` block and `content/schema/balance.schema.json`,
  threaded into the agent `Config` by `platform/config`. Confirm the two initial values
  (suggested `per_threat_intensity: 0.20`, `safety_decay: 0.05`) with balance tuning.

## Notes

- **Two-source split (post-escalation)**: `content/needs.yaml` is the dimension *catalog*;
  `content/balance.yaml needs:<id>` is the consumable *rate* table. This keeps tunable scalars in
  the single tuning file (`balance.yaml`, the auto-tuning target — testing.md §5) while the
  catalog stays in the needs file. The two are merged at `Load` and cross-validated.
- **Conditional driver lives here, perception lives in the agent (BLOCKER-1).** The arithmetic that
  turns "N threats perceived this tick" into a Safety intensity is a pure need-layer concern and
  belongs here next to `Level`/`Demand` (so it is golden-testable in isolation). *Which* perceived
  entities count as threats (the hostile-tag scan) is the agent's perception job (`engine/agent`
  §F) — keeping the what-is-wanted (need pressure) and how-it-is-sensed (perception) split (D5).
- **Why a count, not the agent ids.** The Safety driver only needs the *number* of perceived
  threats this tick; the `[]core.AgentID` is taken (rather than an `int`) to match the task brief's
  signature and to leave room for a future per-threat weighting (e.g. by distance or hostility) —
  but the P6 implementation reads only `len(threats)`, so it stays count-stable and deterministic.
- The on-disk catalog shape (`content/needs.yaml`) uses `id`, `kind`, `default.{posture,
  setpoint, referent}`, and `salience.{curve, gain}`.
- `Need ≠ Goal ≠ Target` (glossary): this module owns the *need/value dimension* layer only.
- Need **levels** (incl. the conditional Safety intensity) are serialized as part of agent `body`
  state (data-contracts §1) by `persist`; this module fixes the dimension *definitions*.
- `Setpoint`/`Posture`/`Referent` here are *defaults*; individual agents perturb the setpoint by
  disposition in `engine/mind/values`. This module carries the generation defaults only.
</content>
</invoke>
