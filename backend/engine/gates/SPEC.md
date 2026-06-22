# SPEC — `engine/gates`

> Status: `READY` (P3 implemented & tested: body-scalar leaves, cost_rule, stamina/apathy/adrenaline gates + conscience urgency-relief. Golden `testdata/golden/p3_gates.json` + table tests in `p3_gates_test.go`.)
> Leaf level: `L2`  ·  Owner agent: `<filled by implementer>`

## Purpose

Owns the immutable **`Registry`** of `Gate` definitions built from `content/gates.yaml`
(D10: gates are data, not code) and the recursive **predicate-tree** evaluator every gate runs
through. A `Gate` is `{id, tags, expr}`: it is matched to an action **by the action's Tags**
(D4) — never by a per-action function — and its boolean `expr` (a recursive AND/OR/NOT tree of
stat-comparison, body-scalar comparison, and tag-membership leaves) decides whether the action is
**visible** to the agent. An action is visible iff **every** matching gate's `expr` is true
(hard AND across matching gates). Gates are evaluated by the planner **at planning time** against
an `AgentSnapshot` reading `ToM[self]` stats (D8) plus the live **body scalars** (Stamina, Mood,
Adrenaline, Urgency); they are never stored on objects (D5: the gate lives on the action side, not
the goal/object side). In addition to the boolean verdict, a gate may emit a **`CostMultiplier`**
(default `1.0`) — the one numeric channel gates carry — used by the planner to scale tag-derived
cost for the matched action (the Adrenaline gate's `0.5×` discount on high-effort/risky/violent
actions).

## Current state (P1/P2 — IMPLEMENTED, schema_version 2, DO NOT regress)

Already shipped and tested (`gates.go`, `eval.go`, `gates_test.go`, `golden_test.go`):

- **`capability_floor`** — an action tagged `uses:<Stat>` is visible only if `ToM[self][<Stat>] ≥ 0.15`
  (one OR/`not tag` branch per capability; D4 tag-matched).
- **`knowledge`** — `abstraction:low|med|high` actions require `ToM[self].Intelligence ≥ 0.20|0.45|0.70`.
- **`conscience`** (base) — `norm:transgressive` actions visible iff `ToM[self].Honesty < 0.40` OR
  `ToM[self].Aggression ≥ 0.65`.
- The recursive `expr` parser/evaluator (`GateExpr{Stat,Op,Value | Tag | And | Or | Not}`), the
  AND-of-matching-gates `Evaluate`, the `Trace`, `IDs()`, `Reads()`, and `Load(io.Reader, *stats.Registry)`.
- Boolean visibility only; `Evaluate` returns `Result{Visible, Trace}` with **no cost channel**.

P3 **extends** this — it does not rewrite it. The three gates above keep their exact predicates.
The schema/contract bumps **2 → 3** (the body-scalar leaf grammar + the `CostMultiplier` channel
are backward-incompatible additions; data-contracts §0).

## Implement (P3 — what this batch adds)

Four new gate behaviours, all expressed as **content `expr` trees** over an extended leaf grammar
(D4/D10 — a data edit, not a Go branch):

1. **`stamina` gate** — actions tagged `effort:high` are visible only while
   `Stamina ≥ gates.stamina_effort_high_threshold` (`0.20`, `balance.yaml`). A drained agent
   (`Stamina < 0.20`) cannot **see** high-effort actions. Body-scalar leaf `{body: Stamina, op, value}`.
2. **`apathy` gate** — when `Mood ≤ gates.apathy_mood_threshold` (`-0.60`), actions tagged
   `abstraction:med` or `abstraction:high` are **invisible** (Mood-driven cognitive narrowing —
   a low-Mood agent stops considering abstract methods). Body-scalar leaf `{body: Mood, …}` +
   tag leaves.
3. **`conscience` extension** — the base `conscience` predicate stays, but gains an **urgency
   relief branch**: when `Urgency > gates.conscience_urgency_threshold` (`0.70`, `balance.yaml`),
   the moral barrier lowers (`Honesty < 0.55` OR `Aggression ≥ 0.50`) so theft/violence become
   visible under pressure (scenario B; scenario D's "others' wellbeing lowers the threshold" path
   is captured as a **design note** — see Out of Scope / Open Questions — P3 golden covers B only).
