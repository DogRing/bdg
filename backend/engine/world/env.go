package world

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sort"
	"strconv"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/navmap"
)

// InstallEnv installs the optional world-integration environment modules.
// Passing nil states/rules leaves the corresponding subsystem off.
func (w *World) InstallEnv(
	cfg EnvConfig,
	nav *navmap.NavMap,
	climateState *climate.State,
	climateRules *climate.Rules,
	floraState *flora.State,
	floraRules *flora.Rules,
	decayState *decay.State,
	decayRules *decay.Rules,
) {
	w.envCfg = cfg
	w.nav = nav
	w.climateState = climateState
	w.climateRules = climateRules
	w.floraState = floraState
	w.floraRules = floraRules
	w.decayState = decayState
	w.decayRules = decayRules
}

func (w *World) runEnvPhase() {
	w.runClimateEnv()
	w.runFloraEnv()
	w.runDecayEnv()
	w.runScentEnv()
}

func (w *World) runClimateEnv() {
	if w.climateState == nil || w.climateRules == nil || w.envCfg.ClimateStep <= 0 {
		return
	}
	if int64(w.tick)%int64(w.envCfg.ClimateStep) != 0 {
		return
	}

	forcing := climate.Forcing{
		HourOfDay:    w.clock.HourOfDay(w.tick),
		AbsHour:      int64(w.clock.Minutes(w.tick)) / 60,
		YearFraction: w.clock.YearFraction(w.tick),
	}
	next, transitions := climate.Step(w.climateState, forcing, w.climateRules, w.envFork(w.tick, "climate"))
	w.climateState = next
	if w.nav == nil {
		return
	}
	for _, tr := range transitions {
		cells := w.climateCellToNavCells(tr.Cell)
		if len(cells) == 0 {
			continue
		}
		w.nav.SetTerrain(cells, navmap.TerrainID(tr.To))
	}
}

func (w *World) runFloraEnv() {
	if w.floraState == nil || w.envCfg.FloraStep <= 0 {
		return
	}
	if int64(w.tick)%int64(w.envCfg.FloraStep) != 0 {
		return
	}

	next, deltas := flora.Step(
		w.floraState,
		w.floraSiteInputs(),
		w.floraRules,
		w.allocObjectID,
		w.envFork(w.tick, "flora"),
	)
	for _, p := range deltas.Spawned {
		w.PlaceObject(p.ID, core.Tag(p.Species), p.Pos, nil)
	}
	for _, id := range deltas.Died {
		w.RemoveObject(id)
	}
	w.floraState = next
}

func (w *World) runDecayEnv() {
	if w.decayState == nil || w.envCfg.DecayStep <= 0 {
		return
	}
	if int64(w.tick)%int64(w.envCfg.DecayStep) != 0 {
		return
	}

	next, deltas := decay.Step(
		w.decayState,
		w.decayEnvInputs(),
		int64(w.envCfg.DecayStep),
		w.decayRules,
		w.envFork(w.tick, "decay"),
	)
	for _, tr := range deltas.Transformed {
		pos := w.decayLotPosition(tr.SourceID)
		id := w.allocObjectID()
		w.PlaceObject(id, core.Tag(tr.Item), pos, nil)
		w.decayLotPos[id] = pos
		if mult, ok := w.decayStorageMult[tr.SourceID]; ok {
			w.decayStorageMult[id] = mult
		}
		w.emit.Emit(core.Event{
			SchemaVersion: 1,
			Tick:          w.tick,
			Type:          "Decayed",
			Payload: map[string]any{
				"source_id": string(tr.SourceID),
				"state":     tr.State,
				"item":      string(tr.Item),
				"qty":       tr.Qty,
				"object_id": string(id),
			},
		})
	}
	for _, id := range deltas.Gone {
		w.RemoveObject(id)
		delete(w.decayLotPos, id)
		delete(w.decayStorageMult, id)
	}
	w.decayState = next
}

func (w *World) climateCellToNavCells(gc climate.GridCell) []navmap.Cell {
	if w.nav == nil || w.envCfg.ClimateGridCols <= 0 || w.envCfg.ClimateGridRows <= 0 {
		return nil
	}
	worldW := w.envCfg.Max.X - w.envCfg.Min.X
	worldH := w.envCfg.Max.Y - w.envCfg.Min.Y
	if worldW <= 0 || worldH <= 0 {
		return nil
	}
	cellW := worldW / float64(w.envCfg.ClimateGridCols)
	cellH := worldH / float64(w.envCfg.ClimateGridRows)
	x0 := w.envCfg.Min.X + float64(gc.X)*cellW
	y0 := w.envCfg.Min.Y + float64(gc.Y)*cellH
	x1 := x0 + cellW
	y1 := y0 + cellH

	start := w.nav.CellOf(core.Vec2{X: x0, Y: y0})
	end := w.nav.CellOf(core.Vec2{X: math.Nextafter(x1, x0), Y: math.Nextafter(y1, y0)})
	seen := make(map[navmap.Cell]struct{})
	cells := make([]navmap.Cell, 0)
	for y := start.Y - 1; y <= end.Y+1; y++ {
		for x := start.X - 1; x <= end.X+1; x++ {
			c := navmap.Cell{X: x, Y: y}
			if _, ok := seen[c]; ok {
				continue
			}
			center := w.nav.CellCenter(c)
			if !pointInClimateRect(center, x0, y0, x1, y1, gc, w.envCfg) {
				continue
			}
			if w.nav.TerrainAt(c) == "" {
				continue
			}
			seen[c] = struct{}{}
			cells = append(cells, c)
		}
	}
	sortNavCells(cells)
	return cells
}

