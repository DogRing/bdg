package world

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/engine/space/scent"
)

// InstallFauna installs the optional animal controller and the world-owned scent grid.
func (w *World) InstallFauna(cfg EnvConfig, faunaRules *fauna.Rules, scentEmitters map[core.Tag][]core.Tag, coverKinds map[core.Tag]bool, animals []fauna.Animal) {
	w.envCfg = cfg
	w.faunaRules = faunaRules
	w.scent = scent.New(cfg.ScentCellSize)
	if cfg.ScentTrailDecay > 0 && len(cfg.ScentTrailStrength) > 0 {
		var strength [scent.NumChannels]float64
		for _, tag := range sortedTagKeys(cfg.ScentTrailStrength) { // sorted: no map-order logic (D12)
			if ch, ok := scentChannelFromTag("scent:" + tag); ok {
				strength[ch] = cfg.ScentTrailStrength[tag]
			}
		}
		w.scent.ConfigureTrail(strength, cfg.ScentTrailDecay, cfg.ScentTrailCap)
	}
	w.scentEmitters = cloneScentEmitters(scentEmitters)
	w.coverKinds = cloneCoverKinds(coverKinds)
	w.coverIndexKey = nil // force a cover-index rebuild now that cover kinds are known
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

// sortedTagKeys returns a map's tag keys in sorted order, so a config map can be read without
// driving logic by map-iteration order (D12).
func sortedTagKeys(m map[core.Tag]float64) []core.Tag {
	out := make([]core.Tag, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
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
	// Build the shared static hazard field once (terrain is static in P_move1); a typed-nil
	// *field.Field must NOT be stored in the interface (it would satisfy != nil and panic on call).
	if !w.hazardFieldBuilt {
		w.hazardField = w.buildHazardField()
		w.hazardFieldBuilt = true
	}
	var hazard fauna.HazardSampler
	if w.hazardField != nil {
		hazard = w.hazardField
	}
	// Build the shared static water-attraction field once (FM4); typed-nil guard as for hazard.
	if !w.waterFieldBuilt {
		w.waterField = w.buildWaterField()
		w.waterFieldBuilt = true
	}
	var water fauna.WaterSampler
	if w.waterField != nil {
		water = w.waterField
	}
	// Animal-by-ID index so fauna combat can resolve Spatial neighbour IDs to Animals (O(nearby)
	// target selection instead of an O(N²) full scan, docs/plans/scaling.md P2).
	byID := make(map[core.ObjectID]fauna.Animal, len(animals))
	for _, a := range animals {
		byID[a.ID] = a
	}
	return &fauna.Snapshot{
		Animals:       animals,
		ByID:          byID,
		Scent:         w.scent,
		Spatial:       w.spatial,
		Terrain:       worldTerrainSampler{nav: w.nav, attrs: w.terrainAttrs},
		Env:           w.buildFaunaEnvSamples(),
		Tick:          w.tick,
		Cadence:       w.envCfg.FaunaCadence,
		Combat:        w.envCfg.FaunaCombat,
		ScentCellSize: w.envCfg.ScentCellSize,
		DT:            w.envCfg.FaunaDT,
		MoveDeadband:  w.envCfg.FaunaMoveDeadband,
		HazardField:   hazard,
		WaterField:    water,
		Cover:         coverLookup{w},
	}
}

func (w *World) buildFaunaEnvSamples() map[core.ObjectID]fauna.EnvSample {
	env := make(map[core.ObjectID]fauna.EnvSample, len(w.animalIDs))
	var global climate.Wind
	var dailyMean float64
	coverOn := w.exposureCover != nil
	// Daylight (FM11): world-uniform diurnal light cue, 1 at solar-noon, 0 at midnight; smooth.
	// Injected into every animal's EnvSample so §6 can drive the Sleep action (D11-style scalar,
	// like temperature). Clock-derived ⇒ deterministic (D12).
	daylight := 0.5 * (1 - math.Cos(2*math.Pi*w.clock.DayFraction(w.tick)))
	if w.climateState != nil {
		global = w.climateState.Wind()
		dailyMean = w.climateState.DailyMeanTemperature()
	}
	for _, id := range w.animalIDs {
		a := w.animals[id]
		if a == nil {
			continue
		}
		// SH1: per-position local wind = global × ε(cell). Shelter-OFF ⇒ global unchanged.
		sample := fauna.EnvSample{Wind: w.localWindAt(a.Pos, global), Daylight: daylight}
		if w.climateState != nil {
			cell := w.climateState.CellAt(a.Pos)
			// SH3: overhead cover buffers sensed temperature toward the day's mean and moisture toward
			// the cell's resting value (docs/plans/shelter.md). The resting-moisture baseline is fetched
			// only when cover is installed (a no-op lerp otherwise). Shelter-OFF / uncovered ⇒ raw.
			baseMoist := cell.Moisture
			if coverOn {
				baseMoist = w.climateState.BaselineMoistureAt(a.Pos)
			}
			sample.Temperature, sample.Moisture = w.localTempMoistureAt(a.Pos, cell.Temperature, cell.Moisture, dailyMean, baseMoist)
		}
		env[id] = sample
	}
	return env
}

type worldTerrainSampler struct {
	nav   *navmap.NavMap
	attrs map[navmap.TerrainID]map[core.Tag]float64
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
		return defaultTerrainCost
	}
	cost := s.nav.BaseCost(s.nav.CellOf(p))
	if cost <= 0 {
		return defaultTerrainCost
	}
	return cost
}

func (w *World) runScentEnv() {
	if w.scent == nil {
		return
	}
	// Fade the spoor layer BEFORE this tick's deposits, so what an animal lays down now is at full
	// strength and only what was already on the ground ages (PD9). Off-lever: no-op when no channel
	// opted in.
	w.scent.DecayTrail()

	// Animals are MOVING occupants: their tile changes every tick, so every channel they emit is
	// re-laid every tick (see depositAnimalScent). Cost is O(live animals) — the same walk the
	// predator channel already paid for.
	for _, id := range w.animalIDs {
		a := w.animals[id]
		if a == nil {
			continue
		}
		w.depositAnimalScent(a)
	}
	// The dynamic layer diffuses EVERY tick: it is rebuilt every tick anyway, and animals are few,
	// so a plume that only existed on cadence ticks would be a pure loss of fidelity for no saving.
	w.scent.Spread(w.scentWind())
	w.scent.Commit()

	// Flora and objects are STATIC occupants — their tiles do not change between ticks — so the
	// expensive layer is rebuilt (and diffused) only on the bulk cadence, and PERSISTS in between
	// rather than being cleared. That persistence is the whole point of the split (클러스터 8b):
	// before it, food scent existed on 1 tick in 6 even though the plants never moved, and herbivore
	// food homing was blind the other five.
	if w.envCfg.ScentSpread > 0 && int64(w.tick)%int64(w.envCfg.ScentSpread) == 0 {
		w.depositFloraScent()
		w.depositObjectScent()
		w.scent.CommitStatic(w.scentWind())
	}
}

// depositAnimalScent lays down every channel this species emits, at the animal's CURRENT position.
//
// Called every tick for every animal — not on the bulk cadence. The scent field's contract is
// "a tile smells of what is in it", and `Grid.Commit` rebuilds the committed field from scratch
// each tick (it swaps buffers and clears the old one, so nothing persists on its own). An animal
// MOVES every tick, so its tile genuinely changes every tick: per-tick deposit is what implements
// the contract for a moving occupant, not a departure from it.
//
// This used to run the non-predator channels on the ScentSpread cadence only, which — combined
// with the clearing Commit — left the prey/food channels EMPTY on 5 ticks out of 6. Measured on
// the shipped fixture: prey scent readable only at `tick%6==1`, zero otherwise, even standing on
// top of the animal. Predators therefore saw no prey scent 83% of the time, their scent-gated Hunt
// utility lost to the wander baseline, and they starved beside prey that was 14 units away.
func (w *World) depositAnimalScent(a *fauna.Animal) {
	if a == nil {
		return
	}
	mag := animalScentMagnitude(a)
	for _, tag := range w.scentEmitters[core.Tag(a.Species)] {
		ch, ok := scentChannelFromTag(tag)
		if !ok {
			continue
		}
		if ch == scent.ChanPrey && a.HiddenUntil > 0 && a.HiddenUntil >= w.tick {
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
			w.scent.DepositStatic(ch, p.Pos, mag)
		}
	}
}

func (w *World) depositObjectScent() {
	for _, id := range w.orderedObjectIDs() {
		obj, ok := w.objects[id]
		if !ok {
			continue
		}
		// Flora plants live in BOTH floraState and w.objects (env.go PlaceObject on spawn), and
		// depositFloraScent already deposited them at their biomass magnitude (Length+Width).
		// Depositing them AGAIN here would add a flat objectScentMagnitude on top, pegging an
		// eaten-bare plant's food scent at a constant — which deletes the PD2 depletion feedback
		// this SPEC describes ("overgrazing shrinks plants → weaker food scent → migration pressure").
		if w.floraState != nil {
			if _, isFlora := w.floraState.PlantByID(id); isFlora {
				continue
			}
		}
		mag := objectScentMagnitude(obj)
		for _, tag := range w.scentEmitters[obj.Kind] {
			ch, ok := scentChannelFromTag(tag)
			if !ok || ch == scent.ChanPredator {
				continue
			}
			w.scent.DepositStatic(ch, obj.Pos, mag)
		}
	}
}

func scentChannelFromTag(tag core.Tag) (scent.Channel, bool) {
	switch tag {
	case tagScentPredator:
		return scent.ChanPredator, true
	case tagScentPrey:
		return scent.ChanPrey, true
	case tagScentFood:
		return scent.ChanFood, true
	case tagScentCarrion:
		return scent.ChanCarrion, true
	default:
		return 0, false
	}
}

func animalScentMagnitude(a *fauna.Animal) float64 {
	if a == nil || a.Vital <= 0 {
		return zeroScalar
	}
	return a.Vital
}

func floraScentMagnitude(p flora.Plant) float64 {
	mag := p.Length + p.Width
	if mag < 0 {
		return zeroScalar
	}
	return mag
}

func objectScentMagnitude(obj objectRecord) float64 {
	if len(obj.Supply) == 0 {
		return defaultScentMagnitude
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
		return defaultScentMagnitude
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

func cloneCoverKinds(in map[core.Tag]bool) map[core.Tag]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[core.Tag]bool, len(in))
	keys := make([]core.Tag, 0, len(in))
	for kind := range in {
		keys = append(keys, kind)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, kind := range keys {
		if in[kind] {
			out[kind] = true
		}
	}
	if len(out) == 0 {
		return nil
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

// Attrs returns the §5 attribute vector of the terrain at p — the SAME content-defined set flora
// reads — which fauna §6 exposes as `terrain.<attr>` (PD10). Returns nil when navmap or the attr
// table is absent, which the context reads as 0 for every terrain operand (off-lever).
func (s worldTerrainSampler) Attrs(p core.Vec2) map[core.Tag]float64 {
	if s.nav == nil || s.attrs == nil {
		return nil
	}
	return s.attrs[s.nav.TerrainAt(s.nav.CellOf(p))]
}
