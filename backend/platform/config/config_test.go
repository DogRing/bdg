package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/scent"
	"github.com/dogring/bdg/engine/space/spatial"
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
  - { from: lake, to: ice, when: "temperature < 0" }
  - { from: river, to: ice, when: "temperature < 0" }
  - { from: ice, to: __origin__, when: "temperature > 2" }
balance:
  ice_type: ice
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
  - id: lake
    base_cost: 3.0
    passable: true
  - id: river
    base_cost: 3.0
    passable: true
  - id: ice
    base_cost: 1.2
    passable: true
`

const validObjectsWorldYAML = `schema_version: 1
object_kinds:
  - id: grass
    mobile: false
    tags: [ scent:food, forage, cover ]
    flora:
      suitability: "moisture*0.5 + (1 - slope)*0.5"
      length_rate: 0.05
      width_rate: 0.04
      stages: [0.1, 0.3]
      yield_stage: 1
      propagate_stage: 1
      shade: { radius: "width * 0.05", opacity: "width * 0.02" }
      propagation: { radius: 3.0, chance: 1.0, carrying_capacity: 4 }
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
      turn_rate: "0.5 + Agility"
      hide_chance: "hunger + Agility"
      diet: [ forage ]
      senses: { smell_radius: 10.0, sight_radius: 14.0, fov_arc: 1.05 }
      cover_cost: 0.7
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
  - id: carcass
    stackable: false
    tags: [ scent:carrion ]
    supply: { Satiety: 0.4 }
`

const combatActionsYAML = `schema_version: 1
actions:
  - id: Attack
    tags: [ "combat:attack" ]
    requires: [ near_other ]
    produces: [ struck ]
    duration: 5
    interruptible: false
  - id: Feed
    tags: [ "feed:carrion" ]
    requires: [ near_carcass ]
    produces: [ sated ]
    duration: 8
    interruptible: true
  - id: Graze
    tags: [ "seek:food" ]
    produces: [ grazed ]
    duration: 10
    interruptible: true
`

const combatObjectsYAML = `schema_version: 1
object_kinds:
  - id: wolf
    mobile: true
    tags: [ scent:predator, threat:predator ]
    fauna:
      actions:
        - { action: Attack, utility: "hunger + target.threat" }
        - { action: Feed, utility: "hunger + scent.carrion" }
      drives:
        - { id: hunger, rate: 0.0008 }
      apparent_temp: "temperature"
      speed: 0
      turn_rate: "0.5 + Agility"
      attack_power: "Strength + target.threat"
      hit: "Agility"
      feed: "scent.carrion + hunger"
      diet: [ game ]
      senses: { smell_radius: 10.0, sight_radius: 14.0, fov_arc: 3.14 }
      cover_cost: 1.2
      terrain_cost: { sand: 1.2 }
      impassable: []
  - id: deer
    mobile: true
    tags: [ scent:prey, game ]
    fauna:
      actions:
        - { action: Graze, utility: "hunger" }
      drives:
        - { id: hunger, rate: 0.0008 }
      apparent_temp: "temperature"
      speed: 0
      turn_rate: "0.2 + Agility"
      hide_chance: "hunger + Agility"
      senses: { smell_radius: 10.0, sight_radius: 14.0, fov_arc: 3.14 }
item_kinds:
  - id: bones
    stackable: true
    supply: { Satiety: 0.0 }
  - id: carcass
    stackable: false
    tags: [ scent:carrion ]
    supply: { Satiety: 0.8 }
    decay:
      baseRate: 1.0
      accel: "0.5 + temperature*0.01 + moisture*0.1"
      states:
        - { name: fresh }
        - { name: rotting, threshold: 0.5, supply: { Satiety: 0.4 } }
        - { name: bones, threshold: 0.9, transform: [ { item: bones, qty: 1 } ] }
        - { name: gone, threshold: 1.0 }
