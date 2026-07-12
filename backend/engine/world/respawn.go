package world

import (
	"math"
	"sort"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// InstallRespawn wires optional timer-respawn-to-target (F9). templates = a canonical animal per species
// (fixture GenSpec), targets = per-species carrying capacity (content), anchors = a per-species spawn
// position for reviving an extinct species (fixture centroid), cadence = ticks between top-ups (balance).
// Zero cadence or empty targets ⇒ respawn OFF (outcome-neutral).
func (w *World) InstallRespawn(templates map[core.Tag]fauna.Animal, targets map[core.Tag]int, anchors map[core.Tag]core.Vec2, cadence core.Tick) {
	w.respawnTemplates = templates
	w.respawnTargets = targets
	w.respawnAnchors = anchors
	w.respawnCadence = cadence
}

// runRespawn tops each species up toward its target on the respawn cadence (F9 — repopulation, NOT birth).
// New members are cloned from the species template (fresh Vital/VitalCap/Stamina) and placed near a living
// member, or at the species anchor if extinct. Deterministic: sorted species order, seeded envFork,
// sorted-id reinsert (D12).
func (w *World) runRespawn() {
	if w.respawnCadence <= 0 || len(w.respawnTargets) == 0 {
		return
	}
	if int64(w.tick)%int64(w.respawnCadence) != 0 {
		return
	}

	counts := make(map[core.Tag]int)
	members := make(map[core.Tag][]core.Vec2)
	for _, id := range w.animalIDs {
		a := w.animals[id]
		if a == nil {
			continue
		}
		sp := core.Tag(a.Species)
		counts[sp]++
		members[sp] = append(members[sp], a.Pos)
	}

	species := make([]core.Tag, 0, len(w.respawnTargets))
	for sp := range w.respawnTargets {
		species = append(species, sp)
	}
	sort.Slice(species, func(i, j int) bool { return species[i] < species[j] })

	fork := w.envFork(w.tick, "respawn")
	spawned := false
	for _, sp := range species {
		tpl, ok := w.respawnTemplates[sp]
		if !ok {
			continue
		}
		deficit := w.respawnTargets[sp] - counts[sp]
		for k := 0; k < deficit; k++ {
			a := cloneAnimal(tpl)
			a.ID = w.allocAnimalID()
			a.Pos = w.respawnPos(sp, members[sp], fork)
			a.Species = fauna.SpeciesID(sp)
			if a.Vital <= 0 {
				a.Vital = defaultFreshAnimalVital
			}
			a.VitalCap = defaultFreshAnimalVital // fresh: no combat scars
			if a.Stamina <= 0 {
				a.Stamina = defaultFreshAnimalVital
			}
			a.EngagedWith = ""
			w.animals[a.ID] = &a
			w.animalIDs = append(w.animalIDs, a.ID)
			w.spatial.Insert(a.ID, a.Pos)
			members[sp] = append(members[sp], a.Pos) // a later spawn can cluster near it
			spawned = true
			// WI-P4/data-contracts §4: AnimalBorn{object_id, species, pos}.
			w.emit.Emit(core.Event{
				SchemaVersion: 1, Tick: w.tick, Type: "AnimalBorn",
				Payload: map[string]any{"object_id": string(a.ID), "species": string(sp), "pos": a.Pos},
			})
		}
	}
	if spawned {
		sortObjectIDs(w.animalIDs)
	}
}

// respawnPos places a new member near a living member (small seeded offset) or at the species anchor if
// the species is extinct. Deterministic given the seeded fork; kept inside the world bounds.
func (w *World) respawnPos(sp core.Tag, live []core.Vec2, fork *rng.RNG) core.Vec2 {
	var base core.Vec2
	switch {
	case len(live) > 0:
		base = live[int(fork.Float64()*float64(len(live)))%len(live)]
	default:
		base = w.respawnAnchors[sp] // zero Vec2 if no anchor
	}
	off := w.envCfg.ScentCellSize
	if off <= 0 {
		off = defaultScentMagnitude
	}
	return w.clampToBounds(core.Vec2{
		X: base.X + (fork.Float64()-centeredRandomOffset)*off,
		Y: base.Y + (fork.Float64()-centeredRandomOffset)*off,
	})
}

// reflectAtBounds clamps p into the world [Min,Max] rectangle AND, on each wall it hits, reflects the
// heading's component across that wall (bounce) so a boundary-bound animal turns back INTO the map instead
// of pinning against the edge and sliding along it (FM13, docs/plans/fauna.md §4.4). The heading component
// is flipped only when it points OUTWARD through the crossed wall, so an animal already steering inward
// keeps its heading. Returns (clamped pos, reflected heading). No-op (returns p, h) when bounds are unset
// (Max ≤ Min) or p is already interior — so interior movement is byte-identical to the old clamp path.
func (w *World) reflectAtBounds(p core.Vec2, h float64) (core.Vec2, float64) {
	if w.envCfg.Max.X <= w.envCfg.Min.X || w.envCfg.Max.Y <= w.envCfg.Min.Y {
		return p, h
	}
	vx, vy := math.Cos(h), math.Sin(h)
	reflected := false
	switch {
	case p.X < w.envCfg.Min.X:
		p.X = w.envCfg.Min.X
		vx, reflected = math.Abs(vx), true // point +X (back inside)
	case p.X > w.envCfg.Max.X:
		p.X = w.envCfg.Max.X
		vx, reflected = -math.Abs(vx), true // point −X
	}
	switch {
	case p.Y < w.envCfg.Min.Y:
		p.Y = w.envCfg.Min.Y
		vy, reflected = math.Abs(vy), true
	case p.Y > w.envCfg.Max.Y:
		p.Y = w.envCfg.Max.Y
		vy, reflected = -math.Abs(vy), true
	}
	if !reflected {
		return p, h // interior — leave heading exact (byte-identical to the old clamp)
	}
	return p, math.Atan2(vy, vx)
}

// clampToBounds keeps a position inside the world's [Min,Max] rectangle so animals (fleeing prey, roaming
// predators) cannot wander off-map. No-op if bounds are not configured (Max ≤ Min).
func (w *World) clampToBounds(p core.Vec2) core.Vec2 {
	if w.envCfg.Max.X <= w.envCfg.Min.X || w.envCfg.Max.Y <= w.envCfg.Min.Y {
		return p
	}
	if p.X < w.envCfg.Min.X {
		p.X = w.envCfg.Min.X
	}
	if p.X > w.envCfg.Max.X {
		p.X = w.envCfg.Max.X
	}
	if p.Y < w.envCfg.Min.Y {
		p.Y = w.envCfg.Min.Y
	}
	if p.Y > w.envCfg.Max.Y {
		p.Y = w.envCfg.Max.Y
	}
	return p
}
