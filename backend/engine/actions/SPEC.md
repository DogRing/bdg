# SPEC — `engine/actions`

> Status: `DRAFT`
> Leaf level: `L3`  ·  Owner agent: `<filled by implementer>`

## Purpose

Owns the immutable **`Registry`** of atomic **`ActionDef`**s built from `content/actions.yaml`
(D10: actions are data, not code) plus the GOAP reverse index (`Producers map[Pred][]ActionID`).
Every action is **atomic and durative** (D3): this module stores leaf actions only — it builds
**no** task trees, methods, or plans (the planner assembles those). An action carries `Tags`
(which drive gate visibility and planner cost, D4 — there is **no per-action gate list and no
per-action cost field**), a `Duration`, optional need-satisfaction `Effect`/`EffectPerMinute`,
its `Target` kind, and the `produces`/`requires` predicates the planner backward-chains over.
This module knows *what* an action is, never *when to do it* (D5).

## Public Interface

```go
package actions

import (
    "io"
    "github.com/dogring/bdg/backend/engine/core"
)

// ActionID names an atomic action (canonical id from content/actions.yaml, e.g. "Forage").
type ActionID string

// TargetKind classifies what an action acts on (the brief's `target.kind`). It is DERIVED at
// Load from the action's content shape (target_kind / the at_target|near_other predicates),
// so the YAML stays in its existing form — no new schema field (see Notes).
type TargetKind uint8

const (
    TargetNone     TargetKind = iota // no target (Rest, Sleep)
    TargetLocation                   // a Vec2 destination (MoveTo; binds via at_target)
    TargetObject                     // an objects.yaml object_kind (Forage→berry_bush, Craft→tool_bench, …)
    TargetAgent                      // another agent (Signal, Attack; binds via near_other)
)

// Effect is a per-Dimension need delta (glossary: Effect = supply). Keys are need ids
// (core.Dimension); the value is the satisfaction delta applied to that need. Reuses the same
// shape as content/objects.yaml `supply` so a consumed item's supply and an action's direct
// effect are interchangeable to the planner (D9).
type Effect map[core.Dimension]float64

// ActionDef is one immutable atomic action (glossary: Action{Tags, Produces, Duration, Effect}).
// Mirrors a content/actions.yaml entry after platform/config has validated it against
// content/schema/actions.schema.json. All fields are read-only after Load.
type ActionDef struct {
    ID       ActionID    // canonical identifier (docs/glossary.md)
    Tags     []core.Tag  // drives gate visibility (engine/gates) + planner cost (D4). NEVER a
                         // per-action gate list or cost number — both are tag-derived elsewhere.
    Duration core.GameMinutes // BASE durative length in game-minutes (≥ 1); engine scales it.

    Target       TargetKind    // none | location | object | agent (derived; see Notes)
    TargetKindID core.Tag       // the objects.yaml object_kind id when Target == TargetObject
                                // (e.g. "berry_bush"); empty otherwise. (Typed as core.Tag only
                                // for the identifier-string shape; it is an object-kind id.)

    Requires    []core.Pred // preconditions that must hold to START (planner; ALL must hold)
    RequiresAny []core.Pred // alt preconditions (planner; ANY of these satisfies the start gate)
    Produces    []core.Pred // predicates this action makes true on completion (GOAP forward)

    ProducesItem core.Tag // item kind placed into inventory on completion (empty if none)
    ConsumesItem core.Tag // item kind removed; its supply becomes the Effect on completion (D9)

    Effect          Effect // direct need deltas applied ON COMPLETION (empty for consumption acts)
    EffectPerMinute Effect // need deltas accrued EACH game-minute while running (durative supply)

    Interruptible bool // durative actions are interruptible unless explicitly false
}

// Registry is the immutable, read-only set of action definitions plus the GOAP reverse index.
// After Load it never changes (no setters, no exported mutable fields). Safe to share across
// goroutines in the read/plan phase.
type Registry struct{ /* opaque: defs map[ActionID]ActionDef + sorted []ActionID + producers index */ }

// Load parses the actions document from r (the bytes of content/actions.yaml — the path is
// injected by platform/config, NEVER a file path here, keeping the engine IO-free) and builds an
// immutable Registry plus its Producers reverse index. It performs SEMANTIC validation (see
// Invariants) and returns an error describing the FIRST violation. STRUCTURAL JSON-schema
// validation (content/schema/actions.schema.json) and cross-file referential integrity
// (StatIDs in tags, need ids in effects, target_kind/item ids in objects.yaml) are NOT done here
// — they are platform/config's job, run before this call.
func Load(r io.Reader) (*Registry, error)

// Get returns the definition for id and whether it exists.
func (reg *Registry) Get(id ActionID) (ActionDef, bool)

// IDs returns ALL action ids in canonical fixed order: sorted lexicographically by ActionID.
// This is the ONE ordering every consumer uses to iterate actions (D12). The returned slice is
// a copy; identical across calls and across processes for the same content.
func (reg *Registry) IDs() []ActionID

// Has reports whether id is a known action.
func (reg *Registry) Has(id ActionID) bool

// Len returns the number of defined actions.
func (reg *Registry) Len() int

// Producers returns the actions whose `Produces` includes pred, in IDs() order (glossary:
// Producers map[Pred][]Action — the GOAP backward-chaining reverse index). Returns nil for an
// unproduced predicate. The slice is a copy.
func (reg *Registry) Producers(pred core.Pred) []ActionID

// Tags returns the union of all Tags across every action, in lexicographic order. Lets the
// planner / a config check enumerate the tag families in use (D4).
func (reg *Registry) Tags() []core.Tag
```

