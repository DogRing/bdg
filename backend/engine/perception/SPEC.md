# SPEC — `engine/perception`

> Status: `DRAFT`
> Leaf level: `L3`  ·  Owner agent: `<filled by implementer>`

## Purpose

Models the three `Sense`s of the glossary (`Sight(LoS) | Smell(gradient) | Hearing`) as **pure,
stateless, per-tick snapshot queries**. Given an observer position and a read-only view of the
current tick's world, it answers "what can this observer perceive right now?" — entities in
line-of-sight, scent gradients, and sound events — without holding any state between ticks. It
consumes `engine/spatial` for proximity (D11: the spatial hash is the *only* source of position
data) and a passed-in `WorldSnapshot` for per-entity tags/opacity (architecture §4: perception
"operates on a passed-in world view"; it never imports `engine/world`). It computes sense
**modeling** only — falloff math, **binary opaque occlusion, and continuous flora-shade attenuation
of line-of-sight** (the "dark forest" emergent effect, `docs/flora.md` 1d / `docs/design.md §5`) —
and does **not** decide what the observer does with a percept (that is `engine/agent`).

## Public Interface

```go
package perception

import (
    "io"
    "github.com/dogring/bdg/engine/core"
    "github.com/dogring/bdg/engine/spatial"
    "github.com/dogring/bdg/engine/actions"
)

// PerceptionConfig holds the three sense radii loaded from content/balance.yaml's
// `perception:` block. World units (free coordinates, D11). Immutable after Load.
type PerceptionConfig struct {
    SightRadius   float64 // line-of-sight range            (balance.yaml perception.sight_radius)
    SmellRadius   float64 // scent-gradient cutoff range     (balance.yaml perception.smell_radius)
    HearingRadius float64 // base hearing range              (balance.yaml perception.hearing_radius)
    ShadeSightTau float64 // sight transmission threshold ∈ [0,1]: a target is occluded by flora
                          // shade iff the composed light transmission along the segment < this
                          // (balance.yaml perception.shade_sight_tau). The "dark forest" cutoff —
                          // keeps Sight's BOOLEAN contract while shade is continuous (flora 1d,
                          // see Open Questions: P_f3 thresholded transmission).
}

// LoadConfig parses ONLY the top-level `perception:` block from balanceDoc (the bytes of
// content/balance.yaml — the path is injected by platform/config, NEVER a file path here,
// keeping the engine IO-free, D10). It returns a descriptive error if the `perception:` block
// is absent or any radius is missing / ≤ 0 (shade_sight_tau must be in [0,1]). STRUCTURAL
// JSON-schema validation (content/schema/balance.schema.json) is platform/config's job, run
// before this call.
func LoadConfig(r io.Reader) (PerceptionConfig, error)

// PerceivedEntity is one entity an observer can see / is a candidate for sensing this tick.
// Distance is the Euclidean distance from the observer (already computed; saves callers a sqrt).
// Tags are a COPY of the entity's current tags (from the WorldSnapshot) — read-only.
type PerceivedEntity struct {
    ID       core.ObjectID
    Pos      core.Vec2
    Distance float64
    Tags     []core.Tag
}

// ScentSignal is one perceived scent: its source id and the gradient strength at the observer.
type ScentSignal struct {
    ID       core.ObjectID
    Strength float64
}

// SoundEvent is a sound emitted by an actor performing a `[loud]`-tagged action at its position
// for the current tick. SourceID is the acting agent/object; Pos is where the sound originated;
// ActionID names the action that emitted it. Distance is filled (observer→Pos) by Hearing on
// the returned (heard) events; it is ignored/zero on the input slice the world supplies.
type SoundEvent struct {
    SourceID core.ObjectID
    ActionID actions.ActionID
    Pos      core.Vec2
    Distance float64
}

// ShadeOccluder is one flora-shade caster the world exposes for the LoS attenuation test
// (NEW — flora 1d). It is the perception-facing projection of engine/flora.Shade: a position,
// a shade Radius, and an Opacity ∈ [0,1] (the per-plant light-blocking fraction). perception
// composes overlapping occluders MULTIPLICATIVELY (transmission = ∏(1 − opacity), flora 1d
// option c) along the sight segment — the "dark forest" emerges from overlap. perception never
// imports engine/flora; the world adapts flora.State.ShadeOf into this projection (dependency
// inversion, exactly like IsOpaque adapts entity opacity). Opacity here is CONTINUOUS, distinct
// from the binary IsOpaque (walls/bodies); both occlusion mechanisms compose in Sight.
type ShadeOccluder struct {
    ID      core.ObjectID
    Pos     core.Vec2
    Radius  float64 // shade footprint radius in world units (continuous, D11 — no tiling)
    Opacity float64 // light-blocking fraction ∈ [0,1] (1.0 = fully opaque; 0 = no shade)
}

// WorldSnapshot is a read-only view over entity positions, tags, opacity, and flora shade for
// the CURRENT tick. The world (engine/world) implements it; perception only reads it (dependency
// inversion — perception does not import engine/world OR engine/flora). EntitiesInRadius is the
// proximity candidate source, backed by the spatial hash (D11). Tags / IsOpaque resolve
// per-entity attributes the spatial hash does not store. ShadeOccluders enumerates the flora
// shade casters near a center (the world backs it with the same spatial hash + flora.ShadeOf).
// Implementations MUST return EntitiesInRadius and ShadeOccluders sorted by ascending ObjectID
// (the spatial-hash contract, engine/spatial/SPEC.md) so this module's composition is stable.
type WorldSnapshot interface {
    EntitiesInRadius(center core.Vec2, radius float64) []PerceivedEntity
    Tags(id core.ObjectID) []core.Tag
    IsOpaque(id core.ObjectID) bool
    // ShadeOccluders returns the flora shade casters whose footprint may intersect a sight
    // query around `center` within `radius` (the segment-test candidate set), ascending
    // ObjectID. Empty when flora is off / no plants nearby (then Sight reduces EXACTLY to the
    // pre-shade boolean-opaque behavior — outcome-neutral, flora-off guard). NEW for flora 1d.
    ShadeOccluders(center core.Vec2, radius float64) []ShadeOccluder
}

// Sensor evaluates the three senses against a WorldSnapshot. It is created once (per agent or
// shared) with the spatial index + config and reused across ticks; it holds NO per-tick state.
type Sensor struct{ /* opaque: *spatial.SpatialHash + PerceptionConfig (read-only) */ }

// NewSensor builds a Sensor from the spatial index and config. The index is the canonical
// position source (D11) used to enumerate sound emitters / sight candidates that share the
// world's tick snapshot. Panics if idx is nil.
func NewSensor(idx *spatial.SpatialHash, cfg PerceptionConfig) *Sensor

// Sight returns every entity within SightRadius of observer that is NOT occluded along the
// straight line observer→entity by EITHER (a) an [opaque] entity (binary IsOpaque) OR (b) flora
// shade whose composed light transmission along the segment falls below ShadeSightTau (the
// continuous "dark forest" cutoff, flora 1d). Result is sorted by Distance ASCENDING; ties
// broken by ascending ObjectID (D12). The observer's own entity (Distance == 0 at observer) is
// excluded from the result. With no ShadeOccluders this is EXACTLY the pre-shade boolean
// behavior (flora-off neutrality).
func (s *Sensor) Sight(observer core.Vec2, world WorldSnapshot) []PerceivedEntity

// Smell returns a ScentSignal for every `[scented]`-tagged entity within SmellRadius of observer,
// with Strength from the gradient formula (no LoS — smell goes around obstacles AND shade).
// Result is sorted by Strength DESCENDING; ties broken by ascending ObjectID (D12).
func (s *Sensor) Smell(observer core.Vec2, world WorldSnapshot) []ScentSignal

// Hearing filters the tick-scoped events slice to those within HearingRadius of observer (no LoS,
// no shade), filling each kept event's Distance (observer→Pos). The input slice is a per-tick
// collection the world supplies (NO persistent subscription); it is not retained. Result is
// sorted by Distance ASCENDING; ties broken by ascending SourceID (D12). The input slice is
// never mutated.
func (s *Sensor) Hearing(observer core.Vec2, events []SoundEvent) []SoundEvent
```