4. **`adrenaline` gate (cost channel)** — when `Adrenaline ≥ 0.70`, the `CostMultiplier` for an
   action carrying `effort:high`, `risk:high`, or any `violent:*` tag is set to
   `gates.adrenaline_cost_multiplier` (`0.50`). This gate is **always visibility-true** (it never
   hides an action); its only effect is the cost discount, surfaced to the planner via
   `Result.CostMultiplier`. After the adrenaline crash (`Adrenaline` drained, `Stamina < 0.20`),
   high-effort actions return to **invisible** through the `stamina` gate — no special-case here.

## Public Interface

```go
package gates

import (
    "io"
    "github.com/dogring/bdg/engine/core"
    "github.com/dogring/bdg/engine/stats"
)

// GateID names a gate definition (canonical id from content/gates.yaml).
type GateID string

// Op is a stat/body-scalar comparison operator used by a GateExpr leaf.
type Op uint8

const (
    OpGE Op = iota // ">="
    OpGT           // ">"
    OpLE           // "<="
    OpLT           // "<"
    OpEQ           // "=="
    OpNE           // "!="
)

// BodyScalar names a dynamic Body field a body-scalar leaf compares (glossary §Dynamics).
// CANONICAL strings, cross-checked by platform/config; NOT a hardcoded switch in eval logic —
// resolved against AgentSnapshot by a sorted-key lookup table (D12).
type BodyScalar string

const (
    BodyStamina    BodyScalar = "Stamina"    // [0, StaminaMax]
    BodyMood       BodyScalar = "Mood"       // signed
    BodyAdrenaline BodyScalar = "Adrenaline" // [0, AdrMax]
    BodyUrgency    BodyScalar = "Urgency"    // [0,1]
)

// GateExpr is one node of a recursive boolean predicate tree (mirrors content/gates.yaml's
// `expr`, validated by content/schema/gates.schema.json schema_version 3). EXACTLY ONE shape is
// populated per node:
//   • stat-comparison leaf: Stat != "" — true iff cmp(AgentSnapshot.SelfStats[Stat], Op, Value)
//     (reads ToM[self], D8).
//   • body-scalar leaf:     Body != "" — true iff cmp(AgentSnapshot.<Body scalar>, Op, Value)
//     (reads the live Body scalar; NEW in v3).
//   • tag-membership leaf:  Tag != ""  — true iff the candidate Action carries exactly Tag.
//   • composite:            And / Or non-nil, or Not non-nil.
// Most callers use Registry.Evaluate and never touch GateExpr directly.
type GateExpr struct {
    // leaf: ToM[self] stat comparison (D8)
    Stat  core.StatID // empty unless this is a stat leaf
    Op    Op
    Value float64
    // leaf: live Body-scalar comparison (NEW v3)
    Body BodyScalar // empty unless this is a body-scalar leaf
    // leaf: tag membership
    Tag core.Tag // empty unless this is a tag leaf
    // composite
    And []GateExpr // true iff every child is true
    Or  []GateExpr // true iff any child is true
    Not *GateExpr  // true iff the child is false
}

// CostRule is the per-gate cost-multiplier rule (NEW v3). A gate with a non-nil CostRule emits
// Mult (instead of the default 1.0) for a matched action whose `expr` (the rule's gating
// predicate) is true. Gates WITHOUT a CostRule never touch CostMultiplier (it stays 1.0). The
// `adrenaline` gate is the sole P3 user (its predicate is the Adrenaline≥0.70 AND tag-membership
// branch; Mult = gates.adrenaline_cost_multiplier).
type CostRule struct {
    Mult float64 // the multiplier emitted when this gate's expr is true (e.g. 0.50)
}

// AgentSnapshot is the READ-ONLY view a gate evaluates against, supplied by the caller (the
// planner / agent) for the agent currently deliberating. Gates MUST NOT mutate it. Stat leaves
// read SelfStats = ToM[self] (D8); body-scalar leaves read the live Body fields; Real Stats are
// not consulted by gate predicates.
type AgentSnapshot struct {
    SelfStats stats.Stats                // ToM[self] — the stat values decisions read (D8)
    // Live Body scalars (NEW v3) — read by body-scalar leaves and the adrenaline CostRule.
    Stamina    float64                   // glossary §Dynamics; [0, StaminaMax]
    Mood       float64                   // signed
    Adrenaline float64                   // [0, AdrMax]
    Urgency    float64                   // [0,1]
    Known     map[core.ObjectID]struct{} // membership only; used by the planner's known-check
}

// Action is the minimal action view a gate reads: its Tags and the target it would act on.
type Action struct {
    Tags   []core.Tag
    Target core.ObjectID // empty if the action has no object target
}

// Registry is the immutable, read-only set of gate definitions. After Load it never changes.
type Registry struct{ /* opaque: gate defs (parsed GateExpr trees + optional CostRule) + sorted []GateID + tag index */ }

// Load parses the gates document from r (the bytes of content/gates.yaml — the path is injected
// by platform/config, NEVER a file path here) together with the already-loaded stats.Registry.
// It builds each gate's GateExpr tree + optional CostRule and performs SEMANTIC validation (see
// Invariants): every StatID referenced by a stat leaf exists in reg, every Body in a body-scalar
// leaf is a known BodyScalar, exactly one shape per node. STRUCTURAL JSON-schema validation
// (content/schema/gates.schema.json, schema_version 3) is platform/config's job, run before this.
func Load(r io.Reader, reg *stats.Registry) (*Registry, error)

// ── Evaluation ────────────────────────────────────────────────────────────────

// Evaluate is the entry the planner calls per candidate action. It returns the action's gate
// verdict: Visible (the AND, across every gate whose `tags` match the action, of that gate's
// `expr`), the per-gate Trace, and the aggregated CostMultiplier (the PRODUCT of every matching
// gate's emitted multiplier; default 1.0 when no CostRule fires). A gate with empty `tags`
// matches every action; an action matched by no gate is Visible with CostMultiplier 1.0.
func (reg *Registry) Evaluate(act Action, a AgentSnapshot) Result

// Result bundles one candidate's gate verdict for the planner.
type Result struct {
    Visible        bool    // AND of every matching gate's expr
    CostMultiplier float64 // PRODUCT of matching gates' cost rules; default 1.0 (NEW v3)
    Trace          Trace   // per matching-gate verdicts, ordered by GateID (D12)
}

// Trace is the per-gate breakdown for the why-trace (ordered by GateID for determinism).
type Trace []GateContribution

// GateContribution is one matching gate's verdict.
type GateContribution struct {
    Gate   GateID
    Passed bool    // this gate's expr evaluated true (the visibility predicate)
    Mult   float64 // the multiplier this gate contributed (1.0 if it has no CostRule)
}

// ── Introspection ────────────────────────────────────────────────────────────

// IDs returns ALL gate ids in canonical fixed order (sorted lexicographically). D12.
func (reg *Registry) IDs() []GateID

// Reads returns the union of StatIDs referenced by any gate's stat leaves, in IDs() order, so
// callers can pre-fill an AgentSnapshot's SelfStats vector.
func (reg *Registry) Reads() []core.StatID

// ReadsBody returns the union of BodyScalars referenced by any gate's body-scalar leaves or
// CostRules, sorted, so callers know which live Body fields the snapshot must carry (NEW v3).
func (reg *Registry) ReadsBody() []BodyScalar
```

