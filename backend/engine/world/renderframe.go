package world

import (
	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
)

// ── RenderView: the render/observation-visible env projection (WI-P4) ──────────
//
// RenderView is the god-view-FILTERED projection of the world's env state — the
// live-key/WorldFrame source persist/api read (data-contracts §2/§4/§10). The
// god-view filter lives in exactly ONE place (here): no stats/drives/vital raw
// vector ever appears on it (only render-relevant fields), mirroring how
// persist.AgentView structurally cannot carry RealStats (persist/SPEC-world.md
// Open Question, RESOLVED (b)). Absent/zero fields ⇒ that subsystem is OFF.

// RenderView is the world's current render-visible env snapshot.
type RenderView struct {
	Tick         core.Tick
	HourOfDay    int
	DayNight     string // "day" | "night", derived from HourOfDay
	YearFraction float64

	ClimateOn   bool // false ⇒ Temperature/Moisture/Raining/Wind* below are zero (climate OFF)
	Temperature float64
	Moisture    float64
	Raining     bool
	WindDir     float64
	WindMag     float64

	Animals []AnimalRenderView // sorted ObjectID (D12); empty when fauna OFF
	Flora   []FloraRenderView  // sorted ObjectID (D12); empty when flora OFF
	Terrain *TerrainRenderView // nil when navmap OFF
}

// AnimalRenderView is one animal's render-visible state — NO Stats/Drives/Vital
// (god-view boundary; mirrors data-contracts §2 sim:{run}:animal:{id}).
type AnimalRenderView struct {
	ID      core.ObjectID
	Species string
	Pos     core.Vec2
	Action  string
	Heading float64
	Stamina float64
}

// FloraRenderView is one plant's render-visible state (data-contracts §2
// sim:{run}:flora). Stage is DERIVED from Length (D9).
type FloraRenderView struct {
	ID      core.ObjectID
	Species string
	Pos     core.Vec2
	Stage   int
	Width   float64
}

// TerrainRenderView is the full render terrain grid — base layout + climate
// overrides (via TerrainAt, which is override-aware) + wear (data-contracts §2
// sim:{run}:terrain / §API /api/terrain). Flat-top hex projected to an offset
// (col,row) rectangular array; index i = row*Cols + col (docs/plans/hex-grid.md).
type TerrainRenderView struct {
	CellSize    float64
	Cols, Rows  int
	Orientation string    // "flat" (flat-top hex); mirrors navmap.Orientation()
	Terrain     []string  // len Cols*Rows, offset(col,row)
	Wear        []float64 // len Cols*Rows, 0 where no wear
	Elevation   []float64 // len Cols*Rows ∈[0,1] or nil — render-only relief (SetTerrainElevation);
	//                       static, full-grid only (never in terrain_delta)
}

// RenderView builds the current render-visible env projection. Called by persist
// (periodic-full live Redis keys) at the render/backup cadence; safe to call
// every tick if a future caller needs that (pure read, no mutation).
func (w *World) RenderView() RenderView {
	hour := w.clock.HourOfDay(w.tick)
	rv := RenderView{
		Tick:         w.tick,
		HourOfDay:    hour,
		DayNight:     dayNightOf(hour),
		YearFraction: w.clock.YearFraction(w.tick),
	}

	if w.climateState != nil {
		rv.ClimateOn = true
		rv.Temperature, rv.Moisture = ambientClimate(w.climateState)
		rv.Raining = w.climateState.Rain().Raining
		wind := w.climateState.Wind()
		rv.WindDir, rv.WindMag = wind.Dir, wind.Mag
	}

	for _, id := range w.animalIDs {
		a := w.animals[id]
		if a == nil {
			continue
		}
		rv.Animals = append(rv.Animals, AnimalRenderView{
			ID: a.ID, Species: string(a.Species), Pos: a.Pos,
			Action: string(a.CurrentAction), Heading: a.Heading, Stamina: a.Stamina,
		})
	}

	if w.floraState != nil {
		for _, p := range w.floraState.Plants() {
			rv.Flora = append(rv.Flora, FloraRenderView{
				ID: p.ID, Species: string(p.Species), Pos: p.Pos,
				Stage: w.floraStageOf(p.Species, p.Length), Width: p.Width,
			})
		}
	}

	rv.Terrain = w.buildTerrainGrid()

	return rv
}

// ── envInstalled ─────────────────────────────────────────────────────────────

