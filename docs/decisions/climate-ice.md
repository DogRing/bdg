# Ice: water→ice terrain freezing (Why)

> Deliberation cut from the living docs per the Documentation Triad. Pointer lives in
> `docs/plans/climate.md §1e` (ICE1–ICE6). Resolved 2026-07-14 (human RESOLVE via AskUserQuestion).
> A follow-on to the winter-snow work (`[[climate-winter-snow]]`, `docs/plans/climate.md §1d`).

## Request
After winter snow shipped, the human asked: **"Can snow become ice? How do we make it realistic?
Let's plan first."**

## The two realistic "ice" mechanisms
Real-world "ice" is two unrelated processes, and they map to very different places in this engine:
- **(A) snow → ice crust** — a lying snowpack that melts by day and refreezes by night (or
  compacts under its own weight) hardens into ice. A refinement of the world-uniform `SnowCover`
  scalar — a second scalar + a melt-refreeze rule + a new ground/precip sprite.
- **(B) water → ice terrain** — lakes and rivers freeze over when it is cold and thaw in spring;
  you can walk across the frozen surface. This is a **terrain-type transition**, which the climate
  subsystem *already has a mechanism for* (the `content/climate.yaml` from×when→to table +
  `navmap.SetTerrain` + the `TerrainOverrides` delta stream). Plan §1 even anticipated it:
  "river-freeze needs an `ice` terrain type."

## Decisions

### ICE1 — what "ice" is → (b) water→ice terrain (P1); snow-crust parked
Chosen for architecture fit and impact. Frozen lakes/rivers are the iconic "ice," and path (b)
reuses the transition table, `SetTerrain` (already built at climate M1), and the terrain delta
stream — near-zero new engine code, mostly content (a new terrain type + freeze/thaw rules). The
literal reading of the question ("snow → ice") is path (a); it is deferred as a later refinement
(so ICE2, the snow-crust physics, and the ICE5(a)/ICE6(a) render/serialization for it are parked).

### ICE3 — which water, and the thaw-identity problem
- **Which water → lake + river.** Sea is excluded from P1 (salinity lowers its freezing point and
  it freezes slowly/coastally — over-scope).
- **⚠ Thaw-identity (the real fork).** The transition table is stateless (`from → to`). Freezing
  both `lake` and `river` into a single `ice` type **loses the origin**, so thawing `ice → ?`
  cannot restore the correct water type (a frozen river would thaw into a lake). Options weighed:
  (i) **separate frozen types** `ice_lake`/`ice_river` — unambiguous thaw, but multiplies terrain
  types; (ii) single `ice` + lossy restore to one canonical water type — rejected (wrong-type
  bug); (iii) **per-cell origin storage** — a new `CellState.FrozenFrom` field records the
  pre-freeze terrain, and thaw restores `Terrain = FrozenFrom` exactly. **Human chose (iii)** for
  exact restoration. Cost: `FrozenFrom` becomes a per-cell serialized field (climate digest
  `cells[]` + data-contracts §10), so path (b) is **not** "zero contract change" after all.
- **`ice` terrain properties (D4)** — `passable`, `base_cost ≈ 1.2`, the water `Swim` required-tag
  removed (you walk on frozen water, you don't swim it). A "slippery" movement tag is a later add.

### ICE3b — how freeze-capture + thaw-restore are expressed → (b) table sentinel `__origin__`
Per-cell `FrozenFrom` forces a choice, because a static `from×when→to` table can express neither
(1) capturing the origin on freeze nor (2) a `to` that is the cell's own `FrozenFrom` on thaw.
Options: (a) engine special-case + `IceType`/`ThawWhen` config (thaw logic leaves the data table —
a mild D4 tension); (b) **table sentinel** — freeze rules stay ordinary (`{from: lake|river, to:
ice, when: "temperature < freezeC"}`, engine auto-captures `FrozenFrom = from` whenever `to` is the
ice type) and the thaw rule is `{from: ice, to: __origin__, when: "temperature > thawC"}`, where
`__origin__` is a reserved token the engine resolves to the cell's `FrozenFrom`; (c) terrain-attr
`freezable`/`frozen_as` — most data-driven but puts the freeze condition outside the table and adds
terrain schema. **Human chose (b):** both freeze and thaw (with an asymmetric `freezeC`/`thawC`
hysteresis gap that prevents flicker) stay declarative in `content/climate.yaml`; the engine learns
only one reserved token plus origin auto-capture.

### ICE4 / ICE5 / ICE6 (rec-confirmed)
- **ICE4** — the freeze/thaw °C thresholds are **literals inside the transition rules' `when`**
  conditions (`temperature < 0` / `temperature > 2` — **non-negative only**: the §6 expr grammar has no
  unary minus / negative literals, OQ-C RESOLVED, so the freeze point is `< 0`, 0°C being water's
  physical freezing point), NOT config/balance fields — identical to the
  existing `moisture < …` transitions (D10, CA3 °C). The ONLY new `balance` field is `ice_type` (→
  `climate.Config.IceType`); the `ice` terrain lives in `terrain.yaml`. `ice_type` absent ⇒ `""` ⇒ ice
  off (no auto-default).
- **ICE5** — render is the **existing terrain-colour path**: `ice` gets a pale blue-white
  `TERRAIN_STYLE` entry (the 3D hex-prism colour follows automatically). No new render mechanism.
- **ICE6** — freeze/thaw ride the existing `Transition`/`TerrainOverrides` delta stream (no new
  wire); the only serialization addition is the per-cell `FrozenFrom` in the climate digest.

## Consequences / staging (ICE-M, when built)
- `content/terrain.yaml`: new `ice` type (passable, base_cost ≈ 1.2, no Swim tag, pale colour attrs).
- `content/climate.yaml`: freeze rules `lake|river → ice when temperature < 0`; thaw rule
  `ice → __origin__ when temperature > 2` (thresholds are `when` literals, asymmetric = hysteresis,
  non-negative per OQ-C); `balance.ice_type: ice`.
- `engine/env/climate`: `CellState.FrozenFrom`; on a transition into the ice type capture
  `FrozenFrom = from`; resolve the `__origin__` sentinel to `FrozenFrom` on thaw (and clear it);
  serialize `FrozenFrom` in `Cells()`.
- `platform/config`: load `balance.ice_type` → `Config.IceType` (absent ⇒ ""); exempt the `__origin__`
  sentinel from the terrain cross-check; validate the sentinel↔ice pairing. The `when` thresholds are
  just compiled as ordinary §6 booleans (no special threshold loading).
- `engine/world` + persist: `FrozenFrom` in the climate digest; freeze/thaw transitions already
  bridge to `navmap.SetTerrain`.
- `frontend`: `TERRAIN_STYLE.ice` colour (2D + 3D). No new render code.
- **Deliberate golden re-baseline** (map M4 / CS-M analogue): frozen water becomes passable, so
  `pathfind` reroutes — the affected navmap/pathfind/world goldens are re-based on purpose.

## Invariants respected
- **D12** — freeze/thaw are deterministic table transitions in fixed GridCell order; `FrozenFrom`
  is per-cell state, no wall-clock, no map-iteration for logic.
- **D10 / D4** — the freeze/thaw rules and the `ice` type are content data; cost/passability derive
  from the `ice` base-cost + tags, not a bespoke per-transition function. The one engine concession
  is the `__origin__` sentinel resolution + origin auto-capture.
- **D11** — `ice` is a terrain *index* cell like any other; agents never snap to it.
