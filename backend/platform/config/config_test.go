package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Test data ─────────────────────────────────────────────────────────────────

// validStatsYAML is a minimal valid stats.yaml for unit tests.
const validStatsYAML = `schema_version: 1
stats:
  - id: Strength
    kind: capability
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Agility
    kind: capability
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Intelligence
    kind: capability
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.18 }
    inherit: 0.5
  - id: Honesty
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.6, sd: 0.2 }
    inherit: 0.4
  - id: Aggression
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.2 }
    inherit: 0.4
`

// validNeedsYAML is a minimal valid needs.yaml for unit tests.
const validNeedsYAML = `schema_version: 1
needs:
  - id: Satiety
    kind: consumable
    default:
      posture: MaintainAbove
      setpoint: 0.55
      referent: Self
    salience:
      curve: deficit
      gain: 1.0
  - id: Hydration
    kind: consumable
    default:
      posture: MaintainAbove
      setpoint: 0.50
      referent: Self
    salience:
      curve: deficit
      gain: 1.1
`

// validActionsYAML is a minimal valid actions.yaml for unit tests.
const validActionsYAML = `schema_version: 1
actions:
  - id: Forage
    tags: [ "uses:Agility", "effort:low", "noise:low", "abstraction:low" ]
    target_kind: berry_bush
    requires: [ at_target ]
    produces: [ has_food ]
    produces_item: berries
    duration: 12
    interruptible: true
  - id: Eat
    tags: [ "effort:low" ]
    requires: [ has_food ]
    produces: [ has_Satiety ]
    duration: 6
    effect: { Satiety: 0.40 }
    interruptible: true
`

// validGatesYAML is a minimal valid gates.yaml for unit tests.
const validGatesYAML = `schema_version: 3
gates:
  - id: capability_floor
    tags: [ "uses:Strength", "uses:Agility", "uses:Intelligence" ]
    expr:
      and:
        - or: [ { not: { tag: "uses:Strength" } },     { stat: Strength,     op: ">=", value: 0.15 } ]
        - or: [ { not: { tag: "uses:Agility" } },       { stat: Agility,      op: ">=", value: 0.15 } ]
        - or: [ { not: { tag: "uses:Intelligence" } },  { stat: Intelligence, op: ">=", value: 0.15 } ]
`

// validBalanceYAML is a minimal valid balance.yaml for unit tests.
const validBalanceYAML = `schema_version: 1
world:
  tick_minutes: 1
  real_scale: 12
  day_minutes: 1440
  spatial_hash_cell: 8.0
  outcome_difficulty_base: 50.0
  backup_every_ticks: 60
perception:
  sight_radius: 18.0
  smell_radius: 10.0
  hearing_radius: 14.0
needs:
  Satiety:   { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
  Hydration: { decay_per_tick: 0.00110, satisfaction_threshold: 0.50 }
tag_levels:
  effort:      { none: 0.0, low: 0.20, med: 0.50, high: 0.90 }
  risk:        { low: 0.20, med: 0.50, high: 0.90 }
cost_terms:
  time:   { per_minute: 0.010 }
  effort: { weight: 1.0 }
mood:
  lambda: 0.25
  decay: 0.02
  baseline: 0.0
adrenaline:
  trigger_urgency: 0.65
  surge: 0.6
  decay: 0.03
  max: 1.0
  crash_stamina_penalty: 0.50
stamina:
  max: 1.0
  drain_per_effort: 0.015
  regen_rest: 0.010
  regen_sleep: 0.030
urgency:
  from_deficit: 1.4
  budget_penalty: 0.6
self_calibration:
  beta: 0.08
gossip:
  alpha: 0.12
  min_trust: 0.05
resentment:
  affinity_drop: 0.15
  aggression_drift: 0.02
  per_trigger: 0.05
  threshold: 0.30
planning:
  budget_base: 24
  budget_per_intelligence: 60
  stickiness: 0.15
  goal_deadband: 0.08
forward_sim:
  horizon_minutes: 720
  horizon_per_intelligence: 1440
  step_minutes: 15
salience:
  proximity_gain: 0.5
  proximity_falloff: 12.0
regen:
  berry_bush: 480
  prey_respawn: 720
generation:
  village_size: 40
  stamina_start: 1.0
  mood_start: 0.0
  initial_belief_noise: 0.15
planner:
  budget:
    max_depth: 6
    max_actions: 16
    max_nodes: 256
  base_horizon_ticks: 720
  urgency_threshold: 0.65
  tag_costs:
    "effort:low": 0.20
    "effort:med": 0.50
coping:
  rebind_min_intelligence: 0.4
  apathy_fail_streak: 3
  apathy_recover_mood: 0.15
  apathy_budget_penalty: 0.5
trade:
  claim_inflate_min: 0.50
  claim_inflate_max: 0.90
social:
  bond_affinity_gain: 0.20
  min_care_threshold: 0.3
threats:
  hostile_tags:
    - "violent:high"
    - "hostile"
  safety_threat_threshold: 0.30
  per_threat_intensity: 0.20
  safety_decay: 0.05
intelligence:
  lookahead_threshold: 0.4
  rebind_threshold: 0.4
  other_intel_threshold: 0.5
values:
  weights:
    Safety:     1.40
    Hydration:  1.05
    Satiety:    1.00
  collective_aggregation_mode: "min"
politics:
  role_convergence_threshold: 0.5
  influence_weight: 0.5
  rely_cost_threshold: 1.2
  relyon_delta: 0.15
  vote_rely_threshold: 0.4
  vote_urgency_threshold: 0.15
  vote_relyon_delta: 0.10
gates:
  stamina_effort_high_threshold: 0.20
  apathy_mood_threshold: -0.60
  conscience_urgency_threshold: 0.70
  adrenaline_cost_multiplier: 0.50
`

