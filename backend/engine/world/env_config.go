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
	// Spoor (ground-scent trail) — PD9. ScentTrailStrength maps a scent CHANNEL name (food/prey/
	// predator/carrion) to the fraction of each deposit that also rubs off onto the ground, so which
	// scents linger is content's call (D4/D10). Decay ≤ 0 ⇒ the whole layer is off and the scent grid
	// is byte-identical to one that never had it.
	ScentTrailStrength map[core.Tag]float64
	ScentTrailDecay    float64
	ScentTrailCap      float64
	FaunaDT           float64
	FaunaMoveDeadband float64           // FM9 locomotion deadband: §6 speed below this ⇒ hold (0 ⇒ off)
	DrinkableTerrains map[core.Tag]bool // FM4: terrain IDs whose water is drinkable (config-derived: salinity ≤ max ∧ moisture ≥ min); water-attraction field sources. Empty ⇒ no water field
	WaterFieldDecay   float64           // FM4: water-attraction intensity decay per world-unit (reach = weight/decay); ≤ 0 ⇒ no water field
	FaunaCadence      fauna.Cadence
	FaunaCombat       fauna.CombatParams
	RespawnCadence    core.Tick
	MaxSpeed          float64
}