> `Load` takes an `io.Reader`, not a path — the engine performs **no filesystem IO**
> (architecture §1). `platform/config` opens `content/actions.yaml`, runs the JSON-schema
> validation + referential integrity, and passes the reader/bytes here.

## Dependencies

- `engine/core` — `Tag`, `Pred`, `Dimension`, `GameMinutes`. (Plus a YAML decoder and stdlib
  `sort`.)
- `engine/stats` — **referenced only by contract**: an action's `uses:<StatID>` tags name stats;
  the *cross-check* that those StatIDs exist is `platform/config`'s referential-integrity step,
  not this module. No runtime call into `engine/stats`.
- `engine/gates` — **referenced only by contract**: an action's `Tags` are what `engine/gates`
  matches at planning time. This module does NOT import `engine/gates` (that would create an
  L3→L2 dependency that the planner already mediates) and stores no gate ids.
- **Contract**: `content/schema/actions.schema.json` defines the on-disk shape; `content/actions.yaml`
  is the data. The tag grammar is `content/README.md §Tags`. `platform/config` bridges
  file → schema-validate → referential-integrity → `Load`.

## Owned Data

- `Registry`, `ActionDef`, `Effect`, and the `ActionID`/`TargetKind` types. The `Registry` is
  **immutable after `Load`** and owns its internal map + precomputed sorted id slice + the
  `Producers` reverse index — no other module mutates it. Returned slices/maps are copies.

## Invariants

- **Atomic only; no trees (D3)**: an `ActionDef` is a single leaf action. The registry stores
  **no** `Method`, `Task`, subtask list, or plan. The only relational structure is the
  `Producers` reverse index (a flat `Pred → []ActionID` map for GOAP), which is **not** a tree —
  it is regenerated mechanically from each action's `Produces`. A grep/struct guard confirms no
  subtask/method field exists.
- **No per-action gate list, no per-action cost (D4)**: `ActionDef` carries `Tags` and nothing
  that names a `GateID` or a cost number. Gate visibility is decided by `engine/gates` matching
  these tags; cost is composed from tags in the planner (`balance.yaml cost_terms × tag_levels`).
  Adding gating/cost behaviour is a content edit (a gate or a tag), never a field here (D10).
- **Objects carry supply; actions stay generic (D9)**: a consumption action (`ConsumesItem != ""`)
  carries an **empty** direct `Effect` — its satisfaction is the consumed item's `supply` from
  `content/objects.yaml`, resolved at apply time by the world, not stored on the action. There is
  **no "future need" / quantity / schedule field** anywhere on an action.
- **What, not when (D5)**: this module describes actions; it never decides whether/when to run
  one, never orders or selects actions, and never reads agent state. Selection is the planner.