> `LoadConfig` takes an `io.Reader`, not a path — the engine performs **no filesystem IO**
> (architecture §1). `platform/config` opens `content/balance.yaml`, runs JSON-schema
> validation, and passes the reader/bytes here (same pattern as `engine/values.Load`).

### Formulas (authoritative)

These are the contract; implementations must match them to float tolerance so golden snapshots
are stable.

```
-- d = observer.Distance(entity.Pos)  (Euclidean, core.Vec2.Distance)

-- SIGHT: an entity at distance d is SEEN iff
sight(entity) = (d ≤ SightRadius)
            AND (no opaque entity intersects the segment observer→entity.Pos)        -- binary
            AND (shadeTransmission(segment) ≥ ShadeSightTau)                          -- continuous

   Opaque occlusion test: march integer steps along the segment observer→entity.Pos
   (Bresenham-style, stepping in world units rounded to the spatial-hash candidate set). At each
   step query the spatial hash for entities near the step point; if any such entity IsOpaque AND
   lies strictly BETWEEN observer and the target (0 < its projection along the segment < d) AND is
   not the target itself, the target is occluded. The target's own opacity never occludes itself.

   Shade transmission (NEW, flora 1d): over the ShadeOccluders near the segment, a caster
   ATTENUATES the segment iff its center's perpendicular distance to the segment is ≤ its Radius
   AND its projection lies strictly between observer and target (same between-test as opaque, and
   the target's own shade never occludes itself). The composed transmission is the MULTIPLICATIVE
   product over all attenuating casters:
       shadeTransmission(segment) = ∏ over attenuating casters c of (1 − c.Opacity)
   (empty product = 1.0 ⇒ no shade ⇒ no attenuation ⇒ pre-shade behavior, flora-off neutral).
   The target is shade-occluded iff shadeTransmission < ShadeSightTau. Two half-opaque casters
   (0.5 each) compose to 0.25 transmission — overlap darkens, the "dark forest" emerges (1d/§5).

-- SMELL: for a [scented] entity, base_strength is the source's emission magnitude (see Notes).
strength = base_strength / (1 + d²)      -- d in world units; d² via core.Vec2.DistSq (no sqrt)
   included iff d ≤ SmellRadius          -- shade does NOT affect smell

   At d = 0:  strength = base_strength / 1            = base_strength
   At d = 1:  strength = base_strength / (1 + 1)      = base_strength / 2
   At d = √3: strength = base_strength / (1 + 3)      = base_strength / 4

-- HEARING: a SoundEvent e is HEARD iff
hear(e) = observer.Distance(e.Pos) ≤ HearingRadius   -- no LoS, no shade; sound goes around both
   the kept event's Distance is set to observer.Distance(e.Pos)
```

