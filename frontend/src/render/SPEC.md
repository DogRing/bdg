# SPEC — `frontend/src/render`

> Status: `APPROVED` (plan: `docs/frontend-plan.md`, Q2/Q3/Q4/Q5/Q6/Q8 RESOLVED 2026-07-02)
> Layer: pure render — imports `src/types.ts` + [`src/assets`](../assets/SPEC.md) only. No hooks,
> no DOM state, no network. Parent: [`frontend/SPEC.md`](../../SPEC.md).
> Split of the former `src/utils/canvasRenderer.ts` (522 lines > ~400 rule); that file is deleted.

## Purpose

Every function that turns `WorldState` + a camera + a clock into pixels: the shared world→canvas
transform, the interactive camera, per-layer draw functions (terrain, flora, fauna, agents,
ambient, transition FX), frame-to-frame motion interpolation, and hit-testing. All functions are
**pure**: `(state slices, CameraState, clockMs) → draws/values`. Same inputs ⇒ same frame — the
frontend's determinism analogue, which is what makes motion unit-testable.

## Module map (each file < 400 lines)

```
render/
  transform.ts   Transform type, buildTransform(cam, viewport), wx/wy world→px, px→world inverse
  camera.ts      CameraState + pure camera reducers (zoom / pan / follow / init)
  animator.ts    adaptive lerp (Q3): displayPos/displayHeading, walk↔run refinement, FX progress
  terrain.ts     terrain grid → offscreen raster + blit; wear trail overlay
  objects.ts     placed resource objects (berry/water/shelter): kind-styled markers + labels
  flora.ts       plants: stage-frame sprite scaled by width (glyph fallback)
  fauna.ts       animals: pose→frame sprite, heading rotation, stamina dim (glyph fallback)
  agents.ts      agent dots + cluster colour/aura + selection ring (unchanged look, Q8)
  fx.ts          transient FX evaluation + draw: spawn / death / attack / grow (Q4)
  ambient.ts     day-night tint, temperature vignette, rain, wind arrow (per parent SPEC)
  hitTest.ts     click → nearest entity {kind:'agent'|'animal', id} | null
  index.ts       re-exports (the only import surface for components)
```

## Public Interface (signatures — the cross-module contract)

```ts
// transform.ts — ONE mapping for every layer (D11: continuous coords, never snap)
export interface Transform { sx: number; sy: number; ox: number; oy: number }
export function buildTransform(cam: CameraState, viewW: number, viewH: number): Transform
export function wx(x: number, tr: Transform): number   // world → canvas px
export function wy(y: number, tr: Transform): number
export function toWorld(px: number, py: number, tr: Transform): Vec2

// camera.ts — interactive top-view (Q6). All reducers pure: (cam, input) → new cam.
export interface CameraState {
  center: Vec2            // world coords
  zoom: number            // px per world unit, clamped [ZOOM_MIN, ZOOM_MAX]
  follow: EntityRef | null // {kind:'agent'|'animal', id} — camera locks to it
}
export function initialCamera(render: RenderConfig | null, pts: Array<{ pos: Vec2 }>,
                              viewW: number, viewH: number): CameraState | null
  // render → fit bounds; else fit the pts bbox; null when neither is known yet (caller retries)
export function cameraZoom(cam: CameraState, cursorPx: Vec2, factor: number,
                           viewW: number, viewH: number): CameraState  // cursor-anchored
export function cameraPan(cam: CameraState, dxPx: number, dyPx: number): CameraState // breaks follow
export function cameraFollow(cam: CameraState, target: EntityRef | null): CameraState
export function cameraTick(cam: CameraState, agents: Iterable<{ id: string; pos: Vec2 }>,
                           animals: Iterable<{ id: string; pos: Vec2 }>, clockMs?): CameraState
  // follow ≠ null → center = followed entity's interpolated displayPos at clockMs;
  // entity gone → follow = null

// animator.ts — adaptive lerp (Q3). Entities carry {pos, prevPos, heading, prevHeading,
// frameAtMs, prevFrameAtMs} maintained by the useWorld reducer.
export function displayPos(e: Interpolable, clockMs: number): Vec2
  // lerp(prevPos→pos) over [prevFrameAtMs→frameAtMs] measured window, clamped t∈[0,1]
export function displayHeading(e: Interpolable, clockMs: number): number  // shortest-arc lerp
export function isRunning(e: Interpolable): boolean
  // displacement rate > RUN_SPEED_THRESHOLD world-units/s → refine pose walk→run
export function fxProgress(fx: FxInstance, def: FxDef, clockMs: number): number | null
  // (clockMs - fx.at)/duration ∈ [0,1]; null once expired (caller skips; reducer prunes)

// draw fns — all take (ctx, slice, tr, deps, clockMs); deps are injected (sprites, theme, styles)
export function drawTerrain(ctx, grid: TerrainGrid | null, tr, raster: TerrainRaster | null): void
export function makeTerrainRaster(grid: TerrainGrid, doc?: DocLike): TerrainRaster | null
  // offscreen 1px/cell raster; re-made on grid identity change; doc injectable for tests
export function drawFlora(ctx, flora: PlantState[], tr, sprites: SpriteCache, clockMs,
                          fx?: FxInstance[]): void   // fx: spawn alpha ramp + grow scale tween
export function drawFauna(ctx, animals: Iterable<AnimalState>, tr, sprites: SpriteCache,
                          clockMs, fx?: FxInstance[]): void // fx: spawn alpha + attack lunge offset
                          // (glyph colours come from the assets manifest, not the theme)
export function drawAgents(ctx, agents: Iterable<AgentState>, tr, theme, selectedId, clockMs): void
export function drawFx(ctx, fx: FxInstance[], tr, sprites, clockMs): void
export function drawAmbient(ctx, climate: ClimateState | null, viewW, viewH, clockMs): void
export function hitTest(agents: Iterable<AgentState>, animals: Iterable<AnimalState>,
                        tr: Transform, px: number, py: number):
  { kind: 'agent' | 'animal'; id: string } | null   // agents win ties; 15px radius
```

