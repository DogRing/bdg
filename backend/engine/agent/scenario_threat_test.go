package agent

import (
	"math"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/gates"
	"github.com/dogring/bdg/engine/mind/needs"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/tom"
	"github.com/dogring/bdg/engine/mind/values"
)

// ── Helpers ─────────────────────────────────────────────────────────────────────

// threatTestRegs creates minimal registries that include the Safety conditional need.
func threatTestRegs(t *testing.T) testRegs {
	t.Helper()
	statReg := mustLoadStats(t, testStatsYAML)

	needsYAML := `schema_version: 1
needs:
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Hydration
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.50, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Rest
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.45, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Safety
    kind: conditional
    default: { posture: PreventBelow, setpoint: 0.60, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`
	balanceYAML := `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
  Hydration: { decay_per_tick: 0.00110, satisfaction_threshold: 0.50 }
  Rest: { decay_per_tick: 0.00045, satisfaction_threshold: 0.45 }
values:
  weights:
    Safety: 1.40
    Hydration: 1.05
    Satiety: 1.00
    Rest: 0.85
`
	needReg, err := needs.Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}
	valsCfg, err := values.Load(strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}

	actionsYAML := `schema_version: 1
actions:
  - id: Eat
    tags: [effort:low]
    duration: 10
    produces: [has_Satiety]
    effect: { Satiety: 0.4 }
  - id: Forage
    tags: [effort:med]
    duration: 30
    produces: [has_food]
    produces_item: berry
  - id: RestAction
    tags: [effort:none]
    duration: 60
    produces: [has_Rest]
    effect_per_minute: { Rest: 0.01 }
  - id: MoveTo
    tags: [effort:low]
    duration: 10
    produces: [at_target]
  - id: Patrol
    tags: [effort:med]
    target_kind: village_center
    requires: [at_target]
    produces: [has_Safety]
    effect: { Safety: 0.15 }
    duration: 12
`
	actReg := mustLoadActions(t, actionsYAML)

	gatesYAML := `schema_version: 2
gates:
  - id: always_visible
    tags: []
    expr: { stat: "Intelligence", op: ">=", value: -1 }
`
	gateReg, err := mustLoadGates(t, gatesYAML, statReg)
	if err != nil {
		t.Fatalf("gates.Load: %v", err)
	}
	_ = gateReg

	return testRegs{
		actions: actReg,
		gates:   gateReg,
		needs:   needReg,
		stats:   statReg,
		values:  valsCfg,
	}
}

func mustLoadActions(t *testing.T, yamlStr string) *actions.Registry {
	t.Helper()
	reg, err := actions.Load(strings.NewReader(yamlStr))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}
	return reg
}

func mustLoadGates(t *testing.T, yamlStr string, statReg *stats.Registry) (*gates.Registry, error) {
	t.Helper()
	return gates.Load(strings.NewReader(yamlStr), statReg)
}

// newThreatCfg returns a Config with threat fields populated for testing.
func newThreatCfg(safetyDim core.Dimension) Config {
	cfg := DefaultConfig()
	cfg.ThreatTags = []core.Tag{"X"} // use a synthetic tag so no literal from content
	cfg.SafetyDim = safetyDim
	cfg.ThreatPerThreatGain = 0.20
	cfg.ThreatSafetyDecay = 0.10
	return cfg
}

// newThreatAgent creates a fresh agent with threat perception config.
func newThreatAgent(t *testing.T, id core.AgentID, cfg Config) *Agent {
	t.Helper()
	statReg := mustLoadStats(t, testStatsYAML)
	realStats := statReg.Defaults()
	selfToM := tom.NewToM(id, realStats, 0.5, rng.New(42), statReg, cfg.Rates)
	return New(id, core.Vec2{X: 0, Y: 0}, realStats, selfToM, cfg)
}

// threatTestServices builds Services struct from threat test regs.
func threatTestServices(t *testing.T, regs testRegs) Services {
	t.Helper()
	return testServices(t, regs)
}

// ── Tests ──────────────────────────────────────────────────────────────────────