## Dependencies

- `engine/core` — `Vec2` (`Distance`/`DistSq`), `ObjectID`, `Tag`. No IO.
- `engine/spatial` — `*spatial.SpatialHash` (via `spatial.New`) for proximity candidates and the
  LoS ray-march candidate set; the **only** source of position data (D11). Uses
  `NearbyEntities`/`NearbyIDs`/`PosOf` only.
- `engine/actions` — `actions.ActionID` (carried on `SoundEvent`; perception does not interpret it).
- `content/balance.yaml perception.*` — `sight_radius`, `smell_radius`, `hearing_radius`,
  `shade_sight_tau`, injected as an `io.Reader` by `platform/config` (D10). Schema:
  `content/schema/balance.schema.json`.
- **Contract only — NOT imported**: `engine/world` implements `WorldSnapshot` (dependency
  inversion); perception defines the interface and never imports `engine/world`. The world adapts
  `engine/flora.Shade`/`ShadeOf` into `ShadeOccluder` — perception **never imports `engine/flora`**
  (that would be an L3→L1-via-world cycle and break the pure-query boundary). The `[scented]`,
  `[opaque]`, `[loud]` tag grammar lives in `content/README.md §Tags`.

## Owned Data

- `Sensor`, `PerceptionConfig`, `PerceivedEntity`, `ScentSignal`, `SoundEvent`, `ShadeOccluder`,
  and the `WorldSnapshot` interface. The `Sensor` is immutable after `NewSensor` (read-only config
  + a borrowed `*spatial.SpatialHash` it does NOT own or mutate). All returned slices are fresh
  copies the caller may sort/retain freely; the input `events` slice to `Hearing` is never mutated.
  Tags on a `PerceivedEntity` are copies of the snapshot's tags (perception never mutates world
  state). `ShadeOccluder` values are copies of the world's flora-shade projection (perception never
  mutates flora state).