> `Load` takes an `io.Reader`, not a path — the engine performs **no filesystem IO**
> (architecture §1). `platform/config` opens `content/gates.yaml`, runs the JSON-schema
> validation (schema_version 3), and passes the reader + the already-built `stats.Registry` here.

## Dependencies

- `engine/core` — `StatID`, `Tag`, `ObjectID`. (Plus a YAML decoder and stdlib `sort`.)
- `engine/stats` — `Stats`, `*Registry` (to reject a stat leaf naming an unknown `StatID` via
  `Registry.Has`, and to range stat ids in `IDs()` order for `Reads`).
- **Contract**: `content/schema/gates.schema.json` (schema_version **3**) defines the on-disk
  predicate-tree + body-scalar leaf + cost-rule shape; `content/gates.yaml` is the data **and the
  authoritative contract for this evaluator**. `content/balance.yaml` `gates:` block supplies the
  thresholds/multiplier authored into the gate `expr`/`CostRule` values (the numbers live in the
  YAML; this module hardcodes none). `platform/config` bridges file → schema-validate → `Load`.

## Owned Data

- `Registry`, `GateExpr`, `CostRule`, and the `GateID`/`Op`/`BodyScalar`/`Result`/`Trace` value
  types. The `Registry` is **immutable after `Load`** and owns its parsed `GateExpr` trees +
  optional `CostRule`s + precomputed sorted id slice + tag-match index — no other module mutates it.
