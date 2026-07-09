# SPEC — `engine/env/exposure`

> Status: `READY` (SH1 — Q-W1..Q-W7 + SH1 SPEC-design RESOLVED in `docs/shelter.md`; shadow ε formula pinned below)
> Leaf level: `L1`  ·  Owner agent: `<filled by implementer>`
> Scope = **SH1** (`docs/shelter.md`): exposure field + local-wind attenuation.

## Purpose

The **shelter/exposure field**: a pure deterministic transform that derives a per-navigation-cell
exposure factor `epsilon ∈ [0,1]` from `blocks_wind` blockers and the current wind sector. `epsilon`
attenuates the world-uniform forcing that `engine/env/climate` already produces:

```
local wind magnitude = climate.Wind.Mag * epsilon(cell)
```

This is the shared foundation for **wind shadows**, later rain/temperature shelter, and caves. The
module does not know about "wall", "house", "mountain", or "cave" kinds. Blockers and forced interior
cells are passed in by `engine/world` from content tags and portal/interior state (D4/D10). It does
not import `navmap`, `climate`, `scent`, `fauna`, or `world`; world adapts those modules into this
pure leaf.

Concept authority: `docs/shelter.md`. Q-W1..Q-W7 are RESOLVED there.

## Public Interface

```go
package exposure

import "github.com/dogring/bdg/engine/kernel/core"

// Cell is the exposure grid index. For SH1 it carries the same axial coordinates as navmap.Cell,
// but exposure does not import navmap. world is the adapter between navmap.Cell and exposure.Cell.
// It is an index only, never an entity position (D11).
type Cell struct{ Q, R int }

// Wind is an injected local-forcing value. Dir is radians, Mag is normalized [0,1].
// It intentionally mirrors climate.Wind / scent.Wind without importing either package.
type Wind struct {
    Dir float64
    Mag float64
}

// Sector is the quantized wind direction. SH1 uses 6 sectors to match the flat-top hex neighbor
// fan and cache one epsilon field per sector (docs/shelter.md Q-W4).
type Sector uint8

const NumSectors = 6

// SectorOf maps wind.Dir to one of six deterministic 60° bins:
//   sector = floor(wrap(w.Dir) / (pi/3))   with w.Dir wrapped into [0, 2*pi)
// An exact 60°-multiple boundary (wind aligned to a hex axis) resolves UP into its bin — the impl
// adds a tiny epsilon so float rounding of the bin width cannot drop e.g. pi from bin 3 to bin 2.
// wind.Mag does not affect the sector. This 6-bin convention is the shared contract with Topology:
// Neighbors(c)[sector] is the downwind neighbor for that sector (see Topology).
func SectorOf(w Wind) Sector

// Topology is the minimal hex-graph adapter exposure needs (navmap remains the geometry authority).
// exposure is purely combinatorial over the hex graph — it needs NO positions/geometry, only:
//   - Neighbors(c)[s] = c's neighbor in the direction of wind Sector s. The array is in SECTOR
//     order matching SectorOf's 6 bins; out-of-bounds entries are allowed and filtered by InBounds
//     during the shadow walk. world builds this from navmap.Neighbors, ordered (via navmap.CellCenter
//     angles) to match SectorOf so the world adapter — not this leaf — owns the geometry alignment.
//   - InBounds(c) reports whether c is a real map cell (world delegates to a public navmap InBounds;
//     see the world sub-spec integration delta — navmap must promote its private inBounds).
// No Cells()/CellCenter(): the sparse Field defaults untouched cells to epsilon 1, and Build never
// enumerates all cells or reads positions, so requiring them would force an unused navmap enumerator.
type Topology interface {
    Neighbors(c Cell) [6]Cell
    InBounds(c Cell) bool
}

// Blocker is one wind-shadow caster derived from content tags such as `blocks_wind`.
// Height controls shadow length; Opacity controls how much epsilon is reduced.
// Footprint is the occupied cells, D12-sorted by world before construction.
type Blocker struct {
    ID        core.ObjectID
    Footprint []Cell
    Height    float64 // world units; <=0 casts no shadow
    Opacity   float64 // clamped [0,1]; 1 = strongest blocker
}

// Interior is a set of cells whose exposure is forced by an enclosing shelter/cave/house.
// SH2 cave interiors use Epsilon=0. Other interiors may choose a partial value later.
type Interior struct {
    ID      core.ObjectID
    Cells   []Cell
    Epsilon float64 // clamped [0,1]
}

// Config carries balance coefficients from content/world.yaml or shelter content.
// No geometry/rate literals are hardcoded in logic (D10).
type Config struct {
    ShadowCellsPerHeight float64 // shadow length = ceil(Height * ShadowCellsPerHeight)
    ShadowFalloff        float64 // per-cell attenuation recovery; >=0
    MinEpsilon           float64 // lower clamp, normally 0
}

// Field is an immutable exposure snapshot for one wind sector. It holds only the sparse set of
// cells whose epsilon != 1; any cell with no stored delta reads as epsilon 1 (the Field carries no
// Topology, so it does not itself bounds-check — callers query cells they know are in-bounds).
type Field struct{ /* opaque */ }

// Build computes the epsilon field for a given wind sector from blockers + interiors, per the
// Shadow Model below. It is pure and deterministic. Each blocker's downwind shadow contributes a
// per-cell attenuation; contributions COMBINE MULTIPLICATIVELY (epsilon *= (1 - atten)), which is
// order-independent — so the sorted iteration (Blocker.ID, footprint cell, downwind distance) is a
// determinism belt-and-suspenders, not load-bearing for the result. interiors are applied last
// (sorted by ID), OVERWRITING epsilon to Interior.Epsilon so an interior forces its value even
// where no blocker shadows it. For SH1 world calls this with interiors == nil (caves land in SH2).
func Build(cfg Config, topo Topology, sector Sector, blockers []Blocker, interiors []Interior) *Field

// Epsilon returns the local exposure factor for c. Out-of-bounds returns 1; callers normally bound-check.
func (f *Field) Epsilon(c Cell) float64

// LocalWind attenuates only wind magnitude. Dir is preserved; Mag is clamped to [0,1].
func (f *Field) LocalWind(c Cell, global Wind) Wind

// Active returns sparse cells whose epsilon != 1, D12-sorted, for debug/render/persist deltas.
func (f *Field) Active() []CellExposure
type CellExposure struct {
    Cell    Cell
    Epsilon float64
}

// Cache stores up to six sector fields. world owns invalidation when blockers/interiors change.
type Cache struct{ /* opaque */ }

func NewCache(cfg Config, topo Topology) *Cache
func (c *Cache) Invalidate()
func (c *Cache) Field(sector Sector, blockers []Blocker, interiors []Interior) *Field

// ── Overhead cover (SH3) ─────────────────────────────────────────────────────
// Rain falls from above, so overhead cover is a SEPARATE, direction-independent field: a Coverer
// lowers ε_cover only on the cells it directly overlies (isotropic, footprint-local — no wind sector,
// no leeward propagation, so no Topology and no per-sector cache). Distinct from the wind Field.

// Coverer is one overhead-cover caster derived from a `covers` tag (Q-S4).
type Coverer struct {
    ID        core.ObjectID
    Footprint []Cell
    Coverage  float64 // clamped [0,1]; 0 = no cover, 1 = full overhead cover (ε_cover → MinEpsilon)
}

// CoverField is an immutable overhead-exposure snapshot (sparse; covered-free cells read ε_cover 1).
type CoverField struct{ /* opaque */ }

// BuildCover computes ε_cover from coverers: a covered cell's ε_cover = ∏(1 − Coverage) over the
// coverers overlying it (order-free; sorted for D12), clamped to MinEpsilon. Coverers only for SH3;
// caves (SH2) will force ε_cover 0 via interiors.
func BuildCover(cfg Config, coverers []Coverer) *CoverField
func (f *CoverField) Epsilon(c Cell) float64      // 1 for any covered-free cell / nil field
func (f *CoverField) Active() []CellExposure       // sparse covered cells, D12-sorted
```

