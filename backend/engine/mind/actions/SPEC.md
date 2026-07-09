# SPEC — `engine/mind/actions`

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

It also owns the **recipe-mediated `Craft`** and the **terrain-altering `Mine`** action shapes
(Materials & Crafting plan §0 "Recipe model — FINAL", P_m3/P_m4): both are ordinary `ActionDef`s — the
*recipe binding* (which recipe), the *slot/alternative input application* (FINAL: per slot the first
satisfiable alternative; `wear` vs `consume` modes; most-decayed-first / most-worn ordering), the
*`ambient` station gate*, the *per-recipe `duration`*, the *`basis_stat` outcome roll* (incl. produced
durability), and the *node depletion + `SetTerrain`* (Xm2/Xm3) are all the **world apply phase**'s job
(see Out of Scope). This module only declares the action shape + the `RecipeMediated` flag the world
reads. A recipe-mediated action (Craft) carries **no** tool tag, **no** target, and **no** duration
(the recipe supplies them); a non-recipe action (`Mine`) keeps an action-level `tool:<family>` tag.

## Public Interface

```go
package actions

import (
    "io"
    "github.com/dogring/bdg/engine/kernel/core"
)

// ActionID names an atomic action (canonical id from content/actions.yaml, e.g. "Forage").
type ActionID string

// TargetKind classifies what an action acts on (the brief's `target.kind`). It is DERIVED at
// Load from the action's content shape (target_kind / the at_target|near_other predicates), so the
// YAML stays in its existing form. A recipe-mediated action (Craft) has NO target_kind — its
// station(s) are the bound recipe's `ambient` tags, bound in-range by the world (TargetNone here).
type TargetKind uint8

const (
    TargetNone     TargetKind = iota // no target (Rest, Sleep, Craft — Craft's stations are recipe `ambient`)
    TargetLocation                   // a Vec2 destination (MoveTo; binds via at_target)
    TargetObject                     // an objects.yaml object_kind (Forage→berry_bush, Mine→ore_node)
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
    ID       ActionID    // canonical identifier (docs/core/glossary.md)
    Tags     []core.Tag  // drives gate visibility (engine/mind/gates) + planner cost (D4). NEVER a
                         // per-action gate list or cost number — both are tag-derived elsewhere.
                         // A NON-recipe action's tool need is a `tool:<family>` tag here (Mine); a
                         // recipe-mediated action (Craft) carries NO tool tag (FINAL — tool = a recipe
                         // input alternative with mode:wear|consume).
    Duration core.GameMinutes // BASE durative length in game-minutes (≥ 1). ZERO for a RecipeMediated
                         // action — the bound recipe's per-recipe `duration` is used (FINAL).

    Target       TargetKind // none | location | object | agent (derived; see Notes)
    TargetKindID core.Tag    // the objects.yaml object_kind id when Target == TargetObject
                            // (e.g. "berry_bush","ore_node"); empty otherwise.

    Requires    []core.Pred // preconditions that must hold to START (planner; ALL must hold)
    RequiresAny []core.Pred // alt preconditions (planner; ANY of these satisfies the start gate)
    Produces    []core.Pred // predicates this action makes true on completion (GOAP forward)

    ProducesItem core.Tag // item kind placed into inventory on completion (empty if none / recipe-driven)
    ConsumesItem core.Tag // item kind removed; its supply becomes the Effect on completion (D9)

    // RecipeMediated marks an action whose inputs/outputs/duration/station come from a
    // content/recipes.yaml RECIPE bound at plan time (D3 — Craft = single atomic action parameterized
    // by a recipe, FINAL). When true, the world reads the bound recipe entirely: ordered input SLOTS
    // (each `{any:[alternative…]}`, first-satisfiable-in-authored-order, D12; an alternative
    // `{tagQuery, amount, mode:wear|consume}`), `ambient` station tags (in-range via the spatial hash),
    // per-recipe `duration`, `basis_stat` (the outcome roll), and `outputs[]{item, base_qty}`. The
    // action def names NO specific recipe (parametric, like MoveTo's target) and carries NO tool tag,
    // NO target_kind, and a ZERO Duration — the planner picks the recipe whose every slot is satisfiable
    // and whose ambient stations are in range, deterministically by sorted recipe id.
    RecipeMediated bool

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
// (StatIDs in tags, need ids in effects, target_kind/item ids in objects.yaml, recipe references)
// are NOT done here — they are platform/config's job, run before this call.
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

- `engine/kernel/core` — `Tag`, `Pred`, `Dimension`, `GameMinutes`. (Plus a YAML decoder and stdlib
  `sort`.)
- `engine/mind/stats` — **referenced only by contract**: an action's `uses:<StatID>` tags name stats;
  the *cross-check* that those StatIDs exist is `platform/config`'s referential-integrity step,
  not this module. No runtime call into `engine/mind/stats`.
- `engine/mind/gates` — **referenced only by contract**: an action's `Tags` (incl. a non-recipe action's
  `tool:*`) are what `engine/mind/gates` matches at planning time. This module does NOT import
  `engine/mind/gates` and stores no gate ids.
- **Contract**: `content/schema/actions.schema.json` defines the on-disk shape; `content/actions.yaml`
  is the data; `content/recipes.yaml` carries the recipes a `RecipeMediated` action binds (the recipe
  registry is `platform/config`'s; this module stores no recipe content, only the `RecipeMediated`
  flag). The tag grammar is `content/README.md §Tags`. `platform/config` bridges file →
  schema-validate → referential-integrity → `Load`.

## Owned Data

- `Registry`, `ActionDef`, `Effect`, and the `ActionID`/`TargetKind` types. The `Registry` is
  **immutable after `Load`** and owns its internal map + precomputed sorted id slice + the
  `Producers` reverse index — no other module mutates it. Returned slices/maps are copies.
- This module owns **no** recipe content, **no** tool durability state, **no** ore-node `remaining` —
  those are content (`recipes.yaml`/`objects.yaml`) compiled by `platform/config` and runtime state
  mutated by `engine/world` (apply phase). The action def only declares `RecipeMediated`.

## Invariants

- **Atomic only; no trees (D3)**: an `ActionDef` is a single leaf action. The registry stores
  **no** `Method`, `Task`, subtask list, or plan. The only relational structure is the
  `Producers` reverse index (a flat `Pred → []ActionID` map for GOAP), regenerated mechanically from
  each action's `Produces`. `Craft` being recipe-mediated does NOT make it a tree: the recipe is a
  parametric BINDING (like MoveTo's target), and the gather→craft chain is assembled by the planner
  via `produces`/`requires`, never hand-drawn here. A grep/struct guard confirms no
  subtask/method/recipe-content field exists (`RecipeMediated` is a flag, not embedded recipe data).
- **No per-action gate list, no per-action cost (D4)**: `ActionDef` carries `Tags` and nothing that
  names a `GateID` or a cost number. Gate visibility is decided by `engine/mind/gates` matching these tags;
  cost is composed from tags in the planner. A non-recipe action's tool requirement is a TAG
  (`Mine`'s `tool:digging`), never a cost field; a recipe-mediated action's tool requirement is a
  recipe input alternative with `mode: wear`/`consume` (FINAL), never an action tag.
- **Objects carry supply; actions stay generic (D9)**: a consumption action (`ConsumesItem != ""`)
  carries an **empty** direct `Effect`. A `RecipeMediated` action carries no `ProducesItem` (outputs
  come from the bound recipe). There is **no "future need"/quantity/schedule field** on an action.
- **What, not when (D5)**: this module describes actions; it never decides whether/when to run one,
  never orders/selects actions, never picks a recipe, never picks which lot/alternative to apply, and
  never reads agent/tool/node state. Selection + binding is the planner; application is the world.
- **Canonical ordering (D12)**: the `Registry` precomputes a single fixed-order `[]ActionID` (sorted
  lexicographically) via `IDs()`. **All action iteration MUST use this slice; ranging over the backing
  map for logic is forbidden.** `Producers(pred)` and `Tags()` are sorted.
- **Immutable after init**: no setter, no writable field; returned slices/maps are copies.
- **Well-formed at load (semantic check)**: `Load` rejects a duplicate `id`, an empty `tags`, a
  `duration < 1` (on a NON-recipe action), an action that is BOTH item-producer and item-consumer of
  an incompatible shape, a consumption action with a non-empty direct `Effect` (D9), and a
  `recipe_mediated` action that ALSO sets `produces_item`, `target_kind`, or `duration` (the recipe
  owns all three — FINAL). Errors name the offending action id.
- **No hardcoded action/kind names (D10)**: never references `"Forage"`, `"Craft"`, `"Mine"`,
  `"tool_bench"`, `"ore_node"`, etc. as literals in logic; all come from loaded data.
- **No IO**: imports no `os`/`net`/filesystem package; reads from an injected `io.Reader` only.

## Acceptance Criteria (testable)

- [ ] **Loads from an injected `io.Reader`**: `Load` builds a `Registry` from in-memory YAML bytes
  (no file path); contains exactly the source action ids. Shipped content includes `Eat`, `MoveTo`,
  `Forage`, `Hunt`, `Craft`, `Mine`, `Rest`, `TakeShelter`, … plus the rest of the catalog.
- [ ] **`Get` round-trips a definition faithfully**: `Get("Hunt")` returns its tags/duration/target as
  before. Table-driven over several actions.
- [ ] **`Craft` is recipe-mediated, target-less, tool-tag-free, duration-less (FINAL/D3/D4)**:
  `Get("Craft")` has `RecipeMediated == true`, `Target == TargetNone`, empty `TargetKindID`,
  `Produces == [has_tool]`, an EMPTY `ProducesItem`, a ZERO `Duration` (the recipe supplies it), and
  `Tags` contain **no** `tool:*` tag (the tool need is a recipe `wear`/`consume` alternative). `Load`
  errors on a synthetic Craft that ALSO sets `produces_item`, `target_kind`, or `duration`.
- [ ] **`Mine` parallels `Fell`, action-tagged tool (Xm4/Xm5)**: `Get("Mine")` has `Target ==
  TargetObject`, `TargetKindID == "ore_node"`, `Tags` include `uses:Strength`/`effort:high`/
  `tool:digging` (the pickaxe gate is an ACTION tag here — Mine is NOT recipe-mediated, Xm5),
  `Requires` includes `at_target`, `Produces == [has_materials]`, and a non-zero `Duration`. Distinct
  id/tags from `Forage` (Xm4). Node yield/`remaining`/`SetTerrain` + the held-tool wear are
  objects.yaml/world, not on the action.
- [ ] **`IDs()` ordering deterministic (D12)**: same lexicographic order across repeated calls and a
  second freshly-`Load`-ed registry from the same bytes.
- [ ] **`Producers` reverse index (GOAP)**: `Producers("has_materials")` returns `[Build, Mine]` for
  the CURRENT catalog (lexicographic, D12 — `Fell` joins this set when the flora `Fell` action ships,
  glossary/flora SPEC Open Questions); `Producers("has_tool")` includes `Craft`;
  `Producers("nonexistent")` is nil. Each returned action's `Produces` actually contains the predicate.
- [ ] **Target kind derived correctly**: `MoveTo`→`TargetLocation`; `Rest`/`Sleep`/`Craft`→`TargetNone`
  (`Craft`'s stations are recipe `ambient`, not an action target); `Forage`/`Mine`/`Eat`→`TargetObject`;
  `Signal`/`Attack`→`TargetAgent`. Table-driven.
- [ ] **Atomic-only guard (D3)** + **No gate/cost field guard (D4)**: struct/grep guards (incl. no
  `TargetTags`/recipe-content field on `ActionDef`).
- [ ] **Consumption→no direct Effect (D9)** + **recipe-mediated→no produces_item/target_kind/duration**:
  `Load` errors on a synthetic action violating either.
- [ ] **Semantic rejects**: table-driven — duplicate id, empty `tags`, `duration < 1` (non-recipe),
  `recipe_mediated`+`produces_item`, `recipe_mediated`+`target_kind`, `recipe_mediated`+`duration`.
- [ ] **Immutable after init**: mutating a returned id/`Producers`/`Tags` slice does not change the
  registry.
- [ ] **No literal action/kind name in source (D10)**: grep guard.

> Structural JSON-schema validation + referential integrity (every `uses:<StatID>` tag names a stat;
> every effect/supply need id exists; every `target_kind`/item id exists in `content/objects.yaml`;
> every recipe a `recipe_mediated` action could bind exists in `content/recipes.yaml`; every recipe
> `wear` alternative's tagQuery is satisfiable only by `tool`-block kinds; every `ambient` tag exists)
> are **platform/config** ACs.

## Out of Scope

- Reading the file from disk, JSON-schema validation, cross-file referential integrity →
  `platform/config`.
- **Gate visibility evaluation** (matching `Tags`/`Mine`'s `tool:digging` to gates) → `engine/mind/gates`.
- **Tag-derived action cost** → `engine/mind/planner` (`content/balance.yaml`).
- **Recipe BINDING + craftable gate (FINAL)** — which recipe to craft; whether it is craftable (every
  slot has a satisfiable alternative AND every `ambient` station is in range, read off the BOUND
  recipe); matching tag-query alternatives to inventory/tools → `engine/mind/planner` (selection) +
  `engine/world` (apply). This module only flags `RecipeMediated`.
- **Craft APPLY (FINAL)** — per ordered slot, take the FIRST satisfiable alternative in authored order
  (D12) and apply its `mode`: `consume` → remove `amount` matching items/units (most-decayed lots
  first, ties by `ObjectID`; a whole durable instance if a tool is consumed as material); `wear` →
  decrement the most-worn matching tool's CURRENT durability by `amount` (ties by `ObjectID`), break
  (object-mortality, §7) at 0, else persist. Check every `ambient` station in range. Roll the outcome
  (success, qty, AND the produced instance's durability) from `basis_stat` (produced tool start
  durability = roll·`wear_max`); place a perishable output as a fresh decay lot `{kind, qty,
  decayAge=0}` (Dm5). Use the recipe's per-recipe `duration`. No partial run on any unsatisfiable
  slot/ambient. → `engine/world` apply (reads `recipes.yaml` + the decay lot rule + the `tool:`
  `wear_max`).
- **The §6 tool-quality OPERAND (Cm3)** — `expr.Context.Attr("tool:<family>.quality")` resolution from
  a held tool's current durability / `wear_max` → `engine/world`/`engine/agent`'s Mine `Context` adapter
  (the `expr` L0 interface is UNCHANGED — it already declares `Attr`; `engine/kernel/expr/SPEC.md` "callers
  adapt"). This module names no operand. (Craft's outcome roll is `basis_stat`-driven; the §6 operand
  is used by the Mine yield.)
- **Mine APPLY (Xm1/Xm2/Xm3 + tool wear)** — decrement the `ore_node`'s object-local `remaining`, roll
  the node's yield table (§6, flora reuse), WEAR the held `tool:digging` instance (decrement current
  durability by the Mine action's world/balance wear rate — Mine has no recipe, so the wear AMOUNT is a
  world rate like navmap `WearOnUse`, NOT a per-item field; break at 0 — same wear path as a recipe
  `wear` alternative), and on `remaining→0` remove the node (object-mortality) + fire one
  `navmap.SetTerrain` over the node's cells → `engine/world` apply (reads `objects.yaml`
  `source.remaining` + `tool.wear_max` + the navmap `SetTerrain` writer). Water stays infinite
  (`depletes:false`); flora stays flora-regen — no extraction.
- **Outcome resolution** (does the roll succeed, scaled by Real Stats) → `engine/world`/`engine/agent`.

## Open Questions

> Materials Cm1–Cm7 + Xm1–Xm6 are all `RESOLVED` and the recipe model is FINAL/LOCKED
> (`docs/plans/materials.md §0/§1`); this SPEC writes from them and re-decides nothing. No remaining OPEN for
> P_m3. (`Hunt`'s prey target note is retained below; it is a recorded fact, not an open decision.)

- **`Hunt`'s "animal" target** (unchanged, recorded): prey is an `objects.yaml` object_kind today, so
  Hunt is `TargetObject(prey)`; flips to `TargetAgent` for free if animals become first-class agents.

## Notes

- **`TargetKind` derivation**: at `Load`, `Target` is computed from the content shape — `target_kind`
  present → `TargetObject` (`TargetKindID` = that id); else `at_target` in `Produces` → `TargetLocation`;
  else `near_other` in `Produces`/`requires` → `TargetAgent`; else `TargetNone`. A `recipe_mediated`
  action has no `target_kind` → `TargetNone` (its stations are the recipe's `ambient`, bound in-range
  by the world).
- **Craft (P_m3) action shape (FINAL)**: `recipe_mediated: true` + `requires: [has_materials]` (the
  generic "recipe inputs satisfiable" precondition) + `produces: [has_tool]`; **no** `tool:*` tag,
  **no** `target_kind`/station field, **no** `duration` (the recipe supplies it). It binds a recipe at
  plan time (D3 parametric); the recipe owns the slot/alternative inputs (`wear`|`consume`), `ambient`
  stations, `duration`, `basis_stat`, and outputs. The world applies: first-satisfiable alternative
  per slot (D12); `consume` removes most-decayed lots first (ties by `ObjectID`); `wear` decrements the
  most-worn matching tool (ties by `ObjectID`), break at 0; `ambient` in-range gate; the `basis_stat`
  roll sets success/qty + produced durability (= roll·`wear_max`); a perishable output → fresh decay
  lot (Dm5). The legacy fixed `produces_item: crafted_tool` / `target_kind: tool_bench` / flat
  `duration` / `tool:cutting` action tag are all REPLACED (a deliberate re-baseline — Materials §2):
  `craft_basic_tool` is consume-only → bare-handed bootstrap; `craft_pickaxe` has a `mode: wear`
  cutting-tool alternative → toolmaking chain (D2).
- **Mine (P_m4) action shape**: `target_kind: ore_node` + tags `uses:Strength`/`effort:high`/
  `noise:med`/`tool:digging` + `requires: [at_target]` + `produces: [has_materials]` + a flat
  `duration` (Mine is NOT recipe-mediated, so it keeps its own duration). Its tool need IS the
  action-level `tool:digging` tag (the actor must hold a pickaxe); the world wears that held tool per
  use (decrement current durability by the Mine action's world/balance wear rate — like navmap
  `WearOnUse`, since the FINAL `tool:` block has no per-item wear amount; break at 0). Parallel to
  flora's `Fell` (destructive-on-depletion) vs `Forage` (non-destructive food) — Xm4 keeps them
  distinct ids so gates/cost/tool differ (D4). The node's yield table + `remaining` +
  `SetTerrain`-on-exhaustion are objects.yaml/world.
- **Tool requirement routes (FINAL)**: a recipe-mediated action expresses a tool as a recipe input
  alternative (`mode: wear` to use-and-wear, or `mode: consume` to consume it as material); a
  non-recipe action expresses a tool as an action `tool:<family>` tag. Either way the wear MECHANISM is
  the same (decrement current durability, break at 0); the wear AMOUNT is the recipe alternative's
  `amount` (recipe path) or the action's world/balance rate (non-recipe path). The §6
  `tool:<family>.quality` Context operand (= current durability / `wear_max`, Cm3) reads a held tool's
  quality. Tool tags themselves are ordinary `core.Tag`s here (opaque).
- **Item supply vs direct effect (D9)** and the `Producers` index notes are unchanged from the P1 SPEC.
- `Duration` is `core.GameMinutes`; the engine scales the base duration by stats/distance at execution
  time (planner/world), not here. A recipe-mediated action's `Duration` is ZERO on the def — the bound
  recipe's per-recipe `duration` is used (FINAL).
```
