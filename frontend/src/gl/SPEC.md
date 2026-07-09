# SPEC — `frontend/src/gl`

> Status: `DRAFT` (plan: `docs/plans/hex-grid.md` H-frontend-3d). Parent: [`frontend/SPEC.md`](../../SPEC.md).
> Sibling of the pure-2D [`src/render`](../render/SPEC.md); the two are **alternative views** of the
> same reduced world data, chosen by a runtime toggle in `App` (default `2d`).

## Purpose

The **WebGL curved-world** renderer: a real-3D, "rolling world" (Animal-Crossing-style) view of the
hex terrain. Unlike `src/render` (pure, one 2D transform, unit-tested against a 2D-context mock), this
layer is **intentionally stateful** — it owns a GL context, a camera, a texture atlas and a continuous
RAF loop — so it lives OUTSIDE `src/render` and does not share its purity invariants. It consumes the
**same `WorldState` slices** the 2D canvas does (terrain grid + live agents/animals/objects); no new
network or reducer surface. Terrain is drawn as lit, curved **hex prisms** in WebGL; entities are
**2D-overlay markers** projected through the exact same view→curve→projection path as the shader.

## Module map

```
gl/
  hex.ts       flat-top odd-q hex math mirroring navmap hex.go: hexCentre (offset→world, ==render/terrain.ts),
               pixelToOffset (world→cell inverse, for seating entities on terrain), neighbour steps
  shaders.ts   GLSL source: tile (instanced curved prisms + walls + water + light) · sky (gradient)
  worldGL.ts   createWorldGL(glCanvas, overlayCanvas) → the stateful renderer handle
```
The React wrapper is [`components/WorldCanvas3D.tsx`](../components/WorldCanvas3D.tsx) (same prop shape
as `WorldCanvas`), so `App` swaps `<WorldCanvas/>` ⟷ `<WorldCanvas3D/>` behind the `2D map`/`3D view`
toggle with zero data-pipeline changes.

## Public Interface

```ts
// worldGL.ts
export interface WorldGL {
  ok: true
  setTerrain(grid: TerrainGrid | null): void   // rebuild the instanced prism buffer + elevation sampler
  fit(render: RenderConfig | null): void        // frame the world: focus = bounds/terrain centre, dist fits
  draw(agents, animals, objects, selectedId, clockMs): void   // one frame: GL terrain + overlay markers
  zoomBy(f): void; tiltBy(dRad): void; orbitBy(dRad): void; panBy(dxPx, dyPx): void  // camera reducers
  pick(px, py): { kind: 'agent' | 'animal'; id: string } | null  // screen → nearest entity (last frame)
  dispose(): void
}
export function createWorldGL(glCanvas, overlayCanvas): WorldGL | { ok: false; error: string }
```

## Behaviour

- **Terrain → prisms.** `setTerrain` walks the offset grid once; each cell becomes one instance
  `{centre(x,z), tileType, elevation, 6×neighbourElevation}`. The vertex shader extrudes a flat-top hex
  prism: top face textured with the flat `base/*_top.png` atlas tile (id→tile via a DATA table, unknown
  → grass), side walls dropped **only toward a lower neighbour** (`min(elev, nbr)`) so equal-height
  seams collapse (no z-fight) and cliffs are exactly as deep as the drop. Off-grid neighbours use a
  skirt so the map edge shows a wall. Rebuilt on grid identity change (small static buffer).
- **Per-cell elevation.** When the grid carries `elevation[]` (`/api/terrain`, generated worlds —
  data-contracts §6), each cell's prism height = `(ELEV_BASE + e·ELEV_SPAN)·cellSize` from ITS OWN
  `e ∈ [0,1]` (and its neighbours' for the walls) — real relief: mountains tall, river valleys carved,
  lakes sitting at their basin's height. Grid without `elevation` (hand-authored fixtures, old runs)
  falls back to the per-type `ELEV_FRAC` table — the pre-elevation look, unchanged. Entity seating uses
  the same per-cell sampler, so agents/animals ride the relief automatically.
- **Curved / rolling world.** View-space `y -= curv·z²` bends distant ground below the horizon;
  **distance fog** fades tiles into the gradient **sky** (horizon colour == fog colour → seamless).
- **Height ↔ tilt.** Relief = `RELIEF_MIN + RELIEF_GAIN·smoothstep(pitch)` (`0.85 + 0.5`) — real height
  even when top-down, rising toward a diorama as the camera leans back. One directional light shades the walls; water tops ripple
  and shimmer (a continuous loop drives `uTime`).
- **Entities.** Agents/animals/objects are projected on the CPU (same view→curve→proj as the shader,
  seated on the sampled terrain elevation) and drawn on the 2D overlay: agent dots (+ selection ring),
  animal dots (species glyph colour from the assets manifest, stamina alpha, position lerped from the
  reducer's interpolation stamps), object markers; all fog-faded by depth. `pick` inverts this for
  click-select (agents win ties, 16 px).
- **Camera.** Orbit (`yaw`), tilt (`pitch` 8–85°), dolly (`dist`, clamped to a fit-relative range),
  pan (`focus`, clamped to world bounds). Wheel = zoom · Alt+wheel = tilt · Shift+wheel = rotate ·
  drag = pan (all in `WorldCanvas3D`). `fit` frames `RenderConfig.bounds` (else the terrain bbox).

## Invariants

- **Additive, not a replacement.** `src/render` + its 71 tests are untouched; 3D is opt-in (toggle,
  default 2D). WebGL unavailable ⇒ `createWorldGL` returns `{ok:false}` and the wrapper shows a
  "switch to 2D" notice — never throws.
- **Open content renders via DATA.** terrain-id→tile/elevation and species→colour are lookup tables
  with fallbacks (grass / prey glyph); no id string conditionals in draw paths; unknown ids never throw.
- **Same data contract.** Reads only the reducer-owned `WorldState` slices already passed to
  `WorldCanvas`; adds no network, no reducer fields. Coordinates stay continuous (D11) — entities are
  seated on terrain height, never snapped to cells.
- **Navmap is the hex authority.** `hex.ts` mirrors `hexCentre` from `render/terrain.ts` (== engine
  `hex.go`); `orientation`/`cellSize` come from the payload.

## Out of Scope (later phases)

- Flora coverage, transition FX (spawn/death/attack/grow), ambient (day-night tint / rain / wind),
  camera-follow, animal sprite billboards (pose × heading), agent cluster colours/labels — the 2D
  renderer still carries these; the 3D view ports them in later `docs/plans/hex-grid.md` phases.
- Culling for very large grids (current build uploads all cells; starter grids are small).
