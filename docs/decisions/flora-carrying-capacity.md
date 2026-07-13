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