// ── Schema files for tests ────────────────────────────────────────────────────

const testStatsSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema_version", "stats"],
  "properties": {
    "schema_version": { "const": 1 },
    "stats": {
      "type": "array",
      "minItems": 1,
      "items": { "$ref": "#/$defs/stat" }
    }
  },
  "$defs": {
    "stat": {
      "type": "object",
      "additionalProperties": false,
      "required": ["id", "kind", "range", "gen", "inherit"],
      "properties": {
        "id": { "type": "string", "pattern": "^[A-Za-z][A-Za-z0-9_]*$" },
        "kind": { "enum": ["capability", "disposition"] },
        "range": { "type": "array", "items": { "type": "number" }, "minItems": 2, "maxItems": 2 },
        "gen": {
          "type": "object",
          "required": ["dist", "mean", "sd"],
          "properties": {
            "dist": { "enum": ["normal", "uniform"] },
            "mean": { "type": "number" },
            "sd": { "type": "number", "minimum": 0 }
          }
        },
        "inherit": { "type": "number", "minimum": 0, "maximum": 1 }
      }
    }
  }
}`

const testNeedsSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema_version", "needs"],
  "properties": {
    "schema_version": { "const": 1 },
    "needs": {
      "type": "array",
      "minItems": 1,
      "items": { "$ref": "#/$defs/need" }
    }
  },
  "$defs": {
    "need": {
      "type": "object",
      "additionalProperties": false,
      "required": ["id", "kind", "default", "salience"],
      "properties": {
        "id": { "type": "string", "pattern": "^[A-Za-z][A-Za-z0-9_]*$" },
        "kind": { "enum": ["consumable", "conditional"] },
        "default": {
          "type": "object",
          "required": ["posture", "setpoint", "referent"],
          "properties": {
            "posture": { "enum": ["Maximize", "MaintainAbove", "PreventBelow"] },
            "setpoint": { "type": "number", "minimum": 0, "maximum": 1 },
            "referent": { "enum": ["Self", "Other", "Place", "Collective"] }
          }
        },
        "salience": {
          "type": "object",
          "required": ["curve", "gain"],
          "properties": {
            "curve": { "enum": ["deficit", "gap_to_max"] },
            "gain": { "type": "number", "minimum": 0 }
          }
        }
      }
    }
  }
}`

const testActionsSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema_version", "actions"],
  "properties": {
    "schema_version": { "const": 1 },
    "actions": {
      "type": "array",
      "minItems": 1,
      "items": { "$ref": "#/$defs/action" }
    }
  },
  "$defs": {
    "action": {
      "type": "object",
      "additionalProperties": false,
      "required": ["id", "tags", "duration"],
      "properties": {
        "id": { "type": "string", "pattern": "^[A-Za-z][A-Za-z0-9_]*$" },
        "tags": { "type": "array", "minItems": 1, "items": { "type": "string" } },
        "target_kind": { "type": "string" },
        "requires": { "type": "array", "items": { "type": "string" } },
        "produces": { "type": "array", "items": { "type": "string" } },
        "produces_item": { "type": "string" },
        "duration": { "type": "integer", "minimum": 1 },
        "effect": { "type": "object", "minProperties": 1, "additionalProperties": { "type": "number" } },
        "interruptible": { "type": "boolean" }
      }
    }
  }
}`

const testGatesSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema_version", "gates"],
  "properties": {
    "schema_version": { "const": 3 },
    "gates": {
      "type": "array",
      "minItems": 1,
      "items": { "$ref": "#/$defs/gate" }
    }
  },
  "$defs": {
    "gate": {
      "type": "object",
      "additionalProperties": false,
      "required": ["id", "expr"],
      "properties": {
        "id": { "type": "string", "pattern": "^[A-Za-z][A-Za-z0-9_]*$" },
        "tags": { "type": "array", "items": { "type": "string" } },
        "expr": { "type": "object" }
      }
    }
  }
}`

const testBalanceSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": [
    "schema_version", "world", "perception", "generation", "tag_levels",
    "cost_terms", "mood", "adrenaline", "stamina", "urgency",
    "self_calibration", "gossip", "salience", "regen",
    "needs", "values", "planner"
  ],
  "properties": {
    "schema_version": { "const": 1 },
    "world": { "type": "object" },
    "perception": { "type": "object", "required": ["sight_radius", "smell_radius", "hearing_radius"] },
    "generation": { "type": "object" },
    "tag_levels": { "type": "object" },
    "cost_terms": { "type": "object" },
    "mood": { "type": "object" },
    "adrenaline": { "type": "object" },
    "stamina": { "type": "object" },
    "urgency": { "type": "object" },
    "self_calibration": { "type": "object" },
    "gossip": { "type": "object" },
    "resentment": { "type": "object" },
    "planning": { "type": "object" },
    "forward_sim": { "type": "object" },
    "salience": { "type": "object" },
    "regen": { "type": "object" },
    "needs": { "type": "object" },
    "values": { "type": "object", "required": ["weights"] },
    "planner": { "type": "object", "required": ["budget", "base_horizon_ticks", "tag_costs", "urgency_threshold"] },
    "intelligence": { "type": "object" },
    "social": { "type": "object" },
    "politics": { "type": "object" },
    "threats": { "type": "object" },
    "trade": { "type": "object" },
    "coping": { "type": "object" },
    "gates": { "type": "object" }
  }
}`

const testWorldSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["schema_version", "bounds", "grids", "motion", "cadence"],
  "properties": {
    "schema_version": { "const": 1 },
    "bounds": { "type": "object", "required": ["min", "max"] },
    "grids": { "type": "object", "required": ["navmap_cell_size", "climate_grid_cols", "climate_grid_rows", "scent_cell_size"] },
    "motion": { "type": "object", "required": ["fauna_dt", "max_speed"] },
    "cadence": { "type": "object", "required": ["climate_step", "flora_step", "decay_step", "scent_spread", "fauna_dormant_period", "fauna_wake_cooldown"] }
  }
}`

const testClimateSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["schema_version", "init", "transitions", "balance"],
  "properties": {
    "schema_version": { "const": 1 },
    "init": { "type": "object" },
    "transitions": { "type": "array" },
    "balance": { "type": "object" }
  }
}`

const testTerrainSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["schema_version", "terrains"],
  "properties": {
    "schema_version": { "const": 1 },
    "terrains": { "type": "array" }
  }
}`

const testObjectsSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["schema_version"],
  "properties": {
    "schema_version": { "const": 1 },
    "object_kinds": { "type": "array" },
    "item_kinds": { "type": "array" }
  }
}`

const validWorldYAML = `schema_version: 1
bounds: { min: [0.0, 0.0], max: [64.0, 64.0] }
grids:
  navmap_cell_size: 8.0
  climate_grid_cols: 4
  climate_grid_rows: 4
  scent_cell_size: 8.4
motion:
  fauna_dt: 1.0
  max_speed: 1.4
cadence:
  climate_step: 60
  flora_step: 60
  decay_step: 60
  scent_spread: 6
  fauna_dormant_period: 100
  fauna_wake_cooldown: 30
`

const validClimateYAML = `schema_version: 1
init:
  initial_moisture: 0.5
  initial_temperature: 12.5
transitions:
  - { from: soil, to: sand, when: "moisture < 0.15" }
balance:
  rain_prob_per_hour: 0.0042
  rain_hard_cap_hours: 720
  rain_dur_min_hours: 2
  rain_dur_max_hours: 12
  moisture_rain_rate: 0.08
  evap_base_rate: 0.01
  evap_temp_scale: 0.0015
  annual_mid: 12.5
  annual_amp: 17.5
  annual_phase: 0.0
  temp_day_peak: 6.0
  temp_night_low: -6.0
  temp_rain_drop: 3.0
  wind_prevailing_dir: 1.5708
  wind_dir_drift: 0.15
  wind_dir_reversion: 0.05
  wind_mag_mean: 0.30
  wind_mag_noise: 0.10
`

const validTerrainYAML = `schema_version: 1
terrains:
  - id: soil
    base_cost: 1.0
    passable: true
    attrs: { moisture: 0.5, slope: 0.1, salinity: 0.0 }
  - id: sand
    base_cost: 1.3
    passable: true
    attrs: { moisture: 0.2, slope: 0.1, salinity: 0.1 }
`

const validObjectsWorldYAML = `schema_version: 1
object_kinds:
  - id: grass
    mobile: false
    tags: [ scent:food, forage ]
    flora:
      suitability: "moisture*0.5 + (1 - slope)*0.5"
      length_rate: 0.05
      width_rate: 0.04
      stages: [0.1, 0.3]
      yield_stage: 1
      propagate_stage: 1
      shade: { radius: "width * 0.05", opacity: "width * 0.02" }
      propagation: { radius: 3.0, chance: 0.30 }
      death_threshold: 0.10
      death_hysteresis: 3
    harvest:
      yields:
        - { item: berries, chance: "0.5", qty: [1, 2] }
  - id: deer
    mobile: true
    tags: [ scent:prey, game ]
    fauna:
      actions:
        - { action: Forage, utility: "hunger * (0.3 + scent.food)" }
      drives:
        - { id: hunger, rate: 0.0008 }
        - { id: thermal }
      apparent_temp: "temperature - wind.mag * 6 - moisture * 3"
      speed: "Agility * 1.2 - thermal * 0.6"
      diet: [ forage ]
      senses: { smell_radius: 10.0, sight_radius: 14.0, fov_arc: 1.05 }
      terrain_cost: { sand: 1.2 }
      impassable: []
item_kinds:
  - id: berries
    stackable: true
    supply: { Satiety: 0.25 }
    decay:
      baseRate: 1.0
      accel: "0.5 + temperature*1.0 + moisture*0.5"
      states:
        - { name: fresh }
        - { name: stale, threshold: 0.4, supply: { Satiety: 0.15 } }
        - { name: gone, threshold: 1.0 }
