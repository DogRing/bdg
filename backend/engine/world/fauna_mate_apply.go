package world

import (
	"math"
	"sort"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// Emergent 2-parent reproduction (PD4 / P_fa4c-2, docs/plans/fauna.md §5).
//
// This runs in the SECOND apply pass, alongside the combat cross-writes and after every animal's
// own-state commit — conception is a cross-animal write (it reads both partners' committed positions
// and writes both cooldowns), so it must not race an own-state commit. Both partners have already
// moved this tick when we measure the distance between them, which is what makes courtship steering
// actually pay off.

// conceptionReach is how close two partners must be for conception (PD4-vi ⓐ RESOLVED: reuse the
// existing engagement proximity rather than adding a species key). It is the same radius that holds
// a combat engagement together, so "close enough to fight" and "close enough to mate" are one
// authored number.
func (w *World) conceptionReach() float64 {
	return w.envCfg.FaunaCombat.DisengageRangeFactor * w.envCfg.ScentCellSize
}

// applyAnimalMate resolves one animal's courtship into a conception, if the pairing is mutual and
// both partners are in range and off cooldown.
//
// MUTUAL CONSENT (PD4-vi ⓒ RESOLVED): the initiator must have picked this partner this tick, AND
// the partner must itself be courting — read off its committed CurrentAction, not off its intent.
// That distinction is load-bearing: partner search only runs in the full pipeline, but herbivores
// spend most ticks on the F45 dormant cheap path and re-arbitrate on ID-STAGGERED phases, so
// "both animals resolved a partner in the same tick" essentially never coincides. Requiring it
// made conception unreachable in a real world (measured: 8000 ticks, animals visibly choosing
// Mate, zero births). Courtship is a state an animal is in, not a single-tick coincidence.
//
// One-sided conception is still refused: a partner whose current action is anything else — hunting,
// fleeing, grazing because it is starving — does not breed. That is what keeps a species' §6
// utility (hunger/fear terms) in control of its own birth rate.
//
// Exactly ONE offspring per pairing: when BOTH animals courted each other in the same tick, only
// the lower ID proceeds, so the outcome cannot depend on apply order (D12).
func (w *World) applyAnimalMate(intent fauna.Intent, byID map[core.ObjectID]fauna.Intent, fork *rng.RNG) {
	partnerID := intent.MateWith
	if partnerID == "" {
		return
	}
	if p, ok := byID[partnerID]; ok && p.MateWith == intent.Animal && intent.Animal > partnerID {
		return // reciprocal this tick: let the lower-ID side conceive, exactly once
	}
	self, other := w.animals[intent.Animal], w.animals[partnerID]
	if self == nil || other == nil {
		return // one of them died earlier in this tick's apply (predation)
	}
	if !w.faunaRules.IsCourting(other.Species, other.CurrentAction) {
		return // the partner is doing something else — no consent, no conception
	}
	if w.tick < self.MateCooldownUntil || w.tick < other.MateCooldownUntil {
		return
	}
	if self.Pos.Distance(other.Pos) > w.conceptionReach() {
		return // courting but not yet close enough — they keep closing next tick
	}

	w.spawnOffspring(self, other, fork)
	self.MateCooldownUntil = w.tick + w.faunaRules.MateCooldown(self.Species)
	other.MateCooldownUntil = w.tick + w.faunaRules.MateCooldown(other.Species)
}

// spawnOffspring creates one newborn from two parents (PD4-iii RESOLVED (i): immediate, adjacent).
//
// The newborn is a copy of the lower-ID parent with everything acquired stripped: Age 0, no drives
// (a missing drive key reads as 0, so it starts sated and unafraid), fresh Vital/Stamina and an
// unscarred VitalCap, no combat/hiding/cooldown state, and no held action. What it keeps is its
// species and its parents' stats, blended.
//
// It is placed exactly at the parent's position — guaranteed passable terrain (the parent is
// standing on it), so no rejection sampling is needed. Space is continuous (D11), so co-located
// animals are legal; the newborn's own steering separates them on the next tick.
func (w *World) spawnOffspring(p1, p2 *fauna.Animal, fork *rng.RNG) {
	child := cloneAnimal(*p1)
	child.ID = w.allocAnimalID()
	child.Age = 0
	child.Stats = w.blendParentStats(p1.Stats, p2.Stats, fork)
	child.Drives = nil
	child.Vital = defaultFreshAnimalVital
	child.VitalCap = defaultFreshAnimalVital
	child.Stamina = defaultFreshAnimalVital
	child.CurrentAction = ""
	child.ActiveUntil = 0
	child.EngagedWith = ""
	child.NextExchangeTick = 0
	child.EngageCooldownUntil = 0
	child.MateCooldownUntil = 0
	child.HiddenUntil = 0
	child.Pos = p1.Pos
	// Seeded facing, for the same reason respawn randomises it: a zero heading is due-east, and a
	// whole generation born facing east marches into the east wall (P_dist3).
	child.Heading = fork.Float64() * 2 * math.Pi

	w.animals[child.ID] = &child
	w.animalIDs = append(w.animalIDs, child.ID)
	sortObjectIDs(w.animalIDs)
	w.spatial.Insert(child.ID, child.Pos)

	// WI-P4/data-contracts §4: AnimalBorn{object_id, species, pos} — the same event respawn emits,
	// so downstream consumers need no new type to see emergent births.
	w.emit.Emit(core.Event{
		SchemaVersion: 1, Tick: w.tick, Type: "AnimalBorn",
		Payload: map[string]any{"object_id": string(child.ID), "species": string(child.Species), "pos": child.Pos},
	})
}

// blendParentStats mixes two parents' base attributes into a newborn's (PD4-iv RESOLVED (a):
// parental blend + variation, weighted by the per-stat `inherit` value already authored in
// content/stats.yaml — this is that parameter's FIRST consumer; it was loaded and validated but
// never read before).
//
// Per stat: draw a seeded lean ∈ [0,1) and blend the two parents by it, then pull the result back
// toward the parental mean by `inherit`:
//
//	raw   = lean·p1 + (1−lean)·p2      // anywhere between the two parents
//	child = mean + (raw − mean)·(1 − inherit)
//
// inherit = 1 ⇒ exactly the parental mean (perfectly faithful heredity);
// inherit = 0 ⇒ anywhere between the parents (heredity washed out by which parent it takes after).
//
// The variation magnitude is therefore the PARENTS' OWN SPREAD — no invented σ, and nothing that
// can drift outside the species' authored range, because the result is always bracketed by the two
// parents. That matters here specifically: fauna fixtures author stats on a 0–100 scale while the
// shared registry declares range [0,1], so anything scaled by the registry's Min/Max or gen.sd
// would be wrong for animals. Staying inside the parental bracket sidesteps that mismatch entirely.
//
// D12: stats are visited in the registry's canonical sorted order and consume exactly one draw
// each, so the same parents under the same seed always produce the same child.
func (w *World) blendParentStats(p1, p2 map[core.StatID]float64, fork *rng.RNG) map[core.StatID]float64 {
	if len(p1) == 0 && len(p2) == 0 {
		return nil
	}
	out := make(map[core.StatID]float64, len(p1))
	for _, id := range w.statIDsForBlend(p1) {
		v1, ok1 := p1[id]
		v2, ok2 := p2[id]
		switch {
		case ok1 && ok2:
			lean := fork.Float64()
			mean := (v1 + v2) / 2
			raw := lean*v1 + (1-lean)*v2
			inherit := 0.0
			if w.svc.Stats != nil {
				if def, ok := w.svc.Stats.Def(id); ok {
					inherit = def.Inherit
				}
			}
			out[id] = mean + (raw-mean)*(1-inherit)
		case ok1:
			out[id] = v1 // only one parent carries it (mixed content): pass it through unchanged
		case ok2:
			out[id] = v2
		}
	}
	return out
}

// statIDsForBlend returns the stat ids to blend, in a fixed order (D12): the registry's canonical
// sorted order when a registry is installed, else the parent's own keys sorted — never a bare map
// range, which would make the draw sequence order-dependent.
func (w *World) statIDsForBlend(p1 map[core.StatID]float64) []core.StatID {
	if w.svc.Stats != nil {
		return w.svc.Stats.IDs()
	}
	ids := make([]core.StatID, 0, len(p1))
	for id := range p1 {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
