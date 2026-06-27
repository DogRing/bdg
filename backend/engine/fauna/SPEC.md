# SPEC — `engine/fauna`

> Status: `DRAFT`
> Leaf level: `L4` (flat, beside `agent`)  ·  Owner agent: `<filled by implementer>`
> Scope: **P_fa1** (`docs/fauna.md §2`). Sub-SPEC: `backend/engine/fauna/scent/SPEC.md` (the scent grid).

## Purpose
The **reduced-reactive animal controller**: a pure, deterministic per-tick **horizon-1 utility
arbitration** over the *read-only* world snapshot that produces **one intent per `Animal`** — for
each animal it scores every candidate atomic action (from the shared `engine/mind/actions`
registry) with that (species × action)'s §6 utility `Program`, picks the max (ties by sorted
`ActionID`), and emits the chosen action + targeting + the steered next position/heading + the
per-tick drive evolution. It owns *how an animal decides and moves and smells* — **no planner, no
ToM, no value stack** (F1/F2/F3): a single real-stat channel, drives, and a scent grid. It does
**not** own *when* it runs (cadence), the combined agent+animal **apply order** or the scent
bulk-pass cadence (those are `engine/world`'s, F41), the navmap/climate state it reads (injected as
values / via a declared sampler), or the object/animal mutation itself (`engine/world` is the sole
mutator, D12 apply phase). No IO, no wall-clock, no global rand: every output is a function of
`(snapshot, Rules, rng)` (D12). Mirrors `engine/env/flora`'s and `engine/env/decay`'s "pure read →
return delta/intent → world applies" shape exactly. Concept & rationale: `docs/design.md §5`
(continuous coords / dynamic terrain) + `§6` (the shared §6 evaluator) + `§7` (object-mortality) +
`docs/fauna.md` (§0 locked, §1 F1–F24 + §1.3 F25–F44 ALL RESOLVED — binding; do NOT re-decide).

