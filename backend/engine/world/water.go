package world

import (
	"math"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/field"
	"github.com/dogring/bdg/engine/space/navmap"
)

// Fauna water-ATTRACTION field (docs/plans/fauna.md §4.1 FM4 / FM4-src). world builds ONE shared static
// attraction field whose sources are the DRINKABLE-water terrain cells (config-derived DrinkableTerrains:
// terrain salinity ≤ max ∧ moisture ≥ min — river/lake in, sea/soil out; no Go terrain-name hardcoding).
// A thirsty animal steers up the field Gradient (toward the nearest water) ONLY when it picks the
// `seek:water` action, so non-drinking species never query it (FM4). Mirrors the hazard field build.
const waterSourceWeight = 1.0 // per-drinkable-cell source intensity; reach = weight / WaterFieldDecay

// buildWaterField enumerates navmap cells over the world bounds (same padded sample step as the hazard
// field), collects the drinkable-water cells as equal-weight field sources, and builds the shared static
// water-attraction field. Deterministic (field.Build is D12 and sorts sources). Returns nil when there is
// no navmap, no drinkable terrain configured, a non-positive decay, or no drinkable cells (⇒ no water
// steering, byte-identical to pre-FM4).
func (w *World) buildWaterField() *field.Field {
	if w.nav == nil || len(w.envCfg.DrinkableTerrains) == 0 || w.envCfg.WaterFieldDecay <= 0 {
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
			t := w.nav.TerrainAt(c)
			if t == "" {
				continue
			}
			if w.envCfg.DrinkableTerrains[t] {
				sources = append(sources, field.Source{Cell: c, Weight: waterSourceWeight})
			}
		}
	}
	if len(sources) == 0 {
		return nil
	}
	return field.Build(w.nav, sources, w.envCfg.WaterFieldDecay, w.nav.Passable)
}