## Shadow Model (SH1) — the exact ε computation

Q-W3 resolved *directional leeward decay*; this pins the formula so the implementer derives, not
invents (CLAUDE.md gate). All quantities are cell-indexed; positions never appear (D11).

For a wind `Sector s`, the **downwind step** is `Neighbors(c)[s]` — index `s` of the fixed 6-neighbor
fan, so the neighbor order MUST align with `SectorOf` (Topology contract).

Per blocker `b` and each of its footprint cells `f`:
1. **Shadow length** `L = ceil(b.Height * cfg.ShadowCellsPerHeight)` cells (0 if `Height <= 0` → no shadow).
2. **Walk downwind** `d = 1..L`, stepping `cell = Neighbors(cell)[s]` from `f` each step, stopping if
   `!InBounds(cell)`.
3. **Attenuation at distance d**: `atten(d) = b.Opacity * max(0, 1 - (d-1)*cfg.ShadowFalloff)`
   — full `Opacity` in the cell immediately leeward (`d=1`), decaying linearly by `ShadowFalloff` per
   cell; `ShadowFalloff = 0` gives a flat shadow of length `L`. Once `atten` hits 0 the walk may stop early.
4. **Accumulate multiplicatively**: `raw[cell] *= (1 - atten(d))`, starting from `raw[cell] = 1`.