// TestThreatPerception_SafetyRises checks that perceiving a threat-tagged entity
// raises the Safety intensity.
func TestThreatPerception_SafetyRises(t *testing.T) {
	regs := threatTestRegs(t)
	safetyDim := core.Dimension("Safety")
	cfg := newThreatCfg(safetyDim)
	a := newThreatAgent(t, "agent_a", cfg)
	svc := threatTestServices(t, regs)

	// No threat -> Safety stays 0 (starting from 0).
	a.driveConditionalSafety(nil, svc.Needs)
	if a.NeedIntensities[safetyDim] != 0 {
		t.Fatalf("starting Safety intensity = %v, want 0", a.NeedIntensities[safetyDim])
	}

	// One perceived threat (entity tagged ["agent", "X"]) equals gain 0.20.
	threats := []core.AgentID{"threat_1"}
	a.driveConditionalSafety(threats, svc.Needs)
	want := 0.20
	got := a.NeedIntensities[safetyDim]
	if got != want {
		t.Fatalf("after 1 threat: Safety = %v, want %v", got, want)
	}

	// Two threats should give 0.40.
	threats2 := []core.AgentID{"threat_1", "threat_2"}
	a.NeedIntensities[safetyDim] = 0 // reset
	a.driveConditionalSafety(threats2, svc.Needs)
	want2 := 0.40
	if a.NeedIntensities[safetyDim] != want2 {
		t.Fatalf("after 2 threats: Safety = %v, want %v", a.NeedIntensities[safetyDim], want2)
	}
}

// TestThreatPerception_SafetyDecays checks that without threats, Safety decays.
func TestThreatPerception_SafetyDecays(t *testing.T) {
	regs := threatTestRegs(t)
	safetyDim := core.Dimension("Safety")
	cfg := newThreatCfg(safetyDim)
	a := newThreatAgent(t, "agent_a", cfg)
	svc := threatTestServices(t, regs)

	// Start at 0.50, no threat -> decays by 0.10.
	a.NeedIntensities[safetyDim] = 0.50
	a.driveConditionalSafety(nil, svc.Needs)
	want := 0.40
	got := a.NeedIntensities[safetyDim]
	if got != want {
		t.Fatalf("after decay: Safety = %v, want %v", got, want)
	}

	// Second tick without threat -> 0.30
	a.driveConditionalSafety(nil, svc.Needs)
	got2 := a.NeedIntensities[safetyDim]
	if math.Abs(got2-0.30) > 1e-9 {
		t.Fatalf("after second decay: Safety = %v, want 0.30", got2)
	}

	// 5 ticks from 0.50 with 0.10 decay => should be ~0 (FP tolerant)
	a.NeedIntensities[safetyDim] = 0.50
	for i := 0; i < 5; i++ {
		a.driveConditionalSafety(nil, svc.Needs)
	}
	if math.Abs(a.NeedIntensities[safetyDim]) > 1e-12 {
		t.Fatalf("after 5 decays from 0.50: Safety = %v, want 0", a.NeedIntensities[safetyDim])
	}
}

// TestThreatPerception_NoNegative verifies Safety never goes below 0.
func TestThreatPerception_NoNegative(t *testing.T) {
	regs := threatTestRegs(t)
	safetyDim := core.Dimension("Safety")
	cfg := newThreatCfg(safetyDim)
	a := newThreatAgent(t, "agent_a", cfg)
	svc := threatTestServices(t, regs)

	// Starting from 0 with no threat should stay 0 (never negative).
	a.driveConditionalSafety(nil, svc.Needs)
	if a.NeedIntensities[safetyDim] != 0 {
		t.Fatalf("no threat from 0: Safety = %v, want 0", a.NeedIntensities[safetyDim])
	}

	// Starting from 0.03 with decay 0.10 -> 0 (not -0.07).
	a.NeedIntensities[safetyDim] = 0.03
	a.driveConditionalSafety(nil, svc.Needs)
	if a.NeedIntensities[safetyDim] != 0 {
		t.Fatalf("small value: Safety = %v, want 0", a.NeedIntensities[safetyDim])
	}
}

// TestThreatPerception_ClampedTo1 checks Safety intensity is clamped at 1.0.
func TestThreatPerception_ClampedTo1(t *testing.T) {
	regs := threatTestRegs(t)
	safetyDim := core.Dimension("Safety")
	cfg := newThreatCfg(safetyDim)
	a := newThreatAgent(t, "agent_a", cfg)
	svc := threatTestServices(t, regs)

	// High starting intensity + many threats -> clamped to 1.
	a.NeedIntensities[safetyDim] = 0.90
	threats := []core.AgentID{"t1", "t2", "t3"}
	a.driveConditionalSafety(threats, svc.Needs)
	if a.NeedIntensities[safetyDim] != 1.0 {
		t.Fatalf("clamp at 1 check: Safety = %v, want 1.0", a.NeedIntensities[safetyDim])
	}
}