- `AgentSnapshot` and `Action` are **borrowed, read-only** views the caller owns; this module never
  mutates them and never retains a reference past the call.

## Invariants

- **Tag-derived, no per-action code (D4)**: a gate selects the actions it governs **only** via its
  `tags` list (applies iff the action carries ANY listed tag; empty = all). A `{tag}` leaf may
  further test the action's own tags inside the predicate. Adding behaviour means adding a gate
  entry in content, not a Go function (D10). The four P3 gates are pure content additions.
- **Decisions read `ToM[self]` for STAT leaves (D8)**: a stat leaf resolves against
  `AgentSnapshot.SelfStats`, NEVER Real Stats. Gate logic never "corrects" SelfStats toward Real
  Stats — calibration is owned by `engine/tom` (β). Underestimation stays self-sealing.
- **Body-scalar leaves read the LIVE Body, not ToM (NEW v3)**: Stamina/Mood/Adrenaline/Urgency are
  objective dynamic state (glossary §Dynamics), not beliefs — a body-scalar leaf reads
  `AgentSnapshot.Stamina/Mood/Adrenaline/Urgency` directly. This is the intentional split: *can I
  reach this rung* is belief (D8, stat leaf); *am I exhausted / panicked right now* is body fact.
  The Body-scalar map is resolved by a sorted-key lookup, never a hardcoded `switch` driving logic.
- **Boolean visibility + a single product cost channel**: a gate's `expr` yields one bool;
  `Result.Visible` is the AND of matching gates. The **only** numeric channel is `CostMultiplier`,
  the PRODUCT of matching gates' fired `CostRule.Mult` (1.0 default). A gate without a `CostRule`
  contributes `Mult = 1.0`. The product is order-independent (commutative) — D12-safe regardless of
  iteration order, but the `Trace` is still emitted in `GateID` order.
- **Gates live on the action side, evaluated at planning time (D5)**: gates are evaluated by the
  planner per candidate action against an `AgentSnapshot`; never stored on objects/values/goals. A
  gate result is transient (recomputed each deliberation).
- **`expr` evaluation semantics**: `And` = true iff every child true; `Or` = true iff any child
  true; `Not` = negation; a stat leaf compares `SelfStats[Stat]`; a body-scalar leaf compares the
  resolved Body field; a tag leaf is membership of `Tag` in `act.Tags` (absent stat/scalar treated
  per its zero default). Evaluation is short-circuit but **side-effect-free**.
- **Canonical ordering (D12)**: gates are stored with a precomputed sorted `[]GateID`; all gate
  iteration (matching, `Trace` assembly, `Reads`/`ReadsBody`) uses this slice — never `map`
  iteration for logic. The `Trace` slice is ordered by `GateID`.
- **Immutable after init**: `Registry` exposes no setter and no writable field; returned slices are
  copies.
- **Unknown references rejected at load time**: `Load` fails if a stat leaf names a `StatID` absent
  from `reg`, if a body-scalar leaf names a `Body` outside the known `BodyScalar` set, if a node
  populates more than one shape, or if a `CostRule.Mult` is ≤ 0.
- **Read-only inputs**: `AgentSnapshot` and `Action` are never mutated. Evaluation is a pure
  function of `(Registry, Action, AgentSnapshot)`.
- **No IO (architecture §1)**: imports no `os`/`net`/filesystem package; reads from an injected
  `io.Reader` only. No JSON-schema validation here (that lives in `platform/config`).

## Acceptance Criteria (testable)

- [x] **P1/P2 gates unchanged (no regression)**: the shipped `capability_floor`, `knowledge`, and
  base `conscience` predicates still load and evaluate identically; the existing golden snapshot
  (or a regenerated, human-approved one) still passes for the non-body-scalar cases.
- [x] **Loads schema_version 3 from an injected `io.Reader`**: `Load` builds a `Registry` from
  in-memory YAML bytes against a stub `stats.Registry`; the registry contains exactly the gate ids
  in the source (`adrenaline`, `apathy`, `capability_floor`, `conscience`, `knowledge`, `stamina`).
