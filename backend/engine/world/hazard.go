package world

import (
	"math"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/field"
	"github.com/dogring/bdg/engine/space/navmap"
)

// Fauna hazard field (docs/plans/fauna.md §4 P_move1 / FM2). world builds ONE shared static danger
// potential field from the terrain and injects it into the fauna snapshot; steering adds e·Repulsion
// (e = the species' HazardAvoidance) so animals veer away from dangerous terrain BEFORE dead-stopping
// at it — a continuous cost, not a block (a strong flee/drive crosses anyway, FM5). Per-species
// differentiation is via `e`, so one shared field suffices in P_move1.
const (
	hazardDangerFloor  = 0.2  // cells below this danger are not sources (normal ground stays neutral)
	hazardCostSpread   = 6.0  // base_cost→danger softener: passable rough terrain (river ford, mountain) is only MILDLY dangerous so it stays crossable (M4-a: river = prey refuge)
	hazardImpassable   = 1.0  // impassable terrain (deep water / cliff) = maximal danger (drowning / fall)
	hazardDecayPerUnit = 0.03 // intensity decay per world-unit; reach = danger/decay (untuned balance)
)

// cellDanger maps a navmap cell to its continuous danger ∈ [0,1] (P_move1/FM2): impassable terrain
// (deep water / cliff) is maximal (drowning / fall); passable-but-rough terrain (river, mountain) is
// MILDLY dangerous ∝ base_cost so it stays crossable; normal ground ~0. Derived from the runtime
// navmap BaseCost/Passable — the available proxy for the intended §5 depth/slope danger (wiring the
// terrain §5 attrs through to world is a separate follow-up; the terrainAttrs map is currently unfed).
func cellDanger(nav *navmap.NavMap, c navmap.Cell) float64 {
	if !nav.Passable(c) {
		return hazardImpassable
	}
	d := (nav.BaseCost(c) - 1) / hazardCostSpread
	if d < 0 {
		return 0
	}
	if d > 1 {
		return 1
	}
	return d
}

// buildHazardField enumerates navmap cells over the world bounds (padded sample step ≤ hex inradius,
// mirroring the climate→navmap bridge), collects those whose danger ≥ floor as weighted field sources,
// and builds the shared static hazard field. Deterministic (field.Build is D12 and sorts sources).
// Returns nil when there is no navmap or no dangerous terrain (⇒ hazard blend off, byte-identical).
func (w *World) buildHazardField() *field.Field {
	if w.nav == nil {
		return nil
	}
	cellSize := w.envCfg.NavmapCellSize
	if cellSize <= 0 {
		return nil
	}
	step := math.Sqrt(3) / 2 * cellSize
	pad := cellSize
	seen := make(map[navmap.Cell]struct{})
	var sources []field.Source
	for y := w.envCfg.Min.Y - pad; y <= w.envCfg.Max.Y+pad; y += step {
		for x := w.envCfg.Min.X - pad; x <= w.envCfg.Max.X+pad; x += step {
			c := w.nav.CellOf(core.Vec2{X: x, Y: y})
			if _, ok := seen[c]; ok {
				continue
			}
			seen[c] = struct{}{}
			if w.nav.TerrainAt(c) == "" {
				continue
			}
			if d := cellDanger(w.nav, c); d >= hazardDangerFloor {
				sources = append(sources, field.Source{Cell: c, Weight: d})
			}
		}
	}
	if len(sources) == 0 {
		return nil
	}
	return field.Build(w.nav, sources, hazardDecayPerUnit, w.nav.Passable)
}