`

type testExprCtx struct {
	attrs map[core.Tag]float64
	stats map[core.StatID]float64
}

func (c testExprCtx) Attr(id core.Tag) (float64, bool) {
	v, ok := c.attrs[id]
	return v, ok
}

func (c testExprCtx) Stat(id core.StatID) float64 {
	return c.stats[id]
}

func (c testExprCtx) Pred(string, core.Tag) bool { return false }

type configMockTerrain struct{}

func (configMockTerrain) FootprintBlocked(core.Vec2) bool { return false }
func (configMockTerrain) TerrainAt(core.Vec2) core.Tag    { return "soil" }
func (configMockTerrain) BaseCost(core.Vec2) float64      { return 1 }
func (configMockTerrain) Attrs(core.Vec2) map[core.Tag]float64 { return nil }

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
		out.DecayRules != nil || out.TerrainTypes != nil || out.ScentEmitters != nil ||
		out.CoverKinds != nil {
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

	assertBalanceAccessors(t, out)
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
	if out.ClimateCfg.IceType != "ice" {
		t.Fatalf("ClimateCfg.IceType = %q, want ice", out.ClimateCfg.IceType)
	}
	if out.NavCfg.CellSize != 8.0 || out.NavCfg.MaxY != 64 {
		t.Fatalf("NavCfg mapping wrong: %+v", *out.NavCfg)
	}
	if out.ClimateRules == nil || out.FloraRules == nil || out.FaunaRules == nil || out.DecayRules == nil {
		t.Fatalf("compiled rules missing: climate=%v flora=%v fauna=%v decay=%v", out.ClimateRules, out.FloraRules, out.FaunaRules, out.DecayRules)
	}
	if len(out.TerrainTypes) != 5 || !out.TerrainTypes["soil"].Passable || !out.TerrainTypes["ice"].Passable {
		t.Fatalf("terrain types not built: %#v", out.TerrainTypes)
	}
	if len(out.ScentEmitters) != 3 ||
		len(out.ScentEmitters["grass"]) != 1 || out.ScentEmitters["grass"][0] != "scent:food" ||
		len(out.ScentEmitters["deer"]) != 1 || out.ScentEmitters["deer"][0] != "scent:prey" ||
		len(out.ScentEmitters["carcass"]) != 1 || out.ScentEmitters["carcass"][0] != "scent:carrion" {
		t.Fatalf("scent emitters not extracted from content tags: %#v", out.ScentEmitters)
	}
	if len(out.CoverKinds) != 1 || !out.CoverKinds["grass"] {
		t.Fatalf("cover kinds not extracted from content tags: %#v", out.CoverKinds)
	}
	if out.WorldEnv.FaunaCombat != (fauna.CombatParams{}) {
		t.Fatalf("absent combat params should stay neutral zeroes: %+v", out.WorldEnv.FaunaCombat)
	}
	ctx := testExprCtx{stats: map[core.StatID]float64{"Strength": 1, "Agility": 1}}
	if got := out.FaunaRules.AttackPower("deer", ctx); got != 0 {
		t.Fatalf("absent attack_power = %v, want 0", got)
	}
	if got := out.FaunaRules.Hit("deer", ctx); got != 1 {
		t.Fatalf("absent hit = %v, want 1", got)
	}
	if got := out.FaunaRules.Feed("deer", ctx); got != 0 {
		t.Fatalf("absent feed = %v, want 0", got)
	}
	if got := out.FaunaRules.HideChance("deer", ctx); got != 1 {
		t.Fatalf("HideChance = %v, want 1", got)
	}
	if got := out.FaunaRules.TurnRate("deer", ctx); got != 1.5 {
		t.Fatalf("TurnRate = %v, want 1.5", got)
	}
	if got := out.FaunaRules.CoverCost("deer"); got != 0.7 {
		t.Fatalf("CoverCost = %v, want 0.7", got)
	}

	// carrying_capacity must survive YAML parsing and rule construction. At n=K the
	// spawn probability is zero for every seed; a dropped/legacy K would still spawn.
	plant := flora.Plant{ID: "grass_1", Species: "grass", Length: 1}
	input := flora.SiteInput{Moisture: 1, TerrainAttrs: map[core.Tag]float64{"slope": 0}, NeighborCount: 4}
	for seed := int64(0); seed < 100; seed++ {
		state := flora.New([]flora.Plant{plant})
		_, deltas := flora.Step(state, map[core.ObjectID]flora.SiteInput{plant.ID: input}, out.FloraRules,
			func() core.ObjectID { return "child" }, rng.New(seed))
		if len(deltas.Spawned) != 0 {
			t.Fatalf("carrying_capacity mapping lost: n=K spawned at seed %d", seed)
		}
	}
}

func TestLoadWorldContentRejectsNegativeFloraCarryingCapacity(t *testing.T) {
	files := worldContentFiles()
	files["objects.yaml"] = strings.Replace(files["objects.yaml"], "carrying_capacity: 4", "carrying_capacity: -1", 1)
	dir := writeTestContent(t, files, worldSchemaFiles())
	_, err := LoadContent(dir)
	if err == nil || !strings.Contains(err.Error(), "carrying_capacity must be >= 0") {
		t.Fatalf("negative carrying_capacity error = %v", err)
	}
}

func TestLoadWorldContentOmittedFloraCarryingCapacityUsesLegacyWeight(t *testing.T) {
	files := worldContentFiles()
	files["objects.yaml"] = strings.Replace(files["objects.yaml"], ", carrying_capacity: 4", "", 1)
	dir := writeTestContent(t, files, worldSchemaFiles())
	out, err := LoadContent(dir)
	if err != nil {
		t.Fatalf("LoadContent omitted carrying_capacity: %v", err)
	}

	plant := flora.Plant{ID: "grass_1", Species: "grass", Length: 1}
	input := flora.SiteInput{Moisture: 1, TerrainAttrs: map[core.Tag]float64{"slope": 0}, NeighborCount: 4}
	spawned := 0
	for seed := int64(0); seed < 100; seed++ {
		state := flora.New([]flora.Plant{plant})
		_, deltas := flora.Step(state, map[core.ObjectID]flora.SiteInput{plant.ID: input}, out.FloraRules,
			func() core.ObjectID { return "child" }, rng.New(seed))
		spawned += len(deltas.Spawned)
	}
	if spawned == 0 {
		t.Fatal("omitted carrying_capacity did not preserve non-zero legacy 1/(1+n) weight")
	}
}

func TestCombatParamsParsedIntoEnvConfig(t *testing.T) {
	files := worldContentFiles()
	files["world.yaml"] = strings.Replace(validWorldYAML, "  fauna_wake_cooldown: 30\n", `  fauna_wake_cooldown: 30
  exchange_min_ticks: 10
  exchange_max_ticks: 20
  engage_cooldown_min_ticks: 50
  engage_cooldown_max_ticks: 100
  disengage_range_factor: 2.0
  stamina_drop_threshold: 0.05
  vital_regen_per_tick: 0.001
  vital_cap_damage_fraction: 0.05
  hide_duration: 100
  hidden_flush_factor: 1.0
  hide_cover_factor: 1.0
  cover_radius_factor: 3.0
  conceal_factor: 1.0
