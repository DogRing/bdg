package world

import (
	"sort"

	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
)

func (w *World) commitAnimalVital(a *fauna.Animal, intent fauna.Intent) {
	if intent.Vital == 0 && intent.VitalCap == 0 && a.Vital > 0 {
		return
	}
	a.Vital = clamp01(intent.Vital)
	a.VitalCap = clamp01(intent.VitalCap)
}

func (w *World) isAttackIntent(intent fauna.Intent) bool {
	return intent.Target != "" && (intent.Damage > 0 || intent.TargetVitalCapDamage > 0 || w.actionHasTag(intent.Action, fauna.TagAttack))
}

func (w *World) applyAnimalAttack(attacker *fauna.Animal, intent fauna.Intent) {
	if attacker == nil || intent.Target == "" {
		return
	}
	target := w.animals[intent.Target]
	if target == nil {
		return
	}
	target.Vital = clamp01(target.Vital - intent.Damage)
	target.VitalCap = clamp01(target.VitalCap - intent.TargetVitalCapDamage)
	if target.Vital > target.VitalCap {
		target.Vital = target.VitalCap
	}
	attacker.EngagedWith = target.ID
	target.EngagedWith = attacker.ID
	target.NextExchangeTick = intent.NextExchangeTick
	target.EngageCooldownUntil = intent.EngageCooldownUntil
	if target.Vital <= 0 {
		w.killAnimal(target.ID)
	}
}

func (w *World) applyAnimalFeed(predator *fauna.Animal, intent fauna.Intent) {
	if predator == nil {
		return
	}
	id, ok := w.resolveFeedCarcass(predator, intent.Target)
	if !ok {
		return
	}
	supply := w.carcassSupply(id)
	if supply <= 0 {
		return
	}
	mult := w.faunaRules.Feed(predator.Species, animalFeedContext{animal: predator})
	if mult <= 0 {
		return
	}
	if predator.Drives == nil {
		predator.Drives = make(map[fauna.DriveID]float64)
	}
	predator.Drives["hunger"] = clamp01(predator.Drives["hunger"] - supply*mult)
	w.consumeCarcass(id)
}

func (w *World) resolveFeedCarcass(predator *fauna.Animal, preferred core.ObjectID) (core.ObjectID, bool) {
	if preferred != "" && w.isCarcassObject(preferred) {
		return preferred, true
	}
	for _, id := range w.objectIDs {
		if !w.isCarcassObject(id) {
			continue
		}
		obj := w.objects[id]
		if predator.Pos.Distance(obj.Pos) <= w.envCfg.ScentCellSize {
			return id, true
		}
	}
	return "", false
}

func (w *World) isCarcassObject(id core.ObjectID) bool {
	obj, ok := w.objects[id]
	return ok && obj.Kind == "carcass"
}

func (w *World) carcassSupply(id core.ObjectID) float64 {
	if w.decayState != nil && w.decayRules != nil {
		for _, lot := range w.decayState.Lots() {
			if lot.ID != id {
				continue
			}
			state := w.decayRules.StateAt(lot.Kind, lot.DecayAge)
			if supply := sumSupply(w.decayRules.SupplyAt(lot.Kind, state)); supply > 0 {
				return supply * float64(lot.Qty)
			}
			break
		}
	}
	if obj, ok := w.objects[id]; ok {
		return sumSupply(obj.Supply)
	}
	return 0
}

func (w *World) consumeCarcass(id core.ObjectID) {
	w.RemoveObject(id)
	delete(w.decayLotPos, id)
	delete(w.decayStorageMult, id)
	if w.decayState == nil {
		return
	}
	lots := w.decayState.Lots()
	next := make([]decay.Lot, 0, len(lots))
	for _, lot := range lots {
		if lot.ID == id {
			continue
		}
		next = append(next, lot)
	}
	w.decayState = decay.New(next)
}

func sumSupply(supply map[core.Dimension]float64) float64 {
	if len(supply) == 0 {
		return 0
	}
	dims := make([]core.Dimension, 0, len(supply))
	for dim := range supply {
		dims = append(dims, dim)
	}
	sort.Slice(dims, func(i, j int) bool { return dims[i] < dims[j] })
	var total float64
	for _, dim := range dims {
		if supply[dim] > 0 {
			total += supply[dim]
		}
	}
	return total
}

func (w *World) killAnimal(id core.ObjectID) {
	a := w.animals[id]
	if a == nil {
		return
	}
	pos := a.Pos
	species := a.Species
	w.removeAnimal(id)
	w.spawnCarcass(pos)
	w.emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          w.tick,
		Type:          "AnimalDied",
		Payload: map[string]any{
			"id":      string(id),
			"species": string(species),
		},
	})
}

func (w *World) spawnCarcass(pos core.Vec2) core.ObjectID {
	id := w.allocObjectID()
	w.PlaceObject(id, "carcass", pos, nil)
	lot := decay.Lot{ID: id, Kind: "carcass", Qty: 1}
	if w.decayState == nil {
		w.decayState = decay.New([]decay.Lot{lot})
	} else {
		w.decayState = w.decayState.WithLot(lot)
	}
	w.decayLotPos[id] = pos
	return id
}

type animalFeedContext struct {
	animal *fauna.Animal
}

func (c animalFeedContext) Stat(id core.StatID) float64 {
	if c.animal == nil {
		return 0
	}
	return c.animal.Stats[id]
}

func (c animalFeedContext) Attr(name core.Tag) (float64, bool) {
	if c.animal == nil {
		return 0, false
	}
	if v, ok := c.animal.Drives[fauna.DriveID(name)]; ok {
		return v, true
	}
	return 0, false
}

func (c animalFeedContext) Pred(string, core.Tag) bool { return false }
