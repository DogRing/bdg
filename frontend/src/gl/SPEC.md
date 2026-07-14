# SPEC — `frontend/src/gl`

> Status: `DRAFT` (plans: `docs/plans/hex-grid.md` H-frontend-3d; atmosphere: `docs/plans/gl-atmosphere.md`).
> Parent: [`frontend/SPEC.md`](../../SPEC.md).
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
  hex.ts         flat-top odd-q hex math mirroring navmap hex.go: hexCentre (offset→world, ==render/terrain.ts),
                 pixelToOffset (world→cell inverse, for seating entities on terrain), neighbour steps
  shaders.ts     GLSL source: tile (instanced curved prisms + walls + water + light + cloud shadow) ·
                 sky (ray-based gradient + procedural sun/moon disc + night stars)
  atmosphere.ts  climate → atmosphere values: day/night colour ramp, sun/moon arc, overcast/rain/wind
                 targets, wrap-aware eased state (τ≈3 s), overlay precip drawing — rain streaks OR
                 (temperature < PRECIP_SNOW_BELOW_C, CS1) snow flakes — + wind-arrow. Atmo carries
                 both `rain` and `snow` (0..1); the temp gate routes the eased precip density to one.
  worldGL.ts     createWorldGL(glCanvas, overlayCanvas) → the stateful renderer handle
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
  draw(agents, animals, objects, selectedId, clockMs, climate): void  // one frame: GL terrain + overlay markers + atmosphere
  zoomBy(f): void; tiltBy(dRad): void; orbitBy(dRad): void; panBy(dxPx, dyPx): void  // camera reducers
  pick(px, py): { kind: 'agent' | 'animal'; id: string } | null  // screen → nearest entity (last frame)
  dispose(): void
}
export function createWorldGL(glCanvas, overlayCanvas, sprites?: SpriteCache | null):
  WorldGL | { ok: false; error: string }
  // sprites: the injected assets cache (same instance the 2D canvas uses) — drives the fauna
  // billboards below; absent/null ⇒ every animal falls back to its glyph-colour dot
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
  **animal sprite billboards** (FE-P6: the species' fauna sheet frame — pose via `poseFor(action)`,
  frame via `frameRect` at `clockMs`, bottom-anchored at the seated ground point, screen-size ∝ the
  old dot radius, **horizontally mirrored** when the world heading points screen-left
  (`cos(heading + yaw) < 0` — sheets face +x; the 3D camera yaws, so mirroring replaces rotation),
  stamina alpha; sheet absent/unready → the glyph-colour dot exactly as before), object markers;
  all fog-faded by depth. Position lerped from the reducer's interpolation stamps. `pick` inverts
  this for click-select (agents win ties, 16 px).
- **Camera.** Orbit (`yaw`), tilt (`pitch` 8–85°), dolly (`dist`, clamped to a fit-relative range),
  pan (`focus`, clamped to world bounds). Wheel = zoom · Alt+wheel = tilt · Shift+wheel = rotate ·
  drag = pan (all in `WorldCanvas3D`). `fit` frames `RenderConfig.bounds` (else the terrain bbox).
- **Atmosphere** (`atmosphere.ts`, plan `docs/plans/gl-atmosphere.md`; drivers = the reducer's
  `ClimateState`, passed per-frame through `draw`; `climate == null` ⇒ the fixed-daylight LEGACY
  constants — same colours/light as pre-atmosphere; the sky gradient itself is ray-based in both
  paths, see plan DL3):
  - **Day/night (hybrid).** A keyframed colour ramp over `hourOfDay` (night/dawn/day/dusk keys)
    sets sky zenith/horizon (== fog == clear colour), light tint and ambient/diffuse intensities;
    a computed **sun/moon arc** drives the directional light so wall shading sweeps across the day.
    Night keeps a dim blue moon light + a clamped ambient floor (`NIGHT_MIN` escape hatch) so the
    view never goes unreadable; entity overlay markers keep full contrast.
  - **Sky detail.** The sky shader is ray-based (camera basis uniforms): horizon-locked gradient,
    procedural sun/moon discs (visibility from elevation), hash-based stars at night (hidden by
    cloud cover). No textures.
  - **Weather.** While `raining`: sky/fog/light ease toward an overcast grey, fog pulls closer,
    hash-based **rain streaks** (2D-overlay port of `render/ambient.ts`, slanted downwind) fall,
    the ground darkens (wet uniform). **Cloud shadows** — moisture/rain-driven noise darkening —
    drift downwind across the terrain.
  - **Wind.** HUD compass arrow (top-left, camera-yaw-corrected so it points where the wind blows
    on screen; length/alpha ∝ `windMag`) + world cues: water-ripple direction/speed follow
    `windDir/windMag`, rain slant and cloud drift follow the same vector.
  - **Smoothing.** All drivers ease client-side toward targets (τ ≈ 3 s; wrap-aware for hour and
    wind angle) — integer `hourOfDay` steps and `raining` flips never pop. Effects are functions of
    `(eased climate, clockMs)`; streak/star patterns are index-hash based (no `Math.random`).

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

- Flora (coverage wash + tree/bush billboards — needs occlusion/depth care against the prisms,
  FE-P6 P6-Q1 deferred it), transition FX (spawn/death/attack/grow), camera-follow, agent cluster
  colours/labels — the 2D renderer still carries these; the 3D view ports them in later phases.
  (Animal sprite billboards shipped with FE-P6.) Atmosphere polish deferred by
  `docs/plans/gl-atmosphere.md` Q3/Q6: world-space rain particles, temperature vignette, seasonal
  ground tint.
- Culling for very large grids (current build uploads all cells; starter grids are small).