## Public Interface
```go
package fauna

import (
    "github.com/dogring/bdg/engine/kernel/core"
    "github.com/dogring/bdg/engine/kernel/expr"
    "github.com/dogring/bdg/engine/kernel/rng"
    "github.com/dogring/bdg/engine/mind/actions"
    "github.com/dogring/bdg/engine/space/spatial"
    "github.com/dogring/bdg/engine/fauna/scent"
)

// ── Identity & state ──────────────────────────────────────────────────────────────

// SpeciesID names a fauna species from content/objects.yaml object_kinds carrying a `fauna:` block
// (e.g. "deer", "wolf"). core.Tag underlying so the content catalog validates it; fauna never parses
// YAML (D10). Flora.SpeciesID parity (F42).
type SpeciesID = core.Tag

// DriveID is an OPEN drive key (F25/F29(ii)/D10 — a new drive is content data, StatID/Stats parity).
// The base drive vocabulary is hunger / fear / thermal / fatigue / repro_readiness (F19/F25); a drive
// id is ALSO its §6 Attr operand name (lowercase, F27 — Attr("hunger") → Drives["hunger"]).
type DriveID = core.Tag

// Animal is one live animal's fauna-owned dynamic state (F29). Pos is the continuous world coordinate
// (D11 — never snapped to a scent cell). Stats is the open base-attribute vector (the same shape as
// engine/mind/stats.Stats = map[core.StatID]float64); fauna does NOT import engine/mind/stats
// (architecture import set: core/expr/rng/actions/spatial) — the vector is the raw open map, READ-ONLY
// here (D7: stat training/aging is a cross-cutting stats/lifecycle concern; §6 only reads it).
// Drives is the open per-drive scalar vector ∈ [0,1] (F29(ii)). Vital is the single mortality scalar
// (F3). Heading is the continuous steering direction in radians and is the reference axis for the FOV
// sight test (F44). CurrentAction is the last-chosen action — it backs the §6 stickiness term (F30(a)),
// NOT an FSM state (D3): the controller exposes it as the per-candidate `is_current` operand.
type Animal struct {
    ID            core.ObjectID            // world owns the id space; shares the spatial ObjectID space
    Species       SpeciesID
    Pos           core.Vec2                // continuous (D11)
    Stats         map[core.StatID]float64  // open base-attribute vector, READ-ONLY (D7); §6 Stat channel
    Drives        map[DriveID]float64      // open drive vector ∈ [0,1] (F29(ii))
    Stamina       float64
    Vital         float64                  // single vital (F3); world owns death (object-mortality, §7)
    Heading       float64                  // steering direction (radians); FOV reference axis (F44)
    CurrentAction actions.ActionID         // last chosen action — §6 stickiness operand, NOT an FSM (F30/D3)
}

// EnvSample is the per-animal exogenous climate world samples at Animal.Pos and injects each tick.
// fauna is a pure transform over VALUES — it does NOT import climate (mirrors flora's SiteInput rule).
// In P1 climate is OFF → all fields are the neutral value world injects (temperature/moisture neutral,
// zero Wind) → apparent_temp neutral → thermal drive stays 0 + scent spread stays local (F10/F21/F33).
// The §6 operand names match the climate/flora/decay Context vocabulary exactly (temperature, moisture,
// wind.dir, wind.mag).
type EnvSample struct {
    Temperature float64     // climate temperature at Pos (operand `temperature`); neutral in P1
    Moisture    float64     // climate moisture at Pos (operand `moisture`); neutral in P1
    Wind        scent.Wind  // {Dir radians, Mag} (operands `wind.dir`/`wind.mag`); zero in P1
}

// TerrainSampler is the read-only navmap view fauna DECLARES and world ADAPTS (dependency inversion —
// expr.Context / perception.WorldSnapshot parity). Steering samples passability at the dynamically
// computed next position, so it cannot be pre-injected like flora's fixed-Pos SiteInput; world
// implements this over its navmap snapshot. fauna must NOT import engine/space/navmap (F35: "Passable/
// TerrainAt sample only, no pathfind", D11). Pure for a given tick snapshot.
type TerrainSampler interface {
    Passable(p core.Vec2) bool // false ⇒ steering must not step into p (avoid only; no pathfind)
}

// Snapshot is the read-only world view the controller scores over (the read phase; parallel-safe —
// every animal's evaluation is independent and reads only immutable/snapshot state, D12 plan phase).
//   Animals — all live animals in sorted ObjectID order (D12); never mutated by Step.
//   Scent   — the scent field; Step calls only its READ side (committed snapshot buffer, next-tick
//             latency, F33). Deposit/Spread/Commit are world's apply-phase + bulk-pass (P_fa2).
//   Spatial — the shared proximity index (combined agent/object/animal ObjectID space) world keeps
//             populated; used READ-ONLY for the F44 sight predator query. (fauna imports spatial.)
//   Terrain — the passability sampler (above).
//   Env     — per-animal EnvSample keyed by Animal.ID; a missing live-animal entry is a world-contract
//             bug (panic, mirrors flora's missing SiteInput).
//   DT      — locomotion time-step magnitude for one tick (world/balance), used by steering (F35).
type Snapshot struct {
    Animals []Animal
    Scent   *scent.Grid
    Spatial *spatial.SpatialHash
    Terrain TerrainSampler
    Env     map[core.ObjectID]EnvSample
    DT      float64
}

// Intent is the controller's per-animal output (ONE per animal, F1/F41). world APPLIES it in the
// combined agent+animal sorted-ObjectID apply order (F41 — Out of Scope) and is the sole mutator of
// Animal state: it enacts Action (with conflict resolution by the relevant stat, ties by ObjectID),
// moves the animal to NextPos (spatial.Move), sets Heading, and commits the proposed Drives/Stamina.
// The proposed Drives are the PASSIVE per-tick evolution (F25(c)); the action's own drive Effect
// (Eat→hunger↓, Rest→fatigue↓) is layered by world when it enacts the action (world the sole mutator).
type Intent struct {
    Animal      core.ObjectID
    Action      actions.ActionID    // max-utility action; ties broken by sorted ActionID (D12)
    Target      core.ObjectID       // resolved target for a targeted action (Hunt→prey, Eat→food);
                                    // empty for untargeted actions (Graze/Flee/Wary/Rest)
    NextPos     core.Vec2           // steered next position (continuous, D11); == Pos if Rest/blocked
    NextHeading float64             // steered next heading (radians)
    Drives      map[DriveID]float64 // passive per-tick drive evolution (F25(c)); world commits it
    Stamina     float64             // proposed next stamina
}

// ── The pure transform world calls (mirrors flora.Step / decay.Step) ────────────────

// Step scores ALL animals and returns ONE Intent each, in sorted Animal-ObjectID order (D12). It is a
// PURE function of (snap, rules, rng): it does NOT mutate snap (incl. snap.Animals, the scent grid, or
// the spatial index) and returns intents world applies. For each animal, in sorted ObjectID order:
//   1. SENSE (F44 two-channel, scent-only-omni after F34↔F44): READ the scent grid at Pos (omni
//      neighbor/upwind, smell radius) → fills scent.{food,prey,predator} + dist.{food,prey} + per-channel
//      coarse direction; QUERY snap.Spatial within the sight radius for predator ANIMALS whose relative
//      bearing is within Heading ± fov_arc → fills sight.predator (1/0) + dist.predator (nearest).
//   2. DRIVES (F25(c)): advance the drive vector — accumulators (hunger/fatigue/repro_readiness) by their
//      Rules rate constants (D9), fear SET-from-context from the predator scent/sight channels (the needs
//      UpdateConditionalNeeds shape, replicated — fauna does not import needs), thermal SET from
//      Rules.AppTemp (OFF/0 in P1). → the proposed Intent.Drives.
//   3. SCORE (F1/F26): build the §6 Context once per animal (Stat→base stats; Attr→drives/scent/dist/
//      sight/apparent_temp/wind), then for each candidate ActionID in Rules.Candidates(species) set the
//      per-candidate `is_current` operand (1 iff == CurrentAction, the §6 stickiness term, F30(a)) and
//      evaluate Rules.Utility(species, action, ctx). Pick the MAX; ties break by sorted ActionID (D12).
//   4. STEER (F35): speed = Rules.Speed(species, ctx) (§6 base-stat speed + fear/fatigue modulation);
//      direction = the resolved channel direction the chosen action seeks/avoids (Graze/Hunt→toward
//      food/prey, Flee→away from predator, Wary→slowly away, Rest→none); NextPos = Pos + dir·speed·DT
//      IFF snap.Terrain.Passable(NextPos) (else stay/slide; no pathfind, D11); NextHeading turns toward dir.
// rng is the injected per-tick fork world supplies (mirrors flora/decay; per-step fork(tick), F41). The
// arbitration, drive advance, and scent read are deterministic and draw nothing; rng is drawn ONLY by a
// stochastic steering term (e.g. a species §6 wander/jitter operand) — same seed ⇒ identical steering.
func Step(snap *Snapshot, rules *Rules, rng *rng.RNG) []Intent

// AttrOperands returns the FIXED controller-resolved §6 Attr operand vocabulary (sorted, de-duplicated)
// fauna exposes — scent.{food,prey,predator}, dist.{food,prey,predator}, sight.predator, apparent_temp,
// temperature, moisture, wind.dir, wind.mag, is_current. platform/config cross-checks each compiled
// program's expr.ReadsAttrs() against this set ∪ the species' drive ids (the drive operands, open per
// D10) at load (flora operand-cross-check parity) so a typo Attr (silently 0, expr policy) is a LOAD
// failure, never a quiet bug. Deterministic; does NOT include the Stat channel (validated against the
// stats registry) or drive ids (those come from the species `fauna:` block).
func AttrOperands() []core.Tag

// ── Rules (the data-defined fauna table; flora.Rules parity, F26/F31) ────────────────

// Rules is the compiled, immutable per-species fauna table from content/objects.yaml `fauna:` blocks:
// per species — the candidate action set + each (species × ActionID) §6 utility Program (F26(a)), the
// drive rate constants + fear set-from-context params (F25(c)), the §6 apparent_temp Program (F40), the
// §6 speed Program (F35), the predator flag (`threat:predator`, F8), the diet/target tags (F7), and the
// sense radii (smell radius, sight radius, fov_arc — F31/F44). Built ONCE by platform/config (parse each
// §6 via engine/kernel/expr, validate StatIDs/operands/action-ids/tags); engine/fauna evaluates it
// READ-ONLY and never parses YAML (D10). Mirrors flora.Rules / decay.Rules exactly.
type Rules struct{ /* opaque; per-SpeciesID compiled §6 programs + drive params + sense radii + tags. */ }

// Candidates returns the species' candidate ActionIDs (= the utility-map keys, F26(a)/F28) in sorted
// order (D12). Each is a SHARED engine/mind/actions registry id (Graze/Flee/Wary/Hunt/MoveTo/Rest); the
// controller scores exactly these. Empty for an unknown species (fauna-off neutrality).
func (r *Rules) Candidates(sp SpeciesID) []actions.ActionID

// Utility evaluates the (species × action) §6 utility Program against ctx → a plain score (F26(a)). The
// controller picks the max over Candidates; ties by sorted ActionID (D12). It is a PURE numeric §6
// expression (design.md §6) — NO RNG, NO cross-action reference, NO sequencing (a flat score set, not a
// tree — D3 guard). Returns a sentinel "never" score (e.g. the formula's natural value) for a species/
// action absent from the table.
func (r *Rules) Utility(sp SpeciesID, act actions.ActionID, ctx expr.Context) float64

// DriveUpdate advances the whole drive vector ONE tick per F25(c) and returns the new vector (clamped
// [0,1]): accumulators rise by their per-species rate constants (hunger/fatigue/repro_readiness — D9,
// no future field); fear is SET-from-context from the resolved predator channels via ctx (scent.predator
// → wary level, sight.predator → flee level — the needs conditional-set shape, rate+level data, F25(c)/
// F43); thermal is SET from AppTemp (OFF → 0 in P1). Pure, no RNG, no map-iteration for logic (iterate
// drive ids in sorted order). It does NOT apply the chosen action's drive Effect (that is world's apply).
func (r *Rules) DriveUpdate(sp SpeciesID, cur map[DriveID]float64, ctx expr.Context, dt float64) map[DriveID]float64

// AppTemp evaluates the species' §6 apparent_temp Program over the climate operands (temperature/
// moisture/wind.mag) + the animal's own attrs (size/base stats), F40 — a per-entity §6 ("winter" emerges
// from sustained low apparent_temp, no season enum). The controller exposes the result as the
// `apparent_temp` operand and DriveUpdate biases the thermal drive by it. P1 climate-OFF: neutral inputs
// ⇒ neutral apparent_temp ⇒ thermal behaviour 0 (decay/flora climate-input-contract parity). Pure, no RNG.
func (r *Rules) AppTemp(sp SpeciesID, ctx expr.Context) float64

// Speed evaluates the species' §6 locomotion speed Program (base = §6(base stats); modulated by
// fear/fatigue drive terms, F35(a)+(c)) → world units per DT. The controller steers NextPos along the
// chosen action's resolved direction at this speed. Pure, no RNG (any wander jitter is a separate rng draw).
func (r *Rules) Speed(sp SpeciesID, ctx expr.Context) float64

// IsPredator reports whether the species carries the `threat:predator` tag (F8) — used to classify which
// nearby spatial entities are predators in the F44 sight query. Pure.
func (r *Rules) IsPredator(sp SpeciesID) bool

// Senses returns the species' sense radii: smell radius (the scent-grid read radius; the scent grid's
// cellSize ∝ this, F32), sight radius (the spatial predator-query radius), and fov_arc (the half-angle,
// radians, of the Heading-relative forward sight cone, F44). All from the `fauna:` senses block (D10).
func (r *Rules) Senses(sp SpeciesID) (smellRadius, sightRadius, fovArc float64)
```