After all blockers: `ε(cell) = clamp(raw[cell], cfg.MinEpsilon, 1)`. Interiors then OVERWRITE (not
multiply) their cells to `Interior.Epsilon`. A cell touched by no shadow/interior stays `ε = 1`.

Worked check (AC "directional shadow"): `Opacity=1, ShadowFalloff=0.5, ShadowCellsPerHeight=1,
Height=3 → L=3`; downwind cells get `atten = 1, 0.5, 0 → ε = 0, 0.5, 1`; upwind/side cells stay `1`.

Two blockers shadowing the same cell with `atten 0.5` and `0.5` give `ε = (1-0.5)(1-0.5) = 0.25`,
independent of which blocker is processed first (multiplicative ⇒ order-free, D12).

## Dependencies

- `engine/kernel/core` — `ObjectID` only (`Cell` is axial `Q,R` ints; this leaf holds no positions).
- *(NOT `engine/space/navmap`)* — world adapts navmap cells/topology to exposure cells so exposure
  remains a pure leaf and navmap stays the hex geometry authority.
- *(NOT `engine/env/climate`)* — `Wind` is an injected value; climate remains world-uniform and
  unchanged.
- *(NOT `engine/space/scent` / `engine/fauna`)* — they consume the world-adapted local wind.

## Owned Data

- `Field`: sparse per-cell epsilon deltas away from `1.0` for one sector.
- `Cache`: memoized `Field` per 6-sector wind direction. It owns no blocker state; invalidation is
  explicit and world-driven.

## Invariants

- **D11** — cells are indices only. All entity positions remain continuous.
- **D12** — no map iteration drives logic. Blockers, footprints, interiors, and `Active()` output are
  sorted before use/output; `Topology.Neighbors` is fixed sector order. Because shadow contributions
  combine multiplicatively (order-free), the result is independent of iteration order regardless.
- **D4/D10** — what blocks wind is data/tag-derived by world/config. This package branches on
  `Blocker` values only, never kind names.
- **Climate untouched** — global wind generation remains in `engine/env/climate`.
- **Outcome-neutral until installed** — absent blockers/interiors yield epsilon `1` everywhere and
  local wind equals global wind.

## Acceptance Criteria

- [ ] **Empty-field neutrality** — `Build` with no blockers/interiors returns epsilon `1` for any
  queried cell; `LocalWind` equals the input wind; `Active()` is empty.
- [ ] **Directional shadow** — a blocker with height/opacity reduces epsilon only in the downwind
  sector cells; upwind cells remain `1`. Shadow length follows `ShadowCellsPerHeight`.
- [ ] **Falloff and clamps** — epsilon reduction weakens with distance, never drops below
  `MinEpsilon`, and never exceeds `1`.
- [ ] **Interior override** — an `Interior{Epsilon:0}` forces its cells to `0` even when no blocker
  shadows them; interiors apply after blockers.
- [ ] **Sector cache** — two calls for the same sector without `Invalidate` return byte-identical
  fields without recomputation; after `Invalidate`, changed blockers affect the result.
- [ ] **D12 determinism golden** — shuffled input blocker/interior order produces the same `Active()`
  ordering and values; two runs/processes reproduce a fixed digest.
- [ ] **No forbidden imports** — parse imports and reject `navmap`, `climate`, `scent`, `fauna`,
  `world`, `time`, and `rng`.

## Out of Scope

- Building the blocker list from object/terrain tags → `engine/world` + `platform/config`.
- Driving cadence and cache invalidation → `engine/world`.
- Scent spread, fauna thermal comfort, apparent temperature → existing consumers after world injects
  local wind/env values.
- Cave portal/interior lifecycle → `engine/world/SPEC-world-shelter.md`.
- Rain/temperature attenuation → SH3.

## Open Questions

None for SH1. `docs/shelter.md` resolves Q-W1..Q-W7. Balance coefficients are data tuning, not
mechanism questions.
