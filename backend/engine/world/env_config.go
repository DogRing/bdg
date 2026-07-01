package world

import (
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
)

// EnvConfig carries world.yaml-derived env geometry, cadence, and fauna/scent
// tuning. platform/config builds it from content; world consumes it when env is
// installed.
type EnvConfig struct {
	Min, Max        core.Vec2
	NavmapCellSize  float64
	ClimateGridCols int
	ClimateGridRows int
	ClimateStep     int
	FloraStep       int
	DecayStep       int
	ScentCellSize   float64
	ScentSpread     int
	FaunaDT         float64
	FaunaCadence    fauna.Cadence
	FaunaCombat     fauna.CombatParams
	RespawnCadence  core.Tick
	MaxSpeed        float64
}