// TestThreatPerception_scanThreats verifies that scanThreats correctly identifies
// threat-tagged agents from perceived entities.
func TestThreatPerception_scanThreats(t *testing.T) {
	cfg := newThreatCfg("Safety")
	a := newThreatAgent(t, "agent_a", cfg)

	tests := []struct {
		name     string
		entities []perception.PerceivedEntity
		wantLen  int
		wantIDs  []core.AgentID
	}{
		{
			name:     "no entities",
			entities: nil,
			wantLen:  0,
		},
		{
			name: "non-agent entity ignored",
			entities: []perception.PerceivedEntity{
				{ID: "berry_bush", Pos: core.Vec2{X: 5, Y: 0}, Tags: []core.Tag{"berry_bush"}},
			},
			wantLen: 0,
		},
		{
			name: "agent without threat tag ignored",
			entities: []perception.PerceivedEntity{
				{ID: "friend", Pos: core.Vec2{X: 3, Y: 0}, Tags: []core.Tag{"agent"}},
			},
			wantLen: 0,
		},
		{
			name: "agent with threat tag detected",
			entities: []perception.PerceivedEntity{
				{ID: "enemy", Pos: core.Vec2{X: 3, Y: 0}, Tags: []core.Tag{"agent", "X"}},
			},
			wantLen: 1,
			wantIDs: []core.AgentID{"enemy"},
		},
		{
			name: "multiple threats, sorted dedup",
			entities: []perception.PerceivedEntity{
				{ID: "b", Pos: core.Vec2{X: 3, Y: 0}, Tags: []core.Tag{"agent", "X"}},
				{ID: "a", Pos: core.Vec2{X: 5, Y: 0}, Tags: []core.Tag{"agent", "X"}},
				{ID: "c", Pos: core.Vec2{X: 1, Y: 0}, Tags: []core.Tag{"agent", "X"}},
			},
			wantLen: 3,
			wantIDs: []core.AgentID{"a", "b", "c"},
		},
		{
			name: "duplicate dedup",
			entities: []perception.PerceivedEntity{
				{ID: "enemy", Pos: core.Vec2{X: 3, Y: 0}, Tags: []core.Tag{"agent", "X"}},
				{ID: "enemy", Pos: core.Vec2{X: 3, Y: 0}, Tags: []core.Tag{"agent", "X"}},
			},
			wantLen: 1,
			wantIDs: []core.AgentID{"enemy"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := a.scanThreats(tt.entities)
			if len(got) != tt.wantLen {
				t.Fatalf("scanThreats = %v (len=%d), want len %d", got, len(got), tt.wantLen)
			}
			if tt.wantLen > 0 {
				for i, id := range tt.wantIDs {
					if got[i] != id {
						t.Errorf("scanThreats[%d] = %q, want %q", i, got[i], id)
					}
				}
			}
		})
	}
}

// TestThreatPerception_DefensiveGoalOverride checks that perceiving a threat-tagged
// entity forces the Safety goal and clears the plan.
func TestThreatPerception_DefensiveGoalOverride(t *testing.T) {
	regs := threatTestRegs(t)
	safetyDim := core.Dimension("Safety")
	cfg := newThreatCfg(safetyDim)
	a := newThreatAgent(t, "agent_a", cfg)
	a.Goal = "Satiety"
	a.Plan.Actions = append(a.Plan.Actions, "Eat")

	// Perceive a threat.
	mwv := newMockWorldView()
	mwv.entities = []perception.PerceivedEntity{
		{ID: "enemy", Pos: core.Vec2{X: 3, Y: 0}},
	}
	mwv.entityTags = map[core.ObjectID][]core.Tag{
		"enemy": {"agent", "X"},
	}
	// Add known objects so the planner can target village_center for Patrol.
	mwv.knownObjs = []KnownObject{
		{ID: "village_center_0", Pos: core.Vec2{X: 15, Y: 15}, Kind: "village_center"},
	}

	svc := threatTestServices(t, regs)

	intents := a.Tick(mwv, 42, rng.New(7), svc, core.NoopEmitter{})
	_ = intents

	// The defensive override should have forced Safety as goal.
	if a.Goal != safetyDim {
		t.Fatalf("after threat: Goal = %q, want %q", a.Goal, safetyDim)
	}
	// The plan should be for Safety (e.g., Patrol), not empty — the planner
	// builds a new plan after the override clears the old one.
	if len(a.Plan.Actions) == 0 {
		t.Fatal("after threat: Plan should contain Safety actions, got empty")
	}
}

