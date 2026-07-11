package fauna

import (
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
)

// AttackPower evaluates the species' §6 damage magnitude program. Returns 0 when
// absent so non-combat content remains outcome-neutral.
func (r *Rules) AttackPower(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok || sd.attackPower == nil {
		return scalarZero
	}
	v := sd.attackPower.EvalNumber(ctx)
	if v < scalarZero {
		return scalarZero
	}
	return v
}

// Hit evaluates the species' §6 hit multiplier/probability program. Returns 1
// when absent so AttackPower may stand alone for simple content.
func (r *Rules) Hit(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return scalarOne
	}
	sd, ok := r.species[sp]
	if !ok || sd.hit == nil {
		return scalarOne
	}
	v := sd.hit.EvalNumber(ctx)
	if v < scalarZero {
		return scalarZero
	}
	return v
}

// Feed evaluates the species' §6 carcass feed-value program. Returns 0 when
// absent so non-combat content remains outcome-neutral.
func (r *Rules) Feed(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok || sd.feed == nil {
		return scalarZero
	}
	v := sd.feed.EvalNumber(ctx)
	if v < scalarZero {
		return scalarZero
	}
	return v
}

// Graze evaluates the species' §6 herbivore graze hunger-recovery program (parallels Feed). Returns 0 when
// absent so non-grazing content remains outcome-neutral.
func (r *Rules) Graze(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok || sd.graze == nil {
		return scalarZero
	}
	v := sd.graze.EvalNumber(ctx)
	if v < scalarZero {
		return scalarZero
	}
	return v
}

// Drink evaluates the species' §6 thirst-recovery program (FM4; parallels Graze). Returns 0 when absent so
// non-drinking content remains outcome-neutral (world applies it only when the animal is at drinkable water).
func (r *Rules) Drink(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok || sd.drink == nil {
		return scalarZero
	}
	v := sd.drink.EvalNumber(ctx)
	if v < scalarZero {
		return scalarZero
	}
	return v
}

// HideChance evaluates the species' §6 cover-hide probability program (M3). Returns 0 when absent so a
// species with no hide_chance never hides (OFF-neutral). Parallels Graze/Feed/AttackPower/Hit.
func (r *Rules) HideChance(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok || sd.hideChance == nil {
		return scalarZero
	}
	v := sd.hideChance.EvalNumber(ctx)
	if v < scalarZero {
		return scalarZero
	}
	return v
}

// CoverCost is the species' cover-drag affinity (M4-b): 0 = unaffected by cover
// flora, higher = bogs down more. World reads it in the move commit to scale
// displacement through cover. Pure.
func (r *Rules) CoverCost(sp SpeciesID) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok {
		return scalarZero
	}
	return sd.coverCost
}

// Diet returns the species' diet/target tags in sorted order.
func (r *Rules) Diet(sp SpeciesID) []core.Tag {
	if r == nil {
		return nil
	}
	sd, ok := r.species[sp]
	if !ok {
		return nil
	}
	return cloneTags(sd.diet)
}
