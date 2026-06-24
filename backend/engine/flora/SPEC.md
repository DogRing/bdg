# SPEC — `engine/flora`

> Status: `DRAFT`
> Leaf level: `L1`  ·  Owner agent: `<filled by implementer>`

## Purpose
The **flora driver**: a pure, deterministic transform over the set of live plant objects (trees,
bushes, grass). Given the current flora state, the terrain/climate inputs at each plant's position
(supplied as values by `world`), and the data-defined flora `Rules`, it produces the per-object
**growth, propagation (new plants), and death (removals)** deltas plus the **shade parameters**
perception consumes. It owns *how plants grow, spread, and die* — it does **not** own *when* it runs
(cadence), the navmap/climate state it reads (those are values `world` injects), the line-of-sight
math the shade feeds (that is `engine/perception`), or the object mutation itself (that is
`engine/world`, the sole object mutator). No IO, no wall-clock, no global rand: every output is a
function of `(state, inputs, Rules, rng)` (D12). Concept & rationale: `docs/design.md §5` (초목 =
flora object, not terrain) + `§7` (object-mortality) + `docs/flora.md` (all §1 resolutions are
binding — adopted-as-`rec`; do NOT re-decide).

## Public Interface
```go
package flora

import (
    "github.com/dogring/bdg/engine/core"
    "github.com/dogring/bdg/engine/expr"
    "github.com/dogring/bdg/engine/rng"
)

// ── Identity & state ──────────────────────────────────────────────────────────────

// SpeciesID names a flora species from content/objects.yaml object_kinds that carry a
// `flora:` block (e.g. "berry_bush", "oak", "grass"). It is core.Tag underlying so the
// content catalog validates it; flora never parses YAML (D10). A species is an object_kind
// with growth — there is no separate flora-only catalog (RESOLVED 1i: flora joins objects[]).
type SpeciesID = core.Tag

// Plant is one live flora object's flora-owned dynamic state. Pos is the continuous world
// coordinate (D11 — never snapped to a cell). Morphology is TWO continuous axes (RESOLVED §1
// refinement, replacing the single Growth scalar):
//   Length — plant HEIGHT in world units ≥ 0. Maturity proxy: discrete Stage is DERIVED from
//            it via the species' stage thresholds, never stored. Drives resource yield (taller
//            ⇒ more wood). Integrates with its OWN per-species §6 length-rate.
//   Width  — plant girth / canopy spread in world units ≥ 0. Drives shade Radius/Opacity (wider
//            ⇒ larger/denser shade). Integrates with its OWN per-species §6 width-rate,
//            independent of Length (a pine grows tall faster than wide; an oak the reverse).
// DeathStreak is the hysteresis counter for sustained-unsuitability death (RESOLVED 1b option a).
// Owner is empty for wild flora; set only for PLANTED flora (RESOLVED 1f option a — economy seam,
// inert until economy ships).
type Plant struct {
    ID          core.ObjectID
    Species     SpeciesID
    Pos         core.Vec2
    Length      float64       // continuous height ≥ 0; stages derived from it (D9: no future field)
    Width       float64       // continuous girth/canopy ≥ 0; shade derived from it (D9: no future field)
    DeathStreak int           // consecutive flora-steps with suitability < θ (hysteresis, 1b)
    Owner       core.AgentID  // zero/empty ⇒ wild (unowned); set by Plant action (1f), inert in P1
}

// SiteInput is the per-plant exogenous environment world samples at Plant.Pos and injects
// each flora step. Flora is a pure transform over VALUES — it does NOT import navmap or
// climate (RESOLVED 1h: L1 leaf, core+expr+rng only). world reads navmap.TerrainAt /
// climate Moisture/Temperature at the plant position and fills this. NeighborCount is the
// count of same-or-any-species plants within the species' propagation/competition radius,
// supplied by world's spatial query (D11 spatial hash) — flora does not hold a position index.
type SiteInput struct {
    Terrain        core.Tag           // terrain type at Pos (drives suitability via Rules)
    TerrainAttrs   map[core.Tag]float64 // §5 terrain attribute vector (grainSize, slope, depth, salinity, …) for §6 suitability operands
    Moisture       float64            // climate Moisture at Pos ∈ [0,1] (suitability operand)
    Temperature    float64            // climate Temperature at Pos ∈ [0,1] (suitability operand)
    NeighborCount  int                // plants within propagation radius of Pos (density; P1 reads it only for propagation weighting, RESOLVED 1c: competition parked)
}

// State is the whole flora field for one run: the live Plant set keyed by ObjectID, held in
// a form iterated in sorted ObjectID order (D12). Snapshot-serializable (data-contracts §6:
// periodic full + sparse spawn/grow/die deltas). Owned by engine/world (one per run); Step
// returns a fresh next and never mutates prev (so plan-snapshot and apply-write don't alias,
// mirroring climate.State / navmap.Snapshot).
type State struct{ /* opaque; see Owned Data. Holds the Plant set in ObjectID order. */ }

// ── Construction ────────────────────────────────────────────────────────────────

// New builds the initial flora State from an already-placed plant set. Placement (world-gen /
// scenario fixtures) is NOT this module's job (RESOLVED 1j: placement = world-gen, objects.yaml
// spirit) — world/config supply the seeded plants. Pure; no RNG draw at construction.
func New(plants []Plant) *State

// ── The pure transform world calls (RESOLVED 1g/1h) ────────────────────────────────

// Step advances the whole flora field ONE flora step (= N ticks; world owns the cadence and
// only calls Step when tick % N == 0, RESOLVED 1g — N parked equal-to-or-offset-from climate
// N). It is a PURE function of (prev, inputs, rules, idAlloc, rng): it does NOT mutate prev,
// and returns the deltas world applies in the apply phase. Two-axis morphology integration
// (Length and Width each advance by their own §6 rate × suitability), sustained-unsuitability
// death (hysteresis), and seeded seed-dispersal propagation all happen here, in sorted ObjectID
// order (D12). rng is the per-step seeded fork world supplies (RESOLVED 1a option a — world
// per-step fork, like climate.Step). idAlloc mints ObjectIDs for new plants deterministically
// (world owns the id space — flora must not invent global ids); it is called in sorted parent-
// ObjectID then deterministic-draw order so the id assignment is reproducible.
//
// inputs[p.ID] is the SiteInput for plant p; a missing entry is a world-contract bug (panic,
// like navmap unknown-id) — every live plant must have its environment sampled.
func Step(
    prev *State,
    inputs map[core.ObjectID]SiteInput,
    rules *Rules,
    idAlloc func() core.ObjectID,
    rng *rng.RNG,
) (next *State, deltas StepDeltas)

// StepDeltas is the world-applied result of one flora step. world is the sole object mutator:
// it adds Spawned to objects[]/spatial, removes Died from objects[]/spatial, and updates the
// Length/Width/DeathStreak of survivors (Grown). All three slices are in sorted ObjectID order (D12).
type StepDeltas struct {
    Spawned []Plant         // new plants (propagation); world adds them to objects[] + spatial
    Died    []core.ObjectID // removed plants (object-mortality, §7); world removes them
    Grown   []GrowthDelta   // survivors whose Length/Width/DeathStreak changed this step
}

// GrowthDelta carries a survivor's new morphology state (the new absolute Length+Width values,
// not increments, so apply is idempotent and order-free across survivors).
type GrowthDelta struct {
    ID          core.ObjectID
    Length      float64
    Width       float64
    DeathStreak int
}

// ── Shade parameters (perception consumes; flora does NOT compute LoS) ──────────────

// Shade is the per-plant occlusion PARAMETER perception reads to attenuate line-of-sight.
// flora exposes the parameter ONLY (RESOLVED 1h: shade is perception's concern; flora emits
// the parameter, not the LoS effect). Radius/Opacity are DERIVED from Width + species via §6
// (RESOLVED 1d option b — `radius = f(width, species)`, no stored constant, no cell marking
// → D11; shade scales with canopy spread = Width, the §1-refinement axis). Opacity ∈ [0,1] is
// the per-plant light-blocking fraction; perception composes overlapping shades
// MULTIPLICATIVELY (transmission ∏(1 − opacity), RESOLVED 1d option c) — flora does NOT pre-sum
// or rasterize shade (no tile field, D11).
type Shade struct {
    ID      core.ObjectID
    Pos     core.Vec2
    Radius  float64 // shade radius in world units, = §6(width, species)
    Opacity float64 // light-blocking fraction ∈ [0,1], = §6(width, species)
}

// ShadeOf returns the Shade parameter for one plant (lazy — RESOLVED 1g: shade is computed on
// demand, not in the bulk Step). perception calls it for occluder candidates the spatial hash
// returns; world adapts flora.State to perception's occluder view (Out of Scope below). Pure;
// reads only the plant's Width + the species shade formula. Returns ok=false if id is unknown.
func (s *State) ShadeOf(id core.ObjectID) (Shade, bool)

// ── Snapshot / serialization (data-contracts §6) ──────────────────────────────────

// Plants returns the live Plant set in D12-sorted (ascending ObjectID) order, for the
// periodic-full serialization channel (data-contracts §6: periodic full + spawn/grow/die
// deltas). Shade is NOT serialized (RESOLVED 1i option a — derived; perception recomputes
// from Pos+Width).
func (s *State) Plants() []Plant

// ── Rules (the data-defined flora table, RESOLVED §0 — content + §6) ────────────────

// Rules is the compiled, immutable per-species flora table from content/objects.yaml `flora:`
// blocks: for each species, the §6 formulas for suitability, the TWO growth rates
// (length-rate + width-rate), shade radius/opacity (= §6(width)), stage thresholds (over
// length), propagation radius/chance, death threshold θ + hysteresis span, and yield table (the
// seeded-roll yields whose qty scales with length, RESOLVED 1e). Built once by platform/config
// (parse the §6 formulas via engine/expr, validate species/item ids). engine/flora evaluates it
// read-only; it never parses YAML (D10). The §6 formulas are compiled expr.Program values
// evaluated against an expr.Context flora builds from SiteInput + Plant (so the DSL stays one
// shared evaluator, glossary; no bespoke per-species Go function — D4).
type Rules struct{ /* opaque; per-SpeciesID compiled §6 programs + thresholds + yield table. */ }

// Suitability evaluates the species' §6 suitability formula over the site (terrain attrs +
// climate moisture/temperature) → a scalar ∈ [0,1]. Pure, deterministic, no RNG (a §6 numeric
// expression; design.md §6). It is the common driver of BOTH growth axes (Length and Width each
// advance by their own rate × this scalar). Below the species' death threshold θ it counts
// toward DeathStreak.
func (r *Rules) Suitability(sp SpeciesID, in SiteInput) float64

// LengthRate / WidthRate evaluate the species' two §6 growth-rate formulas (scalars or §6
// programs). Step integrates Length += LengthRate·Suitability and Width += WidthRate·Suitability
// per flora step, so a species can grow tall faster than wide (RESOLVED §1 refinement). Pure,
// no RNG. (These may be plain constants in content; flora reads them via the same expr path.)
func (r *Rules) LengthRate(sp SpeciesID, in SiteInput) float64
func (r *Rules) WidthRate(sp SpeciesID, in SiteInput) float64

// Stage maps a continuous Length (HEIGHT = maturity proxy) to the species' DERIVED discrete
// stage index (0 = seedling …) via the species' stage thresholds over length (RESOLVED 1b
// option c + §1 refinement — stages are a data threshold over the height axis, not stored, and
// NOT over width). Used for render + gating which yields/shade/propagation apply.
func (r *Rules) Stage(sp SpeciesID, length float64) int

// Yield rolls the species' yield table for a harvest of one plant at the given LENGTH (height),
// using the seeded rng and the actor's Dexterity (RESOLVED 1e + §1 refinement: `chance =
// §6(Dexterity)`; `qty` scales with `length` — a taller tree yields more wood; generic action
// reads the target's table). Returns the items produced this harvest (each {item, qty}). Pure
// given the same rng draw sequence (D12). It does NOT decide whether the plant dies — that is
// the caller's action (Forage = non-destructive; Fell = destructive, see Open Questions /
// world). Dexterity is READ ONLY here (flora never trains it — stat training is out of scope,
// owned elsewhere; see Out of Scope).
func (r *Rules) Yield(sp SpeciesID, length, dexterity float64, rng *rng.RNG) []YieldItem
type YieldItem struct {
    Item core.Tag // item_kind id (objects.yaml), placed into Body.Inventory by world/actions
    Qty  int      // rolled quantity ∈ [min,max] from the yield table entry, scaled by length
}
```

## Dependencies
- `engine/core` — `Vec2`, `Tag`, `ObjectID`, `AgentID`. No IO.
- `engine/expr` — the shared §6 `Formula` evaluator (L0 leaf, **already decided**, `design.md §6`
  line 89): `expr.Program` (compiled formula) + `expr.Context` (operand lookup). flora builds a
  `Context` from `SiteInput` + `Plant` and evaluates the species programs the compiled `Rules`
  carry. flora does NOT parse the DSL (that is `platform/config`'s compile step) and never imports
  `engine/gates` (the boolean subset lives in the shared evaluator, not in gates).
- `engine/rng` — `*RNG` (injected seeded fork; the propagation spawn draws + yield rolls, D12).
- *(NOT `engine/navmap`)* — one-way: world samples `navmap.TerrainAt` / terrain attrs at the plant
  position and injects them as `SiteInput` values (RESOLVED 1h, mirrors climate's no-navmap rule).
- *(NOT `engine/climate`)* — world samples climate `Moisture`/`Temperature` at the plant position and
  injects them as `SiteInput` values (flora reads them as numbers; it never imports climate).
- *(NOT `engine/world`)* — world owns objects[]; it APPLIES `StepDeltas` (spawn/die/grow) and is the
  sole object mutator (D12 apply phase). flora returns deltas and never mutates world state.
- *(NOT `engine/perception`)* — flora exposes `Shade` PARAMETERS only; perception computes the LoS
  occlusion (its SPEC extension). flora must not import perception (would invert L1→L3).

## Owned Data
- The **live `Plant` set** (`ID, Species, Pos, Length, Width, DeathStreak, Owner`) held in `State`,
  snapshot-serialized (data-contracts §6). `State` is owned by `engine/world` (one per run);
  `flora.Step` returns a fresh `next` and never mutates `prev`.
- The **`Rules`** table is owned by `platform/config` (built from `content/objects.yaml` `flora:`
  blocks via `engine/expr`), injected read-only; this module never parses or mutates it.
- This module owns **no** object instances in `objects[]`, **no** spatial-hash entries, and writes
  **no** navmap/perception state — it only emits `StepDeltas` + `Shade` parameters.

## Invariants
- **D12 determinism** — `Step` is a pure function of `(prev, inputs, Rules, idAlloc, rng)`. Same
  inputs + same RNG fork + same `idAlloc` sequence ⇒ byte-identical `next` State and `StepDeltas`.
  No `time.Now()`, no global rand, no wall-clock; the only randomness is the injected `*rng.RNG`.
- **D12 no map-iteration for logic** — the `Plant` set is iterated in **sorted `ObjectID` order**
  for the Step sweep, propagation, and `Plants()`; the `inputs` map is read by sorted plant id, not
  by map-range. `Spawned`/`Died`/`Grown` are in sorted `ObjectID` order. Float accumulation order is
  fixed (Length integration then Width integration per plant in a fixed order).
- **D11 / continuous space** — every `Plant.Pos` and `Shade.Pos`/`Radius` is continuous world units;
  `Length`/`Width` are continuous world-unit morphology axes; flora never snaps a plant to a cell,
  never rasterizes shade into a tile field, and never iterates terrain by tile. Terrain/climate enter
  only as injected `SiteInput` values sampled at `Pos`.
- **D9 no future field** — a `Plant` carries `Length` + `Width` (current morphology, continuous) and
  its supply is the object_kind's `Effect`/`yields`; it carries **no** "amount about to ripen",
  "remaining lifespan", or "future need" field. Stage is DERIVED from `Length`, shade from `Width`,
  yield from `Length` — none is stored. Regeneration is a rate (the yield table refills as `Length`
  grows), never an authored future quantity (RESOLVED 1e).
- **D2/D3 no hardcoded ecosystem** — forests, succession, food-chains, and "dark forest" are
  EMERGENT from base mechanics (clusters of plant objects; shade × suitability feedback). There is
  **no** `from-species → to-species` succession table, **no** forest terrain type, **no** bespoke
  per-species Go growth/death function — all per-species behavior (incl. the two growth rates) is
  `content/objects.yaml` `flora:` §6 data evaluated via `engine/expr` (D4/D10).
- **D4/D10 data-defined** — suitability, the TWO growth rates (length-rate + width-rate), shade
  radius/opacity (= §6(width)), stage thresholds (over length), propagation radius/chance, death θ,
  and yields (qty ∝ length) are all `content/objects.yaml` data; an unknown species/item id is caught
  at load (`platform/config`), never in `Step`.
- **Propagation model is fixed (1a option a)** — new plants are **seed dispersal near the parent**:
  for each mature-enough parent (Stage ≥ the species propagate-stage, Stage derived from `Length`), a
  seeded RNG draws candidate child positions within the species' propagation radius of the parent
  `Pos`; a child spawns with probability = propagation chance × child-site suitability × density
  weighting (NeighborCount). No parent-independent spontaneous spawn in P1 (RESOLVED 1a). New plants
  start at `Length ≈ 0`, `Width ≈ 0`, `DeathStreak = 0`, `Owner` empty (wild).
- **Death model is fixed (1b option a)** — a plant whose `Suitability < θ` increments `DeathStreak`;
  when `DeathStreak` reaches the species hysteresis span it is added to `Died` (object-mortality,
  §7). A step where `Suitability ≥ θ` resets `DeathStreak` to 0 (temporary bad weather does NOT
  flicker forests out). Immediate-threshold death is NOT used (hysteresis is required).
- **Two-axis growth model is fixed (§1 refinement; 1b option c + 1b growth-driver a)** — per flora
  step `Length += LengthRate · Suitability` and `Width += WidthRate · Suitability` (the two §6 growth
  formulas), each clamped to `≥ 0`; the two rates are INDEPENDENT per species (tall-vs-wide shapes).
  Discrete stages are DERIVED from `Length` via `Stage`; shade is DERIVED from `Width`; yield qty
  scales with `Length`. Density competition and seasonal modulation are parked (RESOLVED 1c / climate
  park).
- **Shade is a derived parameter, not stored / not LoS** — `ShadeOf` computes `Radius`/`Opacity`
  from `Width` + species §6 on demand (RESOLVED 1d/1g lazy); flora performs **no** LoS test, **no**
  shade summation, **no** tile field. Multiplicative composition of overlapping shades happens in
  perception (RESOLVED 1d option c). Shade scales with canopy spread (`Width`), NOT with height.
- **Ownership seam inert in P1 (1f)** — `Plant.Owner` exists for the planted-flora economy seam but
  is empty for all wild flora; flora reads it for nothing in P1 (no owner-gated behavior). It is
  populated only when the `Plant` action ships (economy phase) — until then flora behaves as if all
  flora are unowned (RESOLVED 1f: economy 미출하 동안 (b)로 동작).
- **Outcome-neutral until activated** — the introduction phases ship with empty `Rules` / flora-off
  so `Step` emits **no** spawns/deaths and neither `Length` nor `Width` changes, and `ShadeOf`
  returns zero-radius shade so perception LoS is unaffected; existing world/perception goldens hold.
  Activation is a deliberate later phase with its own re-baseline (RESOLVED 1g staging; `docs/flora.md`
  §2, mirroring climate M-staging).
- **Read-only inputs** — `Step` never mutates `prev`, `inputs`, `Rules`, or `idAlloc`'s state beyond
  calling it; `ShadeOf`/`Suitability`/`LengthRate`/`WidthRate`/`Stage`/`Yield` never mutate
  `State`/`Rules`.
- **No individual skills (D7)** — `Yield` READS `dexterity` (a capability stat value world passes
  from the actor's `ToM[self]`/Real Stat) to scale `chance`; it never stores a per-plant or
  per-actor skill, and never trains the stat (training is owned elsewhere — Out of Scope).

## Acceptance Criteria (testable)
- [ ] **Both axes integrate with suitability, independently (§1 refinement)** — over N steps a plant
  in a high-suitability site reaches a higher `Length` AND `Width` than an identical plant in a
  low-suitability site; with a species whose `length_rate ≠ width_rate`, after N steps `Length` and
  `Width` diverge in the rate ratio (a tall-narrow species ends with `Length > Width`, a low-wide
  species the reverse); both are clamped to `≥ 0`; a survivor's `GrowthDelta` carries the new
  absolute `Length` + `Width`. Table-driven.
- [ ] **Stage is derived from Length (1b/D9/§1)** — `Stage` returns the species stage index for a
  given `Length` per the species thresholds (over height, NOT width); crossing a threshold changes
  the stage with no stored stage field on `Plant`; two plants with equal `Length` but different
  `Width` share the same Stage. Table-driven over below/at/above each threshold.
- [ ] **Suitability = §6 over terrain+climate (locked §0)** — with a species suitability formula
  `f(moisture, temperature, slope)`, two sites differing only in `Moisture` yield the formula's
  values to float tolerance; the formula is loaded from a test fixture `flora:` block via
  `engine/expr`, NOT hardcoded.
- [ ] **Propagation = seed dispersal near parent (1a)** — a mature parent (Stage ≥ propagate-stage,
  Stage from `Length`) spawns children only within its propagation radius of its `Pos`; child sites
  with higher suitability and lower `NeighborCount` spawn more often (density/suitability weighting);
  an immature parent (Stage below propagate-stage) spawns nothing; `Spawned` plants start at
  `Length ≈ 0`, `Width ≈ 0`, `DeathStreak = 0`, `Owner` empty. Seeded — a fixed seed reproduces the
  exact child set + positions.
- [ ] **Death needs sustained unsuitability (1b hysteresis)** — a plant at `Suitability < θ` for
  fewer than the hysteresis span is NOT in `Died` (its `DeathStreak` increments); reaching the span
  adds it to `Died`; a step at `Suitability ≥ θ` resets `DeathStreak` to 0 (no flicker). Table-driven.
- [ ] **Shade radius/opacity from Width §6 (1d/§1)** — `ShadeOf` returns `Radius`/`Opacity` = the
  species §6 of `Width`; a wider plant casts a larger/denser shade; two plants with equal `Width` but
  different `Length` cast identical shade; an unknown id returns `ok=false`. flora performs no LoS
  test and returns no composed/summed shade (composition is perception's).
- [ ] **Yield scales with Length + seeded roll + Dexterity scaling (1e/§1)** — `Yield` rolls each
  table entry's `chance = §6(Dexterity)` and a `qty` that scales with `length` (a taller plant yields
  more wood over many seeds) within `[min,max]·f(length)`, from the seeded rng; a higher `dexterity`
  yields more/more-often over many seeds; a fixed seed reproduces the exact items; an immature plant
  (below the species yield stage, Stage from `Length`) yields nothing. Items are valid item_kind ids.
- [ ] **Ownership seam inert (1f)** — with all `Owner` empty (wild), flora behavior is identical to a
  run where `Owner` is ignored; setting `Owner` on a plant changes nothing in P1 `Step`/`ShadeOf`
  (the field is reserved, not yet read). Regression guard for the economy seam.
- [ ] **Flora-off neutrality** — with empty `Rules` (no species programs), a multi-step run emits
  zero `Spawned`/`Died`, leaves `Length`/`Width`/`DeathStreak` unchanged, and `ShadeOf` returns
  zero-radius shade (perception LoS unaffected); existing world/perception goldens hold (RESOLVED 1g).
- [ ] **Sorted-order determinism (D12)** — `Spawned`/`Died`/`Grown` and `Plants()` are in ascending
  `ObjectID` order; the `inputs` map is consumed by sorted plant id; shuffling the `inputs` map
  insertion order yields byte-identical deltas.
- [ ] **Determinism golden** — a fixed `(initial plants, SiteInput sequence, seed, idAlloc sequence,
  Rules)` over N steps yields a byte-identical digest of `Plants()` + the per-step `StepDeltas`
  (D12). A second run from a fresh registry built from the same `content/objects.yaml` reproduces it
  (cross-process). The golden is first established **flora-off** (neutral), then re-baselined on
  activation (RESOLVED 1g).
- [ ] **Resume invariant** — capturing `State` at step T and resuming yields the same step-T+k state
  + deltas as running 0→T+k uninterrupted (data-contracts §5; ties into `rng_state` round-trip).
- [ ] **Missing SiteInput panics** — `Step` with a live plant absent from `inputs` panics (world-
  contract guard, mirrors navmap unknown-id), never silently skips a plant.
- [ ] **No wall-clock / no global rand / no forbidden import (D12 guard)** — grep guard: no `time`
  import for logic, no global `rand`; the only randomness is the injected `*rng.RNG`. No
  `engine/navmap`, `engine/climate`, `engine/world`, `engine/perception`, or `engine/gates` import.
- [ ] **No hardcoded constant / id (D10 guard)** — grep guard: no length-rate / width-rate /
  threshold / radius / species-name / item-name string literal in logic; every constant/formula flows
  from `Rules` (injected by `platform/config`).

## Out of Scope
- *When* to call `Step` (the `tick % N` cadence), the per-step RNG-fork derivation, the `idAlloc`
  ObjectID minting, sampling `navmap.TerrainAt` / terrain attrs / climate `Moisture`/`Temperature`
  at each plant `Pos` to build `SiteInput`, the spatial query that fills `NeighborCount`, and
  APPLYING `StepDeltas` (adding `Spawned`/removing `Died`/updating `Grown` Length+Width in
  `objects[]` + spatial) → `engine/world` (`docs/flora.md` §0/§1,
  `backend/engine/world/SPEC-tick.md`). Flora is a pure transform; world owns cadence + the
  navmap/climate sampling + the object mutation.
- The **LoS occlusion math** that consumes `Shade` (per-segment multiplicative attenuation, the
  occluder query) → `engine/perception` (`backend/engine/perception/SPEC.md`, the shade extension).
  flora emits the `Shade` parameter only.
- The **harvest actions** (`Forage` non-destructive, `Fell` destructive vegetation removal, `Plant`
  for owner-setting), wiring `Yield` to inventory, and triggering object-mortality on `Fell` →
  `content/actions.yaml` + `engine/actions` + `engine/world` apply (see Open Questions — these need
  human decisions before implementation).
- **Stat training** (a Dexterity-using action raising the `Dexterity` stat) → a cross-cutting
  stats/lifecycle concern owned ELSEWHERE (RESOLVED in brief: out of flora scope). flora only READS
  `Dexterity` via §6; it must not train it. Flag to the stats/lifecycle owner.
- Parsing/validating `content/objects.yaml` `flora:` blocks + the yield table, compiling the §6
  formulas (suitability, length-rate, width-rate, shade(width), yield) into `Rules`, and
  cross-checking species/item ids → `platform/config` (`content/schema/objects.schema.json`).
- Serialization wire format / Redis / SSE streaming of the flora field → `platform/persist` +
  `docs/data-contracts.md §6` (this module exposes `Plants()` as the periodic-full source +
  `StepDeltas` as the spawn/grow/die delta source).
- Initial flora **placement** (live procedural + scenario fixtures) → `engine/world` world-gen /
  `docs/testing.md` fixtures (RESOLVED 1j: placement = world-gen, not content).
- Density competition (RESOLVED 1c — parked, frontier seam: shade→neighbor-suitability feedback),
  explicit succession (RESOLVED 1c — emergent only), day/night shade coupling (RESOLVED 1d — parked),
  owner-gated flora behavior (RESOLVED 1f — economy phase).
- The shared §6 `expr` evaluator **implementation** → `engine/expr` (already decided, L0); flora only
  *uses* it (via compiled `Rules`).

## Open Questions
> §1 of `docs/flora.md` is ALL RESOLVED — these are NEW cross-subsystem decision points surfaced
> while writing the SPEC (per the human's request). Each needs a human decision BEFORE implementation
> of the dependent phase; none re-opens a resolved §1 item.

- **Do grass/flowers even have width-shade or yield? (P_f4 — content shape, raised by the two-axis
  refinement).** The §1 refinement explicitly wants species spanning shapes (oak = large length+width,
  grass = low length+low width, flower = low length + moderate/no width). But the schema currently
  requires `shade` and a yield table on every `flora:` species. A grass tuft realistically casts ~no
  occluding shade and yields ~nothing; a flower casts a little shade but no harvest. Options:
  **(a)** keep `shade` required and let near-zero `width` + a `width→0` shade formula produce
  effectively-zero shade (no schema change; grass/flower just author tiny coefficients);
  **(b)** make `flora.shade` and `harvest` OPTIONAL on a species (a shade-less, yield-less ground
  cover is a first-class kind). Recommendation: **(a)** for P_f1 (no schema churn; zero shade falls
  out of `radius = width·k` with small `k`), revisit **(b)** if we want truly shade-exempt species
  to skip the occluder query for perf. **Return to human before P_f4** (it is a content-shape call).
- **Forage/Fell/Plant action additions (P_f4 blocker — actions/world boundary).** RESOLVED 1e chose
  `Forage` (non-destructive) vs `Fell` (destructive = object-mortality) vs `Plant` (owner-setting),
  but the *atomic action definitions* + *who triggers death on Fell* + *where Yield runs* are not yet
  in `content/actions.yaml` / `engine/world`. Options: **(a)** add `Fell`/`Plant` to actions.yaml and
  let `engine/world` apply call `Rules.Yield` + (for Fell) add the target to `flora.Died`;
  **(b)** keep only the existing `Forage` for P_f4 and defer `Fell`/`Plant` to a later phase.
  Recommendation: **(a) for `Fell`+yield, defer `Plant`** to the economy phase (it only sets `Owner`,
  which is inert in P1). This blocks the active-resource phase, not the pure-transform phase. **Return
  to human before P_f4.**
- **Existing `berry_bush` migration (P_f1/P_f4 — content + schema).** `berry_bush` today is a plain
  object_kind with `harvest.depletes:true` + `balance.regen.berry_bush`. Generalizing it into a flora
  species means moving regen onto the object as a yield table (RESOLVED 1e) and adding a `flora:`
  block. Options: **(a)** convert `berry_bush` in place (add `flora:` + `yields:`, drop `depletes` +
  `balance.regen.berry_bush`), accepting a deliberate re-baseline of the hunger-loop goldens;
  **(b)** keep `berry_bush` as a legacy non-flora object_kind and add a NEW `berry_shrub` flora
  species, leaving existing goldens intact. Recommendation: **(b) for the flora-off phases** (zero
  golden churn — new species are dormant until activation), then **(a) at activation** (P_f4) with the
  deliberate re-baseline. **Return to human before P_f4** (it changes the live hunger loop).
- **Perception occluder interface name + ownership (P_f3 blocker — perception/world contract).**
  perception needs to enumerate flora occluders along a sight segment and read each one's
  `Radius`/`Opacity`. The flora SPEC exposes `flora.Shade` + `ShadeOf`, but the perception
  SPEC's `WorldSnapshot` interface must gain an occluder accessor. I coined **`ShadeOccluders`** /
  **`ShadeAt`** on `WorldSnapshot` (see perception SPEC) — both are glossary-conformant uses of the
  new `shade` term, but the exact method shape (return the candidate set vs a composed transmission
  scalar) is a perception design choice. Options: **(a)** `WorldSnapshot.ShadeOccluders(center,
  radius) []ShadeOccluder` (perception composes the multiplicative attenuation); **(b)**
  `WorldSnapshot.LightTransmission(segment) float64` (world/flora composes, perception just scales).
  Recommendation: **(a)** — keeps composition in perception (it already owns LoS), mirrors the
  existing `IsOpaque`/`EntitiesInRadius` split. **Return to human/architect before P_f3.**
- **`Shade` vs binary `IsOpaque` interaction (P_f3 — perception semantics).** perception's existing
  `Sight` uses a binary `IsOpaque` occlusion test. Flora shade is *continuous* (partial attenuation),
  which changes `Sight` from boolean visible/occluded to a *probability/strength* attenuation.
  Options: **(a)** make `Sight` return a visibility STRENGTH (continuous) — bigger change, re-baseline
  perception goldens; **(b)** threshold the composed transmission (seen iff transmission ≥ τ) — keeps
  `Sight`'s boolean contract, smaller blast radius. Recommendation: **(b) for P_f3** (boolean
  threshold, minimal churn), revisit continuous strength as a frontier. **Return to human before P_f3.**
- **Flora cadence N vs climate N (P_f2 — world tuning, non-blocking).** RESOLVED 1g left flora's bulk
  N as "equal-to or offset-from climate N (=60)". Options: **(a)** N = 60 (aligned, simplest);
  **(b)** N = 60 with a tick offset (spread load so flora and climate don't both run on the same
  tick). Recommendation: **(a)** for P_f2 (simplest, profile later). Non-blocking; owned by world's
  cadence config.
- **`NeighborCount` species scope (P_f4 — world spatial query, non-blocking).** Propagation density
  weighting uses `NeighborCount` within the propagation radius. Options: **(a)** count same-species
  only; **(b)** count all flora. Recommendation: **(a)** same-species (intra-species crowding drives
  cluster self-regulation; inter-species is the parked competition seam, RESOLVED 1c). Non-blocking;
  document the chosen scope on `SiteInput.NeighborCount`.

## Notes
- `Step` deliberately mirrors `climate.Step`'s and `navmap`'s "pure read → return delta → world
  applies" shape: it returns `(next, deltas)` and never writes objects[], exactly as `climate.Step`
  returns `(next, transitions)` and `pathfind` returns a path. This is what keeps `world` the single
  object mutator (D12 apply phase).
- The two-axis `Length`/`Width` + derived-stage choice (RESOLVED §1 refinement of 1b option c)
  matches climate's continuous `Moisture` + threshold-transition shape: each morphology axis
  integrates smoothly with suitability (via its OWN rate) while stages are a data threshold over the
  height axis (`Length`) for render/gating. Keep maturity in `Length` (height = maturity proxy) and
  canopy in `Width`; never store a stage. The two rates being independent is what gives species
  distinct shapes (tall pine vs broad oak vs flat grass) from data alone (D2/D10).
- Propagation, death, and yields are the three randomness sites — all on the injected fork (RESOLVED
  1a option a). Keep every draw on the per-step `*rng.RNG` world supplies so the field is byte-
  deterministic from the seed. The two growth-axis integrations are NON-random (suitability × rate).
- Tuning + behavior live in `content/objects.yaml` `flora:` §6 formulas (D10). Adding a species, a
  yield, changing a shape (length-rate vs width-rate ratio), or changing succession behavior is a
  content + §6 data change, never a code change — the §6 DSL is the extension seam (D2/D3:
  succession/forests/food-chains must emerge, not be tabled).
- The `Dexterity` read in `Yield` is the one capability-stat coupling; world passes the actor's stat
  value in. flora is otherwise stat-agnostic (it does not hold `Stats`). Stat *training* belongs to
  the stats/lifecycle owner — see Out of Scope + Open Questions.
- Reference paths: `docs/flora.md` (binding resolutions + §1 two-axis refinement),
  `docs/design.md §5/§6/§7`, `docs/data-contracts.md §6` (periodic full + sparse deltas),
  `docs/glossary.md` (the coined `suitability`/`Length`/`Width`/`shade`/`Fell`/`Plant`/`Dexterity`
  are registered there; `Length`/`Width` replace the old single `Growth` axis this refinement).