- [x] **`stamina` gate hides `effort:high` when drained**: an action tagged `effort:high` with
  `AgentSnapshot.Stamina = 0.10` → `Result.Visible == false`; the same action at `Stamina = 0.30`
  → `Visible == true`; an `effort:low`/`effort:none` action is unaffected at any Stamina.
  Table-driven; **golden snapshot** for the `Stamina = 0.10` (or `0.0`) case (task AC: "Stamina 0
  agent: effort:high invisible").
- [x] **`apathy` gate narrows cognition at low Mood**: `Mood = -0.70` → an `abstraction:med` or
  `abstraction:high` action is `Visible == false`; an `abstraction:low` action stays visible;
  `Mood = -0.50` → all visible. Table-driven over the `-0.60` boundary.
- [x] **`conscience` urgency relief (scenario B)**: a `norm:transgressive` action with
  `ToM[self].Honesty = 0.60` and `Aggression = 0.45` (fails the base predicate) becomes
  `Visible == true` when `Urgency = 0.80` (> `0.70`) via the relief branch (`Honesty < 0.55` OR
  `Aggression ≥ 0.50` — here neither, so it is the **lowered** bound that flips: confirm the
  authored branch makes Take visible at the relaxed thresholds with the intended disposition). At
  `Urgency = 0.50` the base predicate governs and it stays hidden. Table-driven + tie-in to the
  agent/planner scenario-B golden (Take appears in PlanBuilt steps).
- [x] **`adrenaline` gate cost discount**: an action tagged `effort:high` with
  `Adrenaline = 0.75` → `Result.CostMultiplier == 0.50`; the same action at `Adrenaline = 0.40`
  → `CostMultiplier == 1.0`; a `risk:high` and a `violent:high` action both get the discount at
  high Adrenaline; an `effort:low` non-violent action stays `1.0`. The gate never sets
  `Visible == false`. Table-driven.
- [x] **CostMultiplier is the product of matching rules, default 1.0**: an action matched by no
  cost-rule gate yields `CostMultiplier == 1.0`; the adrenaline discount composes multiplicatively
  if a second cost rule is ever added (single-rule today → exactly `0.50`).
- [x] **Body-scalar leaf reads live Body, not ToM (NEW v3)**: a `stamina`/`apathy`/urgency leaf
  evaluation changes only when the corresponding `AgentSnapshot` body field changes; `SelfStats`
  alone never flips a body-scalar leaf. (Guards the D8-vs-body split.)
- [x] **Decisions read `ToM[self]` for stat leaves (D8)**: `capability_floor`/base-`conscience`
  evaluation uses `SelfStats`; the `AgentSnapshot` exposes no Real-Stats field.
- [x] **Unknown references rejected at load (semantic check)**: `Load` errors on a stat leaf naming
  an absent StatID, a body-scalar leaf naming an unknown `Body`, a node with >1 shape, or a
  `CostRule.Mult ≤ 0`. Table-driven.
- [x] **Determinism (D12)**: `IDs()`, `Trace` ordering, `Reads()`/`ReadsBody()` are lexicographic
  and identical across repeated calls and a second `Load` of the same bytes. `Evaluate` is a pure
  function: same `(Action, AgentSnapshot)` → same `Result` (golden over shipped content + fixtures).
- [x] **Read-only inputs**: a property test confirms `AgentSnapshot` and `Action.Tags` are
  unchanged after `Evaluate`.
- [x] **`Reads()`/`ReadsBody()` unions**: `Reads()` returns exactly the stat-leaf StatIDs
  (`Aggression, Agility, Honesty, Intelligence, Strength`); `ReadsBody()` returns
  (`Adrenaline, Mood, Stamina, Urgency`), each sorted.

> Structural JSON-schema validation of `content/gates.yaml` against
> `content/schema/gates.schema.json` (schema_version 3), and referential integrity (StatIDs,
> BodyScalars, balance-key cross-refs) at the file level, are **platform/config** ACs.

## Data Contract (content/gates.yaml + balance.yaml + schema)

`content/gates.yaml` — **schema_version 2 → 3** (data-contracts §0 incompatible bump). The three
existing gates are kept verbatim. Adds the body-scalar leaf, the `cost_rule` field, and the four
new gates. The numeric thresholds/multiplier are taken from `content/balance.yaml` `gates:` /
`adrenaline:` blocks (authored into the gate values; this module reads only the YAML it is handed).

```yaml
schema_version: 3

gates:
  # ── kept verbatim from v2 (P1/P2) ──────────────────────────────────────────
  - id: capability_floor          # uses:<Stat> → ToM[self][Stat] >= 0.15   (unchanged)
  - id: knowledge                 # abstraction:low|med|high → Intelligence floors (unchanged)

  # ── conscience: base predicate kept + urgency-relief OR branch (P3) ────────
  - id: conscience
    tags: [ "norm:transgressive" ]
    expr:
      or:
        # base barrier (v2, unchanged): dispositional drive overrides the norm
        - { stat: Honesty,    op: "<",  value: 0.40 }
        - { stat: Aggression, op: ">=", value: 0.65 }
        # P3 urgency relief: under pressure the barrier lowers (scenario B)
        - and:
            - { body: Urgency, op: ">", value: 0.70 }   # gates.conscience_urgency_threshold
            - or:
                - { stat: Honesty,    op: "<",  value: 0.55 }
                - { stat: Aggression, op: ">=", value: 0.50 }

  # ── stamina (P3): effort:high requires Stamina >= 0.20 ─────────────────────
  - id: stamina
    tags: [ "effort:high" ]
    expr:
      or:
        - { not: { tag: "effort:high" } }
        - { body: Stamina, op: ">=", value: 0.20 }      # gates.stamina_effort_high_threshold

  # ── apathy (P3): Mood <= -0.60 hides abstraction:med|high ──────────────────
  - id: apathy
    tags: [ "abstraction:med", "abstraction:high" ]
    expr:
      or:
        - { body: Mood, op: ">", value: -0.60 }         # gates.apathy_mood_threshold
        - and:
            - { not: { tag: "abstraction:med" } }
            - { not: { tag: "abstraction:high" } }

  # ── adrenaline (P3): cost discount only (never hides) ──────────────────────
  - id: adrenaline
    tags: [ "effort:high", "risk:high", "violent:low", "violent:med", "violent:high" ]
    cost_rule: { mult: 0.50 }                            # gates.adrenaline_cost_multiplier
    expr:                                                # the multiplier fires iff this is true
      and:
        - { body: Adrenaline, op: ">=", value: 0.70 }
        - or:
            - { tag: "effort:high" }
            - { tag: "risk:high" }
            - { tag: "violent:low" }
            - { tag: "violent:med" }
            - { tag: "violent:high" }
```

> **Visibility vs cost-rule gates.** A gate with a `cost_rule` uses its `expr` as the *rule fire*
> condition (emit `Mult` when true), and its **visibility verdict is always true** (it never hides
> an action). A gate without a `cost_rule` is a normal visibility gate (its `expr` true = visible).
> The implementer must keep these two roles distinct so the `adrenaline` gate cannot accidentally
> hide a high-effort action — that hiding is the `stamina` gate's job after the crash.

`content/balance.yaml` — **add a `gates:` block and one `adrenaline:` key** (schema bump in
`content/schema/balance.schema.json` to match):

```yaml
gates:
  stamina_effort_high_threshold: 0.20   # stamina gate floor for effort:high visibility
  apathy_mood_threshold:        -0.60   # mood at/below which abstraction:med+ is hidden
  conscience_urgency_threshold:  0.70   # urgency above which the conscience barrier lowers
  adrenaline_cost_multiplier:    0.50   # cost ×multiplier when Adrenaline >= 0.70 on hard actions
adrenaline:
  # (existing keys: trigger_urgency, surge, decay, max)
  crash_stamina_penalty:         0.50   # Stamina -= AdrDecay × this on the adrenaline crash (agent/SPEC)
```

`content/schema/gates.schema.json` — bump `schema_version.const` 2 → 3, add the `body` leaf
(`{body: <BodyScalar>, op, value}`) to the `oneOf` shapes (mutually exclusive with `stat`/`tag`/
composites), and add an OPTIONAL `cost_rule: { mult: <positive number> }` property on a `gate`.
`platform/config` cross-checks each `body` against the canonical `BodyScalar` set and each gate
threshold value against the `balance.yaml gates:` block.

## Out of Scope

- Reading the file from disk and JSON-schema validation → `platform/config` (architecture §3).
- **Driving the body scalars** (computing Stamina/Mood/Adrenaline/Urgency over time, the surge/
  crash, the stamina drain/regen) → `engine/agent` (`backend/engine/agent/SPEC.md`). This module
  only *reads* the scalars the agent puts in the snapshot. The agent injects them when it builds
  the planner/gate snapshot each tick.
- **Tag-derived base cost** (`cost(action) = Σ planner.tag_costs[tag]`) and applying the
  `CostMultiplier` → `engine/planner` (`backend/engine/planner/SPEC.md`). This module *emits* the
  multiplier; the planner multiplies its tag-cost sum by `Result.CostMultiplier`.
- **Scenario D's "others' wellbeing lowers the conscience threshold" branch** — the
  Social/Standing-driven relief (a love-driven crime where another's wellbeing, not the actor's
  urgency, lowers the barrier) is a **design note** for a later pass. It needs a wellbeing/affinity
  signal the boolean leaf grammar over `ToM[self]` + body scalars cannot express today. P3 ships
  the **urgency** relief (scenario B) only; D is recorded under Open Questions. Do NOT hardcode a
  "wellbeing" branch here (D2).
- Deciding *whether to attempt* a visible action, goal arbitration, `Stickiness`/`Budget` →
  `engine/planner`.
- Self-calibration of `ToM[self]` (β) and gossip → `engine/tom`.

## Open Questions

- **Scenario D (love-driven crime) relief path — NOT blocking P3 (flag to architect).** design §3
  / testing.md §4 fixture D wants *another's* wellbeing (Social/Standing rising for a referent
  Other) to lower the conscience threshold "more strongly than urgency". The current boolean leaf
  grammar reads `ToM[self]` stats + the actor's own body scalars — it has no per-referent wellbeing
  term. P3 implements only the urgency relief (B). To add D properly we likely need either (a) a new
  body-scalar-like input carrying "max ΔStanding for a cared-for Other" computed by the agent and
  put in the snapshot, or (b) move the D relief into the planner's value-gradient (where referent
  wellbeing already lives). Escalate before implementing D so we don't hardcode a "love" meta-system
  (D2). The SPEC records D's design intent; the golden covers B.
