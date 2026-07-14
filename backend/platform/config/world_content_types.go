package config

import (
	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/engine/world"
)

type worldContent struct {
	WorldEnv         *world.EnvConfig
	ClimateCfg       *climate.Config
	ClimateRules     *climate.Rules
	NavCfg           *navmap.Config
	TerrainTypes     map[navmap.TerrainID]navmap.TerrainType
	TerrainAttrs     map[navmap.TerrainID]map[core.Tag]float64
	FloraRules       *flora.Rules
	FaunaRules       *fauna.Rules
	DecayRules       *decay.Rules
	ScentEmitters    map[core.Tag][]core.Tag
	CoverKinds       map[core.Tag]bool
	WindBlockerKinds map[core.Tag]bool
	CovererKinds     map[core.Tag]bool
	RespawnTargets   map[core.Tag]int
}

type worldDoc struct {
	SchemaVersion int `yaml:"schema_version"`
	Bounds        struct {
		Min []float64 `yaml:"min"`
		Max []float64 `yaml:"max"`
	} `yaml:"bounds"`
	Grids struct {
		NavmapCellSize  float64 `yaml:"navmap_cell_size"`
		ClimateGridCols int     `yaml:"climate_grid_cols"`
		ClimateGridRows int     `yaml:"climate_grid_rows"`
		ScentCellSize   float64 `yaml:"scent_cell_size"`
	} `yaml:"grids"`
	Motion struct {
		FaunaDT      float64 `yaml:"fauna_dt"`
		MaxSpeed     float64 `yaml:"max_speed"`
		MoveDeadband float64 `yaml:"move_deadband"` // FM9 locomotion deadband: §6 speed below this ⇒ hold (0 ⇒ off)
	} `yaml:"motion"`
	Water struct {
		SalinityMax float64 `yaml:"salinity_max"` // FM4: terrain salinity ≤ this = fresh (drinkable); excludes sea
		MoistureMin float64 `yaml:"moisture_min"` // FM4: AND moisture ≥ this = open water (river/lake, not damp soil); ≤ 0 ⇒ water field OFF
		FieldDecay  float64 `yaml:"field_decay"`  // FM4: attraction intensity decay per world-unit (reach = weight/decay)
	} `yaml:"water"`
	Cadence struct {
		ClimateStep                int     `yaml:"climate_step"`
		FloraStep                  int     `yaml:"flora_step"`
		DecayStep                  int     `yaml:"decay_step"`
		ScentSpread                int     `yaml:"scent_spread"`
		FaunaDormantPeriod         int     `yaml:"fauna_dormant_period"`
		FaunaWakeCooldown          int     `yaml:"fauna_wake_cooldown"`
		SleepWakeScentThreshold    float64 `yaml:"sleep_wake_scent_threshold"` // SS3: torpor F45 wake gate (predator scent ≥ this to wake a sleeper)
		ExchangeMinTicks           int     `yaml:"exchange_min_ticks"`
		ExchangeMaxTicks           int     `yaml:"exchange_max_ticks"`
		EngageCooldownMinTicks     int     `yaml:"engage_cooldown_min_ticks"`
		EngageCooldownMaxTicks     int     `yaml:"engage_cooldown_max_ticks"`
		DisengageRangeFactor       float64 `yaml:"disengage_range_factor"`
		StaminaDropThreshold       float64 `yaml:"stamina_drop_threshold"`
		StaminaDrainPerTick        float64 `yaml:"stamina_drain_per_tick"`
		StaminaRecoverPerTick      float64 `yaml:"stamina_recover_per_tick"`
		FatiguePursuitPerTick      float64 `yaml:"fatigue_pursuit_per_tick"`
		FatigueRecoverPerTick      float64 `yaml:"fatigue_recover_per_tick"`
		SleepFatigueRecoverPerTick float64 `yaml:"sleep_fatigue_recover_per_tick"` // SS2: deep-torpor fatigue recovery while sleeping
		VitalRegenPerTick          float64 `yaml:"vital_regen_per_tick"`
		VitalCapDamageFraction     float64 `yaml:"vital_cap_damage_fraction"`
		HideDuration               int     `yaml:"hide_duration"`
		HiddenFlushFactor          float64 `yaml:"hidden_flush_factor"`
		HideCoverFactor            float64 `yaml:"hide_cover_factor"`
		CoverRadiusFactor          float64 `yaml:"cover_radius_factor"`
		ConcealFactor              float64 `yaml:"conceal_factor"`
		RespawnCadence             int     `yaml:"respawn_cadence"`
	} `yaml:"cadence"`
}

type climateDoc struct {
	SchemaVersion int `yaml:"schema_version"`
	Init          struct {
		InitialMoisture    float64 `yaml:"initial_moisture"`
		InitialTemperature float64 `yaml:"initial_temperature"`
	} `yaml:"init"`
	Transitions []struct {
		From string `yaml:"from"`
		To   string `yaml:"to"`
		When string `yaml:"when"`
	} `yaml:"transitions"`
	Balance struct {
		IceType           string  `yaml:"ice_type"`
		RainProbPerHour   float64 `yaml:"rain_prob_per_hour"`
		RainHardCapHours  int64   `yaml:"rain_hard_cap_hours"`
		RainDurMinHours   int64   `yaml:"rain_dur_min_hours"`
		RainDurMaxHours   int64   `yaml:"rain_dur_max_hours"`
		MoistureRainRate  float64 `yaml:"moisture_rain_rate"`
		EvapBaseRate      float64 `yaml:"evap_base_rate"`
		EvapTempScale     float64 `yaml:"evap_temp_scale"`
		AnnualMid         float64 `yaml:"annual_mid"`
		AnnualAmp         float64 `yaml:"annual_amp"`
		AnnualPhase       float64 `yaml:"annual_phase"`
		TempDayPeak       float64 `yaml:"temp_day_peak"`
		TempNightLow      float64 `yaml:"temp_night_low"`
		TempRainDrop      float64 `yaml:"temp_rain_drop"`
		WindPrevailingDir float64 `yaml:"wind_prevailing_dir"`
		WindDirDrift      float64 `yaml:"wind_dir_drift"`
		WindDirReversion  float64 `yaml:"wind_dir_reversion"`
		WindMagMean       float64 `yaml:"wind_mag_mean"`
		WindMagNoise      float64 `yaml:"wind_mag_noise"`
		SnowFreezeC       float64 `yaml:"snow_freeze_c"`
		SnowAccumRate     float64 `yaml:"snow_accum_rate"`
		SnowMeltRate      float64 `yaml:"snow_melt_rate"`
	} `yaml:"balance"`
}

