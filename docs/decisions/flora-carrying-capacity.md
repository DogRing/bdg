# Flora propagation: carrying-capacity density weight (Why)

> Deliberation cut from the living docs per the Documentation Triad. Pointer lives in
> `docs/plans/flora.md §1k`. Resolved 2026-07-12 (human RESOLVE via AskUserQuestion).

## Symptom
Live runs with flora active occasionally produced ~80,000 `grass` objects — the map
carpeted with vegetation, which looks bad and is expensive to render/stream.

## Root cause — the old density weight has no equilibrium
Propagation (flora §1a) spawns children near a mature parent with probability
`chance × suitability × densityWeight(n)`, where `n` = same-species neighbours within the
propagation radius. The weight was `1/(1+n)` (`step.go` `densityWeightNumerator/(1+n)`).

That throttles the *per-plant* rate but imposes **no carrying capacity**. Sum it over a
saturated patch of `n+1` plants:

    total spawns/step ≈ (n+1) · chance · suit / (1+n) = chance · suit

— roughly **constant regardless of local density**. So a patch keeps adding plants every
step forever, and the frontier keeps expanding across all suitable terrain. Deaths only fire
on sustained low suitability (`death_threshold`), so on good ground nothing prunes. Result:
linear-plus-frontier (super-linear) growth → tens of thousands of plants. Prior content
tuning (`chance` 0.30→0.15, `propagate_stage` 1→2, `death_threshold` 0.10→0.15) only slowed
the growth; it never made the population converge, because the *shape* of the weight has no
stable fixed point.

## Options considered
- **(A) Carrying-capacity density weight — CHOSEN.** Replace `1/(1+n)` with
  `max(0, 1 − n/K)`, where `K` = a per-species `carrying_capacity` (target neighbour count
  within the propagation radius). At `n ≥ K` local spawning stops, so established patch
  interiors regulate toward `≈ K` neighbours per propagation-radius circle. Frontier plants
  with `n < K` can still spread into suitable habitat: this is not a global population cap.
  Smallest change; deterministic (each eligible parent still consumes the same three draws);
  preserves the parent-near clustered look and D2 emergence (clusters still form by dispersal);
  the plateau density is a single content knob per species.
- **(B) Spontaneous scattered spawn at a target areal density.** Parent-independent spawning
  on suitable ground toward a target density (flora §1a option b/c). Gives an even-meadow look
  rather than clumps, but is a bigger change (world must sample candidate sites) and re-opens
  the resolved parent-near model. Rejected for now — larger blast radius, changes the visual
  character more than asked.
- **(C) Global hard cap per species.** world refuses spawns beyond a fixed total `N`. Bulletproof
  against 80k but arbitrary: an abrupt, spatially uneven stop wherever the last spawns landed,
  and `N` is a magic global constant rather than an emergent density. Rejected — crude.

## Decision
Adopt **(A)**. `carrying_capacity` is an OPTIONAL integer under the species `propagation:`
block. `K > 0` selects the logistic weight `max(0, 1 − n/K)`; `K = 0`/absent keeps the legacy
`1/(1+n)` (so species and test fixtures that don't set it are byte-unchanged, and flora-off
neutrality holds). Same-species scope for `n` (RESOLVED `NeighborCount` scope, SPEC Open Q).

## K calibration (initial, tuning targets — content, not code)
Equilibrium local density ≈ `K` same-species plants within the propagation-radius circle
(area `π·radius²`). Illustrative starting values (balance is an auto-tuning target):

| species     | radius | K  | ≈ density (plants / unit²) |
|-------------|--------|----|-----------------------------|
| grass       | 3.0    | 6  | 0.21  (bounded tuft field)  |
| tall_grass  | 2.0    | 5  | 0.40  (thick but capped)    |
| dry_shrub   | 4.0    | 4  | 0.08  (sparse scrub)        |
| berry_shrub | 6.0    | 4  | 0.035 (spaced bushes)       |
| oak         | 12.0   | 3  | 0.007 (sparse forest)       |

These are local-density tuning targets, not strict caps or a guarantee that total population
stops growing while suitable frontier remains. Raise/lower `K` to make a species denser or
sparser without touching engine code (D10).

## Invariant / gate notes
- Not a new invariant violation: the weight is still a monotone-decreasing density function of
  `n` (D2 emergence intact); `carrying_capacity` is content data (D4/D10), not a bespoke Go
  function; no future-need field on the object (D9 — `K` is a rule constant, not per-plant
  state); determinism is preserved (D12 — an eligible parent consumes angle+distance+test draws
  regardless of K, and the same seed/config repeats). Different spawn outcomes may change future
  parent sets, so the new and legacy trajectories need not consume identical future streams.
