package world

import (
	"sort"

	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/spatial"
)

func (w *World) commitAnimalVital(a *fauna.Animal, intent fauna.Intent) {
	if intent.Vital == zeroScalar && intent.VitalCap == zeroScalar && a.Vital > zeroScalar {
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
	if target.Vital <= zeroScalar {
		// The killer eats its fresh kill immediately: the carcass Feed action rarely fires in time because
		// the predator resumes hunting the next prey before the carrion scent registers, so a successful
		// KILL directly nourishes the attacker (hunger ↓ by its feed §6). The carcass still forms for
		// scavengers (FC8/FC9). Data-driven (D10 via feed §6); no-op if the attacker has no hunger drive.
		if attacker.Drives != nil {
			if _, ok := attacker.Drives[driveHunger]; ok {
				gain := w.faunaRules.Feed(attacker.Species, animalFeedContext{animal: attacker})
				attacker.Drives[driveHunger] = clamp01(attacker.Drives[driveHunger] - gain)
			}
		}
		w.killAnimal(target.ID, causePredation)
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
	if supply <= zeroScalar {
		return
	}
	mult := w.faunaRules.Feed(predator.Species, animalFeedContext{animal: predator})
	if mult <= zeroScalar {
		return
	}
	if predator.Drives == nil {
		predator.Drives = make(map[fauna.DriveID]float64)
	}
	predator.Drives[driveHunger] = clamp01(predator.Drives[driveHunger] - supply*mult)
	w.consumeCarcass(id)
}

// applyAnimalGraze is the herbivore analog of applyAnimalFeed: when a grazing animal (a seek:food action)
// has reached an edible-flora source, it crops it — reducing hunger by the species' §6 graze value. The
// food source must be within reach (one scent cell) so the deer only eats where food actually is (mirrors
// Feed requiring a nearby carcass).
//
// PD2 flora depletion (P_fa4b, docs/plans/fauna.md §5): the grazed flora PLANT loses biomass (Length) in
// proportion to what is actually eaten, and the recovery scales with the biomass AVAILABLE — so an
// overgrazed (short) plant feeds a herbivore less, driving local famine + migration pressure. GrazeDepletion
// (k, flora-Length per 1.0 hunger) is the balance lever; k ≤ 0 (or a non-flora food source) ⇒ recover the
// full §6 value and deplete nothing (byte-identical to pre-P_fa4b).
func (w *World) applyAnimalGraze(a *fauna.Animal) {
	if a == nil {
		return
	}
	plantID, isPlant := w.nearestForageFloraID(a)
	if plantID == "" {
		return
	}
	mult := w.faunaRules.Graze(a.Species, animalFeedContext{animal: a})
	if mult <= zeroScalar {
		return
	}
	if a.Drives == nil {
		a.Drives = make(map[fauna.DriveID]float64)
	}
	k := w.envCfg.FaunaCombat.GrazeDepletion
	if k <= zeroScalar || !isPlant || w.floraState == nil {
		a.Drives[driveHunger] = clamp01(a.Drives[driveHunger] - mult)
		return
	}
	removed := w.floraState.GrazeLength(plantID, mult*k)
	a.Drives[driveHunger] = clamp01(a.Drives[driveHunger] - removed/k)
}

// applyAnimalDrink (FM4): a thirsty animal standing on a DRINKABLE-water cell recovers thirst by the
// species' §6 `drink` factor — parallels applyAnimalGraze (hunger↓ at forage). No-op off water, for a
// species that tracks no `thirst` drive, or one authoring no `drink` program (mult 0). The terrain lookup
// is a deterministic CellOf index read (D11); the drinkable set is config-derived (FM4-src).
func (w *World) applyAnimalDrink(a *fauna.Animal) {
	if a == nil || a.Drives == nil || w.nav == nil || w.faunaRules == nil {
		return
	}
	if _, ok := a.Drives[driveThirst]; !ok {
		return
	}
	if len(w.envCfg.DrinkableTerrains) == 0 || !w.envCfg.DrinkableTerrains[w.nav.TerrainAt(w.nav.CellOf(a.Pos))] {
		return
	}
	mult := w.faunaRules.Drink(a.Species, animalFeedContext{animal: a})
	if mult <= zeroScalar {
		return
	}
	a.Drives[driveThirst] = clamp01(a.Drives[driveThirst] - mult)
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
	if chance <= zeroScalar {
		return
	}
	if hideRNG.Float64() < chance {
		a.HiddenUntil = w.tick + core.Tick(w.envCfg.FaunaCombat.HideDurationTicks)
	}
}

// nearestForageFloraID returns the nearest food-emitting object within grazing reach in deterministic
// object-ID order (strictly-closer wins; on a distance tie the lower ID wins — independent of the spatial
// hash's return order, D12), and whether that object is a live flora plant (present in floraState — the
// PD2 depletion target). "" ⇒ nothing edible in reach (the graze no-op gate, matching the old within-reach
// existence test). Queries the spatial hash near the animal rather than scanning every object (the O(all
// objects) scan per animal was a top per-tick cost at large flora counts, docs/plans/scaling.md P2).
func (w *World) nearestForageFloraID(a *fauna.Animal) (core.ObjectID, bool) {
	if a == nil {
		return "", false
	}
	reach := w.envCfg.ScentCellSize
	var nearest core.ObjectID
	nearestDistance := reach
	for _, e := range w.spatial.NearbyEntities(a.Pos, reach) {
		obj, ok := w.objects[e.ID]
		if !ok || !w.kindEmitsFood(obj.Kind) {
			continue
		}
		distance := a.Pos.Distance(obj.Pos)
		if distance > nearestDistance {
			continue
		}
		if nearest == "" || distance < nearestDistance || (distance == nearestDistance && e.ID < nearest) {
			nearest, nearestDistance = e.ID, distance
		}
	}
	if nearest == "" {
		return "", false
	}
	isPlant := false
	if w.floraState != nil {
		_, isPlant = w.floraState.PlantByID(nearest)
	}
	return nearest, isPlant
}

func (w *World) nearCoverFlora(a *fauna.Animal) bool {
	return w.nearestCoverFloraID(a) != ""
}

// nearestCoverFloraID returns the first nearest cover flora in deterministic
// object-ID order on distance ties. It is also the render cue for a hidden
// animal's occupied bush; no species name is hardcoded (D4/D10).
func (w *World) nearestCoverFloraID(a *fauna.Animal) core.ObjectID {
	if a == nil {
		return ""
	}
	reach := w.envCfg.FaunaCombat.HideCoverFactor * w.envCfg.ScentCellSize
	if reach <= 0 {
		return ""
	}
	// Spatial-hash query near the animal instead of scanning every object (scaling.md P2). The
	// old full scan iterated objectIDs (ascending ID) and updated only on a STRICTLY closer plant,
	// so among equidistant plants it kept the lowest ID. Reproduce that exactly: strictly-closer
	// wins; on a distance tie the lower ID wins — independent of the spatial hash's return order.
	var nearest core.ObjectID
	nearestDistance := reach
	for _, e := range w.spatial.NearbyEntities(a.Pos, reach) {
		obj, ok := w.objects[e.ID]
		if !ok || !w.kindIsCover(obj.Kind) {
			continue
		}
		distance := a.Pos.Distance(obj.Pos)
		if distance > nearestDistance {
			continue
		}
		if nearest == "" || distance < nearestDistance || (distance == nearestDistance && e.ID < nearest) {
			nearest, nearestDistance = e.ID, distance
		}
	}
	return nearest
}

func (w *World) hiddenCoverID(a *fauna.Animal) core.ObjectID {
	if a == nil || a.HiddenUntil == 0 || a.HiddenUntil < w.tick {
		return ""
	}
	return w.nearestCoverFloraID(a)
}

// coverResistance is the M4-b movement drag at p for a species (>= 1; 1 = no
// cover). It sums per-plant linear-falloff overlap over cover-tagged flora times
// the species' cover_cost. Pure/deterministic (D12).
func (w *World) coverResistance(species fauna.SpeciesID, p core.Vec2) float64 {
	if w.faunaRules == nil {
		return unitScalar
	}
	cc := w.faunaRules.CoverCost(species)
	if cc <= zeroScalar {
		return unitScalar
	}
	return unitScalar + w.coverDensity(p)*cc
}

// coverIndexCellSize is the bucket size of the cover-plant spatial index. Correctness holds for
// any positive value (NearbyEntities covers the query circle regardless); this just tunes bucket
// occupancy for cover reaches on the order of a few plant widths.
const coverIndexCellSize = 10.0

// coverIndexFor returns the spatial index of cover-kind plants for the current floraState, lazily
// (re)building it when floraState changed (pointer-keyed) or cover kinds were just (re)installed
// (coverIndexKey reset to nil). Plants are static between flora Steps, so the index is built at
// most once per flora Step. Also refreshes maxCoverWidth (the query-radius bound). Building from
// floraState directly means coverDensity does NOT depend on plants being in the general object
// spatial hash — it works even when a test assigns floraState by hand.
func (w *World) coverIndexFor() *spatial.SpatialHash {
	if w.coverIndex != nil && w.coverIndexKey == w.floraState {
		return w.coverIndex
	}
	idx := spatial.New(coverIndexCellSize)
	w.maxCoverWidth = zeroScalar
	if w.floraState != nil && len(w.coverKinds) > 0 {
		for _, pl := range w.floraState.Plants() {
			if !w.kindIsCover(core.Tag(pl.Species)) {
				continue
			}
			idx.Insert(pl.ID, pl.Pos)
			if pl.Width > w.maxCoverWidth {
				w.maxCoverWidth = pl.Width
			}
		}
	}
	w.coverIndex = idx
	w.coverIndexKey = w.floraState
	return idx
}

// coverDensity sums each nearby cover-kind plant's proximity weight (1 − d/radius, radius =
// CoverRadiusFactor·Width) at p. It queries the cover index within CoverRadiusFactor·maxCoverWidth
// — the widest possible cover reach — so it touches only nearby cover plants, not every plant (the
// O(all-plants) scan + per-call Plants() copy was the dominant cost at large flora counts,
// docs/plans/scaling.md P2). Contributions are summed in ascending plant-ID order, identical to the
// old full-scan (Plants() is ID-sorted), so the exact float accumulation — and goldens — are preserved.
func (w *World) coverDensity(p core.Vec2) float64 {
	if w.floraState == nil {
		return zeroScalar
	}
	idx := w.coverIndexFor()
	if w.maxCoverWidth <= zeroScalar {
		return zeroScalar
	}
	queryRadius := w.envCfg.FaunaCombat.CoverRadiusFactor * w.maxCoverWidth
	nearby := idx.NearbyEntities(p, queryRadius)
	contrib := make([]coverContrib, 0, len(nearby))
	for _, e := range nearby {
		pl, ok := w.floraState.PlantByID(e.ID)
		if !ok {
			continue
		}
		radius := w.envCfg.FaunaCombat.CoverRadiusFactor * pl.Width
		if radius <= zeroScalar {
			continue
		}
		if d := p.Distance(pl.Pos); d < radius {
			contrib = append(contrib, coverContrib{id: pl.ID, weight: unitScalar - d/radius})
		}
	}
	sort.Slice(contrib, func(i, j int) bool { return contrib[i].id < contrib[j].id })
	density := zeroScalar
	for _, c := range contrib {
		density += c.weight
	}
	return density
}

type coverContrib struct {
	id     core.ObjectID
	weight float64
}

func (w *World) kindEmitsFood(kind core.Tag) bool {
	for _, tag := range w.scentEmitters[kind] {
		if tag == tagScentFood {
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
	for _, id := range w.orderedObjectIDs() {
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
	return ok && obj.Kind == kindCarcass
}

func (w *World) carcassSupply(id core.ObjectID) float64 {
	if w.decayState != nil && w.decayRules != nil {
		for _, lot := range w.decayState.Lots() {
			if lot.ID != id {
				continue
			}
			state := w.decayRules.StateAt(lot.Kind, lot.DecayAge)
			if supply := sumSupply(w.decayRules.SupplyAt(lot.Kind, state)); supply > zeroScalar {
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
		return zeroScalar
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

// killAnimal removes a dead animal, spawns its carcass, and emits AnimalDied
// (data-contracts §4: {object_id, species, cause}). cause is the object-
// mortality reason (§7) — currently always "predation" (the only death path
// this engine implements, via combat Vital≤0); the parameter exists so a future
// death cause (starvation, old age, …) is a call-site change, not a new event
// shape.
func (w *World) killAnimal(id core.ObjectID, cause string) {
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
			"object_id": string(id),
			"species":   string(species),
			"cause":     cause,
		},
	})
}

func (w *World) spawnCarcass(pos core.Vec2) core.ObjectID {
	id := w.allocObjectID()
	w.PlaceObject(id, kindCarcass, pos, nil)
	lot := decay.Lot{ID: id, Kind: kindCarcass, Qty: 1}
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
		return zeroScalar
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