`

// ── Tests ─────────────────────────────────────────────────────────────────────

// writeTestContent writes the test YAML and schema files to a temp directory
// and returns the path. The caller is responsible for cleanup.
func writeTestContent(t *testing.T, contentFiles, schemaFiles map[string]string) string {
	t.Helper()
	dir := t.TempDir()

	contentDir := filepath.Join(dir, "content")
	schemaDir := filepath.Join(contentDir, "schema")

	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatalf("mkdir schema dir: %v", err)
	}

	for fn, data := range contentFiles {
		if err := os.WriteFile(filepath.Join(contentDir, fn), []byte(data), 0644); err != nil {
			t.Fatalf("write %s: %v", fn, err)
		}
	}
	for fn, data := range schemaFiles {
		if err := os.WriteFile(filepath.Join(schemaDir, fn), []byte(data), 0644); err != nil {
			t.Fatalf("write schema %s: %v", fn, err)
		}
	}

	return contentDir
}

func baseContentFiles() map[string]string {
	return map[string]string{
		"stats.yaml":   validStatsYAML,
		"needs.yaml":   validNeedsYAML,
		"actions.yaml": validActionsYAML,
		"gates.yaml":   validGatesYAML,
		"balance.yaml": validBalanceYAML,
	}
}

func baseSchemaFiles() map[string]string {
	return map[string]string{
		"stats.schema.json":   testStatsSchema,
		"needs.schema.json":   testNeedsSchema,
		"actions.schema.json": testActionsSchema,
		"gates.schema.json":   testGatesSchema,
		"balance.schema.json": testBalanceSchema,
	}
}

func worldContentFiles() map[string]string {
	files := baseContentFiles()
	files["world.yaml"] = validWorldYAML
	files["climate.yaml"] = validClimateYAML
	files["terrain.yaml"] = validTerrainYAML
	files["objects.yaml"] = validObjectsWorldYAML
	return files
}

func worldSchemaFiles() map[string]string {
	files := baseSchemaFiles()
	files["world.schema.json"] = testWorldSchema
	files["climate.schema.json"] = testClimateSchema
	files["terrain.schema.json"] = testTerrainSchema
	files["objects.schema.json"] = testObjectsSchema
	return files
}

// TestLoadValidContent verifies that Load returns a fully populated LoadOutput
// for valid content files.
func TestLoadValidContent(t *testing.T) {
	contentFiles := map[string]string{
		"stats.yaml":   validStatsYAML,
		"needs.yaml":   validNeedsYAML,
		"actions.yaml": validActionsYAML,
		"gates.yaml":   validGatesYAML,
		"balance.yaml": validBalanceYAML,
	}
	schemaFiles := map[string]string{
		"stats.schema.json":   testStatsSchema,
		"needs.schema.json":   testNeedsSchema,
		"actions.schema.json": testActionsSchema,
		"gates.schema.json":   testGatesSchema,
		"balance.schema.json": testBalanceSchema,
	}

	dir := writeTestContent(t, contentFiles, schemaFiles)
	out, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if out == nil {
		t.Fatal("Load returned nil output")
	}
	if out.StatsRegistry == nil {
		t.Error("StatsRegistry is nil")
	}
	if out.NeedsRegistry == nil {
		t.Error("NeedsRegistry is nil")
	}
	if out.ActionsRegistry == nil {
		t.Error("ActionsRegistry is nil")
	}
	if out.GatesRegistry == nil {
		t.Error("GatesRegistry is nil")
	}
	if out.ValuesConfig == nil {
		t.Error("ValuesConfig is nil")
	}
	if out.ConfigHash() == "" {
		t.Error("ConfigHash is empty")
	}
	if out.WorldEnv != nil || out.ClimateCfg != nil || out.NavCfg != nil ||
		out.ClimateRules != nil || out.FloraRules != nil || out.FaunaRules != nil ||
		out.DecayRules != nil || out.TerrainTypes != nil || out.ScentEmitters != nil {
		t.Fatal("optional world/env fields should be nil when files are absent")
	}

	// Verify ConfigHash is deterministic.
	hash1 := out.ConfigHash()

	out2, err := Load(dir)
	if err != nil {
		t.Fatalf("second Load failed: %v", err)
	}
	hash2 := out2.ConfigHash()

	if hash1 != hash2 {
		t.Errorf("ConfigHash not deterministic: %q vs %q", hash1, hash2)
	}

	// Verify balance fields.
	if out.Balance.World.TickMinutes != 1 {
		t.Errorf("TickMinutes = %d, want 1", out.Balance.World.TickMinutes)
	}
	if out.Balance.Mood.Lambda != 0.25 {
		t.Errorf("Mood.Lambda = %f, want 0.25", out.Balance.Mood.Lambda)
	}
	if out.Balance.Adrenaline.Surge != 0.6 {
		t.Errorf("Adrenaline.Surge = %f, want 0.6", out.Balance.Adrenaline.Surge)
	}
	if out.Balance.Threats.SafetyThreatThreshold != 0.30 {
		t.Errorf("SafetyThreatThreshold = %f, want 0.30", out.Balance.Threats.SafetyThreatThreshold)
	}

	// Verify PlansnerConfig accessor.
	_ = out.Balance.PlannerConfig()
	if out.Balance.PlannerConfig().UrgencyThreshold != 0.65 {
		t.Errorf("Planner UrgencyThreshold = %f, want 0.65", out.Balance.PlannerConfig().UrgencyThreshold)
	}

	// Verify WorldConfig accessor.
	wc := out.Balance.WorldConfig()
	if wc.SpatialHashCell != 8.0 {
		t.Errorf("SpatialHashCell = %f, want 8.0", wc.SpatialHashCell)
	}
	if wc.OutcomeDifficultyBase != 50.0 {
		t.Errorf("OutcomeDifficultyBase = %f, want 50.0", wc.OutcomeDifficultyBase)
	}

	// Verify ClockConfig accessor.
	cc := out.Balance.ClockConfig()
	if cc.TickMinutes != 1 {
		t.Errorf("Clock TickMinutes = %d, want 1", cc.TickMinutes)
	}
	if cc.DayMinutes != 1440 {
		t.Errorf("Clock DayMinutes = %d, want 1440", cc.DayMinutes)
	}

	// Verify AgentConfig accessor.
	ac := out.Balance.AgentConfig(out.NeedsRegistry, out.StatsRegistry)
	if ac.Lambda != 0.25 {
		t.Errorf("Agent Lambda = %f, want 0.25", ac.Lambda)
	}
	if ac.AdrSurge != 0.6 {
		t.Errorf("Agent AdrSurge = %f, want 0.6", ac.AdrSurge)
	}
	if ac.BondAffinityGain != 0.20 {
		t.Errorf("BondAffinityGain = %f, want 0.20", ac.BondAffinityGain)
	}
	if len(ac.ThreatTags) != 2 || ac.ThreatTags[0] != "violent:high" {
		t.Errorf("ThreatTags = %v, want [violent:high hostile]", ac.ThreatTags)
	}
	if ac.InfluenceWeight != 0.5 {
		t.Errorf("InfluenceWeight = %f, want 0.5", ac.InfluenceWeight)
	}
}

func TestLoadWorldContentBuildsEnvAndRules(t *testing.T) {
	dir := writeTestContent(t, worldContentFiles(), worldSchemaFiles())
	out, err := LoadContent(dir)
	if err != nil {
		t.Fatalf("LoadContent world content: %v", err)
	}
	if out.WorldEnv == nil || out.ClimateCfg == nil || out.NavCfg == nil {
		t.Fatalf("world geometry configs were not built")
	}
	if out.WorldEnv.Min.X != 0 || out.WorldEnv.Max.X != 64 || out.WorldEnv.ScentCellSize != 8.4 {
		t.Fatalf("WorldEnv mapping wrong: %+v", *out.WorldEnv)
	}
	if out.ClimateCfg.GridCols != 4 || out.ClimateCfg.InitTemperature != 12.5 {
		t.Fatalf("ClimateCfg mapping wrong: %+v", *out.ClimateCfg)
	}
	if out.NavCfg.CellSize != 8.0 || out.NavCfg.MaxY != 64 {
		t.Fatalf("NavCfg mapping wrong: %+v", *out.NavCfg)
	}
	if out.ClimateRules == nil || out.FloraRules == nil || out.FaunaRules == nil || out.DecayRules == nil {
		t.Fatalf("compiled rules missing: climate=%v flora=%v fauna=%v decay=%v", out.ClimateRules, out.FloraRules, out.FaunaRules, out.DecayRules)
	}
	if len(out.TerrainTypes) != 2 || !out.TerrainTypes["soil"].Passable {
		t.Fatalf("terrain types not built: %#v", out.TerrainTypes)
	}
	if len(out.ScentEmitters) != 2 ||
		len(out.ScentEmitters["grass"]) != 1 || out.ScentEmitters["grass"][0] != "scent:food" ||
		len(out.ScentEmitters["deer"]) != 1 || out.ScentEmitters["deer"][0] != "scent:prey" {
		t.Fatalf("scent emitters not extracted from content tags: %#v", out.ScentEmitters)
	}
}

func TestWorldContentValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		data    string
		wantErr string
	}{
		{
			name: "world schema",
			file: "world.yaml",
			data: `schema_version: 1
