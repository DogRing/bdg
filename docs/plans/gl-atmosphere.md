# GL Atmosphere — Implementation Plan (day/night · weather · wind in the 3D view)

Concept & rationale: this file. Sits beside `docs/plans/hex-grid.md` (which built the 3D curved-world
view; its `gl/SPEC.md` explicitly deferred ambient to "later phases" — this plan is that phase) and
`docs/plans/frontend.md` (whose FE-P4 built the 2D `render/ambient.ts` these effects mirror). It is
the **roadmap**: scope, phasing, per-file deltas, purity, and the Open-question gate. SPECs are edited
in Phase A1, SPEC-first, only after the gate opens.

> **Goal (author-stated, 2026-07-12):** the 3D map should express the **global state of the world** —
> day/night, weather (rain/overcast), wind — instead of its current fixed-daylight look. Purely a
> **presentation** motivation.
>
> **This is frontend-only. No backend, no wire, no invariant edit.** Every driver already reaches the
> client: `ClimateState{temperature, moisture, raining, windDir, windMag, hourOfDay, dayNight,
> yearFraction}` is reduced from `WorldFrame` today and `WorldCanvas3D` already receives the
> (currently unused) `climate` prop. `docs/core/data-contracts.md` does not change.

## 0. Decisions locked (author-set direction — do not re-open)

- **DL1 — Presentation transform only.** Atmosphere is uniforms + overlay drawing in `frontend/src/gl/`
  (+ the `WorldCanvas3D` wiring line). No new SSE field, no `/api/*` change, no reducer change.
- **DL2 — Additive; the 2D renderer is untouched.** `src/render/` (incl. `ambient.ts`) and its vitest
  suite stay byte-identical. The 3D view keeps its own invariants (stateful, not pure).
- **DL3 — Neutrality fallback.** `climate == null` (env-off, social-only runs, mock without climate)
  ⇒ the fixed-daylight LEGACY constants (same sky/fog colours, same light direction/intensities, no
  discs/stars/rain/clouds). One intentional delta: the sky gradient is now **ray-based**
  (horizon-locked) instead of screen-space — required for the celestial bodies and shared by both
  paths so climate arriving mid-session never pops the sky model.
- **DL4 — Continuous, deterministic drivers.** All effects are functions of `(climate, clockMs)`;
  rain/star patterns use the same index-hash technique as 2D `ambient.ts` — no `Math.random`, no
  `Date.now` in draw paths. Client-side smoothing (Q5) exists only because `hourOfDay` arrives as an
  integer step (1 game hour = 5 real minutes live at `real_scale: 12`).
- **DL5 — Open content stays open.** No new terrain-id or species conditionals; any per-type hook goes
  through the existing DATA tables.

## 1. Current baseline (measured from the code)

- `gl/worldGL.ts`: constant sky `ZEN/HOR`, constant directional `LIGHT`, fog colour == horizon
  (seamless join), water ripple from `uTime`, wall shading via one lambert term. `draw()` does **not**
  take climate.
- `gl/shaders.ts`: `tile` (prisms + light + fog) and `sky` (2-stop vertical gradient) programs.
- `components/WorldCanvas3D.tsx`: receives `climate: ClimateState | null` — unused.
- 2D counterpart `render/ambient.ts`: day-night tint, temperature vignette, hash-based rain streaks,
  wind HUD arrow — the visual vocabulary to port/upgrade.

## 2. Phases (each independently shippable; eyeball in `npm run dev` + `npm run mock`)

### A0 — This plan + resolve Open questions (§5) — ✅ all six RESOLVED 2026-07-12.

### A1 — SPEC-first (no code) — ✅ shipped 2026-07-12
- `frontend/src/gl/SPEC.md`: module map + Atmosphere behaviour + `draw(..., climate)` interface;
  ambient line dropped from Out-of-Scope (rain particles / vignette / seasonal stay deferred).
  `frontend/SPEC.md` unchanged (parent already points at the child SPEC by path).
- Not touched: `docs/core/data-contracts.md`, `backend/**`, `src/render/**` (for the record).

### A2 — Day/night (the core look) — ✅ shipped 2026-07-12
`gl/atmosphere.ts` (new): 8-key colour ramp + sun/moon arc + eased drivers; `shaders.ts` sky rewrite
(ray-based gradient, procedural discs, hash stars); lambert intensities as uniforms.

### A3 — Weather — ✅ shipped 2026-07-12
Overcast desaturate/dim + fog pull-in (`fogMul`), overlay rain streaks slanted downwind,
wet-ground darkening folded into the light tint.

### A4 — Wind — ✅ shipped 2026-07-12
HUD compass arrow (top-left, `windDir + yaw`), water-ripple direction/speed from wind
(legacy-identical at `windMag→0`), rain slant + cloud drift on the same vector.

