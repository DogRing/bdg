package config

import (
	"fmt"
	"sort"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/engine/world"
	"gopkg.in/yaml.v3"
)

func buildWorldContent(raw map[string][]byte, statReg *stats.Registry, actReg *actions.Registry) (worldContent, error) {
	var out worldContent

	var terrain terrainDoc
	hasTerrain := len(raw["terrain.yaml"]) > 0
	if hasTerrain {
		if err := yaml.Unmarshal(raw["terrain.yaml"], &terrain); err != nil {
			return out, fmt.Errorf("config: terrain parse: %w", err)
		}
		types, err := buildTerrainTypes(terrain)
		if err != nil {
			return out, err
		}
		out.TerrainTypes = types
	}

	var wd worldDoc
	hasWorld := len(raw["world.yaml"]) > 0
	if hasWorld {
		if err := yaml.Unmarshal(raw["world.yaml"], &wd); err != nil {
			return out, fmt.Errorf("config: world parse: %w", err)
		}
		env, nav, err := buildWorldEnv(wd)
		if err != nil {
			return out, err
		}
		out.WorldEnv = env
		out.NavCfg = nav
	}

	var cd climateDoc
	hasClimate := len(raw["climate.yaml"]) > 0
	if hasClimate {
		if err := yaml.Unmarshal(raw["climate.yaml"], &cd); err != nil {
			return out, fmt.Errorf("config: climate parse: %w", err)
		}
		if !hasTerrain {
			return out, fmt.Errorf("config: climate transitions require terrain.yaml")
		}
		rules, err := buildClimateRules(cd, out.TerrainTypes, statReg)
		if err != nil {
			return out, err
		}
		out.ClimateRules = rules
		if hasWorld {
			out.ClimateCfg = buildClimateConfig(wd, cd)
		}
	}

	hasObjects := len(raw["objects.yaml"]) > 0
	if hasObjects {
		var objects objectsDoc
		if err := yaml.Unmarshal(raw["objects.yaml"], &objects); err != nil {
			return out, fmt.Errorf("config: objects parse: %w", err)
		}
		out.ScentEmitters = buildScentEmitters(objects)
		out.CoverKinds = buildCoverKinds(objects)
		out.WindBlockerKinds = buildWindBlockerKinds(objects)
		out.CovererKinds = buildCovererKinds(objects)
		out.RespawnTargets = buildRespawnTargets(objects)
		ids := collectObjectIDs(objects)
		terrainIDs := terrainIDSet(out.TerrainTypes)
		attrs := terrainAttrSet(terrain)
		if floraRules, err := buildFloraRules(objects, ids.items, attrs, statReg); err != nil {
			return out, err
		} else {
			out.FloraRules = floraRules
		}
		if faunaRules, err := buildFaunaRules(objects, terrainIDs, statReg, actReg); err != nil {
			return out, err
		} else {
			out.FaunaRules = faunaRules
		}
		if decayRules, err := buildDecayRules(objects, ids.items, statReg); err != nil {
			return out, err
		} else {
			out.DecayRules = decayRules
		}
		if err := checkObjectTerrainRefs(objects, terrainIDs); err != nil {
			return out, err
		}
	}

	return out, nil
}

