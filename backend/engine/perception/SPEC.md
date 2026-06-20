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
**modeling** only — falloff math and LoS occlusion — and does **not** decide what the observer
does with a percept (that is `engine/agent`).

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
}

// LoadConfig parses ONLY the top-level `perception:` block from balanceDoc (the bytes of
// content/balance.yaml — the path is injected by platform/config, NEVER a file path here,
// keeping the engine IO-free, D10). It returns a descriptive error if the `perception:` block
// is absent or any radius is missing / ≤ 0. STRUCTURAL JSON-schema validation
// (content/schema/balance.schema.json) is platform/config's job, run before this call.
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

// WorldSnapshot is a read-only view over entity positions, tags, and opacity for the CURRENT
// tick. The world (engine/world) implements it; perception only reads it (dependency inversion —
// perception does not import engine/world). EntitiesInRadius is the proximity candidate source,
// backed by the spatial hash (D11). Tags / IsOpaque resolve per-entity attributes the spatial
// hash does not store. Implementations MUST return EntitiesInRadius sorted by ascending ObjectID
// (the spatial-hash contract, engine/spatial/SPEC.md) so this module's re-sorts are stable.
type WorldSnapshot interface {
    EntitiesInRadius(center core.Vec2, radius float64) []PerceivedEntity
    Tags(id core.ObjectID) []core.Tag
    IsOpaque(id core.ObjectID) bool
}

// Sensor evaluates the three senses against a WorldSnapshot. It is created once (per agent or
// shared) with the spatial index + config and reused across ticks; it holds NO per-tick state.
type Sensor struct{ /* opaque: *spatial.SpatialHash + PerceptionConfig (read-only) */ }

// NewSensor builds a Sensor from the spatial index and config. The index is the canonical
// position source (D11) used to enumerate sound emitters / sight candidates that share the
// world's tick snapshot. Panics if idx is nil.
func NewSensor(idx *spatial.SpatialHash, cfg PerceptionConfig) *Sensor

// Sight returns every entity within SightRadius of observer that is NOT occluded by an opaque
// entity on the straight line observer→entity. Result is sorted by Distance ASCENDING; ties
// broken by ascending ObjectID (D12). The observer's own entity (Distance == 0 at observer) is
// excluded from the result.
func (s *Sensor) Sight(observer core.Vec2, world WorldSnapshot) []PerceivedEntity

// Smell returns a ScentSignal for every `[scented]`-tagged entity within SmellRadius of observer,
// with Strength from the gradient formula (no LoS — smell goes around obstacles). Result is
// sorted by Strength DESCENDING; ties broken by ascending ObjectID (D12).
func (s *Sensor) Smell(observer core.Vec2, world WorldSnapshot) []ScentSignal

// Hearing filters the tick-scoped events slice to those within HearingRadius of observer (no LoS),
// filling each kept event's Distance (observer→Pos). The input slice is a per-tick collection the
// world supplies (NO persistent subscription); it is not retained. Result is sorted by Distance
// ASCENDING; ties broken by ascending SourceID (D12). The input slice is never mutated.
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
sight(entity) = (d ≤ SightRadius) AND (no opaque entity intersects the segment observer→entity.Pos)

   Occlusion test: march integer steps along the segment observer→entity.Pos (Bresenham-style,
   stepping in world units rounded to the spatial-hash candidate set). At each step query the
   spatial hash for entities near the step point; if any such entity IsOpaque AND lies strictly
   BETWEEN observer and the target (0 < its projection along the segment < d) AND is not the
   target itself, the target is occluded. The target's own opacity never occludes itself.

-- SMELL: for a [scented] entity, base_strength is the source's emission magnitude (see Notes).
strength = base_strength / (1 + d²)      -- d in world units; d² via core.Vec2.DistSq (no sqrt)
   included iff d ≤ SmellRadius

   At d = 0:  strength = base_strength / 1            = base_strength
   At d = 1:  strength = base_strength / (1 + 1)      = base_strength / 2
   At d = √3: strength = base_strength / (1 + 3)      = base_strength / 4

-- HEARING: a SoundEvent e is HEARD iff
hear(e) = observer.Distance(e.Pos) ≤ HearingRadius          -- no LoS; sound goes around obstacles
   the kept event's Distance is set to observer.Distance(e.Pos)
