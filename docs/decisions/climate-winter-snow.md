# Winter snow: precipitation form + accumulating snowpack (Why)

> Deliberation cut from the living docs per the Documentation Triad. Pointer lives in
> `docs/plans/climate.md §1d` (CS1–CS5). Resolved 2026-07-14 (human RESOLVE via AskUserQuestion).

## Request
The human asked whether winter snow was implemented, with a specific model in mind:
**at 0–3 °C snow falls but melts (does not accumulate); below 0 °C snow accumulates and the
sprites change.**

## Prior state (partial, and wrong-feeling)
- Precipitation was always **rain**: `climate.State` carried only a `raining` bool, and both
  renderers (`render/ambient.ts` 2D, `gl/atmosphere.ts` 3D) drew rain streaks regardless of
  temperature. Sub-zero rain fell on snow-tinted trees.
- There was **no snowpack state** anywhere — not in the sim, not in the stream, not in the
  contract.
- `frontend/src/assets/manifest.ts:floraSeason()` picked the snow sprite from the
  **instantaneous** temperature (`snowBelowC = 0`). With a live diurnal swing that crosses
  0 °C twice a day, a snow-covered tree flickered on and off between night and afternoon —
  the opposite of "snow accumulates and stays."

So two independent axes were missing: **precipitation FORM** (rain ↔ snow) and **snowpack
STATE** (accumulate ↔ melt). The plan opened both as CS1–CS5 (enumerate-only, per the
Open-Question gate) and the human resolved them.

## Decisions

### CS2 — where the snowpack lives → (b) backend world-uniform scalar
The real fork. Three options:
- **(a) frontend-integrated** — a `useWorld` reducer integrates `snowCover` from each climate
  frame. Cheap, no backend change. **Rejected:** the pack is not in the snapshot, so a new
  viewer connecting mid-winter sees no snow and a reload resets it — per-client divergence,
  which contradicts the determinism + snapshot-consistency posture
  (`[[persist-consistency-upgrade]]`).
- **(b) backend world-uniform scalar** — `SnowCover float64 ∈ [0,1]` in `climate.State`,
  integrated in `Step`, streamed on the `WorldFrame` + the `sim:{run}:climate` hash, resumed
  byte-identically via `Restore(…, snow)`. **Chosen.** Deterministic, identical for every
  client, persistent. It is a new sim mechanism, so it expands the contract
  (`renderframe.go` → `WorldFramePayload` → `persist.ClimateView` → `types.ts`), which is the
  price of correctness here.
- **(c) backend per-cell snowpack** — coarse-grid `snow depth` per cell, enabling patchy melt
  and snow-covered *terrain* rendering. **Parked (frontier):** heavier, and it complicates the
  scent-spread stencil; not needed for the human's stated scope.

**Why world-uniform is enough:** temperature in this model is world-uniform per step
(`T = annualMid + annualAmp·sin(…) + dailyDelta(hour) − rainDrop`, no per-cell term), so the
snowpack derived from it is world-uniform too. A single scalar is the honest representation;
per-cell would store the same value in every cell.

### CS3 — accumulate/melt dynamics → temperature-proportional linear melt
Each climate step (1 game-hour), with `freezeC = SnowFreezeC` (design 0):
```
if temperature < freezeC && raining:  snowCover += SnowAccumRate
else if temperature > freezeC:        snowCover -= SnowMeltRate · max(0, temperature − freezeC)
clamp [0,1]
```
Melt ∝ (temperature − freeze) gives the human's "0–3 °C melts, colder holds" for free, and
makes warmer air melt faster. The alternative — a **constant** melt rate independent of
temperature — was rejected because it cannot express "warmer ⇒ faster." Rates live in
`content/climate.yaml` `balance` (D9 spirit: rates are content; the shape is fixed design).
The update draws **no RNG**, so the fixed `rain → wind` fork order (D12 byte-stability) is
untouched.

### CS1 — falling-precip form → (a) frontend-derived, `snowFallC = 2 °C`
The visual form (rain streaks vs. snow flakes) is a **pure function of the already-streamed**
`raining` + `temperature`, so it needs no backend field: `raining && temperature < 2` draws
snow, else rain, in both `ambient.ts` and `atmosphere.ts`. `2 °C` matches the human's "0–3 °C
is snow." A backend `precip` enum (option b) was rejected as an unnecessary contract change for
something derivable client-side.

### CS4 — sprite-season driver → (a) driven by `snowCover`, not temperature
`floraSeason` now returns `'snow'` when `snowCover ≥ snowCoverThresh` (0.1), so sprites follow
the **accumulated pack** and stop flickering on daily temperature crossings. When `snowCover`
is absent (pre-snow frames/fixtures) it falls back to the old freeze threshold (`temperature <
0`) for compatibility — the fallback is unit-tested, the streamed path is the real contract.
This **supersedes** frontend P6-Q2's temperature-only rule; `docs/plans/frontend.md` §8 and the
`assets.test.ts` season cases were re-baselined.

### CS5 — ground snow layer → (a) none for P1
The human's stated scope was "the sprites change," not "the ground turns white." So P1 renders
falling snow (CS1) + flora snow sprites (CS4) only. A cheap world-uniform white terrain wash
∝ `snowCover` (option b) and per-cell whitening (option c, needs CS2c) are available later if
wanted; neither ships now.

## Consequences / staging
Activation (`plan §1d CS-M`) touched, in one working set: `climate.{Config,State,Step,Restore}`
+ its SPEC + golden; `content/climate.yaml` + schema + `platform/config`; `world` RenderView /
persist snapshot / WorldFrame; `persist.ClimateView` + `main.go`; `data-contracts.md`; and the
frontend `types.ts` / `useWorld` / `manifest.floraSeason` / `ambient.ts` / `atmosphere.ts` /
`worldGL.ts`. The `SnowCover` addition to `climate.State` is a deliberate golden re-baseline
(like CA3), but it is outcome-neutral for existing snapshots because a fresh world starts
snowless and a pre-snow snapshot restores `snow_cover = 0`.

## Invariants respected
- **D12** — the snow integral is deterministic and RNG-free (fixed fork order preserved);
  `Forcing` stays worldtime-derived, no wall-clock.
- **D10** — thresholds/rates are `content/climate.yaml` data, not literals; `snow_freeze_c`
  is a balance constant.
- **Render purity** — snow rendering reads only streamed values (`snowCover`, `temperature`,
  `raining`, wind); no module-level mutable state, no `Math.random`/`Date.now` — flakes are
  index-hash + clock, mirroring the rain streaks.
