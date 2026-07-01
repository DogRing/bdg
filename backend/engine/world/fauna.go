package world

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/engine/space/scent"
)

// InstallFauna installs the optional animal controller and the world-owned scent grid.
func (w *World) InstallFauna(cfg EnvConfig, faunaRules *fauna.Rules, scentEmitters map[core.Tag][]core.Tag, animals []fauna.Animal) {
	w.envCfg = cfg
	w.faunaRules = faunaRules
	w.scent = scent.New(cfg.ScentCellSize)
	w.scentEmitters = cloneScentEmitters(scentEmitters)
	w.animals = make(map[core.ObjectID]*fauna.Animal, len(animals))
	w.animalIDs = nil
	for _, animal := range animals {
		a := cloneAnimal(animal)
		if a.ID == "" {
			a.ID = w.allocAnimalID()
		}
		if _, exists := w.animals[a.ID]; exists {
			continue
		}
		w.animals[a.ID] = &a
		w.animalIDs = append(w.animalIDs, a.ID)
		w.spatial.Insert(a.ID, a.Pos)
	}
	sortObjectIDs(w.animalIDs)
}

func (w *World) faunaInstalled() bool {
	return w.scent != nil || w.faunaRules != nil || len(w.animals) > 0
}

func (w *World) planFaunaIntents() []fauna.Intent {
	if !w.faunaInstalled() {
		return nil
	}
	snap := w.buildFaunaSnapshot()
	return fauna.Step(snap, w.faunaRules, w.envFork(w.tick, "fauna"))
}

func (w *World) buildFaunaSnapshot() *fauna.Snapshot {
	animals := make([]fauna.Animal, 0, len(w.animalIDs))
	for _, id := range w.animalIDs {
		if a := w.animals[id]; a != nil {
			animals = append(animals, cloneAnimal(*a))
		}
	}
	return &fauna.Snapshot{
		Animals:       animals,
		Scent:         w.scent,
		Spatial:       w.spatial,
		Terrain:       worldTerrainSampler{nav: w.nav},
		Env:           w.buildFaunaEnvSamples(),
		Tick:          w.tick,
		Cadence:       w.envCfg.FaunaCadence,
		Combat:        w.envCfg.FaunaCombat,
		ScentCellSize: w.envCfg.ScentCellSize,
		DT:            w.envCfg.FaunaDT,
	}
}

func (w *World) buildFaunaEnvSamples() map[core.ObjectID]fauna.EnvSample {
	env := make(map[core.ObjectID]fauna.EnvSample, len(w.animalIDs))
	var wind scent.Wind
	if w.climateState != nil {
		cw := w.climateState.Wind()
		wind = scent.Wind{Dir: cw.Dir, Mag: cw.Mag}
	}
	for _, id := range w.animalIDs {
		a := w.animals[id]
		if a == nil {
			continue
		}
		sample := fauna.EnvSample{Wind: wind}
		if w.climateState != nil {
			cell := w.climateState.CellAt(a.Pos)
			sample.Temperature = cell.Temperature
			sample.Moisture = cell.Moisture
		}
		env[id] = sample
	}
	return env
}

type worldTerrainSampler struct {
	nav *navmap.NavMap
}

func (s worldTerrainSampler) FootprintBlocked(p core.Vec2) bool {
	if s.nav == nil {
		return false
	}
	return s.nav.FootprintBlocked(s.nav.CellOf(p))
}

func (s worldTerrainSampler) TerrainAt(p core.Vec2) core.Tag {
	if s.nav == nil {
		return ""
	}
	return core.Tag(s.nav.TerrainAt(s.nav.CellOf(p)))
}

func (s worldTerrainSampler) BaseCost(p core.Vec2) float64 {
	if s.nav == nil {
		return 1
	}
	cost := s.nav.BaseCost(s.nav.CellOf(p))
	if cost <= 0 {
		return 1
	}
	return cost
}

func (w *World) runScentEnv() {
	if w.scent == nil {
		return
	}
	for _, id := range w.animalIDs {
		a := w.animals[id]
		if a == nil {
			continue
		}
		w.depositAnimalScent(a, true)
	}
	if w.envCfg.ScentSpread > 0 && int64(w.tick)%int64(w.envCfg.ScentSpread) == 0 {
		for _, id := range w.animalIDs {
			a := w.animals[id]
			if a == nil {
				continue
			}
			w.depositAnimalScent(a, false)
		}
		w.depositFloraScent()
		w.depositObjectScent()
		w.scent.Spread(w.scentWind())
	}
	w.scent.Commit()
}

func (w *World) depositAnimalScent(a *fauna.Animal, predatorCadence bool) {
	if a == nil {
		return
	}
	mag := animalScentMagnitude(a)
	for _, tag := range w.scentEmitters[core.Tag(a.Species)] {
		ch, ok := scentChannelFromTag(tag)
		if !ok || (ch == scent.ChanPredator) != predatorCadence {
			continue
		}
		w.scent.Deposit(ch, a.Pos, mag)
	}
}