## Behaviour

### Camera (Q6)
- **Init**: `RenderConfig` present → center = bounds centre, zoom = `pixelsPerUnit` fitted to the
  viewport; else auto-fit the known-entity bbox (degenerate → default view). An entity-bbox fit is
  PROVISIONAL: if `RenderConfig` arrives after it (REST geometry can lose the race against the
  first SSE entities on a slow link), the camera re-anchors ONCE to the world bounds (skipped
  mid-drag, never repeated). Never jitters otherwise: only user input or follow moves it.
- **Wheel zoom**: multiply `zoom` by `factor`, clamp; keep the world point under the cursor fixed
  (`cameraZoom` solves the new center for that). **Drag pan**: shift center by `d/zoom`; any manual
  pan clears `follow`. **Click-to-follow**: `hitTest` result → select + `cameraFollow`; empty click
  clears both. `cameraTick` re-centers each RAF on the followed entity's *interpolated* position;
  when the entity despawns (death FX may still be playing), follow clears and the camera stays put.

### Layer order (back→front, one `buildTransform` for all)
`terrain → flora → placed objects → animals → agents → fx → ambient`.

### Fauna (motion = pose × interpolation)
Per animal: `pos/heading = displayPos/displayHeading` (Q3) → `pose = poseFor(action)`, refined
`walk→run` when `isRunning` (and `run→walk` when a `run`-posed animal is near-still) → sheet +
`frameRect(def, pose, clockMs)` → `drawImage` rotated to heading, centred, scaled by
`frameW × SPRITE_SCALE / zoom`-independent world size; `globalAlpha = max(0.3, stamina)`. Fallback:
category glyph (chevron; predator sharper + red-tinted) per the assets chain. A `dying` pose is
drawn only via the death FX (below) — live animals never map to `dying` from `poseFor`.

### Flora
Per plant: frame = `min(stage, stageFrames-1)` of the flora sheet, drawn at world size ∝ `width`
(no rotation); glyph = species colour circle. Dense + static: no interpolation.

### Transition FX (Q4 — reducer-owned queue, render evaluates by time)
`FxInstance = { kind:'spawn'|'death'|'attack'|'grow', at:ms, pos:Vec2, species?, heading?, id }`
(appended by `useWorld` on `AnimalBorn/PlantSpawned` → spawn, `AnimalDied/PlantDied` → death,
pose *entering* `attack` → attack, flora `stage` increase → grow; pruned when expired).
- **spawn** (500 ms): the entity's sprite fades/scales in 0→1 (drawn by fx layer only if the entity
  isn't in the live map yet; else alpha ramp applied by fauna/flora draw via matching fx).
- **death** (1500 ms): the *removed* entity keeps drawing from the fx entry — `dying` pose row if
  the sheet has one, else the last sprite — fading to a brief corpse mark (dark X/patch), then gone.
- **attack** (300 ms): lunge — the attacker's sprite offsets `sin(π·t)·LUNGE_DIST` along heading
  (applied by fauna draw when a matching attack fx is active) + an impact flash ring at the target
  side. No backend event needed.
- **grow** (500 ms): the plant scales from old-stage to new-stage size (ease-out).

### Terrain (Q5)
`TerrainGrid = { cellSize, w, h, terrain: TerrainID-index[], wear?: Float32Array }` from
`GET /api/terrain`, kept current by `terrain_delta`. Cells rasterize to an **offscreen canvas**
(`makeTerrainRaster`) in `TERRAIN_STYLE` flat colours + wear trails (`WEAR_TRAIL_COLOR`, alpha ∝
wear); `drawTerrain` only blits through the transform. Raster is rebuilt on grid identity change
(the reducer replaces the grid object on delta merge), never per frame. `grid === null` → draw
nothing (env-off neutrality; the old decorative map is **deleted**).

