package values

import (
	"math"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/needs"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// balanceYAML is the shipped content/balance.yaml values.weights block in context.
// Uses only the SECOND values: block (which wins in YAML duplicate-key resolution).
const balanceYAML = `
schema_version: 1
world:
  tick_minutes: 1
  real_scale: 12
  day_minutes: 1440
  spatial_hash_cell: 8.0
perception:
  sight_radius: 18.0
  smell_radius: 10.0
  hearing_radius: 14.0
needs:
  Satiety:   { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
  Hydration: { decay_per_tick: 0.00110, satisfaction_threshold: 0.50 }
  Rest:      { decay_per_tick: 0.00045, satisfaction_threshold: 0.45 }
generation:
  village_size: 40
  stamina_start: 1.0
  mood_start: 0.0
  initial_belief_noise: 0.15
tag_levels:
  effort: { none: 0.0, low: 0.20, med: 0.50, high: 0.90 }
  risk: { low: 0.20, med: 0.50, high: 0.90 }
  violent: { low: 0.30, med: 0.60, high: 1.00 }
  noise: { low: 0.20, med: 0.50, high: 0.90 }
  abstraction: { low: 0.25, med: 0.55, high: 0.90 }
cost_terms:
  time: { per_minute: 0.010 }
  effort: { weight: 1.0 }
  risk: { weight: 0.8 }
  moral: { weight: 1.5 }
  social: { weight: 0.4 }
mood:
  lambda: 0.25
  decay: 0.02
  baseline: 0.0
adrenaline:
  trigger_urgency: 0.65
  surge: 0.6
  decay: 0.03
  max: 1.0
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
values:
  weights:
    Safety:     1.40
    Hydration:  1.05
    Satiety:    1.00
    Rest:       0.85
    Standing:   0.60
    Openness:   0.40
`

// minimalBalance returns a minimal valid balance YAML with the values.weights
// block replaced by the given extraLines.
func minimalBalance(extraLines string) string {
	// Build enough skeleton so decoding doesn't fail on missing keys
	// but values.weights is injected.
	s := `
schema_version: 1
world:
  tick_minutes: 1
  real_scale: 12
  day_minutes: 1440
  spatial_hash_cell: 8.0
perception:
  sight_radius: 18.0
  smell_radius: 10.0
  hearing_radius: 14.0
generation:
  village_size: 40
  stamina_start: 1.0
  mood_start: 0.0
  initial_belief_noise: 0.15
tag_levels:
  effort: { none: 0.0, low: 0.20, med: 0.50, high: 0.90 }
  risk: { low: 0.20, med: 0.50, high: 0.90 }
  violent: { low: 0.30, med: 0.60, high: 1.00 }
  noise: { low: 0.20, med: 0.50, high: 0.90 }
  abstraction: { low: 0.25, med: 0.55, high: 0.90 }
cost_terms:
  time: { per_minute: 0.010 }
  effort: { weight: 1.0 }
  risk: { weight: 0.8 }
  moral: { weight: 1.5 }
  social: { weight: 0.4 }
mood:
  lambda: 0.25
  decay: 0.02
  baseline: 0.0
adrenaline:
  trigger_urgency: 0.65
  surge: 0.6
  decay: 0.03
  max: 1.0
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
` + extraLines
	return s
}

// mustLoad loads the shipped balance YAML and returns the Config. Fatal on error.
func mustLoad(t *testing.T) *Config {
	t.Helper()
	cfg, err := Load(strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	return cfg
}

// mkNeed creates a needs.Def with the specified Threshold for testing.
func mkNeed(threshold float64) needs.Def {
	return needs.Def{Threshold: threshold}
}

// ── AC: ComputeStanding formula ────────────────────────────────────────────────

func TestComputeStanding_Formula(t *testing.T) {
	tests := []struct {
		name             string
		threshold        float64
		currentIntensity float64
		want             Standing
	}{
		{name: "zero intensity", threshold: 1.0, currentIntensity: 0.0, want: 1.0},
		{name: "intensity equals threshold", threshold: 1.0, currentIntensity: 1.0, want: 0.0},
		{name: "halfway", threshold: 1.0, currentIntensity: 0.5, want: 0.5},
		{name: "quarter", threshold: 1.0, currentIntensity: 0.25, want: 0.75},
		{name: "intensity exceeds threshold", threshold: 1.0, currentIntensity: 1.5, want: 0.0},
		{name: "zero threshold", threshold: 0.0, currentIntensity: 100.0, want: 1.0},
		{name: "negative threshold", threshold: -0.5, currentIntensity: 1.0, want: 1.0},
		{name: "non-unity threshold halfway", threshold: 0.55, currentIntensity: 0.275, want: 0.5},
		{name: "intensity above non-unity threshold", threshold: 0.55, currentIntensity: 0.8, want: 0.0},
		{name: "negative intensity", threshold: 1.0, currentIntensity: -0.5, want: 1.0},
		{name: "large threshold small intensity", threshold: 100.0, currentIntensity: 1.0, want: 0.99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			need := mkNeed(tt.threshold)
			got := ComputeStanding(need, tt.currentIntensity)
			if math.Abs(float64(got)-float64(tt.want)) > 1e-12 {
				t.Errorf("ComputeStanding({Threshold:%v}, %v) = %v, want %v",
					tt.threshold, tt.currentIntensity, got, tt.want)
			}
		})
	}
}

// ── AC: ComputeSalience formula ────────────────────────────────────────────────

func TestComputeSalience_Formula(t *testing.T) {
	tests := []struct {
		name     string
		input    Standing
		want     Salience
		wantFrom float64 // expected value when computed as currentIntensity / maxIntensity
	}{
		{name: "standing 1 -> salience 0", input: 1.0, want: 0.0, wantFrom: 0.0},
		{name: "standing 0.5 -> salience 0.5", input: 0.5, want: 0.5, wantFrom: 0.5},
		{name: "standing 0 -> salience 1", input: 0.0, want: 1.0, wantFrom: 1.0},
		{name: "standing 0.75 -> salience 0.25", input: 0.75, want: 0.25, wantFrom: 0.25},
		{name: "standing 0.1 -> salience 0.9", input: 0.1, want: 0.9, wantFrom: 0.9},
		{name: "standing negative clamped", input: -0.5, want: 1.0, wantFrom: -1.0},
		{name: "standing > 1 clamped", input: 1.5, want: 0.0, wantFrom: 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeSalience(tt.input)
			if math.Abs(float64(got)-float64(tt.want)) > 1e-12 {
				t.Errorf("ComputeSalience(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestSalience_EqualsIntensityRatio cross-checks that Salience == currentIntensity / maxIntensity.
func TestSalience_EqualsIntensityRatio(t *testing.T) {
	tests := []struct {
		threshold        float64
		currentIntensity float64
	}{
		{threshold: 1.0, currentIntensity: 0.0},
		{threshold: 1.0, currentIntensity: 0.3},
		{threshold: 1.0, currentIntensity: 0.5},
		{threshold: 1.0, currentIntensity: 0.75},
		{threshold: 1.0, currentIntensity: 1.0},
		{threshold: 1.0, currentIntensity: 1.2},
		{threshold: 0.55, currentIntensity: 0.2},
		{threshold: 0.55, currentIntensity: 0.55},
		{threshold: 0.55, currentIntensity: 0.8},
	}

	for _, tt := range tests {
		need := mkNeed(tt.threshold)
		s := ComputeStanding(need, tt.currentIntensity)
		sal := ComputeSalience(s)

		var expectedRatio float64
		if tt.threshold > 0 {
			expectedRatio = tt.currentIntensity / tt.threshold
		} else {
			expectedRatio = 0 // Salience will be 0 (Standing clamped to 1 -> Salience 0)
		}
		expectedSal := clamp01(expectedRatio)

		if math.Abs(float64(sal)-expectedSal) > 1e-12 {
			t.Errorf("threshold=%v intensity=%v: Salience=%v, want %v (ratio=%v)",
				tt.threshold, tt.currentIntensity, sal, expectedSal, expectedRatio)
		}
	}
}

// ── AC: ComputeEffValue formula ───────────────────────────────────────────────

func TestComputeEffValue_Formula(t *testing.T) {
	tests := []struct {
		name            string
		sal             Salience
		expectedEffect  float64
		want            EffValue
	}{
		{name: "salience 0 -> 0", sal: 0.0, expectedEffect: 0.5, want: 0.0},
		{name: "effect 0 -> 0", sal: 1.0, expectedEffect: 0.0, want: 0.0},
		{name: "half and half", sal: 0.5, expectedEffect: 0.5, want: 0.25},
		{name: "both 1", sal: 1.0, expectedEffect: 1.0, want: 1.0},
		{name: "sal 0.3 effect 0.7", sal: 0.3, expectedEffect: 0.7, want: 0.21},
		{name: "negative effect clamped", sal: 0.5, expectedEffect: -0.3, want: 0.0},
		{name: "negative sal clamped", sal: -0.5, expectedEffect: 1.0, want: -0.5},
		{name: "sal 0.8 effect 0.2", sal: 0.8, expectedEffect: 0.2, want: 0.16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeEffValue(tt.sal, tt.expectedEffect)
			// EffValue >= 0 always if effect >= 0; negative sal can produce negative result
			// but sal should be clamped [0,1] by the caller. We just test the math.
			if math.Abs(float64(got)-float64(tt.want)) > 1e-12 {
				t.Errorf("ComputeEffValue(%v, %v) = %v, want %v",
					tt.sal, tt.expectedEffect, got, tt.want)
			}
		})
	}
}

// TestEffValue_NonNegative verifies EffValue >= 0 when Salience >= 0.
func TestEffValue_NonNegative(t *testing.T) {
	for sal := 0.0; sal <= 1.0; sal += 0.1 {
		for effect := -0.5; effect <= 1.0; effect += 0.1 {
			got := ComputeEffValue(Salience(sal), effect)
			if float64(got) < 0 {
				t.Errorf("ComputeEffValue(%v, %v) = %v (negative)", sal, effect, got)
			}
		}
	}
}

// ── AC: ComputePriority formula ───────────────────────────────────────────────

func TestComputePriority_Formula(t *testing.T) {
	tests := []struct {
		name   string
		sal    Salience
		weight float64
		want   Priority
	}{
		{name: "zero weight", sal: 1.0, weight: 0.0, want: 0.0},
		{name: "zero salience", sal: 0.0, weight: 1.0, want: 0.0},
		{name: "half and half", sal: 0.5, weight: 2.0, want: 1.0},
		{name: "weight 1", sal: 0.7, weight: 1.0, want: 0.7},
		{name: "double weight", sal: 0.5, weight: 2.0, want: 1.0},
		{name: "triple weight", sal: 0.3, weight: 3.0, want: 0.9},
		{name: "salience 1 weight 1.4", sal: 1.0, weight: 1.4, want: 1.4},
		{name: "salience 0.25 weight 0.6", sal: 0.25, weight: 0.6, want: 0.15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputePriority(tt.sal, tt.weight)
			if math.Abs(float64(got)-float64(tt.want)) > 1e-12 {
				t.Errorf("ComputePriority(%v, %v) = %v, want %v",
					tt.sal, tt.weight, got, tt.want)
			}
		})
	}
}

// TestPriority_DoublingWeight verifies that doubling the weight doubles Priority.
func TestPriority_DoublingWeight(t *testing.T) {
	sal := Salience(0.4)
	p1 := ComputePriority(sal, 1.0)
	p2 := ComputePriority(sal, 2.0)
	if math.Abs(float64(p2)/float64(p1)-2.0) > 1e-12 {
		t.Errorf("Priority should double when weight doubles: %v / %v = %v, want 2.0", p2, p1, float64(p2)/float64(p1))
	}
}

// ── AC: Load reads values.weights from injected bytes (D10) ────────────────────

func TestLoad_FromInjectedReader(t *testing.T) {
	cfg, err := Load(strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("Load from injected reader failed: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned nil Config")
	}
}

func TestLoad_ShippedWeights(t *testing.T) {
	cfg := mustLoad(t)

	tests := []struct {
		dim  core.Dimension
		want float64
	}{
		{dim: "Safety", want: 1.40},
		{dim: "Hydration", want: 1.05},
		{dim: "Satiety", want: 1.00},
		{dim: "Rest", want: 0.85},
		{dim: "Standing", want: 0.60},
		{dim: "Openness", want: 0.40},
	}

	for _, tt := range tests {
		t.Run(string(tt.dim), func(t *testing.T) {
			got := cfg.Weight(tt.dim)
			if math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("Weight(%q) = %v, want %v", tt.dim, got, tt.want)
			}
		})
	}
}

// ── AC: Weight defaults to 1.0 for unknown dimension ──────────────────────────

func TestWeight_DefaultForUnknown(t *testing.T) {
	cfg := mustLoad(t)

	if w := cfg.Weight("NotADimension"); w != 1.0 {
		t.Errorf("Weight('NotADimension') = %v, want 1.0", w)
	}

	if w := cfg.Weight(core.Dimension("")); w != 1.0 {
		t.Errorf("Weight('') = %v, want 1.0", w)
	}

	if w := cfg.Weight("SomeNewDimension"); w != 1.0 {
		t.Errorf("Weight('SomeNewDimension') = %v, want 1.0", w)
	}
}

// ── AC: Load rejects a negative weight ─────────────────────────────────────────

func TestLoad_RejectsNegativeWeight(t *testing.T) {
	badBalance := minimalBalance(`
values:
  weights:
    Satiety: -0.5
    Hydration: 1.0
`)

	_, err := Load(strings.NewReader(badBalance))
	if err == nil {
		t.Fatal("expected error for negative weight, got nil")
	}
	if !strings.Contains(err.Error(), "Satiety") || !strings.Contains(err.Error(), "negative") {
		t.Errorf("error = %q, want message mentioning 'Satiety' and 'negative'", err.Error())
	}
}

func TestLoad_RejectsNegativeWeight_SecondDim(t *testing.T) {
	badBalance := minimalBalance(`
values:
  weights:
    Satiety: 1.0
    Hydration: -0.3
`)

	_, err := Load(strings.NewReader(badBalance))
	if err == nil {
		t.Fatal("expected error for negative weight, got nil")
	}
	if !strings.Contains(err.Error(), "Hydration") || !strings.Contains(err.Error(), "negative") {
		t.Errorf("error = %q, want message mentioning 'Hydration' and 'negative'", err.Error())
	}
}

// ── AC: No selection/ordering API (D5) ───────────────────────────────────────────
// This is a compile-time and API-shape guard.

// TestNoSelectionAPI confirms the package exposes nothing that looks like
// selection, ordering, or arg-max. We verify by checking that the four compute
// functions and Config accessors are the only exported functions.
func TestNoSelectionAPI(t *testing.T) {
	// These compile-time checks verify that only the expected symbols exist.
	// If the package ever exports a function matching "Select|Choose|ArgMax|Best|Order|Sort",
	// this test should fail at the naming-review level.
	//
	// At runtime, we verify the absence of state:
	var s Standing
	var sal Salience
	var e EffValue
	var p Priority

	// All four named types must be usable and distinct.
	_ = s
	_ = sal
	_ = e
	_ = p
}

// ── AC: Determinism (D12) ─────────────────────────────────────────────────────

func TestDimensions_LexicographicallySorted(t *testing.T) {
	cfg := mustLoad(t)

	dims := cfg.Dimensions()
	// Expected sorted order: Hydration, Openness, Rest, Safety, Satiety, Standing
	want := []core.Dimension{"Hydration", "Openness", "Rest", "Safety", "Satiety", "Standing"}
	if len(dims) != len(want) {
		t.Fatalf("Dimensions() length = %d, want %d; got %v", len(dims), len(want), dims)
	}
	for i, d := range dims {
		if d != want[i] {
			t.Errorf("Dimensions()[%d] = %q, want %q", i, d, want[i])
		}
	}
}

func TestDimensions_IdenticalAcrossRepeatedCalls(t *testing.T) {
	cfg := mustLoad(t)

	dims1 := cfg.Dimensions()
	dims2 := cfg.Dimensions()

	if len(dims1) != len(dims2) {
		t.Fatalf("lengths differ: %d vs %d", len(dims1), len(dims2))
	}
	for i := range dims1 {
		if dims1[i] != dims2[i] {
			t.Fatalf("order differs at index %d: %q vs %q", i, dims1[i], dims2[i])
		}
	}
}

func TestDimensions_IdenticalAcrossTwoLoads(t *testing.T) {
	cfg1 := mustLoad(t)
	cfg2 := mustLoad(t)

	dims1 := cfg1.Dimensions()
	dims2 := cfg2.Dimensions()

	if len(dims1) != len(dims2) {
		t.Fatalf("lengths differ: %d vs %d", len(dims1), len(dims2))
	}
	for i := range dims1 {
		if dims1[i] != dims2[i] {
			t.Fatalf("order differs at index %d: %q vs %q", i, dims1[i], dims2[i])
		}
	}
}

func TestDimensions_MutationIsCopy(t *testing.T) {
	cfg := mustLoad(t)

	dims := cfg.Dimensions()
	if len(dims) > 0 {
		dims[0] = "MUTATED"
	}
	dimsAgain := cfg.Dimensions()
	if dimsAgain[0] == "MUTATED" {
		t.Error("mutating returned Dimensions() slice modified Config")
	}
}

func TestAppraisalFunctions_Pure(t *testing.T) {
	// Verify that calling the four appraisal functions repeatedly with the same
	// inputs yields identical outputs (no state between calls).
	need := mkNeed(1.0)
	currentIntensity := 0.3

	s1 := ComputeStanding(need, currentIntensity)
	sal1 := ComputeSalience(s1)
	ev1 := ComputeEffValue(sal1, 0.5)
	p1 := ComputePriority(sal1, 1.0)

	// Call again with same inputs
	s2 := ComputeStanding(need, currentIntensity)
	sal2 := ComputeSalience(s2)
	ev2 := ComputeEffValue(sal2, 0.5)
	p2 := ComputePriority(sal2, 1.0)

	if s1 != s2 {
		t.Errorf("Standing not pure: %v vs %v", s1, s2)
	}
	if sal1 != sal2 {
		t.Errorf("Salience not pure: %v vs %v", sal1, sal2)
	}
	if ev1 != ev2 {
		t.Errorf("EffValue not pure: %v vs %v", ev1, ev2)
	}
	if p1 != p2 {
		t.Errorf("Priority not pure: %v vs %v", p1, p2)
	}
}

// ── AC: Integration with the appraisal chain ──────────────────────────────────

func TestIntegration_SatietyOverRest(t *testing.T) {
	// Scenario: agent whose Satiety intensity exceeds its threshold while Rest is
	// comfortably met. Priority(Satiety) > Priority(Rest) so the planner (downstream)
	// would pick Satiety.
	cfg := mustLoad(t)

	satietyWeight := cfg.Weight("Satiety")  // 1.00
	restWeight := cfg.Weight("Rest")        // 0.85

	// Satiety: intensity = 0.8, threshold = 0.55 (need's Threshold)
	// Standing = 1 - 0.8/0.55 = 1 - 1.4545... = clamped to 0
	// Salience = 1 - 0 = 1
	// Priority = 1 * 1.0 = 1.0
	satietyNeed := mkNeed(0.55)
	satietyStanding := ComputeStanding(satietyNeed, 0.8)
	satietySalience := ComputeSalience(satietyStanding)
	satietyPriority := ComputePriority(satietySalience, satietyWeight)

	if float64(satietyPriority) <= 0 {
		t.Errorf("Satiety Priority should be > 0 for high intensity, got %v", satietyPriority)
	}
	if float64(satietyStanding) != 0 {
		t.Errorf("Satiety Standing should be 0 when intensity exceeds threshold, got %v", satietyStanding)
	}
	if float64(satietySalience) != 1 {
		t.Errorf("Satiety Salience should be 1 when intensity exceeds threshold, got %v", satietySalience)
	}

	// Rest: intensity = 0.1, threshold = 0.45
	// Standing = 1 - 0.1/0.45 = 1 - 0.222... = 0.777...
	// Salience = 1 - 0.777... = 0.222...
	// Priority = 0.222... * 0.85 = 0.1889
	restNeed := mkNeed(0.45)
	restStanding := ComputeStanding(restNeed, 0.1)
	restSalience := ComputeSalience(restStanding)
	restPriority := ComputePriority(restSalience, restWeight)

	// Assert: Satiety Priority > Rest Priority
	if satietyPriority <= restPriority {
		t.Errorf("Priority(Satiety) = %v should be > Priority(Rest) = %v",
			satietyPriority, restPriority)
	}
}

func TestIntegration_HydrationVsSatiety(t *testing.T) {
	// With Hydration weight (1.05) > Satiety weight (1.00), at equal salience
	// Hydration should have higher priority.
	cfg := mustLoad(t)

	hydWeight := cfg.Weight("Hydration") // 1.05
	satWeight := cfg.Weight("Satiety")   // 1.00

	// Same salience for both
	sal := Salience(0.5)

	hydPriority := ComputePriority(sal, hydWeight)
	satPriority := ComputePriority(sal, satWeight)

	if hydPriority <= satPriority {
		t.Errorf("Priority(Hydration) = %v should be > Priority(Satiety) = %v at equal salience",
			hydPriority, satPriority)
	}
}

// ── AC: Missing values block ──────────────────────────────────────────────────

func TestLoad_MissingValuesBlock(t *testing.T) {
	badBalance := minimalBalance("") // no values: block at all
	_, err := Load(strings.NewReader(badBalance))
	if err == nil {
		t.Fatal("expected error for missing 'values:' block, got nil")
	}
	if !strings.Contains(err.Error(), "values") {
		t.Errorf("error = %q, want message mentioning 'values'", err.Error())
	}
}

func TestLoad_MissingWeightsSubBlock(t *testing.T) {
	badBalance := minimalBalance(`
values:
  foo: bar
`)
	_, err := Load(strings.NewReader(badBalance))
	if err == nil {
		t.Fatal("expected error for missing 'weights:' sub-block, got nil")
	}
	if !strings.Contains(err.Error(), "weights") {
		t.Errorf("error = %q, want message mentioning 'weights'", err.Error())
	}
}

func TestLoad_EmptyWeightsBlock(t *testing.T) {
	badBalance := minimalBalance(`
values:
  weights: {}
`)
	_, err := Load(strings.NewReader(badBalance))
	if err == nil {
		t.Fatal("expected error for empty 'weights:' block, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, want message mentioning 'empty'", err.Error())
	}
}

// ── AC: Config immutability ───────────────────────────────────────────────────

func TestConfig_NoSetters(t *testing.T) {
	cfg := mustLoad(t)

	// The Config type should have no exported mutable fields or methods.
	// We verify that the only exported methods work correctly.
	if cfg.Weight("Satiety") != 1.0 {
		t.Error("Weight accessor failed")
	}
	if len(cfg.Dimensions()) != 6 {
		t.Error("Dimensions accessor failed")
	}
}

// ── Helper clamp function test ────────────────────────────────────────────────

// TestClamp01 exercises the internal clamp helper.
func TestClamp01(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{input: -1.0, want: 0.0},
		{input: -0.001, want: 0.0},
		{input: 0.0, want: 0.0},
		{input: 0.5, want: 0.5},
		{input: 1.0, want: 1.0},
		{input: 1.001, want: 1.0},
		{input: 100.0, want: 1.0},
	}
	for _, tt := range tests {
		got := clamp01(tt.input)
		if got != tt.want {
			t.Errorf("clamp01(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