## Invariants

- **No LoS for smell or hearing (by design).** Only `Sight` performs occlusion (opaque AND shade).
  Smell and hearing reach around opaque obstacles AND flora shade — do not "fix" this by adding any
  occlusion check.
- **Shade attenuates Sight ONLY (flora 1d).** The continuous shade transmission test applies to
  `Sight`; it never touches `Smell`/`Hearing`. Shade is composed MULTIPLICATIVELY (∏(1−opacity)),
  never additively/`max` (RESOLVED flora 1d option c) — overlap darkens monotonically.
- **Flora-off / no-shade neutrality.** With an empty `ShadeOccluders` result, `Sight` reduces
  EXACTLY to the pre-shade boolean-opaque behavior (empty product = 1.0 ≥ any ShadeSightTau ≤ 1).
  Existing pre-flora `Sight` goldens hold until shade is activated (flora M-staging; re-baseline is
  a deliberate later phase).
- **Deterministic sorted output (D12).** Every modality returns a deterministically ordered slice:
  - `Sight` → by `Distance` **ascending**, ties broken by ascending `ObjectID`.
  - `Smell` → by `Strength` **descending**, ties broken by ascending `ObjectID`.
  - `Hearing` → by `Distance` **ascending**, ties broken by ascending `SourceID`.
  Logic never iterates a `map` for ordering; candidates and shade occluders from the spatial hash
  arrive already `ObjectID`-sorted, and the multiplicative shade product is over a sorted occluder
  set (float multiply order fixed). Identical inputs across two runs/processes yield byte-identical
  results.
- **Spatial hash is the only position source (D11).** Positions (entities AND shade occluders) come
  from `engine/spatial` (via the `Sensor`'s index and `WorldSnapshot.EntitiesInRadius` /
  `ShadeOccluders`, which the world backs with the same hash); perception never holds or derives a
  separate coordinate store, never tiles the world, and never rasterizes shade into a tile field.
- **No persistent state between ticks.** Each call is a pure snapshot query; `Sensor` holds only
  read-only config + the borrowed index. `Hearing` consumes a per-tick `events` slice with **no
  persistent subscription**; shade occluders are re-queried each tick (no cached shade field).
- **Read-only world view.** Perception never mutates the `WorldSnapshot`, the spatial hash, the
  input `events` slice, or the `ShadeOccluder` projection. It produces no `Event` (no `EventEmitter`).
- **No filesystem IO.** Imports no `os`/`net`/filesystem package; config arrives via an injected
  `io.Reader`; **no hardcoded paths or radii / tau** (D10).