// envInstalled reports whether ANY env subsystem (navmap/climate/flora/decay/
// fauna) was installed — the WorldFrame emission gate (data-contracts §4:
// "Emitted only when env is installed").
func (w *World) envInstalled() bool {
	return w.nav != nil || w.climateState != nil || w.floraState != nil || w.decayState != nil || w.faunaInstalled()
}

// ── WorldFrame emission (WI-P4; replaces the removed frontend mock) ────────────

// emitWorldFrame builds and emits the real WorldFrame graphics frame from live
// env state (data-contracts §4). No-op when env is OFF. God-view (real_stats/
// drives/stats) never appears — only render-relevant fields, same boundary as
// RenderView. apparent_temp is omitted (optional per data-contracts §4; no clean
// public accessor exists for a representative per-animal apparent_temp without
// duplicating fauna's internal §6 Context — see engine/fauna/SPEC.md AppTemp).
func (w *World) emitWorldFrame() {
	if !w.envInstalled() {
		return
	}

	hour := w.clock.HourOfDay(w.tick)
	var temperature float64
	var raining bool
	var windDir, windMag float64
	if w.climateState != nil {
		temperature, _ = ambientClimate(w.climateState)
		raining = w.climateState.Rain().Raining
		wind := w.climateState.Wind()
		windDir, windMag = wind.Dir, wind.Mag
	}

	w.emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          w.tick,
		Type:          "WorldFrame",
		Payload: map[string]any{
			"tick":          int64(w.tick),
			"hour_of_day":   hour,
			"day_night":     dayNightOf(hour),
			"temperature":   temperature,
			"raining":       raining,
			"wind":          map[string]any{"dir": windDir, "mag": windMag},
			"agents":        w.frameAgents(),
			"animals":       w.frameAnimals(),
			"flora_delta":   w.frameFloraDelta(),
			"terrain_delta": w.frameTerrainDelta(),
		},
	})
}

// frameAgents builds the WorldFrame agents[]{id,pos,action} list (data-contracts
// §4) — a render-only subset of what TickDone already carries (no goal/mood).
func (w *World) frameAgents() []map[string]any {
	out := make([]map[string]any, 0, len(w.agentIDs))
	for _, id := range w.agentIDs {
		a := w.agents[id]
		if a == nil {
			continue
		}
		action := ""
		if a.PlanIdx >= 0 && a.PlanIdx < len(a.Plan.Actions) {
			action = string(a.Plan.Actions[a.PlanIdx])
		}
		out = append(out, map[string]any{"id": string(a.ID), "pos": a.Pos, "action": action})
	}
	return out
}

// frameAnimals builds the WorldFrame animals[] list — ALL live animals every
// frame (not a delta), mirroring TickDone.agents[]; small population, cheap.
// Includes `stamina` (beyond the data-contracts §4 payload gist) because the
// frontend reducer/AnimalState already reads+renders it (dimming by stamina;
// frontend/src/hooks/useWorld.ts, frontend/SPEC.md AnimalState) — an
// intentional, harmless superset of the documented gist.
func (w *World) frameAnimals() []map[string]any {
	out := make([]map[string]any, 0, len(w.animalIDs))
	for _, id := range w.animalIDs {
		a := w.animals[id]
		if a == nil {
			continue
		}
		out = append(out, map[string]any{
			"id": string(a.ID), "pos": a.Pos, "species": string(a.Species),
			"action": string(a.CurrentAction), "heading": a.Heading, "stamina": a.Stamina,
		})
	}
	return out
}

// frameFloraDelta converts this tick's sparse flora spawn/grow buffer (populated
// by runFloraEnv) into the WorldFrame flora_delta[]{id,pos,stage} shape.
func (w *World) frameFloraDelta() []map[string]any {
	if len(w.pendingFloraFrame) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(w.pendingFloraFrame))
	for _, e := range w.pendingFloraFrame {
		out = append(out, map[string]any{"id": string(e.ID), "pos": e.Pos, "stage": e.Stage})
	}
	return out
}