func buildWorldEnv(wd worldDoc) (*world.EnvConfig, *navmap.Config, error) {
	if len(wd.Bounds.Min) != 2 || len(wd.Bounds.Max) != 2 {
		return nil, nil, fmt.Errorf("config: world bounds min/max must have two values")
	}
	min := core.Vec2{X: wd.Bounds.Min[0], Y: wd.Bounds.Min[1]}
	max := core.Vec2{X: wd.Bounds.Max[0], Y: wd.Bounds.Max[1]}
	if max.X <= min.X || max.Y <= min.Y {
		return nil, nil, fmt.Errorf("config: world bounds invalid: max must exceed min on both axes")
	}
	// navmap_cell_size (hex terrain/path/render fidelity) is DECOUPLED from spatial_hash_cell
	// (square perception grid) — post-hex-migration they are independent indices over the same
	// continuous space (docs/hex-grid.md, docs/shelter.md Q-M1). No sync constraint between them.
	floor := wd.Motion.MaxSpeed * float64(wd.Cadence.ScentSpread)
	if wd.Grids.ScentCellSize < floor {
		return nil, nil, fmt.Errorf("config: scent_cell_size %.6g < max_speed %.6g * scent_spread %d", wd.Grids.ScentCellSize, wd.Motion.MaxSpeed, wd.Cadence.ScentSpread)
	}
	env := &world.EnvConfig{
		Min:             min,
		Max:             max,
		NavmapCellSize:  wd.Grids.NavmapCellSize,
		ClimateGridCols: wd.Grids.ClimateGridCols,
		ClimateGridRows: wd.Grids.ClimateGridRows,
		ClimateStep:     wd.Cadence.ClimateStep,
		FloraStep:       wd.Cadence.FloraStep,
		DecayStep:       wd.Cadence.DecayStep,
		ScentCellSize:   wd.Grids.ScentCellSize,
		ScentSpread:     wd.Cadence.ScentSpread,
		FaunaDT:         wd.Motion.FaunaDT,
		FaunaCadence: fauna.Cadence{
			DormantPeriod: wd.Cadence.FaunaDormantPeriod,
			WakeCooldown:  core.Tick(wd.Cadence.FaunaWakeCooldown),
		},
		FaunaCombat: fauna.CombatParams{
			ExchangeMinTicks:       wd.Cadence.ExchangeMinTicks,
			ExchangeMaxTicks:       wd.Cadence.ExchangeMaxTicks,
			EngageCooldownMinTicks: wd.Cadence.EngageCooldownMinTicks,
			EngageCooldownMaxTicks: wd.Cadence.EngageCooldownMaxTicks,
			DisengageRangeFactor:   wd.Cadence.DisengageRangeFactor,
			StaminaDropThreshold:   wd.Cadence.StaminaDropThreshold,
			StaminaDrainPerTick:    wd.Cadence.StaminaDrainPerTick,
			StaminaRecoverPerTick:  wd.Cadence.StaminaRecoverPerTick,
			FatiguePursuitPerTick:  wd.Cadence.FatiguePursuitPerTick,
			FatigueRecoverPerTick:  wd.Cadence.FatigueRecoverPerTick,
			VitalRegenPerTick:      wd.Cadence.VitalRegenPerTick,
			VitalCapDamageFraction: wd.Cadence.VitalCapDamageFraction,
			HideDurationTicks:      wd.Cadence.HideDuration,
			HiddenFlushFactor:      wd.Cadence.HiddenFlushFactor,
			HideCoverFactor:        wd.Cadence.HideCoverFactor,
			CoverRadiusFactor:      wd.Cadence.CoverRadiusFactor,
			ConcealFactor:          wd.Cadence.ConcealFactor,
		},
		RespawnCadence: core.Tick(wd.Cadence.RespawnCadence),
		MaxSpeed:       wd.Motion.MaxSpeed,
	}
	nav := &navmap.Config{
		CellSize: wd.Grids.NavmapCellSize,
		MinX:     min.X,
		MinY:     min.Y,
		MaxX:     max.X,
		MaxY:     max.Y,
	}
	return env, nav, nil
}

func buildClimateConfig(wd worldDoc, cd climateDoc) *climate.Config {
	return &climate.Config{
		GridCols:          wd.Grids.ClimateGridCols,
		GridRows:          wd.Grids.ClimateGridRows,
		WorldMin:          core.Vec2{X: wd.Bounds.Min[0], Y: wd.Bounds.Min[1]},
		WorldMax:          core.Vec2{X: wd.Bounds.Max[0], Y: wd.Bounds.Max[1]},
		InitMoisture:      cd.Init.InitialMoisture,
		InitTemperature:   cd.Init.InitialTemperature,
		RainProbPerHour:   cd.Balance.RainProbPerHour,
		RainHardCapHours:  cd.Balance.RainHardCapHours,
		RainDurMinHours:   cd.Balance.RainDurMinHours,
		RainDurMaxHours:   cd.Balance.RainDurMaxHours,
		MoistureRainRate:  cd.Balance.MoistureRainRate,
		EvapBaseRate:      cd.Balance.EvapBaseRate,
		EvapTempScale:     cd.Balance.EvapTempScale,
		AnnualMid:         cd.Balance.AnnualMid,
		AnnualAmp:         cd.Balance.AnnualAmp,
		AnnualPhase:       cd.Balance.AnnualPhase,
		TempDayPeak:       cd.Balance.TempDayPeak,
		TempNightLow:      cd.Balance.TempNightLow,
		TempRainDrop:      cd.Balance.TempRainDrop,
		WindPrevailingDir: cd.Balance.WindPrevailingDir,
		WindDirDrift:      cd.Balance.WindDirDrift,
		WindDirReversion:  cd.Balance.WindDirReversion,
		WindMagMean:       cd.Balance.WindMagMean,
		WindMagNoise:      cd.Balance.WindMagNoise,
	}
}

func buildTerrainTypes(td terrainDoc) (map[navmap.TerrainID]navmap.TerrainType, error) {
	out := make(map[navmap.TerrainID]navmap.TerrainType, len(td.Terrains))
	for _, t := range td.Terrains {
		id := navmap.TerrainID(t.ID)
		if id == "" {
			return nil, fmt.Errorf("config: terrain id is empty")
		}
		if _, dup := out[id]; dup {
			return nil, fmt.Errorf("config: duplicate terrain id %q", id)
		}
		tags := make([]core.Tag, len(t.RequiredTags))
		for i, tag := range t.RequiredTags {
			tags[i] = core.Tag(tag)
		}
		sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })
		out[id] = navmap.TerrainType{BaseCost: t.BaseCost, Passable: t.Passable, RequiredTags: tags}
	}
	return out, nil
}

type objectIDs struct {
	objects map[core.Tag]bool
	items   map[core.Tag]bool
}

func collectObjectIDs(doc objectsDoc) objectIDs {
	ids := objectIDs{objects: make(map[core.Tag]bool), items: make(map[core.Tag]bool)}
	for _, k := range doc.ObjectKinds {
		ids.objects[core.Tag(k.ID)] = true
	}
	for _, k := range doc.ItemKinds {
		ids.items[core.Tag(k.ID)] = true
	}
	return ids
}