grids:
  navmap_cell_size: 8.0
  climate_grid_cols: 4
  climate_grid_rows: 4
  scent_cell_size: 8.4
motion: { fauna_dt: 1.0, max_speed: 1.4 }
cadence:
  climate_step: 60
  flora_step: 60
  decay_step: 60
  scent_spread: 6
  fauna_dormant_period: 100
  fauna_wake_cooldown: 30
`,
			wantErr: "schema world.yaml",
		},
		{
			name:    "scent floor",
			file:    "world.yaml",
			data:    strings.Replace(validWorldYAML, "scent_cell_size: 8.4", "scent_cell_size: 8.39", 1),
			wantErr: "scent_cell_size",
		},
		{
			name:    "grid sync",
			file:    "world.yaml",
			data:    strings.Replace(validWorldYAML, "navmap_cell_size: 8.0", "navmap_cell_size: 4.0", 1),
			wantErr: "grid sync",
		},
		{
			name:    "bounds",
			file:    "world.yaml",
			data:    strings.Replace(validWorldYAML, "max: [64.0, 64.0]", "max: [0.0, 64.0]", 1),
			wantErr: "bounds",
		},
		{
			name:    "fauna operand",
			file:    "objects.yaml",
			data:    strings.Replace(validObjectsWorldYAML, "hunger * (0.3 + scent.food)", "hunger + scent.foood", 1),
			wantErr: "fauna deer utility Forage",
		},
		{
			name:    "fauna action",
			file:    "objects.yaml",
			data:    strings.Replace(validObjectsWorldYAML, "action: Forage", "action: MissingAction", 1),
			wantErr: "unknown action",
		},
		{
			name:    "flora operand",
			file:    "objects.yaml",
			data:    strings.Replace(validObjectsWorldYAML, "moisture*0.5 + (1 - slope)*0.5", "bad_attr + moisture", 1),
			wantErr: "flora grass suitability",
		},
		{
			name:    "flora propagation radius neighbor count",
			file:    "objects.yaml",
			data:    strings.Replace(validObjectsWorldYAML, "propagation: { radius: 3.0, chance: 0.30 }", "propagation: { radius: \"neighbor_count + 1\", chance: 0.30 }", 1),
			wantErr: "flora grass propagation.radius must not read neighbor_count",
		},
		{
			name:    "climate operand",
			file:    "climate.yaml",
			data:    strings.Replace(validClimateYAML, "moisture < 0.15", "humidity < 0.15", 1),
			wantErr: "climate transition 0",
		},
		{
			name:    "climate terrain",
			file:    "climate.yaml",
			data:    strings.Replace(validClimateYAML, "to: sand", "to: bog", 1),
			wantErr: "unknown to terrain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := worldContentFiles()
			files[tt.file] = tt.data
			dir := writeTestContent(t, files, worldSchemaFiles())
			out, err := LoadContent(dir)
			if err == nil {
				t.Fatal("expected load error")
			}
			if out != nil {
				t.Fatal("expected no partial registry on error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestConfigHashIncludesWorldFiles(t *testing.T) {
	dir1 := writeTestContent(t, worldContentFiles(), worldSchemaFiles())
	out1, err := LoadContent(dir1)
	if err != nil {
		t.Fatalf("LoadContent dir1: %v", err)
	}
	files2 := worldContentFiles()
	files2["world.yaml"] = strings.Replace(validWorldYAML, "fauna_dt: 1.0", "fauna_dt: 1.00", 1)
	dir2 := writeTestContent(t, files2, worldSchemaFiles())
	out2, err := LoadContent(dir2)
	if err != nil {
		t.Fatalf("LoadContent dir2: %v", err)
	}
	if out1.ConfigHash() == out2.ConfigHash() {
		t.Fatal("ConfigHash did not change after world.yaml byte change")
	}
}

// TestLoadInvalidStatsYAML verifies that a malformed stats.yaml fails.
func TestLoadInvalidStatsYAML(t *testing.T) {
	contentFiles := map[string]string{
		"stats.yaml": `schema_version: 1