- This resolves the SPEC/plan density-model question for the ACTIVE (post-P_f4) flora only.
  Flora-off phases have no species and are untouched.

## Follow-up (2026-07-13): terrain-dependent density — `carrying_capacity` as a §6 formula
**Motivation.** A flat per-species `K` fills every survivable terrain to the *same* equilibrium
density — terrain (suitability) only changes fill *speed* and death, not the final density. The
user wants density to differ **by terrain** and to read the resulting count **per terrain**.

**Options.** (A) an explicit per-terrain-type map `{terrain_id: K}` (literal, readable);
(B) make `carrying_capacity` a **§6 formula over terrain attrs + climate**, evaluated per site
(mirrors `PropRadius`, which is already a per-site §6 program; new terrains auto-covered);
(C) couple density to the existing suitability (`effK = K·suitability`) — emergent, no authoring.

**Decision — (B).** `carrying_capacity` accepts a scalar **or** a §6 formula. `world` already
injects `SiteInput{TerrainAttrs, Moisture, Temperature}` at each plant; flora evaluates the
formula there, so equilibrium density becomes a data-only function of terrain (D4/D10) with no
new engine mechanism. Semantics: **absent → nil program → legacy `1/(1+n)`**; **present and >0 →
`max(0,1−n/K)`**; **present and ≤0 → density 0** (the species does not establish there — e.g.
deep water when the formula carries a `(1 − depth)` factor). The formula **must not read
`neighbor_count`** (circular — it is the divisor of `n`); `platform/config` rejects that, and
still rejects a **literal negative scalar** (a formula that merely dips ≤0 per-site is fine — it
clamps to 0). expr's numeric context is arithmetic-only (`+ − * /`; comparisons yield Bool and
cannot mix into a number), so per-terrain shaping uses smooth multiplicative penalties, not hard
cutoffs.

**Initial content formulas** (illustrative; tuning targets — operands are the terrain attrs
`slope`/`salinity`/`depth` + climate `moisture`, all ∈ [0,1] so the product is non-negative):

| species     | carrying_capacity formula                              | character                          |
|-------------|--------------------------------------------------------|------------------------------------|
| grass       | `12·moisture·(1−slope)·(1−salinity)·(1−depth)`         | thick on fertile flat fresh soil; 0 on deep water |
| tall_grass  | `10·moisture·(1−slope)·(1−depth)`                      | densest on wet flat ground (marsh edges) |
| dry_shrub   | `6·(1−moisture)·(1−slope)·(1−depth)`                   | dry-loving: dense on arid flats    |
| berry_shrub | `5·moisture·(1−slope)·(1−salinity)·(1−depth)`          | spaced bushes on good ground       |
| oak         | `4·moisture·(1−salinity)·(1−slope)·(1−depth)`          | sparse forest; 0 on water/steep    |
| wildflower  | `6·moisture·(1−slope)·(1−depth)`                       | scattered blooms on moist flats    |

`moisture` is climate-driven, so density now also breathes with weather (drought → sparser) —
a free emergent property (D2). Total count per terrain type is an OBSERVATION deliverable
(a per-terrain flora census over a long run), not a mechanism.

**Prerequisite discovered + fixed: terrain attrs were unfed.** Implementing (B) surfaced that
`world.terrainAttrs` was never populated (`engine/world/hazard.go` noted it as a follow-up), so
`flora.SiteInput.TerrainAttrs` was always empty — every flora §6 that reads `slope`/`salinity`/
`depth` (the existing `suitability` too, not just the new `carrying_capacity`) had silently seen
0, i.e. flora was **moisture-only**. Fixed by carrying the terrain.yaml `attrs` through:
`config.LoadOutput.TerrainAttrs` (built by `buildTerrainAttrs`) → `world.SetTerrainAttrs` (new
setter, mirrors `SetTerrainElevation`) → injected per plant in `floraSiteInputs`. This is plumbing
of an already-designed field (the flora SPEC lists `TerrainAttrs` as world-injected), not a new
mechanism — but it means flora now also **dies/grows by terrain** (grass thins on steep mountain,
etc.), a strict improvement over moisture-only. A per-terrain census
(`worldgen.TestFloraDensityByTerrain`) confirms the gradient: soil densest → sand/river → lake/
bare_rock → mountain → sea ≈ 0 (K→0 via `(1−salinity)·(1−depth)`).
