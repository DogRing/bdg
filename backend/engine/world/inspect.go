package world

import (
	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/scent"
)

// Animals returns the live animal set in deterministic ObjectID order.
func (w *World) Animals() []fauna.Animal {
	out := make([]fauna.Animal, 0, len(w.animalIDs))
	for _, id := range w.animalIDs {
		if a := w.animals[id]; a != nil {
			out = append(out, cloneAnimal(*a))
		}
	}
	return out
}

// ScentIntensityAt exposes the committed scent value for headless integration checks.
func (w *World) ScentIntensityAt(ch scent.Channel, pos core.Vec2) float64 {
	if w.scent == nil {
		return 0
	}
	return w.scent.IntensityAt(ch, pos)
}

// ClimateCellAt returns the climate cell at pos when climate is installed.
func (w *World) ClimateCellAt(pos core.Vec2) (climate.CellState, bool) {
	if w.climateState == nil {
		return climate.CellState{}, false
	}
	return w.climateState.CellAt(pos), true
}