### A5 — Polish (Q6 selection) — ✅ shipped 2026-07-12
Cloud shadows (2-octave sin noise in the tile FS, drifting by accumulated downwind offset).
Deferred per Q6: temperature vignette, seasonal tint. (The winter snow ground wash shipped
2026-07-17 is NOT the seasonal tint — it is climate CS5b, driven by the accumulated `snowCover`
scalar, decision record `docs/plans/climate.md` §1d CS5; mechanism `frontend/src/gl/SPEC.md`.)

> **Verified 2026-07-12** against `dev/mock-server.mjs` (48 s day cycle, rain spells, wandering
> wind) via headless-Chrome screenshots: dusk/night/pre-dawn/noon ramp, night legibility, stars,
> sun disc + glow at sunset, overcast + streaks + cloud shadows while raining, arrow rotation.
> `tsc -b` + `vite build` clean; the 2D suite (101 vitest) untouched and green.

## 3. Per-file deltas (frontend only)

| File | Change |
|------|--------|
| `gl/shaders.ts` | sky shader: time-of-day colours as uniforms (+ stars/sun/moon per Q2); tile shader: `uLight`/light-colour/ambient uniforms already exist or gain colour; optional cloud-shadow noise term (Q6). |
| `gl/worldGL.ts` | `setClimate()` or `draw(..., climate)` (A1 decides the shape); atmosphere state machine: target values from `(climate, clockMs)` + eased current values (Q5); overlay rain streaks + wind arrow (Q3/Q4); fog/clear colour per frame instead of constants. |
| `components/WorldCanvas3D.tsx` | pass the already-received `climate` prop through. |
| `gl/SPEC.md`, `frontend/SPEC.md` | per A1. |

## 4. Purity / perf / risk

- Same-frame reproducibility: atmosphere values are pure in `(climate, clockMs)` given the eased
  state; hash-based streak/star patterns (DL4).
- Perf: everything is uniforms + ≤ a few hundred overlay rects — negligible next to the instanced
  terrain draw. No new textures required (stars/discs can be shader-procedural).
- Risk — **legibility at night**: an observation viewer must not go unreadably dark. Playbook: clamp
  night darkening (min ambient), keep entity markers at full contrast (overlay is drawn after the
  tint decision), and expose one `NIGHT_MIN` constant as the escape hatch.
- Risk — **flicker/popping** on integer `hourOfDay` steps and `raining` flips → Q5 easing is the
  tripwire; transitions must be monotone eases, never snaps (unless Q5 resolves to snap).

## 5. Open questions (all resolved by the human, 2026-07-12)

- **Q1 — Day/night lighting model** *(blocks A2)*. `RESOLVED: (c) hybrid` — keyframed colour ramp
  (sky-zenith/horizon/fog/light-intensity over `hourOfDay`; night/dawn/day/dusk keys, art-directable)
  **plus** a computed sun/moon direction arc driving `uLight` so wall shading sweeps over the day;
  night is floor-lit by a dim blue "moon" so relief stays readable. (Rejected: colour-ramp-only with
  fixed light direction; pure sun-position model with derived colours.)
- **Q2 — Sky detail** *(blocks A2)*. `RESOLVED: (c) full` — procedural **sun/moon disc** + hash-based
  **stars** at night, all in the sky shader, no textures. (Rejected: gradient-only; disc-without-stars.)
- **Q3 — Rain rendering** *(blocks A3)*. `RESOLVED: (a) screen-space overlay streaks` — port of the
  2D `ambient.ts` hash-streak technique onto the 3D overlay canvas, slanted by wind, plus the common
  overcast restyle (sky greys, light dims, fog pulls closer). World-space GL particles deferred as
  polish. (Rejected for now: instanced world-space particles; overcast-only.)
- **Q4 — Wind representation** *(blocks A4)*. `RESOLVED: (c) both` — HUD compass arrow (the legible
  instrument, camera-yaw-corrected) **and** world cues: water-ripple direction/phase-speed keyed to
  `windDir/windMag`, rain-slant (Q3), cloud-shadow drift (Q6).
- **Q5 — Transition smoothing** *(blocks A2)*. `RESOLVED: (b) client-side eased lerp` — all
  atmosphere drivers ease toward their targets with τ ≈ 3 s (wrap-aware for hour-of-day and wind
  angle); no visible pops on the 5-real-minute integer `hourOfDay` steps or `raining` flips.
  (Rejected: snap; reconstructing a continuous clock from tick × `real_scale`.)
- **Q6 — Extra atmosphere scope** *(blocks A5; multi-select)*. `RESOLVED: cloud shadows + wet-ground
  darkening` — moisture-driven noise shadows drifting downwind (one shader term) and a rain-time
  ground-darkening uniform. **Deferred:** temperature vignette (2D port), seasonal ground tint from
  `yearFraction`.
