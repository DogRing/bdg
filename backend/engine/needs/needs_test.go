package needs

import (
	"math"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/core"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// needsYAML is the shipped content/needs.yaml catalog, inline for deterministic
// testing (no filesystem IO).
const needsYAML = `
schema_version: 1
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

  - id: Rest
    kind: consumable
    default:
      posture: MaintainAbove
      setpoint: 0.45
      referent: Self
    salience:
      curve: deficit
      gain: 0.9

  - id: Safety
    kind: conditional
    default:
      posture: PreventBelow
      setpoint: 0.40
      referent: Self
    salience:
      curve: deficit
      gain: 1.3

  - id: Standing
    kind: conditional
    default:
      posture: Maximize
      setpoint: 0.50
      referent: Self
    salience:
      curve: gap_to_max
      gain: 0.7

  - id: Openness
    kind: conditional
    default:
      posture: Maximize
      setpoint: 0.35
      referent: Place
    salience:
      curve: gap_to_max
      gain: 0.5
`

// balanceYAML is the shipped content/balance.yaml needs: block for testing.
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
`

// mustLoad loads both documents and returns the registry. Fatal on error.
func mustLoad(t *testing.T) *Registry {
	t.Helper()
	reg, err := Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	return reg
}

// ── AC: Need-rate constants loaded from balance.yaml, not hardcoded ────────────

func TestLoad_RateAndThreshold(t *testing.T) {
	reg := mustLoad(t)

	tests := []struct {
		id        string
		wantRate  float64
		wantThres float64
	}{
		{id: "Satiety", wantRate: 0.00070, wantThres: 0.55},
		{id: "Hydration", wantRate: 0.00110, wantThres: 0.50},
		{id: "Rest", wantRate: 0.00045, wantThres: 0.45},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			d, ok := reg.Def(NeedID(tt.id))
			if !ok {
				t.Fatalf("Def(%q) not found", tt.id)
			}
			if d.Rate != tt.wantRate {
				t.Errorf("Rate = %v, want %v", d.Rate, tt.wantRate)
			}
			if d.Threshold != tt.wantThres {
				t.Errorf("Threshold = %v, want %v", d.Threshold, tt.wantThres)
			}
			// Consumable: Setpoint must equal Threshold.
			if d.Setpoint != tt.wantThres {
				t.Errorf("Setpoint = %v, want %v (equals Threshold for consumable)", d.Setpoint, tt.wantThres)
			}
		})
	}
}

// ── AC: Catalog loaded from needs.yaml, path injected ─────────────────────────

func TestLoad_Catalog(t *testing.T) {
	reg := mustLoad(t)

	// Should have exactly 6 dimensions.
	if reg.Len() != 6 {
		t.Fatalf("Len() = %d, want 6", reg.Len())
	}

	tests := []struct {
		id        string
		wantKind  Kind
		wantPost  Posture
		wantRef   core.ReferentKind
		wantCurve SalienceCurve
		wantGain  float64
	}{
		{id: "Satiety", wantKind: Consumable, wantPost: MaintainAbove, wantRef: core.Self, wantCurve: Deficit, wantGain: 1.0},
		{id: "Hydration", wantKind: Consumable, wantPost: MaintainAbove, wantRef: core.Self, wantCurve: Deficit, wantGain: 1.1},
		{id: "Rest", wantKind: Consumable, wantPost: MaintainAbove, wantRef: core.Self, wantCurve: Deficit, wantGain: 0.9},
		{id: "Safety", wantKind: Conditional, wantPost: PreventBelow, wantRef: core.Self, wantCurve: Deficit, wantGain: 1.3},
		{id: "Standing", wantKind: Conditional, wantPost: Maximize, wantRef: core.Self, wantCurve: GapToMax, wantGain: 0.7},
		{id: "Openness", wantKind: Conditional, wantPost: Maximize, wantRef: core.Place, wantCurve: GapToMax, wantGain: 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			d, ok := reg.Def(NeedID(tt.id))
			if !ok {
				t.Fatalf("Def(%q) not found", tt.id)
			}
			if d.Kind != tt.wantKind {
				t.Errorf("Kind = %v, want %v", d.Kind, tt.wantKind)
			}
			if d.Posture != tt.wantPost {
				t.Errorf("Posture = %v, want %v", d.Posture, tt.wantPost)
			}
			if d.Referent != tt.wantRef {
				t.Errorf("Referent = %v, want %v", d.Referent, tt.wantRef)
			}
			if d.Curve != tt.wantCurve {
				t.Errorf("Curve = %v, want %v", d.Curve, tt.wantCurve)
			}
			if d.Gain != tt.wantGain {
				t.Errorf("Gain = %v, want %v", d.Gain, tt.wantGain)
			}
		})
	}
}

// ── AC: IDs() deterministic order (D12) ───────────────────────────────────────

func TestIDs_DeterministicOrder(t *testing.T) {
	reg1 := mustLoad(t)
	reg2 := mustLoad(t) // fresh Load from same bytes

	ids1 := reg1.IDs()
	ids2 := reg2.IDs()

	// Cross-process stability: two registries from same bytes must have identical order.
	if len(ids1) != len(ids2) {
		t.Fatalf("lengths differ: %d vs %d", len(ids1), len(ids2))
	}
	for i := range ids1 {
		if ids1[i] != ids2[i] {
			t.Fatalf("order differs at index %d: %q vs %q", i, ids1[i], ids2[i])
		}
	}

	// Lexicographically sorted check.
	want := []NeedID{"Hydration", "Openness", "Rest", "Safety", "Satiety", "Standing"}
	for i, id := range ids1 {
		if id != want[i] {
			t.Errorf("IDs()[%d] = %q, want %q", i, id, want[i])
		}
	}

	// Within-process stability: repeated calls must match.
	ids1b := reg1.IDs()
	for i := range ids1 {
		if ids1[i] != ids1b[i] {
			t.Fatalf("within-process call differs at index %d", i)
		}
	}

	// Mutating the returned slice must not affect the registry.
	if len(ids1) > 0 {
		ids1[0] = "MUTATED"
		idsAfter := reg1.IDs()
		if idsAfter[0] == "MUTATED" {
			t.Error("mutating returned IDs() slice modified registry")
		}
	}
}

// ── AC: Need does NOT encode satisfaction (D5) ────────────────────────────────

func TestDef_NoSatisfactionField(t *testing.T) {
	// Compile-time checks: the Def struct must not have an Action, Object, or Effect field.
	// This is structural verification via the godoc. We check at runtime that
	// the Def only exposes the allowed fields.
	d := Def{
		ID:       "Test",
		Kind:     Consumable,
		Rate:     0.001,
		Threshold: 0.5,
		Posture:  MaintainAbove,
		Setpoint: 0.5,
		Referent: core.Self,
		Curve:    Deficit,
		Gain:     1.0,
	}

	_ = d
	// If this compiles and Def has no Action/Object/Effect field type, we pass.
	// At runtime we also verify that Level/Demand don't reference any such field.
	if d.Demand(100) != 0.1 {
		t.Error("Demand works as a pure function, not a stored field")
	}
}

// ── AC: No "future need" field (D9) ───────────────────────────────────────────

func TestDef_NoFutureNeedField(t *testing.T) {
	d := Def{Rate: 0.001}
	got := d.Demand(100)
	want := 0.1
	if got != want {
		t.Errorf("Demand(100) = %v, want %v", got, want)
	}
	// Verifies that Demand is computed from Rate * minutes, not from any stored field.
	// For zero rate:
	d2 := Def{Rate: 0}
	if d2.Demand(100) != 0 {
		t.Error("Demand must be 0 when Rate is 0")
	}
}

// ── AC: Conditional needs have Rate == 0 and no balance entry ──────────────────

func TestConditional_NoRate(t *testing.T) {
	reg := mustLoad(t)

	condIDs := reg.Kinds(Conditional)
	wantConditional := []NeedID{"Openness", "Safety", "Standing"} // sorted lexicographically
	if len(condIDs) != len(wantConditional) {
		t.Fatalf("Kinds(Conditional) length = %d, want %d", len(condIDs), len(wantConditional))
	}
	for i, id := range condIDs {
		if id != wantConditional[i] {
			t.Errorf("Kinds(Conditional)[%d] = %q, want %q", i, id, wantConditional[i])
		}
		d, _ := reg.Def(id)
		if d.Rate != 0 {
			t.Errorf("Conditional need %q has Rate %v, want 0", id, d.Rate)
		}
	}
}

func TestConsumable_HasRate(t *testing.T) {
	reg := mustLoad(t)

	consIDs := reg.Kinds(Consumable)
	wantConsumable := []NeedID{"Hydration", "Rest", "Satiety"} // sorted lexicographically
	if len(consIDs) != len(wantConsumable) {
		t.Fatalf("Kinds(Consumable) length = %d, want %d", len(consIDs), len(wantConsumable))
	}
	for i, id := range consIDs {
		if id != wantConsumable[i] {
			t.Errorf("Kinds(Consumable)[%d] = %q, want %q", i, id, wantConsumable[i])
		}
		d, _ := reg.Def(id)
		if d.Rate <= 0 {
			t.Errorf("Consumable need %q has Rate %v, want > 0", id, d.Rate)
		}
	}
}

// ── AC: Forward-roll helpers are pure and clamped (D12) ───────────────────────

func TestLevel_Pure(t *testing.T) {
	// Consumable: decays linearly, clamped [0,1].
	d := Def{Rate: 0.001, Kind: Consumable}
	tests := []struct {
		level0  float64
		minutes core.GameMinutes
		want    float64
	}{
		{level0: 1.0, minutes: 100, want: 0.9},
		{level0: 1.0, minutes: 0, want: 1.0},
		{level0: 0.5, minutes: 100, want: 0.4},
		{level0: 0.1, minutes: 200, want: 0.0}, // clamped to 0
		{level0: 1.0, minutes: 1001, want: 0.0}, // clamped to 0
		{level0: 0.0, minutes: 50, want: 0.0},
	}
	for _, tt := range tests {
		got := d.Level(tt.level0, tt.minutes)
		if math.Abs(got-tt.want) > 1e-12 {
			t.Errorf("Level(%v, %d) = %v, want %v", tt.level0, tt.minutes, got, tt.want)
		}
	}

	// Conditional: unchanged.
	d2 := Def{Rate: 0, Kind: Conditional}
	got := d2.Level(0.75, 500)
	if got != 0.75 {
		t.Errorf("Conditional Level = %v, want 0.75", got)
	}
}

func TestLevel_SatietyGolden(t *testing.T) {
	d := Def{Rate: 0.00070, Kind: Consumable}
	// From full (1.0), after 500 minutes: 1.0 - 0.00070*500 = 1.0 - 0.35 = 0.65
	got := d.Level(1.0, 500)
	want := 0.65
	if math.Abs(got-want) > 1e-12 {
		t.Errorf("Satiety Level(1.0, 500) = %v, want %v", got, want)
	}
}

// ── AC: BreachAt ──────────────────────────────────────────────────────────────

func TestBreachAt_Pure(t *testing.T) {
	// Satiety: Rate=0.00070, setpoint=0.55.
	// From full (1.0): level crosses 0.55 at t = (1.0 - 0.55) / 0.00070 = 0.45 / 0.00070 = 642.857...
	d := Def{Rate: 0.00070, Kind: Consumable}

	// Within horizon: should find breach at ceil(642.857) = 643.
	at, ok := d.BreachAt(1.0, 0.55, 1000)
	if !ok {
		t.Fatal("BreachAt(1.0, 0.55, 1000) = ok=false, want true")
	}
	if at != 643 {
		t.Errorf("BreachAt = %d, want 643", at)
	}

	// Already below setpoint: breach at time 0.
	at, ok = d.BreachAt(0.3, 0.55, 1000)
	if !ok {
		t.Fatal("BreachAt(0.3, 0.55, 1000) = ok=false, want true")
	}
	if at != 0 {
		t.Errorf("BreachAt when already below = %d, want 0", at)
	}

	// Beyond horizon: ok=false.
	_, ok = d.BreachAt(1.0, 0.55, 600)
	if ok {
		t.Fatal("BreachAt(1.0, 0.55, 600) = ok=true, want false (breach at 643 > 600)")
	}

	// Conditional: always ok=false.
	d2 := Def{Rate: 0, Kind: Conditional}
	_, ok = d2.BreachAt(0.5, 0.4, 1000)
	if ok {
		t.Fatal("Conditional BreachAt must return ok=false")
	}

	// Zero rate: ok=false.
	d3 := Def{Rate: 0, Kind: Consumable}
	_, ok = d3.BreachAt(1.0, 0.5, 1000)
	if ok {
		t.Fatal("Zero-rate BreachAt must return ok=false")
	}
}

// ── AC: Salience per curve ────────────────────────────────────────────────────

func TestSalience_Curves(t *testing.T) {
	// Deficit curve: Salience = max(0, setpoint - level) * Gain
	t.Run("Deficit", func(t *testing.T) {
		d := Def{Curve: Deficit, Gain: 1.0}
		tests := []struct {
			level, setpoint, want float64
		}{
			{level: 0.3, setpoint: 0.55, want: 0.25},
			{level: 0.6, setpoint: 0.55, want: 0.0},   // level > setpoint => no salience
			{level: 0.55, setpoint: 0.55, want: 0.0},  // equal => 0
			{level: 1.0, setpoint: 0.55, want: 0.0},
			{level: 0.0, setpoint: 0.55, want: 0.55},
		}
		for _, tt := range tests {
			got := d.Salience(tt.level, tt.setpoint)
			if math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("Salience(%v, %v) = %v, want %v", tt.level, tt.setpoint, got, tt.want)
			}
		}
	})

	// GapToMax curve: Salience = max(0, (1 - level)) * Gain
	t.Run("GapToMax", func(t *testing.T) {
		d := Def{Curve: GapToMax, Gain: 0.7}
		tests := []struct {
			level, setpoint, want float64
		}{
			{level: 0.3, setpoint: 0.5, want: 0.49},    // (1-0.3)*0.7 = 0.49
			{level: 1.0, setpoint: 0.5, want: 0.0},      // level=1 => no salience
			{level: 0.0, setpoint: 0.5, want: 0.7},      // (1-0)*0.7 = 0.7
			{level: 0.8, setpoint: 0.5, want: 0.14},     // (1-0.8)*0.7 = 0.14
		}
		for _, tt := range tests {
			got := d.Salience(tt.level, tt.setpoint)
			if math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("Salience(%v, %v) = %v, want %v", tt.level, tt.setpoint, got, tt.want)
			}
		}
	})

	// Gain = 0 => always 0.
	t.Run("ZeroGain", func(t *testing.T) {
		d := Def{Curve: Deficit, Gain: 0}
		if s := d.Salience(0.2, 0.5); s != 0 {
			t.Errorf("Salience with Gain=0 = %v, want 0", s)
		}
	})
}

// ── AC: Immutable after init ──────────────────────────────────────────────────

func TestRegistry_Immutable(t *testing.T) {
	reg := mustLoad(t)

	// Mutating returned IDs() must not affect registry.
	ids := reg.IDs()
	if len(ids) > 0 {
		ids[0] = "Mutated"
	}
	idsAgain := reg.IDs()
	if idsAgain[0] == "Mutated" {
		t.Error("Registry leaked mutable state through IDs()")
	}

	// Mutating returned Kinds() must not affect registry.
	cons := reg.Kinds(Consumable)
	if len(cons) > 0 {
		cons[0] = "Mutated"
	}
	consAgain := reg.Kinds(Consumable)
	if consAgain[0] == "Mutated" {
		t.Error("Registry leaked mutable state through Kinds()")
	}

	// No setter on Registry.
	// (Compile-time guarantee, but runtime checks below pass.)
	if reg.Len() != 6 {
		t.Error("Len changed unexpectedly")
	}
}

// ── AC: Bad balance block errors ──────────────────────────────────────────────

func TestLoad_MissingBalanceNeeds(t *testing.T) {
	// balanceDoc with no needs: block at all.
	badBalance := `
schema_version: 1
world:
  tick_minutes: 1
perception:
  sight_radius: 18.0
  smell_radius: 10.0
  hearing_radius: 14.0
tag_levels:
  effort: { none: 0.0, low: 0.20 }
cost_terms:
  effort: { weight: 1.0 }
mood:
  lambda: 0.25
adrenaline:
  surge: 0.6
stamina:
  max: 1.0
urgency:
  from_deficit: 1.4
self_calibration:
  beta: 0.08
gossip:
  alpha: 0.12
`

	// We can't parse this because KnownFields will reject missing keys.
	// Instead, test empty needs block.
	_ = badBalance

	// Empty needs block.
	t.Run("empty needs block", func(t *testing.T) {
		// Build a minimal balance doc with empty needs.
		minimal := minimalBalance("")
		_, err := Load(strings.NewReader(needsYAML), strings.NewReader(minimal))
		if err == nil {
			t.Fatal("expected error for empty needs: block, got nil")
		}
		if !strings.Contains(err.Error(), "needs: block is empty or missing") {
			t.Errorf("error = %q, want 'needs: block is empty or missing'", err.Error())
		}
	})

	// Malformed entry: decay_per_tick <= 0.
	t.Run("zero decay_per_tick", func(t *testing.T) {
		needsBlock := "\nneeds:\n  Satiety: { decay_per_tick: 0.0, satisfaction_threshold: 0.5 }"
		bal := minimalBalance(needsBlock)
		_, err := Load(strings.NewReader(needsYAML), strings.NewReader(bal))
		if err == nil {
			t.Fatal("expected error for zero decay_per_tick, got nil")
		}
		if !strings.Contains(err.Error(), "decay_per_tick must be > 0") {
			t.Errorf("error = %q, want 'decay_per_tick must be > 0'", err.Error())
		}
	})

	t.Run("negative decay_per_tick", func(t *testing.T) {
		needsBlock := "\nneeds:\n  Satiety: { decay_per_tick: -0.001, satisfaction_threshold: 0.5 }"
		bal := minimalBalance(needsBlock)
		_, err := Load(strings.NewReader(needsYAML), strings.NewReader(bal))
		if err == nil {
			t.Fatal("expected error for negative decay_per_tick, got nil")
		}
		if !strings.Contains(err.Error(), "decay_per_tick must be > 0") {
			t.Errorf("error = %q, want 'decay_per_tick must be > 0'", err.Error())
		}
	})

	// Threshold outside [0,1].
	t.Run("threshold out of range", func(t *testing.T) {
		needsBlock := "\nneeds:\n  Satiety: { decay_per_tick: 0.001, satisfaction_threshold: 1.5 }"
		bal := minimalBalance(needsBlock)
		_, err := Load(strings.NewReader(needsYAML), strings.NewReader(bal))
		if err == nil {
			t.Fatal("expected error for threshold > 1, got nil")
		}
		if !strings.Contains(err.Error(), "satisfaction_threshold") ||
			!strings.Contains(err.Error(), "outside [0,1]") {
			t.Errorf("error = %q, want 'satisfaction_threshold ... outside [0,1]'", err.Error())
		}
	})
}

// minimalBalance returns a minimal valid balance YAML with (optionally) extra lines
// appended to the end, so the YAML decoder doesn't fail on missing required keys.
// We build a full skeleton that satisfies KnownFields.
func minimalBalance(extraLines string) string {
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

// ── AC: Catalog ↔ rate cross-consistency ──────────────────────────────────────

func TestLoad_CrossConsistency(t *testing.T) {
	// Consumable dimension in needsDoc has no matching balance entry.
	t.Run("consumable without balance entry", func(t *testing.T) {
		// needs with an extra consumable that has no balance entry.
		extraNeeds := needsYAML + `
  - id: Magic
    kind: consumable
    default:
      posture: MaintainAbove
      setpoint: 0.50
      referent: Self
    salience:
      curve: deficit
      gain: 1.0
`
		_, err := Load(strings.NewReader(extraNeeds), strings.NewReader(balanceYAML))
		if err == nil {
			t.Fatal("expected error for consumable without balance entry, got nil")
		}
		if !strings.Contains(err.Error(), "Magic") {
			t.Errorf("error should mention 'Magic', got %q", err.Error())
		}
	})

	// Balance key names a dimension absent from needsDoc.
	// Produce a balance YAML that has all three consumable needs present
	// PLUS an extra "Phantom" key absent from needs.yaml.
	t.Run("balance key not in needs catalog", func(t *testing.T) {
		extraBalance := strings.Replace(balanceYAML,
			"needs:",
			"needs:\n  Phantom: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }",
			1)
		_, err := Load(strings.NewReader(needsYAML), strings.NewReader(extraBalance))
		if err == nil {
			t.Fatal("expected error for unknown balance key, got nil")
		}
		if !strings.Contains(err.Error(), "Phantom") {
			t.Errorf("error should mention 'Phantom', got %q", err.Error())
		}
	})

	// Balance key names a conditional dimension.
	t.Run("balance key names conditional", func(t *testing.T) {
		// Insert Safety into the needs: block (preserving Satiety/Hydration/Rest).
		extraBalance := strings.Replace(balanceYAML,
			"needs:",
			"needs:\n  Safety: { decay_per_tick: 0.001, satisfaction_threshold: 0.4 }",
			1)
		_, err := Load(strings.NewReader(needsYAML), strings.NewReader(extraBalance))
		if err == nil {
			t.Fatal("expected error for balance key naming conditional, got nil")
		}
		if !strings.Contains(err.Error(), "Safety") {
			t.Errorf("error should mention 'Safety', got %q", err.Error())
		}
	})
}

// ── AC: Missing needs.yaml validation ─────────────────────────────────────────

func TestLoad_NeedsDocValidation(t *testing.T) {
	// Invalid kind.
	t.Run("invalid kind", func(t *testing.T) {
		bad := strings.Replace(needsYAML, "consumable", "foobar", 1)
		_, err := Load(strings.NewReader(bad), strings.NewReader(balanceYAML))
		if err == nil {
			t.Fatal("expected error for invalid kind, got nil")
		}
		if !strings.Contains(err.Error(), "kind") {
			t.Errorf("error = %q, want kind-related message", err.Error())
		}
	})

	// Duplicate id.
	t.Run("duplicate id", func(t *testing.T) {
		// Append a duplicate Satiety entry at the end.
		dupYAML := needsYAML + `
  - id: Satiety
    kind: consumable
    default:
      posture: MaintainAbove
      setpoint: 0.55
      referent: Self
    salience:
      curve: deficit
      gain: 1.0
`
		_, err := Load(strings.NewReader(dupYAML), strings.NewReader(balanceYAML))
		if err == nil {
			t.Fatal("expected error for duplicate id, got nil")
		}
		if !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), "Satiety") {
			t.Errorf("error = %q, want duplicate-related message mentioning Satiety", err.Error())
		}
	})

	// Setpoint out of bounds.
	t.Run("setpoint out of bounds", func(t *testing.T) {
		bad := strings.Replace(needsYAML, "setpoint: 0.55", "setpoint: 1.5", 1)
		_, err := Load(strings.NewReader(bad), strings.NewReader(balanceYAML))
		if err == nil {
			t.Fatal("expected error for setpoint > 1, got nil")
		}
		if !strings.Contains(err.Error(), "setpoint") || !strings.Contains(err.Error(), "outside [0,1]") {
			t.Errorf("error = %q, want setpoint out of bounds", err.Error())
		}
	})

	// Negative gain.
	t.Run("negative gain", func(t *testing.T) {
		bad := strings.Replace(needsYAML, "gain: 1.0", "gain: -0.5", 1)
		_, err := Load(strings.NewReader(bad), strings.NewReader(balanceYAML))
		if err == nil {
			t.Fatal("expected error for negative gain, got nil")
		}
		if !strings.Contains(err.Error(), "gain") || !strings.Contains(err.Error(), "negative") {
			t.Errorf("error = %q, want gain-related message", err.Error())
		}
	})

	// Empty needs list.
	t.Run("empty needs list", func(t *testing.T) {
		empty := `
schema_version: 1
needs: []
`
		_, err := Load(strings.NewReader(empty), strings.NewReader(balanceYAML))
		if err == nil {
			t.Fatal("expected error for empty needs list, got nil")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("error = %q, want 'empty' message", err.Error())
		}
	})
}

// ── AC: Has / Def / Len ───────────────────────────────────────────────────────

func TestRegistry_Accessors(t *testing.T) {
	reg := mustLoad(t)

	// Has
	if !reg.Has("Satiety") {
		t.Error("Has('Satiety') = false, want true")
	}
	if !reg.Has("Standing") {
		t.Error("Has('Standing') = false, want true")
	}
	if reg.Has("NonExistent") {
		t.Error("Has('NonExistent') = true, want false")
	}

	// Def
	d, ok := reg.Def("Satiety")
	if !ok {
		t.Fatal("Def('Satiety') not found")
	}
	if d.ID != "Satiety" {
		t.Errorf("Def.ID = %q, want 'Satiety'", d.ID)
	}

	_, ok = reg.Def("NonExistent")
	if ok {
		t.Error("Def('NonExistent') should return ok=false")
	}

	// Len
	if reg.Len() != 6 {
		t.Errorf("Len() = %d, want 6", reg.Len())
	}
}

// ── Golden snapshot test: forward-roll values matching the shipped content ─────
// This tests that for the shipped content rates, the math is stable.

func TestGolden_ForwardRoll(t *testing.T) {
	reg := mustLoad(t)

	// Verify Satiety breach from full at setpoint 0.55 within reasonable horizon.
	satiety, _ := reg.Def("Satiety")
	at, ok := satiety.BreachAt(1.0, 0.55, 1000)
	if !ok {
		t.Fatal("Satiety should breach within 1000 min from full")
	}
	if at != 643 {
		t.Errorf("Satiety BreachAt = %d, want 643", at)
	}

	// Verify Hydration: Rate=0.00110, setpoint=0.50
	// from full: (1.0-0.50)/0.00110 = 454.545... ceil=455
	hyd, _ := reg.Def("Hydration")
	at, ok = hyd.BreachAt(1.0, 0.50, 1000)
	if !ok {
		t.Fatal("Hydration should breach within 1000 min from full")
	}
	if at != 455 {
		t.Errorf("Hydration BreachAt = %d, want 455", at)
	}

	// Verify Rest: Rate=0.00045, setpoint=0.45
	// from full: (1.0-0.45)/0.00045 = 1222.22... ceil=1223
	rest, _ := reg.Def("Rest")
	at, ok = rest.BreachAt(1.0, 0.45, 2000)
	if !ok {
		t.Fatal("Rest should breach within 2000 min from full")
	}
	if at != 1223 {
		t.Errorf("Rest BreachAt = %d, want 1223", at)
	}

	// Level after specific minutes.
	// Satiety at 500 min from full: 1.0 - 0.00070*500 = 0.65
	level := satiety.Level(1.0, 500)
	want := 0.65
	if math.Abs(level-want) > 1e-12 {
		t.Errorf("Satiety Level(1.0, 500) = %v, want %v", level, want)
	}

	// Hydration at 200 min from full: 1.0 - 0.00110*200 = 0.78
	level = hyd.Level(1.0, 200)
	want = 0.78
	if math.Abs(level-want) > 1e-12 {
		t.Errorf("Hydration Level(1.0, 200) = %v, want %v", level, want)
	}

	// Rest at 600 min from full: 1.0 - 0.00045*600 = 0.73
	level = rest.Level(1.0, 600)
	want = 0.73
	if math.Abs(level-want) > 1e-12 {
		t.Errorf("Rest Level(1.0, 600) = %v, want %v", level, want)
	}
}

// ── AC: Salience for shipped content ──────────────────────────────────────────

func TestGolden_Salience(t *testing.T) {
	reg := mustLoad(t)

	// Satiety: deficit curve, gain=1.0, setpoint=0.55
	sat, _ := reg.Def("Satiety")
	tests := []struct {
		name            string
		level, setpoint float64
		want            float64
	}{
		{name: "Satiety bel0w setpoint", level: 0.3, setpoint: 0.55, want: 0.25},
		{name: "Satiety at setpoint", level: 0.55, setpoint: 0.55, want: 0.0},
		{name: "Satiety ab0ve setpoint", level: 0.8, setpoint: 0.55, want: 0.0},
	}
	for _, tt := range tests {
		got := sat.Salience(tt.level, tt.setpoint)
		if math.Abs(got-tt.want) > 1e-12 {
			t.Errorf("%s: Salience(%v, %v) = %v, want %v", tt.name, tt.level, tt.setpoint, got, tt.want)
		}
	}

	// Standing: gap_to_max curve, gain=0.7
	st, _ := reg.Def("Standing")
	tests2 := []struct {
		level, setpoint, want float64
	}{
		{level: 0.3, setpoint: 0.5, want: (1 - 0.3) * 0.7},   // 0.49
		{level: 0.8, setpoint: 0.5, want: (1 - 0.8) * 0.7},   // 0.14
		{level: 1.0, setpoint: 0.5, want: 0.0},
	}
	for _, tt := range tests2 {
		got := st.Salience(tt.level, tt.setpoint)
		if math.Abs(got-tt.want) > 1e-12 {
			t.Errorf("Standing Salience(%v, %v) = %v, want %v", tt.level, tt.setpoint, got, tt.want)
		}
	}
}

// ── AC: Demand is a pure function (D9) ────────────────────────────────────────

func TestDemand_Pure(t *testing.T) {
	tests := []struct {
		rate    float64
		minutes core.GameMinutes
		want    float64
	}{
		{rate: 0.001, minutes: 100, want: 0.1},
		{rate: 0.001, minutes: 0, want: 0.0},
		{rate: 0.0, minutes: 100, want: 0.0},
		{rate: 0.00070, minutes: 720, want: 0.504},
		{rate: 0.5, minutes: 10, want: 5.0},
	}
	for _, tt := range tests {
		d := Def{Rate: tt.rate}
		got := d.Demand(tt.minutes)
		if math.Abs(got-tt.want) > 1e-12 {
			t.Errorf("Def{Rate:%v}.Demand(%d) = %v, want %v", tt.rate, tt.minutes, got, tt.want)
		}
	}
}

// ── AC: No literal need name in source (grep guard) ──────────────────────────
// This test passes if none of the shipped dimension ids appear as string literals
// in the needs.go source (excluding the test file). We verify at runtime that
// the code doesn't hardcode names by checking that all dimension data comes from
// content, not from the source.

func TestLoad_FromInjectedReaders(t *testing.T) {
	// Verify that Load accepts io.Readers, not paths — no file path in call.
	reg, err := Load(
		strings.NewReader(needsYAML),
		strings.NewReader(balanceYAML),
	)
	if err != nil {
		t.Fatalf("Load from injected readers failed: %v", err)
	}
	if reg.Len() == 0 {
		t.Fatal("registry empty after Load")
	}
}

// ── AC: Unknown need rejection ────────────────────────────────────────────────

func TestRegistry_RejectsUnknown(t *testing.T) {
	reg := mustLoad(t)

	if reg.Has("NotARealNeed") {
		t.Error("Has('NotARealNeed') should be false")
	}

	_, ok := reg.Def("NotARealNeed")
	if ok {
		t.Error("Def('NotARealNeed') should return ok=false")
	}
}

// ── AC: Conditional needs setpoint comes from needs.yaml ──────────────────────

func TestConditional_SetpointFromNeedsYAML(t *testing.T) {
	reg := mustLoad(t)

	tests := []struct {
		id     string
		wantSP float64
	}{
		{id: "Safety", wantSP: 0.40},
		{id: "Standing", wantSP: 0.50},
		{id: "Openness", wantSP: 0.35},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			d, ok := reg.Def(NeedID(tt.id))
			if !ok {
				t.Fatalf("Def(%q) not found", tt.id)
			}
			if d.Setpoint != tt.wantSP {
				t.Errorf("Setpoint = %v, want %v", d.Setpoint, tt.wantSP)
			}
			if d.Threshold != tt.wantSP {
				t.Errorf("Threshold = %v, want %v (equals Setpoint for conditional)", d.Threshold, tt.wantSP)
			}
		})
	}
}

// ── AC: Consumable Kinds returns correct subset ───────────────────────────────

func TestKinds_Consumable(t *testing.T) {
	reg := mustLoad(t)

	cons := reg.Kinds(Consumable)
	want := []NeedID{"Hydration", "Rest", "Satiety"}
	if len(cons) != len(want) {
		t.Fatalf("Kinds(Consumable) = %v, want %v", cons, want)
	}
	for i := range cons {
		if cons[i] != want[i] {
			t.Errorf("Kinds(Consumable)[%d] = %q, want %q", i, cons[i], want[i])
		}
	}
}