- **Sense modeling only (D5 separation-style boundary).** This module says what is *perceivable*,
  never what the agent *wants* or *does* with a percept — no goals, no planning, no value appraisal.
- **Observer excluded from Sight.** An entity exactly at the observer position (the observer's own
  body) is not returned as a perceived entity; the observer's own shade never occludes itself.

## Acceptance Criteria (testable)

- [ ] **Sight occlusion (D11/LoS):** an entity behind an `[opaque]` entity on the straight line
  observer→target is **not** returned; the same target with the opaque entity removed (or moved off
  the segment) **is** returned. Table-driven over collinear / off-segment / target-is-the-opaque
  cases.
- [ ] **Sight shade attenuation (NEW, flora 1d):** a target behind flora shade whose composed
  transmission < `ShadeSightTau` is **not** returned; with `ShadeSightTau` lowered (or the occluder
  removed) the same target **is** returned. Two half-opacity casters on the segment compose to 0.25
  transmission (multiplicative); a single 0.5 caster composes to 0.5. An off-segment shade caster
  does not attenuate. Table-driven.
- [ ] **Sight flora-off neutrality (NEW):** with `ShadeOccluders` returning empty, `Sight` results
  are byte-identical to the pre-shade boolean-opaque behavior for the same scene (regression guard;
  the shade extension is outcome-neutral until activated).
- [ ] **Sight radius bound:** an entity exactly at `SightRadius` is returned; one just beyond
  (`d > SightRadius`) is not. The observer's own entity (d == 0) is excluded.
- [ ] **Sight ordering (D12):** results are sorted by `Distance` ascending, ties by `ObjectID`;
  inserting candidates (and shade occluders) in shuffled order yields the same order and the same
  composed transmission.
- [ ] **Smell needs no LoS and ignores shade:** a `[scented]` entity fully behind an `[opaque]` wall
  AND inside dense flora shade is still returned (asserts smell ignores both occlusion mechanisms).
- [ ] **Smell formula:** `strength` equals `base_strength/(1+d²)` at `d = 0` (= base), `d = 1`
  (= base/2), `d = √3` (= base/4) to float tolerance; an entity beyond `SmellRadius` is excluded;
  a non-`[scented]` entity in range is excluded.
- [ ] **Smell ordering (D12):** results are sorted by `Strength` descending, ties by `ObjectID`.
- [ ] **Hearing range:** a `[loud]`-action `SoundEvent` with `d ≤ HearingRadius` is returned with
  `Distance` filled; one with `d > HearingRadius` is not. No LoS / no shade check (an event behind an
  opaque wall and inside shade is still heard).
- [ ] **Hearing ordering (D12):** results are sorted by `Distance` ascending, ties by `SourceID`;
  shuffled input yields the same order. The input slice is unmodified after the call.
- [ ] **Config load:** `LoadConfig` parses `sight_radius`/`smell_radius`/`hearing_radius`/
  `shade_sight_tau` from a `perception:` block in injected YAML bytes; a document with the
  `perception:` block **missing** returns a descriptive error naming the missing block; a radius ≤ 0
  or a `shade_sight_tau` outside `[0,1]` returns a descriptive error naming the offending field.
- [ ] **No persistent state:** calling `Hearing` with a non-empty slice then with an empty slice
  returns empty (no events remembered); two `Sensor` instances built from identical inputs produce
  identical results for the same query (stateless determinism).
- [ ] **Golden snapshot (docs/testing.md):** a fixed scenario exercising all modalities (one
  opaque-occluded sight target, one shade-occluded sight target, two scent sources at different
  distances, two sound events one in/one out of range) produces a stable, sorted golden snapshot
  across runs/processes. First established **shade-off** (neutral), then re-baselined on activation.

> Structural JSON-schema validation of `content/balance.yaml` against
> `content/schema/balance.schema.json` is a **platform/config** AC (it owns the file IO + schema).
> This module proves only the semantic checks reachable from the injected reader.

