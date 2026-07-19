package fauna

import "github.com/dogring/bdg/engine/kernel/core"

// Mate seeking (PD4-i / P_fa4c-2, docs/plans/fauna.md §5).
//
// Partner search deliberately reuses the combatTarget machine rather than adding a scent channel
// (PD4-i RESOLVED (i)): mating is a PROXIMITY event, so a spatial-hash neighbour query is both the
// cheap and the behaviourally right answer — no new scent deposit, no new grid, nothing extra to
// spread or commit each tick. The shape is identical to combatTarget: an argmin with an explicit
// (distance, then ID) tie-break, so the result never depends on iteration order (D12).

// containsAttr reports whether a compiled program's operand list mentions attr.
func containsAttr(attrs []core.Tag, want core.Tag) bool {
	for _, a := range attrs {
		if a == want {
			return true
		}
	}
	return false
}

// kinCount counts same-species animals within the animal's sight radius — the `kin_count` §6
// operand (PD4-v). It is the soft crowding backstop on breeding: a species writes
// `- kin_count * w` into its Mate utility, so a crowded animal courts less. Deliberately a raw
// COUNT rather than a normalised "density": normalising would need a divisor the engine would have
// to invent, whereas content can divide in §6 (D10). Same reason dist.*/scent.* expose raw magnitudes.
//
// This is a HARD CAP ON NOTHING — it lowers a utility, it does not gate conception. The population
// ceiling proper is meant to come from food scarcity (the `(1 - hunger)` term every Mate utility
// already carries); this only keeps a local crowd from compounding while that loop does its work.
//
// Cost: one spatial query per scoring animal, so it is skipped entirely unless the species actually
// reads the operand (Rules.readsKinCount), keeping it free for every species that does not.
func kinCount(a Animal, snap *Snapshot, radius float64) float64 {
	if radius <= scalarZero {
		return scalarZero
	}
	n := 0
	consider := func(other Animal) {
		if other.ID != a.ID && other.Species == a.Species && a.Pos.Distance(other.Pos) <= radius {
			n++
		}
	}
	if snap.Spatial != nil && snap.ByID != nil {
		for _, e := range snap.Spatial.NearbyEntities(a.Pos, radius) {
			if other, ok := snap.ByID[e.ID]; ok {
				consider(other)
			}
		}
	} else {
		for _, other := range snap.Animals {
			consider(other)
		}
	}
	return float64(n)
}

// mateTargetInfo is the resolved partner for one animal this tick.
type mateTargetInfo struct {
	id       core.ObjectID
	pos      core.Vec2
	distance float64
	found    bool
}

// mateEligible reports whether `other` may be conceived with by `self` right now: a DIFFERENT
// animal of the SAME species, sexually mature, and past its post-conception refractory window.
//
// There is deliberately no sex/gender field (D2/D10): the emergent model is "two mature adults of
// one species", and the cooldown is what paces breeding. Adding sexes would be a content+schema
// extension, not a behaviour the engine should assume.
func mateEligible(self, other Animal, snap *Snapshot, rules *Rules) bool {
	if other.ID == self.ID || other.Species != self.Species {
		return false
	}
	if rules.maturity(other.Species, other.Age) < scalarOne {
		return false
	}
	return snap.Tick >= other.MateCooldownUntil
}

// mateTarget returns the nearest eligible partner within searchRange, or a zero info when the
// animal itself is not ready or nobody qualifies. Pure: no RNG, no mutation.
//
// Candidates come from the spatial neighbour query when the snapshot carries one (O(nearby)),
// falling back to a full Animals scan for the arena/test snapshots that have no index — the same
// two-path structure combatTarget uses, so both callers scale identically (docs/plans/scaling.md P2).
func mateTarget(a Animal, snap *Snapshot, rules *Rules, searchRange float64) mateTargetInfo {
	if searchRange <= scalarZero {
		return mateTargetInfo{}
	}
	// A self that is immature or still in its refractory window seeks nobody — checking here keeps
	// the caller (and the steer channel) free of eligibility logic.
	if rules.maturity(a.Species, a.Age) < scalarOne || snap.Tick < a.MateCooldownUntil {
		return mateTargetInfo{}
	}

	var best mateTargetInfo
	consider := func(other Animal) {
		if !mateEligible(a, other, snap, rules) {
			return
		}
		dist := a.Pos.Distance(other.Pos)
		if dist > searchRange {
			return
		}
		info := mateTargetInfo{id: other.ID, pos: other.Pos, distance: dist, found: true}
		if !best.found || info.distance < best.distance || (info.distance == best.distance && info.id < best.id) {
			best = info
		}
	}
	if snap.Spatial != nil && snap.ByID != nil {
		for _, e := range snap.Spatial.NearbyEntities(a.Pos, searchRange) {
			if other, ok := snap.ByID[e.ID]; ok { // ok=false ⇒ a non-animal (object/agent) neighbour
				consider(other)
			}
		}
	} else {
		for _, other := range snap.Animals {
			consider(other)
		}
	}
	return best
}

// readsKinCount reports whether the species' §6 utilities mention `kin_count`, answered once at
// NewRules. Gates the per-animal crowding query so it costs nothing for species that never use it.
func (r *Rules) readsKinCount(sp SpeciesID) bool {
	if r == nil {
		return false
	}
	sd, ok := r.species[sp]
	return ok && sd.readsKinCount
}
