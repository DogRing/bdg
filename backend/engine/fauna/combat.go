package fauna

import (
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
)

const predatorTargetThreat = 1.0

type combatTargetInfo struct {
	id       core.ObjectID
	pos      core.Vec2
	threat   float64
	distance float64
	found    bool
}

type combatIntentState struct {
	target               core.ObjectID
	engagedWith          core.ObjectID
	nextExchangeTick     core.Tick
	engageCooldownUntil  core.Tick
	damage               float64
	targetVitalCapDamage float64
}

func combatTarget(a Animal, snap *Snapshot, rules *Rules, targetRange float64) combatTargetInfo {
	if targetRange <= 0 {
		return combatTargetInfo{}
	}
	diet := rules.Diet(a.Species)
	if len(diet) == 0 {
		return combatTargetInfo{}
	}
	// Engaged: return the specific EngagedWith partner (no diet/range filter — the old code
	// returned it regardless), resolved directly by ID.
	if targetID := a.EngagedWith; targetID != "" {
		if other, ok := animalByID(snap, targetID); ok {
			return combatTargetInfo{id: other.ID, pos: other.Pos, threat: combatThreatOf(rules, other), distance: a.Pos.Distance(other.Pos), found: true}
		}
		return combatTargetInfo{}
	}

	// Free: nearest diet-match within targetRange. This is an argmin with an explicit (distance,
	// then ID) tie-break — order-independent — so no sort is needed (the old per-call
	// sortAnimalsByID allocated + sorted a full copy of every animal on EVERY call: O(N²·logN) +
	// the dominant GC source at large populations, docs/plans/scaling.md P2). Candidates come from
	// a Spatial neighbour query (O(nearby)) when available, else a full Animals scan (ecosim/tests).
	var best combatTargetInfo
	consider := func(other Animal) {
		if other.ID == a.ID {
			return
		}
		dist := a.Pos.Distance(other.Pos)
		if !rules.dietMatches(diet, other.Species) || dist > targetRange {
			return
		}
		if other.HiddenUntil > 0 && other.HiddenUntil >= snap.Tick && dist > snap.Combat.HiddenFlushFactor*snap.ScentCellSize {
			return
		}
		info := combatTargetInfo{id: other.ID, pos: other.Pos, threat: combatThreatOf(rules, other), distance: dist, found: true}
		if !best.found || info.distance < best.distance || (info.distance == best.distance && info.id < best.id) {
			best = info
		}
	}
	if snap.Spatial != nil && snap.ByID != nil {
		for _, e := range snap.Spatial.NearbyEntities(a.Pos, targetRange) {
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

// combatThreatOf is the target's threat weight: nonzero for a predator target (a prey animal
// eyeing a predator), else zero.
func combatThreatOf(rules *Rules, other Animal) float64 {
	if rules.IsPredator(other.Species) {
		return predatorTargetThreat
	}
	return scalarZero
}

// animalByID resolves an animal by ID via the snapshot's ByID index, falling back to a linear
// Animals scan when the index is absent (ecosim/tests).
func animalByID(snap *Snapshot, id core.ObjectID) (Animal, bool) {
	if snap.ByID != nil {
		a, ok := snap.ByID[id]
		return a, ok
	}
	for _, a := range snap.Animals {
		if a.ID == id {
			return a, true
		}
	}
	return Animal{}, false
}

// dietMatches reports whether the TARGET species carries any of the diet tags (D10 tag-driven — a wolf's
// diet [game] matches a deer that carries the `game` content tag). The engine reads the target's kind tags
// from Rules (populated by config from objects.yaml), NOT the SpeciesID, so adding a new prey needs no
// engine change — just the tag. A wolf diet listing a species id (e.g. "deer") still works iff that id is
// also authored as one of the species' tags.
func (r *Rules) dietMatches(diet []core.Tag, targetSp SpeciesID) bool {
	sd, ok := r.species[targetSp]
	if !ok {
		return false
	}
	for _, d := range diet {
		for _, tag := range sd.tags {
			if d == tag {
				return true
			}
		}
	}
	return false
}

func resolveCombat(
	a Animal, target combatTargetInfo, act actions.ActionID,
	snap *Snapshot, rules *Rules, ctx *animalContext, r *rng.RNG,
) combatIntentState {
	state := combatIntentState{
		engagedWith:         a.EngagedWith,
		nextExchangeTick:    a.NextExchangeTick,
		engageCooldownUntil: a.EngageCooldownUntil,
	}
	tag := rules.steerChannelFor(a.Species, act)
	if tag != TagAttack {
		if state.engagedWith != "" && shouldDisengage(a, target, snap.Combat, snap.ScentCellSize) {
			state.engagedWith = ""
			state.nextExchangeTick = core.Tick(0)
		}
		return state
	}
	if state.engagedWith != "" && shouldDisengage(a, target, snap.Combat, snap.ScentCellSize) {
		state.engagedWith = ""
		state.nextExchangeTick = core.Tick(0)
		return state
	}
	if state.engagedWith == "" {
		if !target.found || snap.Tick < a.EngageCooldownUntil {
			return state
		}
		state.target = target.id
		state.engagedWith = target.id
		state.nextExchangeTick = snap.Tick + randTickRange(r, snap.Combat.ExchangeMinTicks, snap.Combat.ExchangeMaxTicks)
		state.engageCooldownUntil = snap.Tick + randTickRange(r, snap.Combat.EngageCooldownMinTicks, snap.Combat.EngageCooldownMaxTicks)
		return state
	}
	state.target = state.engagedWith
	if target.found && snap.Tick >= a.NextExchangeTick {
		state.damage = rules.AttackPower(a.Species, ctx) * rules.Hit(a.Species, ctx)
		if state.damage < scalarZero {
			state.damage = scalarZero
		}
		state.targetVitalCapDamage = state.damage * snap.Combat.VitalCapDamageFraction
		state.nextExchangeTick = snap.Tick + randTickRange(r, snap.Combat.ExchangeMinTicks, snap.Combat.ExchangeMaxTicks)
	}
	return state
}

func shouldDisengage(a Animal, target combatTargetInfo, params CombatParams, cellSize float64) bool {
	if a.Stamina <= params.StaminaDropThreshold {
		return true
	}
	if !target.found {
		return true
	}
	return target.distance > params.DisengageRangeFactor*cellSize
}

func randTickRange(r *rng.RNG, minTicks, maxTicks int) core.Tick {
	if maxTicks <= minTicks {
		return core.Tick(minTicks)
	}
	return core.Tick(minTicks + r.Intn(maxTicks-minTicks+1))
}

func effectiveVitalCap(a Animal) float64 {
	if a.VitalCap <= scalarZero || a.VitalCap > scalarOne {
		return scalarOne
	}
	return a.VitalCap
}

func regenVital(a Animal, dt float64, params CombatParams) float64 {
	cap := effectiveVitalCap(a)
	v := a.Vital + params.VitalRegenPerTick*dt
	if v > cap {
		return cap
	}
	if v < scalarZero {
		return scalarZero
	}
	return v
}

// nextVital is the proposed self Vital each tick: the free regen (regenVital) MINUS the PD3 starvation
// bleed (starveDrain) and the PD11 old-age bleed (senescenceDrain) — sign-symmetric (docs/plans/fauna.md
// §5, P_fa4b; §7 aging). Net is negative only while a
// coupled drive (hunger) is saturated and its VitalDrain exceeds VitalRegenPerTick, so a fed animal never
// loses Vital. Reads start-of-tick a.Drives (parallels regenVital reading a.Vital). Clamped ≥ 0; the world
// removes the animal when Vital ≤ 0 (death is world-owned, F3). With no drive authoring VitalDrain,
// starveDrain ≡ 0 ⇒ nextVital ≡ regenVital (byte-identical off-lever). Pure, no RNG.
func nextVital(a Animal, rules *Rules, dt float64, params CombatParams) float64 {
	v := regenVital(a, dt, params)
	// Two non-combat bleed channels share this path: acute drive saturation (PD3 starvation) and old age
	// (PD11 senescence). They SUM rather than override — a starving elder dies faster than either alone,
	// which is the honest composition and needs no interaction rule.
	drain := rules.starveDrain(a.Species, a.Drives, dt) + rules.senescenceDrain(a.Species, a.Age, dt)
	if drain > scalarZero {
		v -= drain
		if v < scalarZero {
			v = scalarZero
		}
	}
	return v
}

// nextStamina drains stamina per tick while ENGAGED in combat and recovers it while free (FC6 / scenario
// #8 — "the predator stops when its stamina drops first"). Once stamina falls to StaminaDropThreshold the
// existing resolveCombat disengage fires; it then recovers and can re-engage → burst-style pursuit. Rates
// are balance data (CombatParams); zero ⇒ neutral (stamina unchanged, as before).
func nextStamina(a Animal, engaged bool, dt float64, params CombatParams) float64 {
	s := a.Stamina
	if engaged {
		s -= params.StaminaDrainPerTick * dt
	} else {
		s += params.StaminaRecoverPerTick * dt
	}
	if s > scalarOne {
		return scalarOne
	}
	if s < scalarZero {
		return scalarZero
	}
	return s
}