## Out of Scope

- Position storage, radius-candidate gathering, bucketing → `engine/spatial` (this module consumes
  `*spatial.SpatialHash`; it stores no coordinates of its own, D11).
- Emitting / collecting `[loud]` sound events into the per-tick slice, implementing `WorldSnapshot`
  (which entity has which tag/opacity this tick), and **adapting `engine/flora.Shade`/`ShadeOf` into
  the `ShadeOccluder` set** → `engine/world` apply/tick phase (this module only filters/queries the
  supplied view). The flora shade PARAMETER (`Radius`/`Opacity` = §6(growth)) is computed in
  `engine/flora` (`backend/engine/flora/SPEC.md`); world projects it; perception composes the LoS
  attenuation.
- Deciding what to do with a percept — attention, fear, goal raising, gossip → `engine/agent` /
  `engine/values` / `engine/tom` (perception only reports what is perceivable, not its meaning).
- Action tag semantics (what `[loud]`/`[scented]`/`[opaque]` mean elsewhere) and tag-derived cost →
  `engine/actions` / `engine/gates` / `engine/planner`; perception treats tags as opaque strings.
- Per-stat acuity (sharper sight from high `Agility`, etc.) — **not in P1**; see Open Questions.
- Day/night coupling of shade (shade only matters by day, RESOLVED flora 1d) — **parked**; P1 shade
  is time-independent. Night base-sight reduction is a `worldtime`/`perception` concern handled
  separately, not by the shade transmission test.

## Open Questions

- **Naming drift (resolved here, flag if it recurs):** the architect brief named the index type
  `spatial.Index` and the candidate query `EntitiesInRadius`. The frozen `engine/spatial/SPEC.md`
  and `docs/glossary.md §World & time` canonicalize the type as **`SpatialHash`** (constructed via
  `spatial.New`) with `NearbyEntities`. This SPEC uses the **glossary/sibling-contract** names —
  `*spatial.SpatialHash` for `NewSensor`, and `WorldSnapshot.EntitiesInRadius`/`ShadeOccluders` as
  the *world's* read-only adapters (which internally call `NearbyEntities`). No glossary identifier
  is introduced outside the vocabulary. Does **not** block P1.
- **Shade occluder interface name + composition site (P_f3 ESCALATION — perception/world/flora
  contract).** I coined **`ShadeOccluder`** + **`WorldSnapshot.ShadeOccluders`** here (glossary-
  conformant uses of the new `shade` term, registered this batch). The alternative is
  `WorldSnapshot.LightTransmission(segment) float64` (world/flora composes; perception just
  thresholds). Recommendation: **keep `ShadeOccluders`** — composition stays in perception (it owns
  LoS), mirroring the `IsOpaque`/`EntitiesInRadius` split. **Flag to human/architect before P_f3.**
- **Continuous visibility STRENGTH vs boolean threshold (P_f3 ESCALATION — perception semantics).**
  Shade is continuous, but `Sight` currently returns a boolean visible set. P1 thresholds the
  composed transmission at `ShadeSightTau` (boolean contract preserved, smallest blast radius). A
  later phase could return a per-target visibility STRENGTH (e.g. for fear/attention scaling), which
  re-baselines perception goldens and changes the `Sight` return type. Recommendation: **threshold
  for P_f3**; revisit continuous strength as a frontier. **Flag to human before P_f3.**
- **`base_strength` source (NOT blocking P1).** The smell gradient needs each `[scented]` source's
  emission magnitude. P1 assumption: `base_strength` is a fixed per-source constant the
  `WorldSnapshot` exposes via the entity's content (e.g. an objects.yaml `scent_strength`, or a
  uniform `1.0` if absent). If a `scent_strength` field is added to `content/objects.yaml`, surface
  it on `WorldSnapshot` (e.g. `ScentStrength(id) float64`) and update the formula's `base_strength`
  binding here — flag to architect before any feature that needs per-source scent magnitudes.
