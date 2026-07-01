package fauna

import (
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
)

// AttackPower evaluates the species' §6 damage magnitude program. Returns 0 when
// absent so non-combat content remains outcome-neutral.
func (r *Rules) AttackPower(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return 0
	}
	sd, ok := r.species[sp]
	if !ok || sd.attackPower == nil {
		return 0
	}
	v := sd.attackPower.EvalNumber(ctx)
	if v < 0 {
		return 0
	}
	return v
}

// Hit evaluates the species' §6 hit multiplier/probability program. Returns 1
// when absent so AttackPower may stand alone for simple content.
func (r *Rules) Hit(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return 1
	}
	sd, ok := r.species[sp]
	if !ok || sd.hit == nil {
		return 1
	}
	v := sd.hit.EvalNumber(ctx)
	if v < 0 {
		return 0
	}
	return v
}

// Feed evaluates the species' §6 carcass feed-value program. Returns 0 when
// absent so non-combat content remains outcome-neutral.
func (r *Rules) Feed(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return 0
	}
	sd, ok := r.species[sp]
	if !ok || sd.feed == nil {
		return 0
	}
	v := sd.feed.EvalNumber(ctx)
	if v < 0 {
		return 0
	}
	return v
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
