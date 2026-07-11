package world

import (
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
)

// EnvConfig carries world.yaml-derived env geometry, cadence, and fauna/scent
// tuning. platform/config builds it from content; world consumes it when env is
// installed.
type EnvConfig struct {
	Min, Max          core.Vec2
	NavmapCellSize    float64
	ClimateGridCols   int
	ClimateGridRows   int
	ClimateStep       int
	FloraStep         int
	DecayStep         int
	ScentCellSize     float64
	ScentSpread       int
	FaunaDT           float64
	FaunaMoveDeadband float64           // FM9 locomotion deadband: §6 speed below this ⇒ hold (0 ⇒ off)
	DrinkableTerrains map[core.Tag]bool // FM4: terrain IDs whose water is drinkable (config-derived: salinity ≤ max ∧ moisture ≥ min); water-attraction field sources. Empty ⇒ no water field
	WaterFieldDecay   float64           // FM4: water-attraction intensity decay per world-unit (reach = weight/decay); ≤ 0 ⇒ no water field
	FaunaCadence      fauna.Cadence
	FaunaCombat       fauna.CombatParams
	RespawnCadence    core.Tick
	MaxSpeed          float64
}