- **Canonical ordering (D12)**: the `Registry` precomputes a single fixed-order `[]ActionID`
  (sorted lexicographically) via `IDs()`. **All action iteration — here and in every consumer —
  MUST use this slice; ranging over the backing map for logic is forbidden.** `Producers(pred)`
  and `Tags()` return slices in this same lexicographic order.
- **Immutable after init**: `Registry` exposes no setter and no writable field; once `Load`
  returns, contents never change for the run's lifetime. Returned slices/maps are copies.
- **Well-formed at load (semantic check)**: `Load` rejects a duplicate `id`, an empty `tags`
  list, a `duration < 1`, an action that is BOTH a producer-of-item and a consumer-of-item of an
  incompatible shape, and a consumption action that also declares a non-empty direct `Effect`
  (D9 conflict). Errors name the offending action id.
- **No hardcoded action names (D10)**: this package never references `"Forage"`, `"Eat"`, etc. as
  literals in logic; all actions come from the loaded data.
- **No IO**: imports no `os`/`net`/filesystem package; reads from an injected `io.Reader` only.
  No JSON-schema validation here (that lives in `platform/config`).

## Acceptance Criteria (testable)

- [ ] **Loads from an injected `io.Reader`**: `Load` builds a `Registry` from in-memory YAML
  bytes (no file path); the registry contains exactly the action ids in the source. For the
  shipped content this includes the P1 atomic actions `Eat`, `MoveTo`, `Forage`, `Hunt`,
  `Craft`, `Rest` (the brief's eat/move/gather/hunt/craft/rest) plus the rest of the catalog.
- [ ] **`Get` round-trips a definition faithfully**: `Get("Hunt")` returns `Tags` containing
  `uses:Strength`, `uses:Agility`, `effort:high`, `noise:high`, `risk:med`, `violent:low`,
  `abstraction:med`; `Duration == 35`; `Target == TargetObject` with `TargetKindID == "prey"`;
  `Produces == [has_food]`; `ProducesItem == "raw_meat"`. Table-driven over several actions.
- [ ] **`IDs()` ordering is deterministic (D12)**: `IDs()` returns the same lexicographically
  sorted order across repeated calls in a process AND yields the identical order in a second
  freshly-`Load`-ed registry from the same bytes (cross-process stability).
- [ ] **`Producers` reverse index (GOAP)**: `Producers("has_food")` returns `[Forage, Hunt]`
  (lexicographic, D12); `Producers("at_target")` includes `MoveTo`; `Producers("nonexistent")`
  returns nil. Each returned action's `Produces` actually contains the queried predicate.
- [ ] **Target kind derived correctly**: `MoveTo` → `TargetLocation`; `Rest`/`Sleep` →
  `TargetNone`; `Forage`/`Craft`/`Eat` (object/item) → `TargetObject`; `Signal`/`Attack`
  (near_other) → `TargetAgent`. Table-driven.
- [ ] **Atomic-only guard (D3)**: a struct/grep guard confirms `ActionDef` exposes no
  method/task/subtask/plan field; the only relational output is the flat `Producers` index.
- [ ] **No gate/cost field guard (D4)**: a struct/grep guard confirms `ActionDef` exposes no
  field naming a `GateID` and no numeric cost field; only `Tags` drive gates/cost.
- [ ] **Consumption action carries no direct Effect (D9)**: `Get("Eat")` has `ConsumesItem ==
  "any_food"` and an EMPTY `Effect`; `Load` errors on a synthetic action that both consumes an
  item and declares a non-empty `Effect`.
- [ ] **Semantic rejects**: `Load` errors (table-driven, each naming the offending id) on a
  duplicate id, an empty `tags`, and `duration < 1`.
- [ ] **Immutable after init**: the public API exposes no mutator; mutating a returned id slice,
  `Producers` slice, or `ActionDef.Tags` copy does not change the registry.
- [ ] **No literal action name in source (D10)**: grep guard — no `"Forage"`/`"Eat"`/… literal in
  `engine/actions` logic.

> Structural JSON-schema validation of `content/actions.yaml` against
> `content/schema/actions.schema.json`, and referential integrity (every `uses:<StatID>` tag
> names a stat in `content/stats.yaml`; every effect/supply need id exists in
> `content/needs.yaml`; every `target_kind`/item id exists in `content/objects.yaml`) are
> **platform/config** ACs — it owns the file IO + the schemas. This module proves only semantic
> checks reachable from the injected reader.

## Out of Scope

- Reading the file from disk, JSON-schema validation, and cross-file referential integrity →
  `platform/config` (architecture §3, `content/README.md §Load & validate flow`).
- **Gate visibility evaluation** (matching an action's `Tags` to gates) → `engine/gates`
  (this module supplies the `Tags`; it does not evaluate them).
- **Tag-derived action cost** (`cost_terms × tag_levels`) → `engine/planner`
  (`content/balance.yaml`). No cost lives here.
- **Selecting / ordering / scheduling actions, HTN methods, GOAP search, provisioning** →
  `engine/planner` (this module supplies only the `ActionDef`s and the `Producers` index).
- **Applying an action's Effect / consuming an item / resolving the consumed item's supply** →
  `engine/world` apply phase (reads `objects.yaml` supply, D9).
- **Outcome resolution** (does the Hunt succeed, scaled by Real Stats) → `engine/world` /
  `engine/agent`; this module only declares the action shape.

## Open Questions

- **Object-kind targeting representation (NOT blocking P1).** The brief asked for an explicit
  `target: {kind, required_tags}` object on each action. The existing `content/actions.yaml`
  (already frozen, schema_version 1) expresses the target via `target_kind` + the
  `at_target`/`near_other` predicates instead, which is more uniform with the GOAP `requires`
  model and avoids a schema change. This SPEC therefore **derives** `TargetKind` at Load rather
  than adding a new content field. If a future action needs `required_tags` on its target (e.g.
  "any object tagged `flammable`"), add a `target_tags: []Tag` field to
  `content/schema/actions.schema.json` and surface it here — flag to architect before P-anything
  that needs it.
- **`Hunt`'s "animal" target.** The brief calls Hunt's target an *agent (animal)*. In the
  current content an animal is an `objects.yaml` object_kind (`prey`, `mobile: true`), so Hunt is
  `TargetObject(prey)`, not `TargetAgent`. If animals become first-class agents later, Hunt's
  derived `TargetKind` flips to `TargetAgent` with no SPEC change (the derivation reads the
  resolved kind). Recorded so the planner/world author knows prey is an object today.

## Notes

- **`TargetKind` derivation (no new schema field)**: at `Load`, `Target` is computed from the
  content shape — `target_kind` present → `TargetObject` (with `TargetKindID` = that id);
  else `at_target` in `Produces` (the action *is* the move, e.g. `MoveTo`) → `TargetLocation`;
  else `near_other` in `Produces` (the action *is* the approach toward an agent, e.g. `Approach`)
  → `TargetAgent`; else `near_other` in `requires` (a social action acting on a nearby agent)
  → `TargetAgent`; else `TargetNone`. This keeps the on-disk YAML in
  its existing, schema-valid form (the brief's `{kind, required_tags}` object is represented by
  the existing `target_kind` + predicate convention; see Open Questions).
- **Tag grammar** is documented once in `content/README.md §Tags` and `content/gates.yaml`
  header; this module treats tags as opaque `core.Tag` strings and never interprets a family.
  The `uses:<Stat>` / `effort:*` / `risk:*` / `violent:*` / `noise:*` / `abstraction:*` /
  `norm:transgressive` / `social*`/`cooperative` families are read by gates (`engine/gates`) and
  the planner cost — not here.
- **Item supply vs direct effect (D9)**: `Effect`/`EffectPerMinute` are for self-sourced
  satisfaction (e.g. `Sleep` → `Rest`). A consumption action leaves `Effect` empty and names
  `ConsumesItem`; the world resolves that item's `supply` from `content/objects.yaml` at apply
  time. Both shapes are `Effect` (`map[Dimension]float64`) so the planner treats them uniformly
  when forward-simulating need satisfaction.
- The `Producers` index is the glossary `Producers map[Pred][]Action`; it is the engine-side
  GOAP reverse index the planner backward-chains over. It is derived state — built at `Load`,
  never serialized (data-contracts §1 carries no action catalog; the catalog is fixed by
  `config_hash`, data-contracts §3).
- `Duration` is `core.GameMinutes` (the authored unit, `core/SPEC.md`); the engine scales the
  base duration by stats/distance at execution time (planner/world), not here.
