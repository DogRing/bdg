package fauna

import (
	"math"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/scent"
)

// ── Cheap path (DORMANT, off-boundary) ───────────────────────────────────────

func cheapPath(a Animal, snap *Snapshot, rules *Rules, newActiveUntil core.Tick) Intent {
	// Drive advance: only accumulators + fear decay (no full sense, no sight query).
	newDrives := rules.cheapDriveAdvance(a.Species, a.Drives, snap.DT)

	// Cheap steer: continue along Heading. Evaluate speed with minimal context.
	env := snap.Env[a.ID] // already validated in Step (panic path is before cheapPath)
	appTemp := computeAppTemp(a, env, rules)
	sCtx := &animalContext{
		animal:  &a,
		drives:  newDrives,
		reading: scent.Reading{}, // zero sense on cheap path
		env:     env,
		appTemp: appTemp,
	}

	speed := rules.Speed(a.Species, sCtx)
	if speed < 0 {
		speed = 0
	}

	// Direction: current heading.
	dir := core.Vec2{X: math.Cos(a.Heading), Y: math.Sin(a.Heading)}

	var nextPos core.Vec2
	var nextHeading float64
	if speed <= 0 {
		nextPos = a.Pos
		nextHeading = a.Heading
	} else {
		tentative := a.Pos.Add(dir.Scale(speed * snap.DT))
		terrain := snap.Terrain.TerrainAt(tentative)
		mult, passable := rules.TerrainCost(a.Species, terrain)
		blocked := snap.Terrain.FootprintBlocked(tentative) || !passable
		if blocked {
			nextPos = a.Pos
			nextHeading = a.Heading
		} else {
			baseCost := snap.Terrain.BaseCost(tentative)
			effectiveCost := baseCost * mult
			if effectiveCost < 1 {
				effectiveCost = 1
			}
			effectiveSpeed := speed / effectiveCost
			nextPos = a.Pos.Add(dir.Scale(effectiveSpeed * snap.DT))
			nextHeading = a.Heading
		}
	}

	return Intent{
		Animal:              a.ID,
		Action:              a.CurrentAction, // held (dormant off-boundary)
		Target:              "",
		NextPos:             nextPos,
		NextHeading:         nextHeading,
		Drives:              newDrives,
		Stamina:             a.Stamina,
		Vital:               regenVital(a, snap.DT, snap.Combat),
		VitalCap:            effectiveVitalCap(a),
		ActiveUntil:         newActiveUntil,
		EngagedWith:         a.EngagedWith,
		NextExchangeTick:    a.NextExchangeTick,
		EngageCooldownUntil: a.EngageCooldownUntil,
	}
}
