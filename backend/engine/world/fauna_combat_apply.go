package world

import (
	"sort"

	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
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
	attacker.HiddenUntil = 0
	target.HiddenUntil = 0
	target.NextExchangeTick = intent.NextExchangeTick
	target.EngageCooldownUntil = intent.EngageCooldownUntil
	if target.Vital <= 0 {
		// The killer eats its fresh kill immediately: the carcass Feed action rarely fires in time because
		// the predator resumes hunting the next prey before the carrion scent registers, so a successful
		// KILL directly nourishes the attacker (hunger ↓ by its feed §6). The carcass still forms for
		// scavengers (FC8/FC9). Data-driven (D10 via feed §6); no-op if the attacker has no hunger drive.
		if attacker.Drives != nil {
			if _, ok := attacker.Drives["hunger"]; ok {
				gain := w.faunaRules.Feed(attacker.Species, animalFeedContext{animal: attacker})
				attacker.Drives["hunger"] = clamp01(attacker.Drives["hunger"] - gain)
			}
		}
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

// applyAnimalGraze is the herbivore analog of applyAnimalFeed: when a grazing animal (a seek:food action)
// has reached an edible-flora source, it crops it — reducing hunger by the species' §6 graze value. The
// food source must be within reach (one scent cell) so the deer only eats where food actually is (mirrors
// Feed requiring a nearby carcass). Flora is not depleted in P1.
func (w *World) applyAnimalGraze(a *fauna.Animal) {
	if a == nil || !w.nearForageFlora(a) {
		return
	}
	mult := w.faunaRules.Graze(a.Species, animalFeedContext{animal: a})
	if mult <= 0 {
		return
	}
	if a.Drives == nil {
		a.Drives = make(map[fauna.DriveID]float64)
	}
	a.Drives["hunger"] = clamp01(a.Drives["hunger"] - mult)
}

func (w *World) applyAnimalHiding(a *fauna.Animal, hideRNG *rng.RNG) {
	if a == nil || w.faunaRules == nil || hideRNG == nil {
		return
	}
	if w.faunaRules.IsPredator(a.Species) {
		return
	}
	if a.EngagedWith != "" || (a.HiddenUntil > 0 && a.HiddenUntil >= w.tick) {
		return
	}
	if !w.actionHasTag(a.CurrentAction, fauna.TagFleePred) {
		return
	}
	if !w.nearCoverFlora(a) {
		return
	}
	chance := w.faunaRules.HideChance(a.Species, animalFeedContext{animal: a})
	if chance <= 0 {
		return
	}
	if hideRNG.Float64() < chance {
		a.HiddenUntil = w.tick + core.Tick(w.envCfg.FaunaCombat.HideDurationTicks)
	}
}

// nearForageFlora reports whether an edible-flora object (a scent:food emitter) is within grazing reach.
func (w *World) nearForageFlora(a *fauna.Animal) bool {
	reach := w.envCfg.ScentCellSize
	for _, id := range w.objectIDs {
		obj, ok := w.objects[id]
		if !ok || !w.kindEmitsFood(obj.Kind) {
			continue
		}
		if a.Pos.Distance(obj.Pos) <= reach {
			return true
		}
	}
	return false
}

func (w *World) nearCoverFlora(a *fauna.Animal) bool {
	reach := w.envCfg.FaunaCombat.HideCoverFactor * w.envCfg.ScentCellSize
	if reach <= 0 {
		return false
	}
	for _, id := range w.objectIDs {
		obj, ok := w.objects[id]
		if !ok || !w.kindIsCover(obj.Kind) {
			continue
		}
		if a.Pos.Distance(obj.Pos) <= reach {
			return true
		}
	}
	return false
}

func (w *World) kindEmitsFood(kind core.Tag) bool {
	for _, tag := range w.scentEmitters[kind] {
		if tag == "scent:food" {
			return true
		}
	}
	return false
}

func (w *World) kindIsCover(kind core.Tag) bool {
	return w.coverKinds[kind]
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