stats:
  - id: Strength
    kind: INVALID_KIND  # should be "capability" or "disposition"
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
`,
		"needs.yaml":   validNeedsYAML,
		"actions.yaml": validActionsYAML,
		"gates.yaml":   validGatesYAML,
		"balance.yaml": validBalanceYAML,
	}
	schemaFiles := map[string]string{
		"stats.schema.json":   testStatsSchema,
		"needs.schema.json":   testNeedsSchema,
		"actions.schema.json": testActionsSchema,
		"gates.schema.json":   testGatesSchema,
		"balance.schema.json": testBalanceSchema,
	}

	dir := writeTestContent(t, contentFiles, schemaFiles)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for invalid stats.yaml, got nil")
	}
	if !strings.Contains(err.Error(), "stats") {
		t.Errorf("error should mention stats file: got %v", err)
	}
}

// TestLoadInvalidNeedsMissingField verifies schema enforcement for needs.
func TestLoadInvalidNeedsMissingField(t *testing.T) {
	contentFiles := map[string]string{
		"stats.yaml": validStatsYAML,
		"needs.yaml": `schema_version: 1
needs:
  - id: Satiety
    kind: consumable
    # missing "default" block — schema requires it
    salience:
      curve: deficit
      gain: 1.0
`,
		"actions.yaml": validActionsYAML,
		"gates.yaml":   validGatesYAML,
		"balance.yaml": validBalanceYAML,
	}
	schemaFiles := map[string]string{
		"stats.schema.json":   testStatsSchema,
		"needs.schema.json":   testNeedsSchema,
		"actions.schema.json": testActionsSchema,
		"gates.schema.json":   testGatesSchema,
		"balance.schema.json": testBalanceSchema,
	}

	dir := writeTestContent(t, contentFiles, schemaFiles)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for incomplete needs.yaml, got nil")
	}
}

// TestLoadInvalidBalanceMissingRequired verifies the balance schema rejects
// missing required top-level keys.
func TestLoadInvalidBalanceMissingRequired(t *testing.T) {
	contentFiles := map[string]string{
		"stats.yaml":   validStatsYAML,
		"needs.yaml":   validNeedsYAML,
		"actions.yaml": validActionsYAML,
		"gates.yaml":   validGatesYAML,
		"balance.yaml": `schema_version: 1