- **Cost channel re-entering gates (RESOLVED for P3, confirm with planner author).** schema_version
  2 deliberately made gates boolean-only and moved cost to the planner. P3 re-introduces ONE numeric
  channel (`CostMultiplier`) for the Adrenaline discount, because the discount is naturally
  tag+body-conditioned (the same matching machinery gates already own) and the planner only needs
  to multiply. This is a scoped, multiplicative channel — NOT the return of the old weighted-term
  gates. The planner SPEC must read `Result.CostMultiplier` and apply it to its tag-cost sum.
  Confirm the planner author wires this (the planner currently ignores the field).
- **Body-scalar leaf vs planner urgency relaxation (overlap, NOT blocking P3).** The planner
  already has an `Urgency > urgency_threshold` relaxation that flips a failing gate visible
  (search.go). With the conscience urgency-relief branch now IN the gate `expr`, the two could
  double-relax. Decision for P3: the **gate** owns the conscience-specific relief (precise: only
  lowers the moral barrier, only for `norm:transgressive`); the planner's blanket relaxation should
  be **scoped down or removed** so it does not also unlock, e.g., capability-gated actions under
  urgency. Flag to the planner author — pick ONE owner per relief to avoid double-counting.

## Notes

- The `expr` grammar, the gate match rule (`tags` = ANY-of), and the new `body` leaf + `cost_rule`
  are documented at the top of `content/gates.yaml`; this module implements that grammar exactly.
  On any mismatch, fix the contract there first (CLAUDE.md SPEC-first rule).
- A **stat** leaf reads `ToM[self]` (D8); a **body** leaf reads the live Body scalar — keep the two
  resolution paths separate. The `AgentSnapshot` carries both: `SelfStats` for beliefs, the four
  float fields for body facts.
- The `Trace` (now carrying per-gate `Passed` + `Mult`) feeds the why-trace: surface each matching
  gate's verdict and any cost multiplier in `PlanBuilt` (data-contracts §4) so "why was this hidden"
  and "why was this cheap under adrenaline" are both reconstructable (NFR-3).
- `schema_version` bumped 2→3 with this contract change (data-contracts §0). `platform/config`
  refuses a `content/gates.yaml` whose `schema_version` does not match the schema's `const`.