## Dependencies
- `engine/kernel/core` — `Vec2`, `Tag`, `ObjectID`, `StatID`. No IO.
- `engine/kernel/expr` — the shared §6 `Formula` evaluator (L0, already decided). fauna OWNS the compiled
  per-species `Program`s inside `Rules` (utility / apparent_temp / speed) and evaluates them: it builds an
  `expr.Context` adapter (`Stat`→base stats; `Attr`→drives/scent/dist/sight/climate/`is_current`; `Pred`→
  unused, always false — animals have no §6 predicates in P1) exactly as flora evaluates its programs.
  fauna does NOT parse the DSL (that is `platform/config`'s compile step) and never imports
  `engine/mind/gates`. expr L0 is UNCHANGED (F27 — all fauna operands ride the lowercase/dotted `Attr`
  channel, the flora `moisture` / Cm3 `tool:<family>.quality` pattern; no new expr method).
- `engine/kernel/rng` — `*RNG` (injected per-tick fork; only a stochastic steering term draws from it, D12).
- `engine/mind/actions` — `ActionID`, the shared `Registry` (candidate actions are SHARED registry ids;
  the controller reads action defs for the Duration→`EffectPerMinute` rate denominator, F30(a)). fauna
  uses the registry's `IDs()`-style sorted ordering for the utility tie-break. It does NOT use gates/cost
  (F1 — no planner): an action's `Tags` are read only for utility tag-matching / steer channel, never as
  a gate or cost.
- `engine/space/spatial` — `*SpatialHash` (`NearbyEntities`, sorted by ObjectID) for the F44 sight
  predator query; READ-ONLY in the score phase. world keeps it populated (combined ObjectID space).
- `engine/fauna/scent` — the **scent grid** sub-module (`backend/engine/fauna/scent/SPEC.md`): `Grid`,
  `Channel`, `Wind`, `Reading`. fauna OWNS the grid for P1 (F36); the controller calls only its READ side.
- *(NOT `engine/world` / `engine/agent` / `engine/mind/planner`)* — the L4 cut: fauna emits intents and
  world applies them (combined agent+animal ObjectID order, F41); fauna never imports world/agent/planner,
  so it introduces no cycle (same dependency-inversion as climate/flora/decay). Asserted by an import guard.
- *(NOT `engine/space/navmap` / `engine/env/climate` / `engine/mind/needs` / `engine/mind/stats` /
  `engine/mind/tom` / `engine/mind/values` / `engine/mind/gates` / `engine/mind/perception`)* — navmap is
  reached via the declared `TerrainSampler` (world adapts); climate via injected `EnvSample` values; the
  fear conditional-set replicates the needs shape without importing needs; the base-stat vector is the raw
  open map (no stats import); there is no ToM/value/gate/perception coupling (F2/F3).

## Owned Data
- The **`Animal` entity type** + the live animal set is held by `engine/world` (one per run, snapshot-
  serialized — `docs/data-contracts.md §6`, P_fa5); `fauna.Step` reads a `Snapshot` and never mutates it.
- The **scent grid** (`engine/fauna/scent.Grid`) is fauna-owned for P1 (F36) — its deposit/spread/read
  state. world DRIVES the deposit (apply phase) + spread bulk pass + commit (P_fa2); fauna defines the
  grid behaviour. See the scent sub-SPEC for ownership detail.
- The **`Rules`** table is owned by `platform/config` (built from `content/objects.yaml` `fauna:` blocks
  via `engine/kernel/expr`), injected read-only; this module never parses or mutates it.
- This module owns **no** object instances in `objects[]`, **no** spatial-hash entries, **no** navmap/
  climate state — it only emits `[]Intent` (and, via the scent sub-module, the scent field).
- **§6 Attr operand vocabulary** (the F27 bridge, cross-checked by `platform/config`): the fixed
  controller-resolved set from `AttrOperands()` — `scent.food`/`scent.prey`/`scent.predator`,
  `dist.food`/`dist.prey`/`dist.predator`, `sight.predator`, `apparent_temp`, `temperature`, `moisture`,
  `wind.dir`, `wind.mag`, `is_current` — plus the species' open drive ids (the drive operands, D10). The
  Stat channel (UPPERCASE) is the base-attribute vector, validated against the stats registry by config.

## Invariants
- **D12 determinism** — `Step` is a pure function of `(snap, Rules, rng)`. Same snapshot + same RNG fork
  ⇒ byte-identical `[]Intent`. No `time.Now()`, no global rand, no wall-clock; the only randomness is the
  injected `*rng.RNG` (drawn only by a steering jitter term, if any).
- **D12 no map-iteration for logic** — animals are scored in **sorted `ObjectID` order**; intents are
  returned sorted by `Animal`; candidate actions are iterated via `Rules.Candidates` (sorted) and ties
  break by sorted `ActionID`; drive ids are advanced in sorted order; the `snap.Env` map is read by
  sorted animal id; `spatial.NearbyEntities` is already ObjectID-sorted. No map-range drives any decision.
- **D11 / continuous space** — every `Pos`/`NextPos`/`Heading` is continuous; the scent grid is an
  auxiliary INDEX (the animal reads the cell it falls in + neighbors, never snapped to it); steering moves
  along a continuous vector and only SAMPLES `Terrain.Passable` (no tile iteration, no pathfind, F35/D11).
- **D3 no FSM / no hand-drawn tree** — action choice is a flat horizon-1 `max` over §6 utility *scores*
  (F1/F26). There is **no** behaviour tree, **no** explicit wary→flee transition, **no** commit+interrupt
  state machine: Wary vs Flee is purely the continuous `fear` value crossing the §6 utility bands
  (F43/F30(a)); stickiness is the §6 `is_current` term, not a stored mode. A utility `Program` must be a
  pure score — it may not reference another action's score or encode sequencing (D3 guard, F26).
- **D4 / D10 data-defined** — utility, drive rates + fear levels, apparent_temp, speed, sense radii,
  predator/diet tags are ALL `content/objects.yaml` `fauna:` §6/data evaluated via `engine/kernel/expr`;
  there is **no** bespoke per-species Go decision/steer/drive function. A species is content (D10); an
  unknown species/operand/action-id is caught at load (`platform/config`), never in `Step`.
- **D2 emergence** — no hardcoded meta-institution: husbandry/taming, herds/territory, food-chains,
  predator-roles all EMERGE from base mechanics (drives + utility + tags). There is **no** `tame` flag,
  **no** herd/role type, **no** authored food-chain table (F7/F13). Predator→prey is a tag relation
  (`threat:predator` / `edible`/`prey` diet), not a hardcoded pairing.
- **D5 / D1 — drive ≠ Value** — the drive set is a SEPARATE motivation machine from the agent `Value`
  stack (reduced loop, §0-1); fauna has no `Value`/`Standing`/`Salience` and never imports values. (The
  D4-risk dot-product utility F26(b)/(c) is rejected — utility is a per-(species×action) §6 score.)
- **D6 / D8 — no ToM** — animals carry no belief distribution and no self-perception; F2 collapses the
  attempt/judge two-channel into the single real-stat channel. Predator→agent Safety flows only through
  the perception hostile-tag channel world wires (F8), not through any fauna ToM.
- **D7 read-only stats** — `Animal.Stats` is read by §6 (the `Stat` channel) and never written here;
  stat training/aging is a cross-cutting stats/lifecycle concern (flora `Dexterity` parity). fauna stores
  no per-action skill — competence is always §6 composition of the current base attributes (D7).
- **D9 no future field** — a drive accumulator rises by a RATE (hunger/fatigue/repro_readiness); there is
  **no** "future need", "time-until-hungry", or stored provisioning field on `Animal` or in a drive.
- **D12 fixed-order, injected RNG** — the scent grid spread is a fixed-order stencil with no RNG; the FOV
  bearing test iterates the ObjectID-sorted `NearbyEntities` set; the per-tick rng is the injected fork.
- **Read-only inputs** — `Step` never mutates `snap` (incl. `snap.Animals`, the grid, the spatial index),
  `Rules`, or the `rng` beyond drawing from it. The `expr.Context` adapter is pure for a given animal.
- **Outcome-neutral until activated (the P_fa1 safety lever)** — with NO species placed there are zero
  `Animal`s in the snapshot ⇒ `Step` returns ZERO intents ⇒ no behaviour, no scent deposits, no animal
  state change; the legacy `prey` timer-respawn object stays untouched; existing world goldens hold.
  Activation (placing `fauna:` species) is the deliberate P_fa3 re-baseline (climate/flora/decay parity).

## Acceptance Criteria (testable)
- [ ] **Utility max + ID tie-break determinism (F1/F26/D12)** — over a fixture species whose candidate
  set is scored against a stub Context, `Step` picks the action with the strictly-highest
  `Rules.Utility`; when two candidates score EQUAL, the lexicographically-smaller `ActionID` wins; the
  choice is identical across repeated calls and a second `Rules` built from the same content. Table-driven.
- [ ] **One intent per animal, sorted (D12)** — `Step` returns exactly one `Intent` per snapshot animal,
  in ascending `Animal` ObjectID order; shuffling `snap.Animals`/`snap.Env` insertion order yields
  byte-identical intents.
- [ ] **Drive integration per F25(c)** — over N ticks an accumulator drive (hunger) rises by `rate·N`
  clamped to `[0,1]`; with `scent.predator` present in the Context, `fear` is SET to the species wary
  level; with `sight.predator` present, `fear` is SET to the (higher) flee level; with neither, fear
  decays; `thermal` stays 0 while climate is OFF (neutral `apparent_temp`). Table-driven over the channels.
- [ ] **Seeded steering reproducible (F35/D12)** — for a species whose `Speed`/steering includes a
  stochastic jitter term, two `Step` runs with the same seed produce byte-identical `NextPos`/`NextHeading`;
  a different seed differs; steering never steps into an impassable cell (`Terrain.Passable(NextPos)`
  false ⇒ animal stays / slides, never teleports, D11); Rest ⇒ `NextPos == Pos`.
- [ ] **Scent deposit / spread / read determinism + neutrality (F20–F24/F32–F34)** — delegated to the
  scent sub-SPEC ACs (`backend/engine/fauna/scent/SPEC.md`): predator-channel every-tick source deposit,
  fixed-order stencil bulk spread on `tick%Ns` with next-tick latency, omni neighbor/upwind read; with no
  deposits a read returns all-absent (controller fills `scent.*`=0, `dist.*`=large). The controller-side
  AC: a `Step` whose grid has a `predator` on-cell within smell radius sets the animal's `scent.predator`
  operand to 1 and a coarse `dist.predator` direction away-from-source for a Flee/Wary steer.
- [ ] **Sight FOV predator query (F44(c-ii)/D11/D12)** — a predator ANIMAL within the sight radius AND
  within `Heading ± fov_arc` sets `sight.predator`=1 + `dist.predator`=its distance; the same predator
  OUTSIDE the FOV cone (behind, |bearing| > fov_arc) sets `sight.predator`=0 (rear blind spot); a
  non-predator nearby entity is ignored; the nearest qualifying predator wins; the bearing test iterates
  the ObjectID-sorted `NearbyEntities` set (deterministic). Table-driven over bearings around the cone edge.
- [ ] **Shared-registry scoring (F28)** — `Rules.Candidates(species)` is a subset of the shared
  `actions.Registry.IDs()` (Graze/Flee/Wary/Hunt/MoveTo/Rest); the controller scores exactly those and
  reads each action's def (e.g. Duration) from the shared registry — it builds NO fauna-private action set.
- [ ] **Context bridge = lowercase/dotted Attr; stats = Stat (F27)** — the `expr.Context` adapter
  resolves `Strength`/`Agility`/… via `Stat` (base-attribute vector) and `hunger`/`fear`/`scent.predator`/
  `dist.food`/`sight.predator`/`apparent_temp`/`wind.dir`/`is_current` via `Attr`; an unknown Attr returns
  `ok=false` (→ expr's 0 policy); `AttrOperands()` returns the fixed vocabulary sorted + de-duplicated and
  matches the operands the fixture programs reference. Table-driven.
- [ ] **fauna-OFF neutrality (the P_fa1 lever)** — with an empty `Rules` and/or zero animals, `Step`
  returns no intents, draws nothing from rng, performs no scent deposit, and leaves all input state
  unchanged; existing world goldens hold (the regression guard for shipping the controller dark).
- [ ] **Read-only inputs** — a property test confirms `snap.Animals`, the scent grid, the spatial index,
  and `Rules` are unchanged after `Step`.
- [ ] **Missing EnvSample panics** — `Step` with a live animal absent from `snap.Env` panics (world-
  contract guard, mirrors flora/decay), never silently skips an animal.
- [ ] **Determinism golden (D12)** — a fixed `(snapshot sequence, seed, Rules)` over N ticks yields a
  byte-identical digest of the per-tick `[]Intent`; a second run from a registry built from the same
  `content/objects.yaml` reproduces it (cross-process). First established fauna-OFF (neutral), then
  re-baselined at activation (P_fa3).
- [ ] **No wall-clock / no global rand / no forbidden import (D12 guard)** — grep/import guard: no `time`
  import for logic, no global `rand`; the import set is EXACTLY `core`, `expr`, `rng`, `actions`,
  `spatial`, `fauna/scent` (+ stdlib `sort`/`math`); NO `engine/world`, `engine/agent`,
  `engine/mind/planner`, `engine/space/navmap`, `engine/env/climate`, `engine/mind/needs`,
  `engine/mind/stats`, `engine/mind/tom`, `engine/mind/values`, `engine/mind/gates`, or
  `engine/mind/perception` import.
- [ ] **No hardcoded constant / id (D10 guard)** — grep guard: no species-name / action-name / drive-name
  / operand / rate / threshold / sense-radius string literal in logic; every constant/formula flows from
  `Rules` (injected by `platform/config`) or the actions registry. The fixed `AttrOperands()` vocabulary
  is the one declared operand list (the F27 bridge), not scattered literals.

## Out of Scope
- **The combined agent+animal apply ORDER + the per-tick `fork(tick)` RNG derivation + scent bulk-pass
  cadence wiring** (world drives `Grid.Deposit` in the apply phase + `Grid.Spread` on `tick % Ns` + the
  `Grid.Commit` next-tick swap, F33/F41) → `engine/world` (P_fa2, `backend/engine/world/SPEC-tick.md`).
  **Apply contract documented here, implemented there:** world collects fauna `[]Intent` alongside agent
  intents, applies them in ONE sorted-ObjectID sequence (D12; conflicts on a grabbed resource resolve by
  the relevant stat, ties by ObjectID), moves each animal (`spatial.Move`), commits Drives/Stamina/Heading,
  layers the enacted action's drive Effect, and owns Vital/death (object-mortality, §7). fauna only emits
  intents + defines the scent behaviour.
- **`carcass` object + `Butcher` extract + decay-lot mapping** (a dead animal → parameterized `carcass`
  kind + `raw_meat` decay lot, unbutchered → `rotten_matter`) → world/actions/`engine/env/decay` (P_fa3,
  F11/F12/F37/F38).
- **`content/objects.yaml` `fauna:` block parse + §6 compile into `Rules` + operand/StatID/action-id/tag
  cross-check** (`ReadsAttrs()` ⊆ `AttrOperands()` ∪ species drive ids; `Reads()` ⊆ stats registry) →
  `platform/config` (`content/schema/objects.schema.json`).
- **`Graze`/`Flee`/`Wary` `content/actions.yaml` entries** (new shared-registry actions; the
  `actions.IDs()`/`Producers` golden re-baseline they cause) → `content` + `engine/mind/actions` (P_fa3,
  F28/F43). This SPEC consumes them via the shared registry; it adds no action.
- **Climate `temperature`/`moisture`/`wind` operand SOURCE + `apparent_temp` activation** (climate-OFF in
  P1 → neutral `EnvSample`; "winter" emergence, wind-driven long-range scent + upwind homing) →
  `engine/env/climate` + `engine/world` (P_fa4, `docs/climate.md §1c` CA1–CA3; operand names/units MUST
  match across docs).
- **Reproduction**: P1 uses the legacy `balance.regen.prey_respawn` timer (world); emergent drive-gated
  birth (`repro_readiness`) → world/balance (P_fa4, F9/F39). fauna only advances the `repro_readiness`
  accumulator; it spawns nothing.
- **Serialization / SSE of the `animals[]` field** (periodic full + sparse spawn/move/die delta) →
  `platform/persist` + `docs/data-contracts.md §6` (P_fa5).
- **`docs/glossary.md` sync** of the F19/F42/F43/F44 coined terms (`Animal`/`SpeciesID`/`DriveID`/`Wary`/
  scent channels/`wind.dir`/`wind.mag`/`fov_arc`/`Heading`/`is_current`) → a separate glossary step.
- The shared §6 `expr` evaluator **implementation** → `engine/kernel/expr` (L0); fauna only USES it.

## Open Questions
> `docs/fauna.md` §1 (F1–F24) and §1.3 (F25–F44, incl. F43/F44) are **ALL RESOLVED** (human-confirmed
> 2026-06-26). This SPEC writes from those resolutions and **invents no mechanism**. The items below are
> the NEW plumbing/naming seams surfaced while writing the SPEC (flora-SPEC precedent) — each follows
> directly from a RESOLVED decision, **none re-opens an F-item, and NONE blocks P_fa1 build**; they are
> confirmations for the glossary-sync / world-wiring steps. (No genuine new *mechanism* seam appeared, so
> no STOP.)
- **`is_current` stickiness operand name (glossary-sync, non-blocking).** F30(a) RESOLVED stickiness as a
  §6 *term*; a §6 program can only express it via an operand, so the controller exposes a per-candidate
  `is_current` ∈ {0,1} Attr (1 iff the candidate == `Animal.CurrentAction`). This name is NOT in F42's
  coined list. Recommend adopting `is_current`; confirm in the `glossary.md` sync (F42). The mechanism is
  resolved — only the name is new.
- **`TerrainSampler` world-contract shape (P_fa2 wiring, non-blocking).** F35 says steering samples
  navmap `Passable`/`TerrainAt`, but fauna may not import navmap (locked import set), so it DECLARES the
  `TerrainSampler` interface and world adapts it (perception-`WorldSnapshot` / flora-`SiteInput` parity).
  Open: does steering need only `Passable(p)` (avoid impassable) or also a `Cost`/`TerrainAt` term for
  speed modulation? Recommend `Passable`-only for P_fa1 (F35 "avoid only, no pathfind"); add a cost term
  only if a species §6 speed formula needs terrain. Confirm at the world wiring (P_fa2).
- **`core.Stats` shorthand (non-blocking).** `docs/fauna.md` F29(i) writes "open `core.Stats`", but the
  open stat vector type lives in `engine/mind/stats` (`Stats = map[core.StatID]float64`) and the locked
  import set excludes `stats`. This SPEC uses the raw open map inline (`map[core.StatID]float64`,
  byte-identical shape) so no `stats` import is needed. Recommend keeping it inline (minimal import set);
  flag only if the human prefers fauna to import `engine/mind/stats` to reuse the named `stats.Stats`.
- **Agents-as-predator for sight (P_fa3 scope, non-blocking).** F8 says a predator is any moving entity
  carrying `threat:predator`. P_fa1 sight is scoped to predator ANIMALS (entities in the fauna snapshot
  whose species `IsPredator`); an agent carrying `threat:predator` would need world to inject its
  predator-ness into the sight query. Recommend deferring agent-as-threat injection to P_fa3 (no agent
  import in fauna). Confirm scope at activation.

## Notes
- `Step` deliberately mirrors `flora.Step`/`decay.Step`: pure read → return per-entity delta/intent →
  `world` applies. This keeps `world` the single mutator (D12 apply phase) and adds no cycle (F5/F41 —
  fauna is dependency-inverted exactly like the env drivers).
- **Two motivation machines, kept apart (D5).** The drive set is the animal's whole motivation; it must
  NOT drift into the agent `Value` stack and vice-versa. Keep utility a per-(species×action) §6 score
  (F26(a)) — never an EffValue dot-product (F26(c) rejected) and never an engine-coded drive↔action map
  (F26(b) rejected, D4 risk).
- **Wary vs Flee is one continuous fear value (F43/D3).** Do not write a wary→flee transition. `scent.
  predator` raises fear into the wary band (Wary wins the §6 max), `sight.predator` raises it into the
  flee band (Flee wins). The "two-stage" behaviour is an emergent consequence of the continuous value
  crossing the data utility bands — implementing it as a state machine is a D3 defect.
- **Sensing is two clean channels (F44(c)/F34):** smell = the omni scent grid (early-warning → Wary),
  sight = the spatial-hash forward-FOV predator query (proximity → Flee). The F34 omni rule is now
  *scent-only*; sight is the Heading-relative cone (continuous bearing, D11), not a scent-grid read.
- The scent grid is split into its own sub-SPEC to keep this file ≤ ~400 lines (CLAUDE.md split rule):
  see **`backend/engine/fauna/scent/SPEC.md`** for the cell/bitset shape, the deposit/spread/read/commit
  contract, `cellSize ∝ smell radius`, and the determinism + neutrality ACs.
- Tuning + behaviour live in `content/objects.yaml` `fauna:` §6 (D10). Adding a species, a drive, a
  predator/prey relation, or changing a steer/utility is a content + §6 change, never a code change
  (D2/D3 — herds/husbandry/food-chains must emerge).
- Reference paths: `docs/fauna.md` (binding F1–F44 resolutions), `docs/design.md §5/§6/§7`,
  `backend/engine/env/flora/SPEC.md` + `backend/engine/env/decay/SPEC.md` (the pure-Step template),
  `backend/engine/kernel/expr/SPEC.md` (the §6 evaluator + `Attr`/`Stat` case-based classification fauna
  rides), `backend/engine/mind/actions/SPEC.md` (the shared candidate registry),
  `backend/engine/space/spatial/SPEC.md` (`NearbyEntities` sight query), `docs/architecture.md §2/§4/§5`
  (fauna = L4, stage 6, import set + dependency-inversion), `docs/glossary.md` (the F19/F42/F43/F44 terms).