world:
  tick_minutes: 1
  real_scale: 12
  day_minutes: 1440
  spatial_hash_cell: 8.0
  outcome_difficulty_base: 50.0
  backup_every_ticks: 60
# missing perception, needs, values, planner, etc.
`,
	}
	schemaFiles := map[string]string{
		"stats.schema.json":   testStatsSchema,
		"needs.schema.json":   testNeedsSchema,
		"actions.schema.json": testActionsSchema,
		"gates.schema.json":   testGatesSchema,
		"balance.schema.json": testBalanceSchema,
	}

	dir := writeTestContent(t, contentFiles, schemaFiles)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for incomplete balance.yaml, got nil")
	}
	if !strings.Contains(err.Error(), "balance") {
		t.Errorf("error should mention balance file: got %v", err)
	}
}

// TestLoadInvalidActionsMissingID verifies that actions with missing required
// field gets caught by schema validation.
func TestLoadInvalidActionsMissingID(t *testing.T) {
	contentFiles := map[string]string{
		"stats.yaml": validStatsYAML,
		"needs.yaml": validNeedsYAML,
		"actions.yaml": `schema_version: 1
actions:
  - tags: [ "test:valid" ]  # missing required "id" field
    duration: 5
`,
		"gates.yaml":   validGatesYAML,
		"balance.yaml": validBalanceYAML,
	}
	schemaFiles := map[string]string{
		"stats.schema.json":   testStatsSchema,
		"needs.schema.json":   testNeedsSchema,
		"actions.schema.json": testActionsSchema,
		"gates.schema.json":   testGatesSchema,
		"balance.schema.json": testBalanceSchema,
	}

	dir := writeTestContent(t, contentFiles, schemaFiles)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for actions with missing id, got nil")
	}
}

// TestLoadMissingFile verifies that a missing content file returns an error.
func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for missing directory, got nil")
	}
}

// TestLoadInvalidYAML verifies that invalid YAML syntax gets caught.
func TestLoadInvalidYAML(t *testing.T) {
	contentFiles := map[string]string{
		"stats.yaml":   `::: not valid yaml`,
		"needs.yaml":   validNeedsYAML,
		"actions.yaml": validActionsYAML,
		"gates.yaml":   validGatesYAML,
		"balance.yaml": validBalanceYAML,
	}
	schemaFiles := map[string]string{
		"stats.schema.json":   testStatsSchema,
		"needs.schema.json":   testNeedsSchema,
		"actions.schema.json": testActionsSchema,
		"gates.schema.json":   testGatesSchema,
		"balance.schema.json": testBalanceSchema,
	}

	dir := writeTestContent(t, contentFiles, schemaFiles)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

// TestConfigHashDeterminism verifies that ConfigHash is deterministic
// and changes when file contents change.
func TestConfigHashDeterminism(t *testing.T) {
	contentFiles := map[string]string{
		"stats.yaml":   validStatsYAML,
		"needs.yaml":   validNeedsYAML,
		"actions.yaml": validActionsYAML,
		"gates.yaml":   validGatesYAML,
		"balance.yaml": validBalanceYAML,
	}
	schemaFiles := map[string]string{
		"stats.schema.json":   testStatsSchema,
		"needs.schema.json":   testNeedsSchema,
		"actions.schema.json": testActionsSchema,
		"gates.schema.json":   testGatesSchema,
		"balance.schema.json": testBalanceSchema,
	}

	dir := writeTestContent(t, contentFiles, schemaFiles)
	out1, err := Load(dir)
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	out2, err := Load(dir)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}

	if out1.ConfigHash() != out2.ConfigHash() {
		t.Errorf("hashes differ for identical content: %q vs %q",
			out1.ConfigHash(), out2.ConfigHash())
	}
}

// TestLoadWithRealContent tests loading the actual shipped content directory.
// This is a golden-adjacent test that verifies the production content loads.
func TestLoadWithRealContent(t *testing.T) {
	// Look for content directory relative to the test file's location.
	// We search upward from the test file for the content/ directory.
	contentDir := findContentDir(t)
	if contentDir == "" {
		t.Skip("content directory not found; run from repo root")
	}

	out, err := Load(contentDir)
	if err != nil {
		t.Fatalf("Load shipped content: %v", err)
	}
	if out == nil || out.StatsRegistry == nil {
		t.Fatal("output is nil or missing StatsRegistry")
	}

	// Basic sanity checks.
	if out.Balance.World.TickMinutes != 1 {
		t.Errorf("shipped TickMinutes = %d, want 1", out.Balance.World.TickMinutes)
	}
	if out.Balance.Mood.Lambda != 0.25 {
		t.Errorf("shipped Mood.Lambda = %f, want 0.25", out.Balance.Mood.Lambda)
	}
	if out.Balance.World.DayMinutes != 1440 {
		t.Errorf("shipped DayMinutes = %d, want 1440", out.Balance.World.DayMinutes)
	}

	// Verify all stats loaded.
	statsIDs := out.StatsRegistry.IDs()
	if len(statsIDs) == 0 {
		t.Error("no stats loaded from shipped content")
	}

	// Try the accessor methods.
	_ = out.Balance.AgentConfig(out.NeedsRegistry, out.StatsRegistry)
	_ = out.Balance.PlannerConfig()
	_ = out.Balance.WorldConfig()
	_ = out.Balance.ClockConfig()
}

// findContentDir searches up the directory tree for a content/ directory.
func findContentDir(t *testing.T) string {
	t.Helper()
	// Start from the test file's assumed location:
	// backend/platform/config/config_test.go -> look for content/ at ../../../
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Walk up to find content/ directory.
	dir := wd
	for range 5 { // up to 5 levels
		candidate := filepath.Join(dir, "content")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			// Verify it has the expected sub-files.
			if _, err := os.Stat(filepath.Join(candidate, "stats.yaml")); err == nil {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// TestConfigHashChangesWithContent verifies that ConfigHash changes when
// file contents differ.
func TestConfigHashChangesWithContent(t *testing.T) {
	baseContent := map[string]string{
		"stats.yaml":   validStatsYAML,
		"needs.yaml":   validNeedsYAML,
		"actions.yaml": validActionsYAML,
		"gates.yaml":   validGatesYAML,
		"balance.yaml": validBalanceYAML,
	}
	schemaFiles := map[string]string{
		"stats.schema.json":   testStatsSchema,
		"needs.schema.json":   testNeedsSchema,
		"actions.schema.json": testActionsSchema,
		"gates.schema.json":   testGatesSchema,
		"balance.schema.json": testBalanceSchema,
	}

	// First load — baseline.
	dir1 := writeTestContent(t, baseContent, schemaFiles)
	out1, err := Load(dir1)
	if err != nil {
		t.Fatalf("baseline Load: %v", err)
	}

	// Second load with a different stats.yaml.
	modifiedContent := make(map[string]string)
	for k, v := range baseContent {
		modifiedContent[k] = v
	}
	modifiedContent["stats.yaml"] = `schema_version: 1