```

## Dependencies

- `engine/core` — `Vec2` (`Distance`/`DistSq`), `ObjectID`, `Tag`. No IO.
- `engine/spatial` — `*spatial.SpatialHash` (via `spatial.New`) for proximity candidates and the
  LoS ray-march candidate set; the **only** source of position data (D11). Uses
  `NearbyEntities`/`NearbyIDs`/`PosOf` only.
- `engine/actions` — `actions.ActionID` (carried on `SoundEvent`; perception does not interpret it).
- `content/balance.yaml perception.*` — `sight_radius`, `smell_radius`, `hearing_radius`, injected
  as an `io.Reader` by `platform/config` (D10). Schema: `content/schema/balance.schema.json`.
- **Contract only — NOT imported**: `engine/world` implements `WorldSnapshot` (dependency
  inversion); perception defines the interface and never imports `engine/world` (that would be an
  L3→L6 cycle). The `[scented]`, `[opaque]`, `[loud]` tag grammar lives in `content/README.md §Tags`.

## Owned Data

- `Sensor`, `PerceptionConfig`, `PerceivedEntity`, `ScentSignal`, `SoundEvent`, and the
  `WorldSnapshot` interface. The `Sensor` is immutable after `NewSensor` (read-only config + a
  borrowed `*spatial.SpatialHash` it does NOT own or mutate). All returned slices are fresh copies
  the caller may sort/retain freely; the input `events` slice to `Hearing` is never mutated. Tags
  on a `PerceivedEntity` are copies of the snapshot's tags (perception never mutates world state).

## Invariants

- **No LoS for smell or hearing (by design).** Only `Sight` performs the occlusion test. Smell and
  hearing reach around opaque obstacles — do not "fix" this by adding an LoS check.
- **Deterministic sorted output (D12).** Every modality returns a deterministically ordered slice:
  - `Sight` → by `Distance` **ascending**, ties broken by ascending `ObjectID`.
  - `Smell` → by `Strength` **descending**, ties broken by ascending `ObjectID`.
  - `Hearing` → by `Distance` **ascending**, ties broken by ascending `SourceID`.
  Logic never iterates a `map` for ordering; candidates from the spatial hash arrive already
  `ObjectID`-sorted and are re-sorted by the per-modality key above. Identical inputs across two
  runs/processes yield byte-identical results.
- **Spatial hash is the only position source (D11).** Positions come from `engine/spatial` (via the
  `Sensor`'s index and `WorldSnapshot.EntitiesInRadius`, which the world backs with the same hash);
  perception never holds or derives a separate coordinate store, and never tiles the world.
- **No persistent state between ticks.** Each call is a pure snapshot query; `Sensor` holds only
  read-only config + the borrowed index. `Hearing` consumes a per-tick `events` slice with **no
  persistent subscription** — events are not buffered, queued, or remembered across ticks.
- **Read-only world view.** Perception never mutates the `WorldSnapshot`, the spatial hash, or the
  input `events` slice. It produces no `Event` (no `EventEmitter`).
- **No filesystem IO.** Imports no `os`/`net`/filesystem package; config arrives via an injected
  `io.Reader`; **no hardcoded paths or radii** (D10).
- **Sense modeling only (D5 separation-style boundary).** This module says what is *perceivable*,
  never what the agent *wants* or *does* with a percept — no goals, no planning, no value appraisal.
- **Observer excluded from Sight.** An entity exactly at the observer position (the observer's own
  body) is not returned as a perceived entity.

## Acceptance Criteria (testable)

- [ ] **Sight occlusion (D11/LoS):** an entity behind an `[opaque]` entity on the straight line
  observer→target is **not** returned; the same target with the opaque entity removed (or moved off
  the segment) **is** returned. Table-driven over collinear / off-segment / target-is-the-opaque
  cases.
- [ ] **Sight radius bound:** an entity exactly at `SightRadius` is returned; one just beyond
  (`d > SightRadius`) is not. The observer's own entity (d == 0) is excluded.
- [ ] **Sight ordering (D12):** results are sorted by `Distance` ascending, ties by `ObjectID`;
  inserting candidates in shuffled order yields the same order.
- [ ] **Smell needs no LoS:** a `[scented]` entity fully behind an `[opaque]` wall is still returned
  (asserts smell ignores occlusion).
- [ ] **Smell formula:** `strength` equals `base_strength/(1+d²)` at `d = 0` (= base), `d = 1`
  (= base/2), `d = √3` (= base/4) to float tolerance; an entity beyond `SmellRadius` is excluded;
  a non-`[scented]` entity in range is excluded.
- [ ] **Smell ordering (D12):** results are sorted by `Strength` descending, ties by `ObjectID`.
- [ ] **Hearing range:** a `[loud]`-action `SoundEvent` with `d ≤ HearingRadius` is returned with
  `Distance` filled; one with `d > HearingRadius` is not. No LoS check (an event behind an opaque
  wall is still heard).
- [ ] **Hearing ordering (D12):** results are sorted by `Distance` ascending, ties by `SourceID`;
  shuffled input yields the same order. The input slice is unmodified after the call.
- [ ] **Config load:** `LoadConfig` parses `sight_radius`/`smell_radius`/`hearing_radius` from a
  `perception:` block in injected YAML bytes; a document with the `perception:` block **missing**
  returns a descriptive error naming the missing block; a radius ≤ 0 returns a descriptive error
  naming the offending field.
- [ ] **No persistent state:** calling `Hearing` with a non-empty slice then with an empty slice
  returns empty (no events remembered); two `Sensor` instances built from identical inputs produce
  identical results for the same query (stateless determinism).
- [ ] **Golden snapshot (docs/testing.md):** a fixed 3-agent scenario exercising all three
  modalities (one occluded sight target, two scent sources at different distances, two sound events
  one in/one out of range) produces a stable, sorted golden snapshot across runs/processes.

> Structural JSON-schema validation of `content/balance.yaml` against
> `content/schema/balance.schema.json` is a **platform/config** AC (it owns the file IO + schema).
> This module proves only the semantic checks reachable from the injected reader.

## Out of Scope

- Position storage, radius-candidate gathering, bucketing → `engine/spatial` (this module consumes
  `*spatial.SpatialHash`; it stores no coordinates of its own, D11).
- Emitting / collecting `[loud]` sound events into the per-tick slice, and implementing
  `WorldSnapshot` (which entity has which tag/opacity this tick) → `engine/world` apply/tick phase
  (this module only filters/queries the supplied view).
- Deciding what to do with a percept — attention, fear, goal raising, gossip → `engine/agent` /
  `engine/values` / `engine/tom` (perception only reports what is perceivable, not its meaning).
- Action tag semantics (what `[loud]`/`[scented]`/`[opaque]` mean elsewhere) and tag-derived cost →
  `engine/actions` / `engine/gates` / `engine/planner`; perception treats tags as opaque strings.
- Per-stat acuity (sharper sight from high `Agility`, etc.) — **not in P1**; see Open Questions.

## Open Questions

- **Naming drift (resolved here, flag if it recurs):** the architect brief named the index type
  `spatial.Index` and the candidate query `EntitiesInRadius`. The frozen `engine/spatial/SPEC.md`
  and `docs/glossary.md §World & time` canonicalize the type as **`SpatialHash`** (constructed via
  `spatial.New`) with `NearbyEntities`. This SPEC uses the **glossary/sibling-contract** names —
  `*spatial.SpatialHash` for `NewSensor`, and `WorldSnapshot.EntitiesInRadius` as the *world's*
  read-only adapter (which internally calls `NearbyEntities`). No glossary identifier is introduced
  outside the vocabulary. Does **not** block P1.
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
  does **not** store tags or opacity. `WorldSnapshot` supplies those per-entity attributes for the
  current tick (`Tags`, `IsOpaque`). `Sight` gathers candidates via `EntitiesInRadius` (hash-backed,
  already `ObjectID`-sorted), then asks `IsOpaque` for the occluders along the segment — keeping a
  single position source (D11) while letting the world own attribute lookup.
- **`DistSq` on the hot path.** Use `core.Vec2.DistSq` for the radius cutoff and the smell `d²`
  term; only call `Distance` (sqrt) once per kept percept to fill `PerceivedEntity.Distance` /
  `SoundEvent.Distance` and for `Sight`'s distance-sort key. The spatial hash already filters to an
  exact `DistSq ≤ r²` set, so the radius pre-filter is essentially free.
- **Bresenham occlusion is a candidate pre-filter, not a pixel raster (D11).** The world is not
  tiled, so the integer step march is over the spatial-hash candidate disc, not a grid. The exact
  occlusion test is geometric: an opaque entity occludes iff its perpendicular distance to the
  segment is within its blocking extent and its projection lies strictly between observer and
  target. The "Bresenham-style integer steps" only bound which hash candidates to test; keep the
  final include/exclude decision on the geometric segment test so results are tiling-free and stable.
- **Tag grammar** (`[opaque]`, `[scented]`, `[loud]`, `noise:*`) is documented in
  `content/README.md §Tags`; this module treats them as opaque `core.Tag` strings and never parses a
  family. `[loud]` is an **action** tag (the emitter is set when the world builds the per-tick
  `[]SoundEvent` from acting agents); `[opaque]`/`[scented]` are **entity** tags resolved via
  `WorldSnapshot`. The `hearing_radius` in `balance.yaml` is a *base*; per the comment there it may
  later be scaled by an action's `noise:*` level — that scaling, if added, multiplies the radius in
  `engine/world` when building the event or in `engine/agent`, not here (perception uses the base).
- Perception holds **no `EventEmitter`** and emits no why-trace; observability of *what an agent
  perceived* (if needed) is an `engine/agent` concern. This module is a pure query layer.
- Snapshot/serialization: perception output is **derived, per-tick, never persisted**
  (data-contracts carries no percept cache); it is recomputed each tick from positions + the world
  view, mirroring how `engine/spatial` treats its index as rebuildable derived state.