### Ambient
As specified in the parent SPEC §Ecosystem rendering: day-night tint from
`dayNight`+`hourOfDay`, temperature vignette, animated rain streaks when `raining`, wind HUD arrow
(`windDir`/`windMag`). `climate === null` → no-op.

## Invariants

- **Purity.** No `Math.random()`, no `Date.now()`/`performance.now()` inside render/animator fns —
  the clock arrives as `clockMs`. No module-level mutable state (sprite cache, terrain raster, and
  theme are injected). Same state + same clock ⇒ same pixels.
- **One transform (D11).** Every layer draws through the same `buildTransform(cam, viewport)`
  result per frame; positions stay continuous floats; terrain cells are an index rasterized *under*
  entities — entities never snap to cells.
- **Open content renders via data.** No species/ActionID/TerrainID string conditionals in this
  folder (grep guard) — all differentiation flows through the assets manifest + fallback chain;
  unknown ids draw fallbacks, never throw.
- **Env-off neutrality.** `animals/flora/climate/terrain` empty ⇒ `drawFauna/drawFlora/drawAmbient/
  drawTerrain` draw nothing; the canvas equals the social-only render.
- **Reducer owns state; render owns nothing.** Fx entries, interpolation fields, and the terrain
  grid live in `WorldState` (written only by `useWorld`); render reads. The only render-side cache
  is the derived terrain raster + sprite cache, both injected and rebuilt from state.
- **Camera is the only view state** and lives in one `CameraState` (component state in
  `WorldCanvas`/`App`); reducers are pure; zoom is always clamped.

## Acceptance Criteria (Vitest; canvas ops assertable via a 2D-context mock)

- [ ] **Transform round-trip** — `toWorld(wx(p),wy(p)) ≈ p` for random points at several zooms.
- [ ] **Cursor-anchored zoom** — after `cameraZoom` at cursor c, `toWorld(c)` is unchanged (±ε);
  zoom clamps at both ends.
- [ ] **Pan breaks follow** — `cameraPan` on a following camera returns `follow: null`;
  `cameraTick` with `follow` centres on the entity's `displayPos`, and clears follow when the id
  is absent from both live maps and fx.
- [ ] **Adaptive lerp** — entity with `prevPos=(0,0)@1000ms, pos=(10,0)@2000ms`: `displayPos` at
  1500 ms = (5,0); at 2500 ms = (10,0) (clamped, no overshoot). Heading 350°→10° passes through 0°
  (shortest arc), never 180°.
- [ ] **Pose × frame** — a wolf with `action:'hunt_deer'` draws the `attack` row; frame index
  cycles at the clip fps as `clockMs` advances; an animal whose sheet lacks the pose falls back
  `walk→idle→glyph` (no throw for `species:'unknown_beast'`).
- [ ] **Run refinement** — displacement rate above threshold upgrades pose `walk→run`; near-zero
  displacement with a `flee` action still draws `run` row only if `isRunning` (rule documented).
- [ ] **Death FX** — after `AnimalDied` (entity removed, fx appended): at +0 ms the sprite still
  draws (fading); at +1500 ms `fxProgress` = null and nothing draws; a corpse mark is drawn in the
  final third. **Spawn FX** — alpha ramps 0→1 over 500 ms. **Attack FX** — sprite offset peaks at
  t=0.5 along heading. **Grow FX** — plant size eases old→new stage size.
- [ ] **Terrain raster** — a 3×3 grid with `water/plain/forest` blits 9 flat-colour cells; unknown
  TerrainID paints `TERRAIN_DEFAULT`; wear>0 overlays trail alpha; `drawTerrain` performs no
  per-cell work when the grid object is unchanged (raster reuse asserted via mock call counts).
- [ ] **Hit-test** — click within 15 px selects the nearest agent over an animal at equal
  distance; empty space → null; animals ARE clickable (returns `kind:'animal'` — used for
  camera-follow, not AgentDetail).
- [ ] **Env-off neutrality** — with empty env slices, the mock-context op list is identical to the
  social-only baseline (no terrain/flora/fauna/ambient/fx ops).
- [ ] **Purity guard** — grep: no `Math.random`, no `Date.now`, no `performance.now`, no top-level
  `let`/`new Image` in `src/render/`.

## Out of Scope

- Manifest tables, sprite cache, pose mapping, FX durations → [`src/assets/SPEC.md`](../assets/SPEC.md).
- Reducer changes (fx queue, interpolation fields, terrain merge) → parent
  [`frontend/SPEC.md`](../../SPEC.md) (§State) — implemented in `hooks/useWorld.ts`.
- Backend `GET /api/terrain` + `WorldFrame` emission → `backend/platform/api` (pending SPEC delta,
  `docs/frontend-plan.md` Q5) — this layer consumes the already-reduced `TerrainGrid`.
- Scent/navmap-cost debug overlays, minimap, screenshot/export.