stats:
  - id: Strength
    kind: capability
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Agility
    kind: capability
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Intelligence
    kind: capability
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.18 }
    inherit: 0.5
  - id: Honesty
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.6, sd: 0.2 }
    inherit: 0.4
  - id: Aggression
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.2 }
    inherit: 0.4
  - id: Greed  # added stat — makes this content different
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.2 }
    inherit: 0.4
`

	dir2 := writeTestContent(t, modifiedContent, schemaFiles)
	out2, err := Load(dir2)
	if err != nil {
		t.Fatalf("modified Load: %v", err)
	}

	if out1.ConfigHash() == out2.ConfigHash() {
		t.Error("ConfigHash should differ when content changes")
	}
}

// TestBalanceAgentConfig verifies the AgentConfig accessor produces correct values.
func TestBalanceAgentConfig(t *testing.T) {
	contentFiles := map[string]string{
		"stats.yaml":   validStatsYAML,
		"needs.yaml":   validNeedsYAML,
		"actions.yaml": validActionsYAML,
		"gates.yaml":   validGatesYAML,
		"balance.yaml": validBalanceYAML,
	}
	schemaFiles := map[string]string{
		"stats.schema.json":   testStatsSchema,
		"needs.schema.json":   testNeedsSchema,
		"actions.schema.json": testActionsSchema,
		"gates.schema.json":   testGatesSchema,
		"balance.schema.json": testBalanceSchema,
	}

	dir := writeTestContent(t, contentFiles, schemaFiles)
	out, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ac := out.Balance.AgentConfig(out.NeedsRegistry, out.StatsRegistry)

	// Mood
	if ac.Lambda != 0.25 {
		t.Errorf("Lambda = %f, want 0.25", ac.Lambda)
	}
	if ac.MoodDecay != 0.02 {
		t.Errorf("MoodDecay = %f, want 0.02", ac.MoodDecay)
	}

	// Adrenaline
	if ac.AdrSurge != 0.6 {
		t.Errorf("AdrSurge = %f, want 0.6", ac.AdrSurge)
	}
	if ac.CrashStaminaPenalty != 0.50 {
		t.Errorf("CrashStaminaPenalty = %f, want 0.50", ac.CrashStaminaPenalty)
	}

	// Coping
	if ac.ApathyFailStreak != 3 {
		t.Errorf("ApathyFailStreak = %d, want 3", ac.ApathyFailStreak)
	}
	if ac.RebindMinIntelligence != 0.4 {
		t.Errorf("RebindMinIntelligence = %f, want 0.4", ac.RebindMinIntelligence)
	}

	// Trade
	if ac.ClaimInflateMin != 0.50 {
		t.Errorf("ClaimInflateMin = %f, want 0.50", ac.ClaimInflateMin)
	}
	if ac.ClaimInflateMax != 0.90 {
		t.Errorf("ClaimInflateMax = %f, want 0.90", ac.ClaimInflateMax)
	}

	// Social
	if ac.BondAffinityGain != 0.20 {
		t.Errorf("BondAffinityGain = %f, want 0.20", ac.BondAffinityGain)
	}

	// Politics
	if ac.RelyCostThreshold != 1.2 {
		t.Errorf("RelyCostThreshold = %f, want 1.2", ac.RelyCostThreshold)
	}
	if ac.InfluenceWeight != 0.5 {
		t.Errorf("InfluenceWeight = %f, want 0.5", ac.InfluenceWeight)
	}

	// Threat tags
	if len(ac.ThreatTags) != 2 {
		t.Errorf("len(ThreatTags) = %d, want 2", len(ac.ThreatTags))
	}
}
