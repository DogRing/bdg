package flora

import (
	"math"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// Step advances the whole flora field ONE flora step (= N ticks; cadence is world's).
//
// Pure function of (prev, inputs, rules, idAlloc, rng): does NOT mutate prev, inputs,
// Rules, or idAlloc's state beyond calling it; returns a fresh next State and StepDeltas.
//
// D12 iteration order: sorted ObjectID order for all phases.
//
// RNG draw order within Step (FIXED — D12 byte-stable):
//
//	Growth phase: no RNG draws (suitability × rate is deterministic).
//	Propagation phase: for each SURVIVOR in sorted ID order (mature only):
//	  1. Float64 → angle in [0, 2π) for candidate child position
//	  2. Float64 → distance fraction in [0, 1) × PropRadius for candidate position
//	  3. Float64 → spawn test against (PropChance × suitability × densityWeight)
//	  idAlloc() called only on a positive spawn test (sorted parent order → reproducible IDs).
//	Immature survivors (Stage < PropagateStage) are skipped with NO RNG draws (D12).
//
// Density-weighting functional form (1k — monotone decreasing in NeighborCount):
//
//	K>0 (CarryingCapacity):  densityWeight(n) = max(0, 1 − n/K)   → 0 at n=K (established
//	                         patch interiors regulate toward density ≈K; not a global cap)
//	K=0 (legacy):            densityWeight(n) = 1 / (1 + n)       → never 0 (no equilibrium)
//
// A crowded site propagates less than an open site either way; only K>0 imposes a stable
// local density. K changes the spawn-test OUTCOME, never the three draws consumed by an
// eligible parent in this Step. Later Steps may contain different parent sets; D12 guarantees
// repeatability for the same seed/config, not legacy-trajectory identity.
//
// Missing SiteInput for a live plant panics (world-contract guard, mirrors navmap unknown-id).
// Empty/nil rules = flora-off: no growth, no propagation, no death; StepDeltas is empty;
// next State plants are unchanged copies of prev (RESOLVED 1g neutrality).
func Step(
	prev *State,
	inputs map[core.ObjectID]SiteInput,
	rules *Rules,
	idAlloc func() core.ObjectID,
	r *rng.RNG,
) (next *State, deltas StepDeltas) {
	// ── Phase 1: Growth + death hysteresis (sorted ID order; D12) ────────────
	// workMap accumulates survivors. Plants in deltas.Died are excluded.
	workMap := make(map[core.ObjectID]Plant, len(prev.plants))

	for _, p := range prev.plants { // already sorted by ID (D12 invariant of State)
		// Panic if world failed to sample the environment at this plant's position.
		in, ok := inputs[p.ID]
		if !ok {
			panic("flora.Step: missing SiteInput for plant " + string(p.ID))
		}

		// flora-off: no species programs → plant is unchanged, always survives.
		if rules == nil || len(rules.bySpecies) == 0 {
			workMap[p.ID] = p
			continue
		}
		sr, hasSR := rules.bySpecies[p.Species]
		if !hasSR {
			// Unknown species in this Rules set (platform/config validates at load).
			// Treat as flora-off for this individual plant.
			workMap[p.ID] = p
			continue
		}

		ctx := floraContext{site: in, plant: p}

		// Suitability drives BOTH growth axes and death hysteresis; clamped ∈ [0,1].
		suit := evalNum(sr.Suitability, ctx)
		if suit < probabilityMin {
			suit = probabilityMin
		} else if suit > probabilityMax {
			suit = probabilityMax
		}

		// Two-axis growth (RESOLVED §1 refinement): each axis integrates independently.
		// Length += LengthRate·suit ; Width += WidthRate·suit ; both clamped ≥ 0.
		// Float accumulation order: Length first, then Width (fixed for D12).
		newLength := p.Length + evalNum(sr.LengthRate, ctx)*suit
		if newLength < nonNegativeFloor {
			newLength = nonNegativeFloor
		}
		newWidth := p.Width + evalNum(sr.WidthRate, ctx)*suit
		if newWidth < nonNegativeFloor {
			newWidth = nonNegativeFloor
		}

		// Death hysteresis (RESOLVED 1b option a):
		//   suit < θ → increment DeathStreak
		//   suit ≥ θ → reset DeathStreak to 0 (temporary bad weather does NOT flicker forests out)
		newStreak := p.DeathStreak
		if suit < sr.DeathThreshold {
			newStreak++
		} else {
			newStreak = 0
		}

		// Object mortality: streak reaches hysteresis span → Died (§7).
		// Guard DeathHysteresis > 0: a zero value means "death disabled" for this species.
		if sr.DeathHysteresis > 0 && newStreak >= sr.DeathHysteresis {
			deltas.Died = append(deltas.Died, p.ID)
			continue // do NOT add to workMap: plant is removed from next State
		}

		// Survivor: record updated morphology.
		updated := Plant{
			ID:          p.ID,
			Species:     p.Species,
			Pos:         p.Pos,
			Length:      newLength,
			Width:       newWidth,
			DeathStreak: newStreak,
			Owner:       p.Owner, // RESOLVED 1f: inert in P1 — copy unchanged
		}
		workMap[updated.ID] = updated

		// Emit GrowthDelta only when morphology actually changed.
		if newLength != p.Length || newWidth != p.Width || newStreak != p.DeathStreak {
			deltas.Grown = append(deltas.Grown, GrowthDelta{
				ID:          updated.ID,
				Length:      newLength,
				Width:       newWidth,
				DeathStreak: newStreak,
			})
		}
	}

	// ── Phase 2: Propagation — seed dispersal near parent (RESOLVED 1a) ──────
	// Only runs when Rules is non-empty. Iterates SURVIVORS in sorted ID order (D12).
	if rules != nil && len(rules.bySpecies) > 0 {
		// Extract and sort survivors (workMap was built by iterating sorted prev.plants;
		// new entries from potential prior spawns do not exist at this stage; workMap
		// contains only survivors from Phase 1). Sorting ensures D12 RNG draw order.
		survivors := make([]Plant, 0, len(workMap))
		for _, p := range workMap {
			survivors = append(survivors, p)
		}
		sort.Slice(survivors, func(i, j int) bool {
			return survivors[i].ID < survivors[j].ID
		})

		for _, p := range survivors {
			sr, hasSR := rules.bySpecies[p.Species]
			if !hasSR {
				continue
			}

			// Stage is DERIVED from Length — never stored (D9/RESOLVED §1 refinement).
			// Inline the threshold walk to avoid calling r.Stage (avoids an extra map lookup).
			stage := 0
			for i, thr := range sr.Stages {
				if p.Length >= thr {
					stage = i + 1
				}
			}
			// Immature: skip with NO RNG draws (D12 — consuming draws for a no-op would
			// shift the sequence for all subsequent parents).
			if stage < sr.PropagateStage {
				continue
			}

			in := inputs[p.ID] // already validated in Phase 1
			ctx := floraContext{site: in, plant: p}

			// Evaluate propagation radius (may depend on site conditions).
			propRadius := evalNum(sr.PropRadius, ctx)

			// RNG draw 1: angle (uniform [0, 2π)) — determines direction of seed travel.
			angle := r.Float64() * fullCircleRadians
			// RNG draw 2: distance fraction (uniform [0, 1)) × PropRadius.
			// Documented choice: uniform in [0, PropRadius] (not sqrt-corrected for circle
			// uniformity). Gives higher density near parent; simpler + still within PropRadius.
			dist := r.Float64() * propRadius

			childPos := core.Vec2{
				X: p.Pos.X + dist*math.Cos(angle),
				Y: p.Pos.Y + dist*math.Sin(angle),
			}

			// Spawn probability: PropChance × suitability × densityWeight(NeighborCount).
			// Documented density-weighting: 1/(1+n), monotone decreasing (see package doc).
			suit := evalNum(sr.Suitability, ctx)
			if suit < probabilityMin {
				suit = probabilityMin
			} else if suit > probabilityMax {
				suit = probabilityMax
			}
			propChance := evalNum(sr.PropChance, ctx)
			dw := densityWeight(in.NeighborCount, sr.CarryingCapacity)
			spawnProb := propChance * suit * dw
			if spawnProb < probabilityMin {
				spawnProb = probabilityMin
			} else if spawnProb > probabilityMax {
				spawnProb = probabilityMax
			}

			// RNG draw 3: spawn test.
			if r.Float64() < spawnProb {
				// idAlloc called in sorted parent order → reproducible ID assignment.
				child := Plant{
					ID:          idAlloc(),
					Species:     p.Species,
					Pos:         childPos,
					Length:      seedlingLength,
					Width:       seedlingWidth,
					DeathStreak: freshDeathStreak,
					Owner:       "", // wild (RESOLVED 1f)
				}
				workMap[child.ID] = child
				deltas.Spawned = append(deltas.Spawned, child)
			}
		}
	}

	// ── Build next State (sorted by ID, D12) ─────────────────────────────────
	nextPlants := make([]Plant, 0, len(workMap))
	for _, p := range workMap {
		nextPlants = append(nextPlants, p)
	}
	sort.Slice(nextPlants, func(i, j int) bool {
		return nextPlants[i].ID < nextPlants[j].ID
	})
	nextIdx := make(map[core.ObjectID]Plant, len(nextPlants))
	for _, p := range nextPlants {
		nextIdx[p.ID] = p
	}

	// Sort all delta slices (D12 — output is always in ascending ObjectID order).
	// Grown and Died are already in sorted order (iterated from sorted prev.plants),
	// but we sort unconditionally for correctness under all future refactors.
	sort.Slice(deltas.Died, func(i, j int) bool {
		return deltas.Died[i] < deltas.Died[j]
	})
	sort.Slice(deltas.Grown, func(i, j int) bool {
		return deltas.Grown[i].ID < deltas.Grown[j].ID
	})
	sort.Slice(deltas.Spawned, func(i, j int) bool {
		return deltas.Spawned[i].ID < deltas.Spawned[j].ID
	})

	return &State{
		plants: nextPlants,
		idx:    nextIdx,
		rules:  rules, // carry rules into next State for lazy ShadeOf evaluation
	}, deltas
}
