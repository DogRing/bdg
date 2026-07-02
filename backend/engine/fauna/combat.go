package fauna

import (
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
)

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
	targetID := a.EngagedWith
	animals := sortAnimalsByID(snap.Animals)
	var best combatTargetInfo
	for _, other := range animals {
		if other.ID == a.ID {
			continue
		}
		dist := a.Pos.Distance(other.Pos)
		if targetID != "" && other.ID != targetID {
			continue
		}
		if targetID == "" && (!rules.dietMatches(diet, other.Species) || dist > targetRange) {
			continue
		}
		if targetID == "" && other.HiddenUntil > 0 && other.HiddenUntil >= snap.Tick && dist > snap.Combat.HiddenFlushFactor*snap.ScentCellSize {
			continue
		}
		threat := 0.0
		if rules.IsPredator(other.Species) {
			threat = 1.0
		}
		info := combatTargetInfo{id: other.ID, pos: other.Pos, threat: threat, distance: dist, found: true}
		if targetID != "" {
			return info
		}
		if !best.found || info.distance < best.distance || (info.distance == best.distance && info.id < best.id) {
			best = info
		}
	}
	return best
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
			state.nextExchangeTick = 0
		}
		return state
	}
	if state.engagedWith != "" && shouldDisengage(a, target, snap.Combat, snap.ScentCellSize) {
		state.engagedWith = ""
		state.nextExchangeTick = 0
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
		if state.damage < 0 {
			state.damage = 0
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
	if a.VitalCap <= 0 || a.VitalCap > 1 {
		return 1
	}
	return a.VitalCap
}

func regenVital(a Animal, dt float64, params CombatParams) float64 {
	cap := effectiveVitalCap(a)
	v := a.Vital + params.VitalRegenPerTick*dt
	if v > cap {
		return cap
	}
	if v < 0 {
		return 0
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
	if s > 1 {
		return 1
	}
	if s < 0 {
		return 0
	}
	return s
}
