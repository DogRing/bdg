# `engine/space/field` — SPEC

**Role.** A deterministic scalar **potential field** over a `navmap` snapshot, built by a multi-source
**weighted flood** and **sampled at continuous positions** to yield an intensity + gradient. Foundation
of the fauna **potential-field steering** subsystem (`docs/plans/fauna.md §4`, P_move1 / FM1/FM2 /
FM-build). A **hazard** field (source weight = terrain danger) yields a `Repulsion` that **scales with
danger SEVERITY and proximity** — deep water / a cliff push harder and farther than a shallow bank — a
**continuous cost, never an absolute block** (a stronger drive/flight overcomes it, FM5). The same
primitive later serves resource **attraction** (water for thirst, FM4): attraction = toward increasing
intensity (`Gradient`).

**Invariants.**
- **D11** — the navmap grid is an **auxiliary index**, like `scent`/`navmap`/`pathfind`; agent positions
  stay continuous `float`. This package **snaps nothing** into a position — it reads `CellOf(p)` to look
  up a value and returns a continuous direction. **Pure query**; never mutates the navmap.
- **D12** — determinism. `Build` seeds sources into the max-heap in **sorted (R,Q)** order; the heap
  orders by `(intensity desc, Cell.R, Cell.Q)`; neighbours come from `navmap.Neighbors`' fixed canonical
  order; the intensity map is never range-iterated for ordering. `Gradient` sums neighbour contributions
  in `Neighbors`' fixed order. Same `(navmap, sources, decay, passable)` ⇒ byte-identical.

**Model.** Intensity at a cell = **MAX over sources** of `Weight − decayPerUnit·dist`, floored at 0,
where `dist` is the **geometric** distance (`‖CellCenter(a)−CellCenter(b)‖`, uniform ≈ √3·CellSize per
neighbour) accumulated through the **passable** graph (so danger "flows around" impassable regions).
Because a source decays from its own `Weight`, a **stronger source reaches farther** (`reach = Weight/
decayPerUnit`) — an emergent "big hazards are scary from farther" with no per-source range param. A
**source cell may itself be impassable** (a cliff / deep-water hazard IS the origin at its full weight);
only *expansion targets* are gated by `passable`.

## Public Interface

```go
package field

// Source is one weighted origin cell (a hazard cell's danger, a water cell's attractiveness, …). Weight
// is the intensity AT the cell; it decays linearly to 0 at Weight/decayPerUnit world-units away. ≤0 ignored.
type Source struct {
    Cell   navmap.Cell
    Weight float64
}

// Field is an immutable scalar potential field over a navmap: at each cell, MAX over sources of
// Weight − decay·dist (floored at 0), distance through the passable graph. Cells no source reaches are
// absent (intensity 0). Sampled at CONTINUOUS positions (D11). Pure/read-only after Build.
type Field struct { /* opaque: navmap ref + per-cell intensity */ }

// Build runs a multi-source weighted MAX-flood from sources over m, expanding only through cells where
// passable(cell) is true, decaying by decayPerUnit × geometric step length and keeping the max. A source
// seeds at its Weight even if the cell is impassable (an origin, not an expansion target). decayPerUnit
// ≤ 0 ⇒ sources only (no spread). passable == nil ⇒ navmap.Passable. Pure, D12.
func Build(m *navmap.NavMap, sources []Source, decayPerUnit float64, passable func(navmap.Cell) bool) *Field

// IntensityAt returns the field intensity at continuous p (0 where no source reaches). Index read (D11).
func (f *Field) IntensityAt(p core.Vec2) float64

// Gradient returns the UNIT direction of INCREASING intensity at p (toward the nearest/strongest source),
// from finite differences to p's cell neighbours (fixed order, D12). Zero where flat/degenerate.
// Repulsion = negate (away from danger); attraction (FM4) = as-is (toward the source).
func (f *Field) Gradient(p core.Vec2) core.Vec2

// Repulsion returns the away-from-danger steering vector at p: local intensity × the UNIT away-direction
// (= intensity × −Gradient). Magnitude scales with danger SEVERITY + proximity; zero where safe/flat.
// The fauna steer scales this by the species' §6 hazard multiplier and blends it into the heading (FM5).
func (f *Field) Repulsion(p core.Vec2) core.Vec2
```

## Consumers
`world` builds ONE shared static hazard `Field` lazily (sources = navmap cells whose danger ≥ floor,
weight = danger, where danger = impassable→1 else `(BaseCost−1)/K` — the runtime proxy for the §5
depth/slope intent, `docs/plans/fauna.md §4` FM2; a single `decayPerUnit` balance constant), **caches
it** (static terrain ⇒ built once, rebuilt only on a terrain change) and injects it into the fauna
snapshot via the fauna-declared `HazardSampler` interface (dependency inversion — fauna imports no
field/navmap). `engine/fauna` steer adds `e·Repulsion(Pos)` (e = the species' `HazardAvoidance`
multiplier) to the chosen direction — per-species differentiation via `e`, so one shared field suffices
in P_move1 (FM5). Later phases (FM4 water attraction, FM3 dynamic predator-flee, per-species fields for
fish-inversion) reuse `Build` with different sources / a rebuild cadence.

## Dependencies
`core`, `engine/space/navmap` (L2, beside `pathfind`). Imports no sibling implementation.