type terrainDoc struct {
	SchemaVersion int `yaml:"schema_version"`
	Terrains      []struct {
		ID           string             `yaml:"id"`
		BaseCost     float64            `yaml:"base_cost"`
		Passable     bool               `yaml:"passable"`
		RequiredTags []string           `yaml:"required_tags"`
		Attrs        map[string]float64 `yaml:"attrs"`
	} `yaml:"terrains"`
}

type objectsDoc struct {
	SchemaVersion int             `yaml:"schema_version"`
	ObjectKinds   []objectKindDoc `yaml:"object_kinds"`
	ItemKinds     []itemKindDoc   `yaml:"item_kinds"`
}

type objectKindDoc struct {
	ID     string   `yaml:"id"`
	Mobile bool     `yaml:"mobile"`
	Tags   []string `yaml:"tags"`
	Flora  *struct {
		Suitability    any       `yaml:"suitability"`
		LengthRate     any       `yaml:"length_rate"`
		WidthRate      any       `yaml:"width_rate"`
		Stages         []float64 `yaml:"stages"`
		YieldStage     int       `yaml:"yield_stage"`
		PropagateStage int       `yaml:"propagate_stage"`
		Shade          struct {
			Radius  any `yaml:"radius"`
			Opacity any `yaml:"opacity"`
		} `yaml:"shade"`
		Propagation struct {
			Radius           any `yaml:"radius"`
			Chance           any `yaml:"chance"`
			CarryingCapacity any `yaml:"carrying_capacity"` // K: 1k density target — scalar OR §6(terrain attrs); absent = legacy 1/(1+n)
		} `yaml:"propagation"`
		DeathThreshold  float64 `yaml:"death_threshold"`
		DeathHysteresis int     `yaml:"death_hysteresis"`
	} `yaml:"flora"`
	Fauna *struct {
		Actions []struct {
			Action  string `yaml:"action"`
			Utility any    `yaml:"utility"`
		} `yaml:"actions"`
		Drives []struct {
			ID        string  `yaml:"id"`
			Rate      float64 `yaml:"rate"`
			Decay     float64 `yaml:"decay"`
			WaryLevel float64 `yaml:"wary_level"`
			FleeLevel float64 `yaml:"flee_level"`
		} `yaml:"drives"`
		ApparentTemp    any      `yaml:"apparent_temp"`
		ComfortTemp     float64  `yaml:"comfort_temp"`
		ThermalBand     float64  `yaml:"thermal_band"`
		HazardAvoidance float64  `yaml:"hazard_avoidance"`
		MoveDeadband    float64  `yaml:"move_deadband"` // FM14a per-species deadband (>0 overrides global motion.move_deadband)
		Speed           any      `yaml:"speed"`
		TurnRate        any      `yaml:"turn_rate"`
		AttackPower     any      `yaml:"attack_power"`
		Hit             any      `yaml:"hit"`
		Feed            any      `yaml:"feed"`
		Graze           any      `yaml:"graze"`
		Drink           any      `yaml:"drink"`
		HideChance      any      `yaml:"hide_chance"`
		CoverCost       float64  `yaml:"cover_cost"`
		RespawnTarget   int      `yaml:"respawn_target"`
		Diet            []string `yaml:"diet"`
		Senses          struct {
			SmellRadius float64 `yaml:"smell_radius"`
			SightRadius float64 `yaml:"sight_radius"`
			FovArc      float64 `yaml:"fov_arc"`
		} `yaml:"senses"`
		TerrainCost map[string]float64 `yaml:"terrain_cost"`
		Impassable  []string           `yaml:"impassable"`
	} `yaml:"fauna"`
	Harvest *struct {
		Yields any `yaml:"yields"`
	} `yaml:"harvest"`
	Source *struct {
		DepletedTerrain string `yaml:"depleted_terrain"`
	} `yaml:"source"`
}

type itemKindDoc struct {
	ID     string             `yaml:"id"`
	Tags   []string           `yaml:"tags"`
	Supply map[string]float64 `yaml:"supply"`
	Decay  *struct {
		BaseRate float64 `yaml:"baseRate"`
		Accel    any     `yaml:"accel"`
		States   []struct {
			Threshold float64            `yaml:"threshold"`
			Supply    map[string]float64 `yaml:"supply"`
			Transform []struct {
				Item string `yaml:"item"`
				Qty  int    `yaml:"qty"`
			} `yaml:"transform"`
		} `yaml:"states"`
	} `yaml:"decay"`
}

type statSet struct{ reg *stats.Registry }

func (s statSet) Has(id core.StatID) bool { return s.reg != nil && s.reg.Has(id) }