func (w *World) depositFloraScent() {
	if w.floraState == nil {
		return
	}
	for _, p := range w.floraState.Plants() {
		mag := floraScentMagnitude(p)
		for _, tag := range w.scentEmitters[core.Tag(p.Species)] {
			ch, ok := scentChannelFromTag(tag)
			if !ok || ch == scent.ChanPredator {
				continue
			}
			w.scent.Deposit(ch, p.Pos, mag)
		}
	}
}

func (w *World) depositObjectScent() {
	for _, id := range w.objectIDs {
		obj, ok := w.objects[id]
		if !ok {
			continue
		}
		mag := objectScentMagnitude(obj)
		for _, tag := range w.scentEmitters[obj.Kind] {
			ch, ok := scentChannelFromTag(tag)
			if !ok || ch == scent.ChanPredator {
				continue
			}
			w.scent.Deposit(ch, obj.Pos, mag)
		}
	}
}

func scentChannelFromTag(tag core.Tag) (scent.Channel, bool) {
	switch tag {
	case "scent:predator":
		return scent.ChanPredator, true
	case "scent:prey":
		return scent.ChanPrey, true
	case "scent:food":
		return scent.ChanFood, true
	case "scent:carrion":
		return scent.ChanCarrion, true
	default:
		return 0, false
	}
}

func animalScentMagnitude(a *fauna.Animal) float64 {
	if a == nil || a.Vital <= 0 {
		return 0
	}
	return a.Vital
}

func floraScentMagnitude(p flora.Plant) float64 {
	mag := p.Length + p.Width
	if mag < 0 {
		return 0
	}
	return mag
}

func objectScentMagnitude(obj objectRecord) float64 {
	if len(obj.Supply) == 0 {
		return 1
	}
	dims := make([]core.Dimension, 0, len(obj.Supply))
	for dim := range obj.Supply {
		dims = append(dims, dim)
	}
	sort.Slice(dims, func(i, j int) bool { return dims[i] < dims[j] })
	var mag float64
	for _, dim := range dims {
		if obj.Supply[dim] > 0 {
			mag += obj.Supply[dim]
		}
	}
	if mag <= 0 {
		return 1
	}
	return mag
}

func (w *World) scentWind() scent.Wind {
	if w.climateState == nil {
		return scent.Wind{}
	}
	cw := w.climateState.Wind()
	return scent.Wind{Dir: cw.Dir, Mag: cw.Mag}
}

func (w *World) removeAnimal(id core.ObjectID) {
	if _, ok := w.animals[id]; !ok {
		return
	}
	delete(w.animals, id)
	w.spatial.Remove(id)
	for i, animalID := range w.animalIDs {
		if animalID == id {
			w.animalIDs = append(w.animalIDs[:i], w.animalIDs[i+1:]...)
			return
		}
	}
}

func (w *World) allocAnimalID() core.ObjectID {
	for {
		w.nextAnimalSeq++
		id := core.ObjectID("an:" + strconv.FormatInt(w.nextAnimalSeq, 10))
		if _, ok := w.animals[id]; ok {
			continue
		}
		if _, ok := w.objects[id]; ok {
			continue
		}
		if _, ok := w.agents[core.AgentID(id)]; ok {
			continue
		}
		return id
	}
}

func cloneAnimal(a fauna.Animal) fauna.Animal {
	a.Stats = cloneStatFloatMap(a.Stats)
	a.Drives = cloneFaunaDrives(a.Drives)
	return a
}

func cloneScentEmitters(in map[core.Tag][]core.Tag) map[core.Tag][]core.Tag {
	if len(in) == 0 {
		return nil
	}
	out := make(map[core.Tag][]core.Tag, len(in))
	keys := make([]core.Tag, 0, len(in))
	for kind := range in {
		keys = append(keys, kind)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, kind := range keys {
		tags := append([]core.Tag(nil), in[kind]...)
		sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })
		deduped := tags[:0]
		for _, tag := range tags {
			if len(deduped) == 0 || deduped[len(deduped)-1] != tag {
				deduped = append(deduped, tag)
			}
		}
		out[kind] = deduped
	}
	return out
}

func cloneFaunaDrives(in map[fauna.DriveID]float64) map[fauna.DriveID]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[fauna.DriveID]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStatFloatMap(in map[core.StatID]float64) map[core.StatID]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[core.StatID]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sortObjectIDs(ids []core.ObjectID) {
	sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
}

func animalDigest(animals map[core.ObjectID]*fauna.Animal, ids []core.ObjectID) string {
	var b strings.Builder
	for _, id := range ids {
		a := animals[id]
		if a == nil {
			continue
		}
		fmt.Fprintf(&b, "%s|%s|%.4f,%.4f|%.4f|%.4f|%s", a.ID, a.Species, a.Pos.X, a.Pos.Y, a.Vital, a.Stamina, a.CurrentAction)
		keys := make([]string, 0, len(a.Drives))
		for k := range a.Drives {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "|%s=%.4f", key, a.Drives[fauna.DriveID(key)])
		}
		fmt.Fprint(&b, "\n")
	}
	return b.String()
}

func rngNew(seed int64) *rng.RNG {
	return rng.New(seed)
}