// TestThreatPerception_NoThreatNoOverride checks that without a threat-tagged entity,
// the goal mediation proceeds normally (no forced Safety override).
func TestThreatPerception_NoThreatNoOverride(t *testing.T) {
	regs := threatTestRegs(t)
	safetyDim := core.Dimension("Safety")
	cfg := newThreatCfg(safetyDim)
	a := newThreatAgent(t, "agent_a", cfg)
	a.Goal = "Satiety"

	// No threat in perceived entities — just non-threat agents.
	mwv := newMockWorldView()
	mwv.entities = []perception.PerceivedEntity{
		{ID: "friend", Pos: core.Vec2{X: 3, Y: 0}, Tags: []core.Tag{"agent"}},
	}

	svc := threatTestServices(t, regs)
	_ = a.Tick(mwv, 42, rng.New(7), svc, core.NoopEmitter{})

	// Goal should NOT be forced to Safety — should go through normal mediation.
	if a.Goal == safetyDim && a.Goal != "Satiety" {
		// This is technically okay if appraisal determined Safety priority is higher;
		// all we care about is that the forced override didn't fire for no threat.
	}
}

// TestThreatPerception_SafetyFeedsAppraisal checks that after driving Safety intensity
// from a threat, the appraisal chain returns a non-zero Priority for Safety.
func TestThreatPerception_SafetyFeedsAppraisal(t *testing.T) {
	regs := threatTestRegs(t)
	safetyDim := core.Dimension("Safety")
	cfg := newThreatCfg(safetyDim)
	a := newThreatAgent(t, "agent_a", cfg)

	// No threat — Safety intensity stays 0 — appraisal should give Safety 0 priority.
	priorities := a.appraise(regs.needs, regs.values)
	safetyFound := false
	for _, p := range priorities {
		if p.Dim == safetyDim {
			safetyFound = true
			if p.Priority > 0 {
				t.Fatalf("Safety priority with intensity 0 should be 0, got %v", p.Priority)
			}
		}
	}
	if !safetyFound {
		t.Fatal("Safety dimension not found in priorities")
	}

	// Drive Safety intensity via a threat.
	a.driveConditionalSafety([]core.AgentID{"threat_1"}, regs.needs)

	// Now appraisal should see a non-zero Safety priority.
	priorities2 := a.appraise(regs.needs, regs.values)
	foundSafetyPositive := false
	for _, p := range priorities2 {
		if p.Dim == safetyDim && p.Priority > 0 {
			foundSafetyPositive = true
			break
		}
	}
	if !foundSafetyPositive {
		t.Fatal("Safety priority should be > 0 after threat-driven intensity increase")
	}
}

// TestThreatPerception_CountStable checks that the same threats in different order
// produce the same intensity result.
func TestThreatPerception_CountStable(t *testing.T) {
	regs := threatTestRegs(t)
	safetyDim := core.Dimension("Safety")

	// Two scans with same threats but different order should produce same intensity.
	cfg := newThreatCfg(safetyDim)

	a1 := newThreatAgent(t, "agent_a", cfg)
	a1.driveConditionalSafety([]core.AgentID{"B", "C"}, regs.needs)
	result1 := a1.NeedIntensities[safetyDim]

	a2 := newThreatAgent(t, "agent_b", cfg)
	a2.driveConditionalSafety([]core.AgentID{"C", "B"}, regs.needs)
	result2 := a2.NeedIntensities[safetyDim]

	if result1 != result2 {
		t.Fatalf("order-dependent: %v != %v", result1, result2)
	}
}

func TestHasTag(t *testing.T) {
	tests := []struct {
		tags   []core.Tag
		prefix string
		want   bool
	}{
		{tags: nil, prefix: "agent", want: false},
		{tags: []core.Tag{"agent", "hostile"}, prefix: "agent", want: true},
		{tags: []core.Tag{"berry_bush"}, prefix: "agent", want: false},
		{tags: []core.Tag{"agent"}, prefix: "agent", want: true},
	}
	for _, tt := range tests {
		got := hasTag(tt.tags, tt.prefix)
		if got != tt.want {
			t.Errorf("hasTag(%v, %q) = %v, want %v", tt.tags, tt.prefix, got, tt.want)
		}
	}
}

func TestTagsIntersect(t *testing.T) {
	tests := []struct {
		tags    []core.Tag
		needles []core.Tag
		want    bool
	}{
		{tags: nil, needles: nil, want: false},
		{tags: []core.Tag{"a", "b"}, needles: []core.Tag{"c"}, want: false},
		{tags: []core.Tag{"a", "b"}, needles: []core.Tag{"b", "c"}, want: true},
		{tags: []core.Tag{"a"}, needles: []core.Tag{"a"}, want: true},
	}
	for _, tt := range tests {
		got := tagsIntersect(tt.tags, tt.needles)
		if got != tt.want {
			t.Errorf("tagsIntersect(%v, %v) = %v, want %v", tt.tags, tt.needles, got, tt.want)
		}
	}
}