// frameTerrainDelta builds the WorldFrame terrain_delta[]{cell,terrain?,wear?}
// list from the navmap's current override + active-wear sets (data-contracts
// §4/§6). CHOICE: re-emits every cell that has EVER deviated from the base
// layout or currently carries wear (not a true since-last-tick delta) — a
// deliberate simplification (no additional per-tick transient buffer for wear,
// which changes gradually and idempotently overwrites on the frontend); see the
// WI-P4 implementation report.
func (w *World) frameTerrainDelta() []map[string]any {
	if w.nav == nil {
		return []map[string]any{}
	}
	type entry struct {
		hasTerrain bool
		terrain    string
		hasWear    bool
		wear       float64
	}
	byCell := make(map[navmap.Cell]*entry)
	for _, tc := range w.nav.TerrainOverrides() {
		e := byCell[tc.Cell]
		if e == nil {
			e = &entry{}
			byCell[tc.Cell] = e
		}
		e.hasTerrain, e.terrain = true, string(tc.Terrain)
	}
	for _, wc := range w.nav.ActiveWear() {
		e := byCell[wc.Cell]
		if e == nil {
			e = &entry{}
			byCell[wc.Cell] = e
		}
		e.hasWear, e.wear = true, wc.Wear
	}
	cells := make([]navmap.Cell, 0, len(byCell))
	for c := range byCell {
		cells = append(cells, c)
	}
	sortNavCells(cells) // D12: sorted R-major then Q (reused from env.go)

	cols, _ := w.nav.OffsetDims()
	out := make([]map[string]any, 0, len(cells))
	for _, c := range cells {
		e := byCell[c]
		col, row := w.nav.CellToOffset(c)
		m := map[string]any{"cell": row*cols + col} // offset index i=row*cols+col (data-contracts §4/§6)
		if e.hasTerrain {
			m["terrain"] = e.terrain
		}
		if e.hasWear {
			m["wear"] = e.wear
		}
		out = append(out, m)
	}
	return out
}

// ── Terrain grid (full, for the periodic live key + /api/terrain) ──────────────

// buildTerrainGrid returns the full render terrain grid, or nil when navmap is
// OFF. The grid is navmap's offset (col,row) projection of its flat-top hex field
// (navmap is the hex authority — OffsetDims/OffsetToCell/CellToOffset, hex-grid.md),
// so terrain_delta's offset index (i=row*Cols+col) addresses the SAME array. offset
// (0,0) is the (MinX,MinY)-anchored corner hex (axial 0,0), matching the anchor
// world already uses to bridge climate GridCells to navmap hexes.
func (w *World) buildTerrainGrid() *TerrainRenderView {
	if w.nav == nil || w.envCfg.NavmapCellSize <= 0 {
		return nil
	}
	cols, rows := w.nav.OffsetDims()
	if cols <= 0 || rows <= 0 {
		return nil
	}

	terrain := make([]string, cols*rows)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			c := w.nav.OffsetToCell(col, row)
			terrain[row*cols+col] = string(w.nav.TerrainAt(c))
		}
	}
	wear := make([]float64, cols*rows)
	for _, wc := range w.nav.ActiveWear() {
		col, row := w.nav.CellToOffset(wc.Cell)
		if col < 0 || col >= cols || row < 0 || row >= rows {
			continue
		}
		wear[row*cols+col] = wc.Wear
	}

	return &TerrainRenderView{
		CellSize:    w.envCfg.NavmapCellSize,
		Cols:        cols,
		Rows:        rows,
		Orientation: w.nav.Orientation(),
		Terrain:     terrain,
		Wear:        wear,
		Elevation:   w.terrainElev, // nil when no generated relief (SetTerrainElevation)
	}
}

// ── Small pure helpers ───────────────────────────────────────────────────────

// floraFrameEntry is one sparse flora spawn/grow render delta entry (this
// tick), buffered in World.pendingFloraFrame by runFloraEnv (env.go).
type floraFrameEntry struct {
	ID    core.ObjectID
	Pos   core.Vec2
	Stage int
}

// dayNightOf derives the WorldFrame/climate-key day_night field from HourOfDay
// (data-contracts §4: "day_night derives from hour_of_day"). Matches the
// established frontend mock convention (frontend/dev/mock-server.mjs): day is
// [6,18), night otherwise.
func dayNightOf(hour int) string {
	if hour >= 6 && hour < 18 {
		return "day"
	}
	return "night"
}

// ambientClimate reduces the per-cell climate grid to one representative
// (temperature, moisture) pair for the single-value render keys (sim:{run}:
// climate ambient hash / WorldFrame.temperature) — the mean over all cells, in
// Cells()'s fixed sorted order (deterministic summation order, D12).
func ambientClimate(cs *climate.State) (temperature, moisture float64) {
	cells := cs.Cells()
	if len(cells) == 0 {
		return 0, 0
	}
	var sumT, sumM float64
	for _, gcs := range cells {
		sumT += gcs.State.Temperature
		sumM += gcs.State.Moisture
	}
	n := float64(len(cells))
	return sumT / n, sumM / n
}