func pointInClimateRect(p core.Vec2, x0, y0, x1, y1 float64, gc climate.GridCell, cfg EnvConfig) bool {
	inX := p.X >= x0 && p.X < x1
	if gc.X == cfg.ClimateGridCols-1 {
		inX = p.X >= x0 && p.X <= x1
	}
	inY := p.Y >= y0 && p.Y < y1
	if gc.Y == cfg.ClimateGridRows-1 {
		inY = p.Y >= y0 && p.Y <= y1
	}
	return inX && inY
}

func (w *World) floraSiteInputs() map[core.ObjectID]flora.SiteInput {
	plants := w.floraState.Plants()
	inputs := make(map[core.ObjectID]flora.SiteInput, len(plants))
	speciesByID := make(map[core.ObjectID]flora.SpeciesID, len(plants))
	for _, p := range plants {
		speciesByID[p.ID] = p.Species
	}
	for _, p := range plants {
		var terrain core.Tag
		if w.nav != nil {
			terrain = core.Tag(w.nav.TerrainAt(w.nav.CellOf(p.Pos)))
		}
		attrs := cloneTerrainAttrs(w.terrainAttrs[navmap.TerrainID(terrain)])
		var moisture, temperature float64
		if w.climateState != nil {
			cell := w.climateState.CellAt(p.Pos)
			moisture = cell.Moisture
			temperature = cell.Temperature
		}
		in := flora.SiteInput{
			Terrain:      terrain,
			TerrainAttrs: attrs,
			Moisture:     moisture,
			Temperature:  temperature,
		}
		in.NeighborCount = w.floraNeighborCount(p, speciesByID, in)
		inputs[p.ID] = in
	}
	return inputs
}

func (w *World) floraNeighborCount(p flora.Plant, speciesByID map[core.ObjectID]flora.SpeciesID, in flora.SiteInput) int {
	radius := 0.0
	if w.floraRules != nil {
		radius = w.floraRules.PropRadius(p.Species, p, in)
	}
	nearby := w.spatial.NearbyEntities(p.Pos, radius)
	count := 0
	for _, e := range nearby {
		if e.ID == p.ID {
			continue
		}
		if speciesByID[e.ID] == p.Species {
			count++
		}
	}
	return count
}

func (w *World) decayEnvInputs() map[core.ObjectID]decay.EnvInput {
	lots := w.decayState.Lots()
	inputs := make(map[core.ObjectID]decay.EnvInput, len(lots))
	for _, lot := range lots {
		pos := w.decayLotPosition(lot.ID)
		var moisture, temperature float64
		if w.climateState != nil {
			cell := w.climateState.CellAt(pos)
			moisture = cell.Moisture
			temperature = cell.Temperature
		}
		mult := 1.0
		if v, ok := w.decayStorageMult[lot.ID]; ok {
			mult = v
		}
		inputs[lot.ID] = decay.EnvInput{
			Temperature:     temperature,
			Moisture:        moisture,
			StorageRateMult: mult,
		}
	}
	return inputs
}

func (w *World) decayLotPosition(id core.ObjectID) core.Vec2 {
	if pos, ok := w.decayLotPos[id]; ok {
		return pos
	}
	if obj, ok := w.objects[id]; ok {
		return obj.Pos
	}
	return core.Vec2{}
}

func (w *World) allocObjectID() core.ObjectID {
	for {
		w.nextObjectSeq++
		id := core.ObjectID("obj:" + strconv.FormatInt(w.nextObjectSeq, 10))
		if _, ok := w.objects[id]; ok {
			continue
		}
		if _, ok := w.agents[core.AgentID(id)]; ok {
			continue
		}
		return id
	}
}

func (w *World) envFork(tick core.Tick, channel string) *rng.RNG {
	h := fnv.New64a()
	_, _ = h.Write([]byte("world-env"))
	_, _ = h.Write([]byte(w.rootRNG.State().Data))
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(tick))
	_, _ = h.Write(buf[:])
	_, _ = h.Write([]byte(channel))
	return rng.New(int64(h.Sum64()))
}

func cloneTerrainAttrs(in map[core.Tag]float64) map[core.Tag]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[core.Tag]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sortNavCells(cells []navmap.Cell) {
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Y != cells[j].Y {
			return cells[i].Y < cells[j].Y
		}
		return cells[i].X < cells[j].X
	})
}