`, 1)
	dir := writeTestContent(t, files, worldSchemaFiles())
	out, err := LoadContent(dir)
	if err != nil {
		t.Fatalf("LoadContent combat params: %v", err)
	}
	got := out.WorldEnv.FaunaCombat
	want := fauna.CombatParams{
		ExchangeMinTicks:       10,
		ExchangeMaxTicks:       20,
		EngageCooldownMinTicks: 50,
		EngageCooldownMaxTicks: 100,
		DisengageRangeFactor:   2.0,
		StaminaDropThreshold:   0.05,
		VitalRegenPerTick:      0.001,
		VitalCapDamageFraction: 0.05,
		HideDurationTicks:      100,
		HiddenFlushFactor:      1.0,
		HideCoverFactor:        1.0,
		CoverRadiusFactor:      3.0,
		ConcealFactor:          1.0,
	}
	if got != want {
		t.Fatalf("FaunaCombat = %+v, want %+v", got, want)
	}
}

func TestFaunaCombatContentCompilesAndCrossChecks(t *testing.T) {
	files := worldContentFiles()
	files["actions.yaml"] = combatActionsYAML
	files["objects.yaml"] = combatObjectsYAML
	dir := writeTestContent(t, files, worldSchemaFiles())
	out, err := LoadContent(dir)
	if err != nil {
		t.Fatalf("LoadContent combat content: %v", err)
	}
	ctx := testExprCtx{
		attrs: map[core.Tag]float64{"target.threat": 0.25, "scent.carrion": 0.7, "hunger": 0.2},
		stats: map[core.StatID]float64{"Strength": 0.6, "Agility": 0.8},
	}
	if got := out.FaunaRules.AttackPower("wolf", ctx); math.Abs(got-0.85) > 1e-12 {
		t.Fatalf("AttackPower = %v, want 0.85", got)
	}
	if got := out.FaunaRules.Hit("wolf", ctx); math.Abs(got-0.8) > 1e-12 {
		t.Fatalf("Hit = %v, want 0.8", got)
	}
	if got := out.FaunaRules.Feed("wolf", ctx); math.Abs(got-0.9) > 1e-12 {
		t.Fatalf("Feed = %v, want 0.9", got)
	}
	if got := out.FaunaRules.HideChance("deer", ctx); math.Abs(got-1.0) > 1e-12 {
		t.Fatalf("HideChance = %v, want 1.0", got)
	}
	if got := out.FaunaRules.TurnRate("wolf", ctx); math.Abs(got-1.3) > 1e-12 {
		t.Fatalf("TurnRate = %v, want 1.3", got)
	}
	if got := out.FaunaRules.CoverCost("wolf"); got != 1.2 {
		t.Fatalf("CoverCost = %v, want 1.2", got)
	}

	animals := []fauna.Animal{
		{
			ID: "wolf_1", Species: "wolf", Pos: core.Vec2{}, Stats: map[core.StatID]float64{"Strength": 0.6, "Agility": 0.8},
			Drives: map[fauna.DriveID]float64{"hunger": 0.8}, Stamina: 1, Vital: 1, CurrentAction: "Feed",
		},
		{ID: "deer_1", Species: "deer", Pos: core.Vec2{X: 1}, Stats: map[core.StatID]float64{}, Drives: map[fauna.DriveID]float64{}, Stamina: 1, Vital: 1},
	}
	snap := &fauna.Snapshot{
		Animals: animals,
		Scent:   scent.New(1),
		Spatial: spatial.New(8),
		Terrain: configMockTerrain{},
		Env: map[core.ObjectID]fauna.EnvSample{
			"wolf_1": {},
			"deer_1": {},
		},
		Tick:          10,
		Cadence:       fauna.Cadence{DormantPeriod: 10, WakeCooldown: 5},
		Combat:        fauna.CombatParams{ExchangeMinTicks: 10, ExchangeMaxTicks: 10, EngageCooldownMinTicks: 50, EngageCooldownMaxTicks: 50, DisengageRangeFactor: 2},
		ScentCellSize: 1,
		DT:            1,
	}
	intents := fauna.Step(snap, out.FaunaRules, rng.New(1))
	var wolfIntent fauna.Intent
	for _, it := range intents {
		if it.Animal == "wolf_1" {
			wolfIntent = it
		}
	}
	if wolfIntent.Action != "Attack" || wolfIntent.Target != "deer_1" || wolfIntent.EngagedWith != "deer_1" {
		t.Fatalf("combat action tag not wired through config: %+v", wolfIntent)
	}
}

func TestFaunaCombatUnknownOperandRejected(t *testing.T) {
	files := worldContentFiles()
	files["actions.yaml"] = combatActionsYAML
	files["objects.yaml"] = strings.Replace(combatObjectsYAML, `attack_power: "Strength + target.threat"`, `attack_power: "Strength + target.threaat"`, 1)
	dir := writeTestContent(t, files, worldSchemaFiles())
	out, err := LoadContent(dir)
	if err == nil {
		t.Fatal("expected combat operand error, got nil")
	}
	if out != nil {
		t.Fatal("expected no partial registry on combat operand failure")
	}
	if !strings.Contains(err.Error(), "fauna wolf attack_power") || !strings.Contains(err.Error(), "target.threaat") {
		t.Fatalf("error %q does not name species/formula/operand", err)
	}
}

func TestFaunaHideChanceUnknownOperandRejected(t *testing.T) {
	files := worldContentFiles()
	files["actions.yaml"] = combatActionsYAML
	files["objects.yaml"] = strings.Replace(combatObjectsYAML, `hide_chance: "hunger + Agility"`, `hide_chance: "hunger + bush.cover"`, 1)
	dir := writeTestContent(t, files, worldSchemaFiles())
	out, err := LoadContent(dir)
	if err == nil {
		t.Fatal("expected hide_chance operand error, got nil")
	}
	if out != nil {
		t.Fatal("expected no partial registry on hide_chance operand failure")
	}
	if !strings.Contains(err.Error(), "fauna deer hide_chance") || !strings.Contains(err.Error(), "bush.cover") {
		t.Fatalf("error %q does not name species/formula/operand", err)
	}
}

func TestCarcassDecayStatesLoaded(t *testing.T) {
	files := worldContentFiles()
	files["actions.yaml"] = combatActionsYAML
	files["objects.yaml"] = combatObjectsYAML
	dir := writeTestContent(t, files, worldSchemaFiles())
	out, err := LoadContent(dir)
	if err != nil {
		t.Fatalf("LoadContent carcass decay: %v", err)
	}
	if out.DecayRules == nil {
		t.Fatal("DecayRules nil")
	}
	if got := out.DecayRules.StateAt("carcass", 0.6); got != 1 {
		t.Fatalf("carcass state at 0.6 = %d, want rotting index 1", got)
	}
	supply := out.DecayRules.SupplyAt("carcass", 1)
	if supply["Satiety"] != 0.4 {
		t.Fatalf("carcass rotting supply = %#v, want Satiety 0.4", supply)
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
			// flora 1l fail-loud pairing: reading thermal_stress without a positive
			// thermal_band would make the operand a silent constant 0 (the failure mode
			// that killed berry_shrub live — docs/decisions/flora-thermal-comfort.md).
			name:    "flora thermal_stress without band",
			file:    "objects.yaml",
			data:    strings.Replace(validObjectsWorldYAML, "moisture*0.5 + (1 - slope)*0.5", "moisture*0.5 + (1 - thermal_stress)*0.5", 1),
			wantErr: "flora grass reads thermal_stress but thermal_band is 0",
		},
		{
			// flora 1m: K is evaluated by world-gen density placement too, which runs before
			// climate exists — a temperature term there would silently mean something else.
			name:    "flora carrying capacity reads temperature",
			file:    "objects.yaml",
			data:    strings.Replace(validObjectsWorldYAML, "carrying_capacity: 4", "carrying_capacity: \"4 * temperature\"", 1),
			wantErr: "must not read temperature",
		},
		{
			name:    "flora carrying capacity reads thermal_stress",
			file:    "objects.yaml",
			data:    strings.Replace(validObjectsWorldYAML, "carrying_capacity: 4", "carrying_capacity: \"4 * thermal_stress\"", 1),
			wantErr: "must not read thermal_stress",
		},
		{
			name:    "flora propagation radius neighbor count",
			file:    "objects.yaml",
			data:    strings.Replace(validObjectsWorldYAML, "radius: 3.0", "radius: \"neighbor_count + 1\"", 1),
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
		{
			name:    "climate origin without ice type",
			file:    "climate.yaml",
			data:    strings.Replace(validClimateYAML, "  ice_type: ice\n", "", 1),
			wantErr: "requires balance.ice_type",
		},
		{
			name: "climate origin without freeze rule",
			file: "climate.yaml",
			data: strings.ReplaceAll(
				validClimateYAML,
				"to: ice",
				"to: sand",
			),
			wantErr: "requires a freeze rule",
		},
		{
			name:    "climate unknown ice type",
			file:    "climate.yaml",
			data:    strings.Replace(validClimateYAML, "ice_type: ice", "ice_type: glacier", 1),
			wantErr: "ice_type \"glacier\" is not a terrain id",
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

	// Verify shipped content is structurally complete without pinning literal
	// balance values that are owned by content.
	statsIDs := out.StatsRegistry.IDs()
	if len(statsIDs) == 0 {
		t.Error("no stats loaded from shipped content")
	}
	if len(out.NeedsRegistry.IDs()) == 0 {
		t.Error("no needs loaded from shipped content")
	}
	if len(out.ActionsRegistry.IDs()) == 0 {
		t.Error("no actions loaded from shipped content")
	}
	assertBalanceAccessors(t, out)
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

func assertBalanceAccessors(t *testing.T, out *LoadOutput) {
	t.Helper()
	if out == nil {
		t.Fatal("LoadOutput is nil")
	}
	b := &out.Balance

	wc := b.WorldConfig()
	if wc.SpatialHashCell != b.World.SpatialHashCell {
		t.Errorf("WorldConfig.SpatialHashCell = %v, want parsed balance value %v", wc.SpatialHashCell, b.World.SpatialHashCell)
	}
	if wc.OutcomeDifficultyBase != b.World.OutcomeDifficultyBase {
		t.Errorf("WorldConfig.OutcomeDifficultyBase = %v, want parsed balance value %v", wc.OutcomeDifficultyBase, b.World.OutcomeDifficultyBase)
	}
	if wc.BackupEveryTicks != b.World.BackupEveryTicks {
		t.Errorf("WorldConfig.BackupEveryTicks = %v, want parsed balance value %v", wc.BackupEveryTicks, b.World.BackupEveryTicks)
	}

	cc := b.ClockConfig()
	if cc.TickMinutes != int64(b.World.TickMinutes) {
		t.Errorf("ClockConfig.TickMinutes = %v, want parsed balance value %v", cc.TickMinutes, b.World.TickMinutes)
	}
	if cc.DayMinutes != int64(b.World.DayMinutes) {
		t.Errorf("ClockConfig.DayMinutes = %v, want parsed balance value %v", cc.DayMinutes, b.World.DayMinutes)
	}

	pc := b.PlannerConfig()
	if pc.UrgencyThreshold != b.Planner.UrgencyThreshold {
		t.Errorf("PlannerConfig.UrgencyThreshold = %v, want parsed balance value %v", pc.UrgencyThreshold, b.Planner.UrgencyThreshold)
	}
	if pc.BaseHorizonTicks != b.Planner.BaseHorizonTicks {
		t.Errorf("PlannerConfig.BaseHorizonTicks = %v, want parsed balance value %v", pc.BaseHorizonTicks, b.Planner.BaseHorizonTicks)
	}
	if pc.Budget.MaxDepth != b.Planner.Budget.MaxDepth ||
		pc.Budget.MaxActions != b.Planner.Budget.MaxActions ||
		pc.Budget.MaxNodes != b.Planner.Budget.MaxNodes {
		t.Errorf("PlannerConfig.Budget = %+v, want parsed balance values %+v", pc.Budget, b.Planner.Budget)
	}
	for tag, want := range b.Planner.TagCosts {
		if got := pc.TagCosts[core.Tag(tag)]; got != want {
			t.Errorf("PlannerConfig.TagCosts[%q] = %v, want parsed balance value %v", tag, got, want)
		}
	}

	ac := b.AgentConfig(out.NeedsRegistry, out.StatsRegistry)
	if ac.Lambda != b.Mood.Lambda || ac.MoodDecay != b.Mood.Decay || ac.MoodBaseline != b.Mood.Baseline {
		t.Errorf("AgentConfig mood fields = (%v,%v,%v), want parsed balance mood %+v",
			ac.Lambda, ac.MoodDecay, ac.MoodBaseline, b.Mood)
	}
	if ac.AdrTriggerUrgency != b.Adrenaline.TriggerUrgency ||
		ac.AdrSurge != b.Adrenaline.Surge ||
		ac.AdrDecay != b.Adrenaline.Decay ||
		ac.AdrMax != b.Adrenaline.Max ||
		ac.CrashStaminaPenalty != b.Adrenaline.CrashStaminaPenalty {
		t.Errorf("AgentConfig adrenaline fields do not match parsed balance adrenaline %+v", b.Adrenaline)
	}
	if ac.StaminaMax != b.Stamina.Max ||
		ac.DrainPerEffort != b.Stamina.DrainPerEffort ||
		ac.RegenRest != b.Stamina.RegenRest ||
		ac.RegenSleep != b.Stamina.RegenSleep {
		t.Errorf("AgentConfig stamina fields do not match parsed balance stamina %+v", b.Stamina)
	}
	if ac.RebindMinIntelligence != b.Intelligence.RebindThreshold {
		t.Errorf("AgentConfig.RebindMinIntelligence = %v, want parsed balance value %v",
			ac.RebindMinIntelligence, b.Intelligence.RebindThreshold)
	}
	if ac.ApathyFailStreak != b.Coping.ApathyFailStreak ||
		ac.ApathyRecoverMood != b.Coping.ApathyRecoverMood ||
		ac.ApathyBudgetPenalty != b.Coping.ApathyBudgetPenalty {
		t.Errorf("AgentConfig coping fields do not match parsed balance coping %+v", b.Coping)
	}
	if ac.ClaimInflateMin != b.Trade.ClaimInflateMin || ac.ClaimInflateMax != b.Trade.ClaimInflateMax {
		t.Errorf("AgentConfig trade fields = (%v,%v), want parsed balance trade %+v",
			ac.ClaimInflateMin, ac.ClaimInflateMax, b.Trade)
	}
	if ac.BondAffinityGain != b.Social.BondAffinityGain || ac.MinCareThreshold != b.Social.MinCareThreshold {
		t.Errorf("AgentConfig social fields do not match parsed balance social %+v", b.Social)
	}
	if ac.RelyCostThreshold != b.Politics.RelyCostThreshold ||
		ac.RelyOnDelta != b.Politics.RelyOnDelta ||
		ac.VoteRelyThreshold != b.Politics.VoteRelyThreshold ||
		ac.UrgencyThreshold != b.Politics.VoteUrgencyThreshold ||
		ac.VoteRelyOnDelta != b.Politics.VoteRelyOnDelta ||
		ac.InfluenceWeight != b.Politics.InfluenceWeight {
		t.Errorf("AgentConfig politics fields do not match parsed balance politics %+v", b.Politics)
	}
	if len(ac.ThreatTags) != len(b.Threats.HostileTags) {
		t.Errorf("AgentConfig.ThreatTags len = %d, want parsed hostile tag len %d", len(ac.ThreatTags), len(b.Threats.HostileTags))
	} else {
		for i, want := range b.Threats.HostileTags {
			if ac.ThreatTags[i] != core.Tag(want) {
				t.Errorf("AgentConfig.ThreatTags[%d] = %q, want parsed hostile tag %q", i, ac.ThreatTags[i], want)
			}
		}
	}
	for tag, want := range b.TagLevels.Effort {
		if got := ac.EffortLevels[core.Tag(tag)]; got != want {
			t.Errorf("AgentConfig.EffortLevels[%q] = %v, want parsed balance value %v", tag, got, want)
		}
	}
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

// TestBalanceAgentConfig verifies the balance accessors project parsed content
// into engine configs without pinning the test to duplicated YAML literals.
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

	assertBalanceAccessors(t, out)
}