- **Sense acuity by stat (NOT P1).** D7 (no individual skills; competence = base attributes)
  implies sight/smell/hearing acuity could scale with `Agility`/`Intelligence`. P1 uses flat radii
  from `balance.yaml`. If acuity scaling is wanted, the per-agent acuity multiplier is computed in
  `engine/agent` (which holds `Stats`) and passed in as an effective radius — perception stays
  stat-agnostic. Recorded so the `agent` author knows where acuity belongs.

## Notes

- **Position vs attributes split.** The spatial hash (`engine/spatial`) stores only `(ID, Pos)`. It
  does **not** store tags, opacity, or shade. `WorldSnapshot` supplies those per-entity attributes
  for the current tick (`Tags`, `IsOpaque`, `ShadeOccluders`). `Sight` gathers candidates via
  `EntitiesInRadius` (hash-backed, already `ObjectID`-sorted), then asks `IsOpaque` (binary walls)
  and composes `ShadeOccluders` (continuous flora shade) along the segment — keeping a single
  position source (D11) while letting the world own attribute lookup.
- **Two occlusion mechanisms, one segment.** `IsOpaque` is the hard binary occluder (walls, bodies);
  flora shade is the soft continuous one ("dark forest"). They are tested on the same segment in one
  pass: a target is seen iff it passes the radius bound, the binary opaque test, AND the
  shade-transmission threshold. Keep them separate so a wall (full block) and a tree canopy (partial
  attenuation) compose correctly and goldens stay legible.
- **`DistSq` on the hot path.** Use `core.Vec2.DistSq` for the radius cutoff and the smell `d²`
  term; only call `Distance` (sqrt) once per kept percept to fill `PerceivedEntity.Distance` /
  `SoundEvent.Distance` and for `Sight`'s distance-sort key. The spatial hash already filters to an
  exact `DistSq ≤ r²` set, so the radius pre-filter is essentially free. The shade perpendicular-
  distance test uses squared distances too (compare to `Radius²`).
- **Bresenham occlusion is a candidate pre-filter, not a pixel raster (D11).** The world is not
  tiled, so the integer step march is over the spatial-hash candidate disc, not a grid. The exact
  occlusion test is geometric: an opaque entity (or shade caster) occludes/attenuates iff its
  perpendicular distance to the segment is within its blocking/shade extent and its projection lies
  strictly between observer and target. The "Bresenham-style integer steps" only bound which hash
  candidates to test; keep the final include/exclude (and the shade transmission product) on the
  geometric segment test so results are tiling-free and stable.
- **Tag grammar** (`[opaque]`, `[scented]`, `[loud]`, `noise:*`) is documented in
  `content/README.md §Tags`; this module treats them as opaque `core.Tag` strings and never parses a
  family. `[loud]` is an **action** tag (the emitter is set when the world builds the per-tick
  `[]SoundEvent` from acting agents); `[opaque]`/`[scented]` are **entity** tags resolved via
  `WorldSnapshot`. Flora shade is NOT a tag — it is the continuous `ShadeOccluder` projection
  (Radius/Opacity = §6(growth)), distinct from the binary `[opaque]` tag. The `hearing_radius` in
  `balance.yaml` is a *base*; per the comment there it may later be scaled by an action's `noise:*`
  level — that scaling, if added, multiplies the radius in `engine/world` when building the event or
  in `engine/agent`, not here (perception uses the base).
- Perception holds **no `EventEmitter`** and emits no why-trace; observability of *what an agent
  perceived* (if needed) is an `engine/agent` concern. This module is a pure query layer.
- Snapshot/serialization: perception output is **derived, per-tick, never persisted**
  (data-contracts carries no percept cache); it is recomputed each tick from positions + the world
  view (including shade, which is itself derived from flora `Pos`+`Growth`), mirroring how
  `engine/spatial` treats its index as rebuildable derived state.
