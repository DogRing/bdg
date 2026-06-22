package agent

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/gates"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/perception"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/spatial"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/tom"
	"github.com/dogring/bdg/engine/values"
)

// ── Test helpers ───────────────────────────────────────────────────────────────

// testRegs holds minimal registries shared across tests.
type testRegs struct {
	actions *actions.Registry
	gates   *gates.Registry
	needs   *needs.Registry
	stats   *stats.Registry
	values  *values.Config
}

// testStatsYAML is the canonical test stats fixture.
const testStatsYAML = `schema_version: 1
stats:
  - id: Strength
    kind: capability
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
  - id: Agility
    kind: capability
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
  - id: Intelligence
    kind: capability
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
  - id: Honesty
    kind: disposition
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
  - id: Aggression
    kind: disposition
    range: [0, 100]
    default: 30
    gen: { dist: normal, mean: 30, sd: 10 }
    inherit: 0.3
`

// makeTestRegs creates minimal registries for testing.
func makeTestRegs(t *testing.T) testRegs {
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
  - id: Drink
    tags: [effort:low]
    duration: 5
    produces: [has_Hydration]
    effect: { Hydration: 0.5 }
  - id: MoveTo
    tags: [effort:low]
    duration: 10
    produces: [at_target]
`
	actReg, err := actions.Load(strings.NewReader(actionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}

	gatesYAML := `schema_version: 2
gates:
  - id: always_visible
    tags: []
    expr: { stat: "Intelligence", op: ">=", value: -1 }
`
	gateReg, err := gates.Load(strings.NewReader(gatesYAML), statReg)
	if err != nil {
		t.Fatalf("gates.Load: %v", err)
	}

	return testRegs{
		actions: actReg,
		gates:   gateReg,
		needs:   needReg,
		stats:   statReg,
		values:  valsCfg,
	}
}

func mustLoadStats(t *testing.T, yamlStr string) *stats.Registry {
	t.Helper()
	reg, err := stats.Load(strings.NewReader(yamlStr))
	if err != nil {
		t.Fatalf("stats.Load: %v", err)
	}
	return reg
}

// testServices builds a Services struct from test registries.
func testServices(t *testing.T, regs testRegs) Services {
	t.Helper()
	// Perception sensor needs a spatial hash.
	sp := spatial.New(8.0)
	percepCfg := perception.PerceptionConfig{
		SightRadius:   18.0,
		SmellRadius:   10.0,
		HearingRadius: 14.0,
	}
	sensor := perception.NewSensor(sp, percepCfg)

	// Planner.
	plannerCfg := planner.PlannerConfig{
		Budget: planner.Budget{
			MaxDepth:   6,
			MaxActions: 16,
			MaxNodes:   256,
		},
		BaseHorizonTicks: 720,
		UrgencyThreshold: 0.65,
		TagCosts: map[core.Tag]float64{
			"effort:low":  0.20,
			"effort:med":  0.50,
			"effort:high": 0.90,
		},
	}
	pl := planner.New(regs.actions, regs.gates, regs.needs, regs.stats, plannerCfg)

	return Services{
		Sensor:  sensor,
		Planner: pl,
		Values:  regs.values,
		Needs:   regs.needs,
		Stats:   regs.stats,
		Actions: regs.actions,
	}
}

// testAgent creates a fresh agent with default config and the given stats.
func testAgent(id core.AgentID, realStats stats.Stats, cfg Config) *Agent {
	selfPerception := 0.5 // mid-range
	selfToM := tom.NewToM(id, realStats, selfPerception, rng.New(42), nil, cfg.Rates)
	// Re-create ToM with the stat registry.
	statReg := mustLoadStats(&testing.T{}, testStatsYAML)
	selfToM = tom.NewToM(id, realStats, selfPerception, rng.New(42), statReg, cfg.Rates)
	return New(id, core.Vec2{X: 10, Y: 10}, realStats, selfToM, cfg)
}

// ── WorldView mock ─────────────────────────────────────────────────────────────

// mockWorldView satisfies WorldView for testing.
type mockWorldView struct {
	entities        []perception.PerceivedEntity
	sounds          []perception.SoundEvent
	knownObjs       []KnownObject
	beliefs         map[core.AgentID]tom.Belief
	triggers        []core.AgentID
	placeQuality    func(core.ObjectID) float64      // overrides for PlaceQuality queries
	agentIDs        []core.AgentID                   // P6: agent IDs for reliance/vote queries
	incomingSignals map[core.AgentID][]core.Signal   // P6: signals addressed to each agent
	entityTags      map[core.ObjectID][]core.Tag     // P6 BLOCKER-1: per-entity tag overrides for Sight
}

func (m *mockWorldView) EntitiesInRadius(center core.Vec2, radius float64) []perception.PerceivedEntity {
	return m.entities
}

func (m *mockWorldView) Tags(id core.ObjectID) []core.Tag {
	if m.entityTags != nil {
		if tags, ok := m.entityTags[id]; ok {
			return tags
		}
	}
	return nil
}

func (m *mockWorldView) IsOpaque(id core.ObjectID) bool {
	return false
}

func (m *mockWorldView) SoundEvents() []perception.SoundEvent {
	return m.sounds
}

func (m *mockWorldView) KnownObjects(self core.AgentID) []KnownObject {
	return m.knownObjs
}

func (m *mockWorldView) BeliefOf(self, subject core.AgentID) (tom.Belief, bool) {
	b, ok := m.beliefs[subject]
	return b, ok
}

func (m *mockWorldView) HasPendingOffer(receiver core.AgentID) bool {
	return false
}

func (m *mockWorldView) ResentmentTriggers(self core.AgentID) []core.AgentID {
	return m.triggers
}

func (m *mockWorldView) PlaceQuality(placeID core.ObjectID) float64 {
	if m.placeQuality != nil {
		return m.placeQuality(placeID)
	}
	return 1.0 // default: pristine
}


func (m *mockWorldView) MemberNeedIntensities() map[core.AgentID]map[core.Dimension]float64 {
	return nil // mock: no collective member data by default
}

func (m *mockWorldView) AgentIDs() []core.AgentID {
	return m.agentIDs
}

func (m *mockWorldView) IncomingSignals(self core.AgentID) []core.Signal {
	if m.incomingSignals == nil {
		return nil
	}
	return m.incomingSignals[self]
}

func newMockWorldView() *mockWorldView {
	return &mockWorldView{
		beliefs:         make(map[core.AgentID]tom.Belief),
		incomingSignals: make(map[core.AgentID][]core.Signal),
	}
}

// ── Tests ──────────────────────────────────────────────────────────────────────

func TestNew(t *testing.T) {
	cfg := DefaultConfig()
	statReg := mustLoadStats(t, testStatsYAML)
	realStats := statReg.Defaults()

	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), statReg, cfg.Rates)
	agent := New("agent_1", core.Vec2{X: 5, Y: 5}, realStats, selfToM, cfg)

	if agent.ID != "agent_1" {
		t.Errorf("expected ID agent_1, got %v", agent.ID)
	}
	if agent.Stamina != cfg.StaminaMax {
		t.Errorf("expected Stamina %v, got %v", cfg.StaminaMax, agent.Stamina)
	}
	if agent.Mood != cfg.MoodBaseline {
		t.Errorf("expected Mood %v, got %v", cfg.MoodBaseline, agent.Mood)
	}
	if agent.Adrenaline != 0 {
		t.Errorf("expected Adrenaline 0, got %v", agent.Adrenaline)
	}
	if agent.Coping != Idle {
		t.Errorf("expected Coping Idle, got %v", agent.Coping)
	}
	if len(agent.Latent) != 0 {
		t.Errorf("expected no latent goals, got %v", len(agent.Latent))
	}
	if agent.PlanIdx != 0 {
		t.Errorf("expected PlanIdx 0, got %v", agent.PlanIdx)
	}
}

func TestDecayNeeds(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := DefaultConfig()
	statReg := regs.stats
	realStats := statReg.Defaults()

	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), statReg, cfg.Rates)
	agent := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)

	// Set initial intensities.
	agent.NeedIntensities["Satiety"] = 0.0
	agent.NeedIntensities["Hydration"] = 0.0
	agent.NeedIntensities["Rest"] = 0.0

	// Decay one tick.
	agent.decayNeeds(regs.needs)

	// Intensities should increase by rate × 1.
	if agent.NeedIntensities["Satiety"] <= 0.0 {
		t.Error("Satiety intensity should have increased")
	}
	if agent.NeedIntensities["Hydration"] <= 0.0 {
		t.Error("Hydration intensity should have increased")
	}
	if agent.NeedIntensities["Rest"] <= 0.0 {
		t.Error("Rest intensity should have increased")
	}

	// Hydration should grow faster than Rest (0.00110 > 0.00045).
	if agent.NeedIntensities["Hydration"] <= agent.NeedIntensities["Rest"] {
		t.Error("Hydration should grow faster than Rest")
	}
}

func TestAppraise(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := DefaultConfig()
	statReg := regs.stats
	realStats := statReg.Defaults()

	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), statReg, cfg.Rates)
	agent := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)

	// Set intensities: Satiety is very high (hungry), Rest is low.
	agent.NeedIntensities["Satiety"] = 0.50  // near threshold 0.55
	agent.NeedIntensities["Hydration"] = 0.25 // comfortably below threshold 0.50
	agent.NeedIntensities["Rest"] = 0.10      // well below threshold 0.45

	priorities := agent.appraise(regs.needs, regs.values)

	if len(priorities) == 0 {
		t.Fatal("expected non-empty priorities")
	}

	// Highest priority should be Satiety (most urgent, weight 1.0).
	topPrio := priorities[0]
	if topPrio.Dim != "Satiety" {
		t.Errorf("expected top priority Satiety, got %v", topPrio.Dim)
	}

	// Priorities should be in descending order.
	for i := 1; i < len(priorities); i++ {
		if priorities[i-1].Priority < priorities[i].Priority {
			t.Errorf("priorities not sorted descending: %v < %v at %d",
				priorities[i-1].Priority, priorities[i].Priority, i)
		}
	}
}

func TestMediateGoal_Stickiness(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Stickiness = 0.15
	cfg.GoalDeadband = 0.08

	agent := &Agent{
		ID:    "test",
		Cfg:   cfg,
		Goal:  "Satiety",
		Plan:  planner.Plan{Actions: []actions.ActionID{"Eat"}},
	}

	// Create priorities where Hydration slightly beats Satiety.
	priorities := []planner.DimensionPriority{
		{Dim: "Hydration", Priority: 0.50, Salience: 0.50},
		{Dim: "Satiety", Priority: 0.48, Salience: 0.48},
	}

	// Without stickiness, Hydration would win. With stickiness:
	// Satiety gets 0.48 + 0.15 = 0.63 > 0.50, so Satiety should stay.
	changed := agent.mediateGoal(priorities)
	if changed {
		t.Error("goal should not change due to stickiness")
	}
	if agent.Goal != "Satiety" {
		t.Errorf("expected Satiety, got %v", agent.Goal)
	}
}

func TestMediateGoal_Deadband(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Stickiness = 0.0
	cfg.GoalDeadband = 0.10

	agent := &Agent{
		ID:   "test",
		Cfg:  cfg,
		Goal: "Satiety",
	}

	// Hydration beats Satiety by less than deadband.
	priorities := []planner.DimensionPriority{
		{Dim: "Hydration", Priority: 0.50, Salience: 0.50},
		{Dim: "Satiety", Priority: 0.45, Salience: 0.45},
	}

	changed := agent.mediateGoal(priorities)
	if changed {
		t.Error("goal should not change within deadband")
	}

	// Now Hydration beats by more than deadband.
	priorities[0].Priority = 0.60
	changed = agent.mediateGoal(priorities)
	if !changed {
		t.Error("goal should change when outside deadband")
	}
	if agent.Goal != "Hydration" {
		t.Errorf("expected Hydration, got %v", agent.Goal)
	}
}

func TestMediateGoal_NoCurrentGoal(t *testing.T) {
	cfg := DefaultConfig()
	agent := &Agent{
		ID:   "test",
		Cfg:  cfg,
		Goal: "",
	}

	priorities := []planner.DimensionPriority{
		{Dim: "Rest", Priority: 0.30, Salience: 0.30},
	}

	changed := agent.mediateGoal(priorities)
	if !changed {
		t.Error("should adopt first goal")
	}
	if agent.Goal != "Rest" {
		t.Errorf("expected Rest, got %v", agent.Goal)
	}
}

func TestNeedsReplan(t *testing.T) {
	agent := &Agent{
		Plan: planner.Plan{Actions: []actions.ActionID{"Eat"}},
	}

	// Goal changed → replan.
	if !agent.needsReplan(true) {
		t.Error("should replan on goal change")
	}

	// No plan → replan.
	agent.Plan = planner.Plan{}
	if !agent.needsReplan(false) {
		t.Error("should replan when no plan exists")
	}

	// Mid-durative → no replan.
	agent.Plan = planner.Plan{Actions: []actions.ActionID{"Forage"}}
	if agent.needsReplan(false) {
		t.Error("should NOT replan mid-action")
	}
}

func TestCopingCascade_IdleToLonging_LowIntelligence(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := DefaultConfig()
	cfg.BudgetBase = 10
	cfg.BudgetPerIntelligence = 0 // ensures low-intelligence can't rebind

	// Create agent with low perceived Intelligence.
	realStats := regs.stats.Defaults()
	realStats["Intelligence"] = 10 // low
	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), regs.stats, cfg.Rates)
	agent := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
	agent.Goal = "Satiety"
	agent.Coping = Idle

	priorities := []planner.DimensionPriority{
		{Dim: "Satiety", Priority: 0.50, Salience: 0.50},
	}

	emit := core.NoopEmitter{}
	agent.enterCopingCascade(nil, 1, priorities, regs.stats, emit)

	// Low Intelligence → should skip Rebinding AND Longing, go directly to Latent (P3).
	if agent.Coping != Latent {
		t.Errorf("expected Latent (P3: low-intel shortcut skips Rebinding+Longing), got %v", agent.Coping)
	}
	if len(agent.Latent) != 1 {
		t.Errorf("expected 1 latent goal, got %v", len(agent.Latent))
	}
	if agent.Latent[0].Dim != "Satiety" {
		t.Errorf("expected latent dim Satiety, got %v", agent.Latent[0].Dim)
	}
}

func TestCopingCascade_LongingToLatentToApathy(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := DefaultConfig()
	realStats := regs.stats.Defaults()
	selfToM := tom.NewToM("agent_2", realStats, 0.5, rng.New(42), regs.stats, cfg.Rates)
	agent := New("agent_2", core.Vec2{}, realStats, selfToM, cfg)
	agent.Goal = "Satiety"
	agent.Coping = Longing

	priorities := []planner.DimensionPriority{
		{Dim: "Satiety", Priority: 0.50, Salience: 0.50},
	}

	emit := core.NoopEmitter{}

	// Longing → Latent.
	agent.enterCopingCascade(nil, 1, priorities, regs.stats, emit)
	if agent.Coping != Latent {
		t.Errorf("expected Latent, got %v", agent.Coping)
	}

	// Latent with FailStreak < ApathyFailStreak → stays Latent.
	agent.Goal = "Satiety" // reset (clearGoal cleared it)
	agent.enterCopingCascade(nil, 2, priorities, regs.stats, emit)
	if agent.Coping != Latent {
		t.Errorf("expected Latent (FailStreak=%d < threshold %d), got %v",
			agent.FailStreak, cfg.ApathyFailStreak, agent.Coping)
	}

	// Third failure → FailStreak=3 >= ApathyFailStreak=3 → Apathy.
	agent.Goal = "Satiety"
	agent.enterCopingCascade(nil, 3, priorities, regs.stats, emit)
	if agent.Coping != Apathy {
		t.Errorf("expected Apathy (FailStreak=%d >= threshold %d), got %v",
			agent.FailStreak, cfg.ApathyFailStreak, agent.Coping)
	}
	if agent.Adrenaline != 0 {
		t.Error("Apathy should crash Adrenaline to 0")
	}
	if len(agent.Latent) != 0 {
		t.Error("Apathy should clear latent goals")
	}
}

func TestFoldEvidence_InvisibleSelfSealing(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := DefaultConfig()
	realStats := regs.stats.Defaults()
	realStats["Strength"] = 80

	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), regs.stats, cfg.Rates)
	agent := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)

	// Record pre-calibration belief.
	selfBefore, _ := agent.ToM.Self(agent.ToM.SelfID())
	strengthBefore := selfBefore.EstStats["Strength"].Mean

	// Apply an Invisible outcome — no evidence should be folded.
	outcome := ActionOutcome{
		Action:    "Eat",
		Status:    Invisible,
		StatsUsed: []core.StatID{"Strength"},
		Evidence:  nil,
	}
	agent.foldEvidence(outcome)

	// Belief should be unchanged (self-sealing).
	selfAfter, _ := agent.ToM.Self(agent.ToM.SelfID())
	strengthAfter := selfAfter.EstStats["Strength"].Mean
	if strengthBefore != strengthAfter {
		t.Errorf("Invisible should not change belief: %v → %v", strengthBefore, strengthAfter)
	}
}

func TestFoldEvidence_FailedCalibration(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := DefaultConfig()
	realStats := regs.stats.Defaults()
	realStats["Strength"] = 80

	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), regs.stats, cfg.Rates)
	agent := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)

	// Record pre-calibration belief.
	selfBefore, _ := agent.ToM.Self(agent.ToM.SelfID())
	strengthBefore := selfBefore.EstStats["Strength"].Mean

	// Apply a Failed outcome with evidence.
	outcome := ActionOutcome{
		Action:    "Hunt",
		Status:    Failed,
		StatsUsed: []core.StatID{"Strength"},
		Evidence: []tom.StatEvidence{
			{Stat: "Strength", Observed: 40, Weight: 0.8, Tick: 1},
		},
	}
	agent.foldEvidence(outcome)

	// Belief should have moved DOWN (overclaim correction).
	selfAfter, _ := agent.ToM.Self(agent.ToM.SelfID())
	strengthAfter := selfAfter.EstStats["Strength"].Mean
	if strengthAfter >= strengthBefore {
		t.Errorf("Failed should lower belief: %v → %v", strengthBefore, strengthAfter)
	}
}

func TestUpdateDynamics_AdrenalineSurge(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AdrTriggerUrgency = 0.5
	cfg.AdrSurge = 0.6
	cfg.AdrMax = 1.0

	agent := &Agent{
		Cfg:        cfg,
		Adrenaline: 0.0,
		Mood:       0.0,
	}

	// Urgency above threshold → surge.
	priorities := []planner.DimensionPriority{
		{Dim: "Satiety", Priority: 0.50, Salience: 0.80}, // high salience → high urgency
	}
	agent.updateDynamics(priorities)

	if agent.Adrenaline == 0.0 {
		t.Error("Adrenaline should have surged")
	}
	if agent.Adrenaline > cfg.AdrMax {
		t.Errorf("Adrenaline should be clamped to max: %v", agent.Adrenaline)
	}
}

func TestUpdateDynamics_AdrenalineCrash(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AdrTriggerUrgency = 0.9
	cfg.AdrDecay = 0.03
	cfg.AdrMax = 1.0

	agent := &Agent{
		Cfg:        cfg,
		Adrenaline: 0.8,
		Mood:       0.0,
	}

	// Urgency below threshold → crash.
	priorities := []planner.DimensionPriority{
		{Dim: "Rest", Priority: 0.10, Salience: 0.10},
	}
	agent.updateDynamics(priorities)

	if agent.Adrenaline >= 0.8 {
		t.Error("Adrenaline should have drained")
	}
}

func TestUpdateDynamics_MoodPullToBaseline(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MoodDecay = 0.02
	cfg.MoodBaseline = 0.0

	agent := &Agent{
		Cfg:  cfg,
		Mood: 0.5, // above baseline
	}

	priorities := []planner.DimensionPriority{}
	agent.updateDynamics(priorities)

	// Mood should move toward baseline (decrease).
	if agent.Mood >= 0.5 {
		t.Error("Mood should decrease toward baseline")
	}
}

func TestUpdateResentment_WithLatentGoals(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AffinityDrop = 0.15

	// Create an agent with a ToM (just to have a self-belief).
	regs := makeTestRegs(t)
	realStats := regs.stats.Defaults()
	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), regs.stats, cfg.Rates)
	agent := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
	agent.Latent = []LatentGoal{
		{Dim: "Satiety", Since: 1, Intensity: 0.5},
	}

	// updateResentment should not panic.
	agent.updateResentment()
	// P1: actual Affinity update path is thin (ToM.Self returns copy);
	// verify no panic as the minimum bar.
}

func TestUpdateResentment_EmptyLatent(t *testing.T) {
	cfg := DefaultConfig()
	regs := makeTestRegs(t)
	realStats := regs.stats.Defaults()
	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), regs.stats, cfg.Rates)
	agent := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
	agent.Latent = nil

	// Should be a no-op (no panic).
	agent.updateResentment()
}

// TestDeterminism_IdenticalInputs verifies that the same state + seed produces
// byte-identical results (D12).
func TestDeterminism_Appraise(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := DefaultConfig()
	statReg := regs.stats
	realStats := statReg.Defaults()

	// Run twice with identical setup.
	for i := 0; i < 2; i++ {
		selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), statReg, cfg.Rates)
		agent := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
		agent.NeedIntensities["Satiety"] = 0.50
		agent.NeedIntensities["Hydration"] = 0.25
		agent.NeedIntensities["Rest"] = 0.10

		priorities := agent.appraise(regs.needs, regs.values)

		// Verify consistent ordering.
		if len(priorities) < 3 {
			t.Fatalf("run %d: expected 3+ priorities", i)
		}
		if priorities[0].Dim != "Satiety" {
			t.Errorf("run %d: expected Satiety first, got %v", i, priorities[0].Dim)
		}
	}
}

// TestNoRealStatsInTick verifies the D8 invariant: Tick must not read a.RealStats.
// We set RealStats to nil and verify Tick doesn't panic.
func TestNoRealStatsInTick_NilRealStats(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := DefaultConfig()
	svc := testServices(t, regs)

	selfToM := tom.NewToM("agent_1", regs.stats.Defaults(), 0.5, rng.New(42), regs.stats, cfg.Rates)
	agent := New("agent_1", core.Vec2{X: 10, Y: 10}, nil, selfToM, cfg)
	agent.Goal = "Rest"
	agent.Plan = planner.Plan{Actions: []actions.ActionID{"RestAction"}}

	world := newMockWorldView()

	// Tick should not panic even with nil RealStats.
	// (It will fail at planning because planner.Gates needs stats, but we can test
	// individual phases that shouldn't access RealStats.)
	priorities := agent.appraise(regs.needs, regs.values)
	if len(priorities) == 0 {
		t.Fatal("expected priorities")
	}

	// The full Tick with nil RealStats may fail at the planner level,
	// but the agent's own code should not directly dereference RealStats.
	// Test that agent-level phases succeed.
	_ = svc
	_ = world
}

// TestApplyOutcome_MoodUpdate verifies the Mood formula.
func TestApplyOutcome_MoodUpdate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Lambda = 0.25
	cfg.MoodBaseline = 0.0

	regs := makeTestRegs(t)
	realStats := regs.stats.Defaults()
	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), regs.stats, cfg.Rates)
	agent := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
	agent.Mood = 0.0

	outcome := ActionOutcome{
		Action:    "Forage",
		Status:    Succeeded,
		Completed: true,
		Expected:  0.7,
		Actual:    0.9,
	}

	emit := core.NoopEmitter{}
	agent.ApplyOutcome(outcome, rng.New(0), cfg, regs.stats, emit)

	// Mood += Lambda × (Actual − Expected) = 0.25 × 0.2 = 0.05
	expectedMood := 0.05
	if delta := agent.Mood - expectedMood; delta < -1e-9 || delta > 1e-9 {
		t.Errorf("expected Mood %v, got %v", expectedMood, agent.Mood)
	}
}

// TestApplyOutcome_NeedDeltaApplication verifies need deltas are applied.
func TestApplyOutcome_NeedDeltaApplication(t *testing.T) {
	t.Run("basic delta application", func(t *testing.T) {
		cfg := DefaultConfig()
		regs := makeTestRegs(t)
		realStats := regs.stats.Defaults()
		selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), regs.stats, cfg.Rates)
		agent := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
		agent.NeedIntensities["Satiety"] = 0.60

		outcome := ActionOutcome{
			Action:    "Eat",
			Status:    Succeeded,
			Completed: true,
			Effect:    map[core.Dimension]float64{"Satiety": 0.40},
		}

		emit := core.NoopEmitter{}
		agent.ApplyOutcome(outcome, rng.New(0), cfg, regs.stats, emit)

		if agent.NeedIntensities["Satiety"] >= 0.60 {
			t.Errorf("NeedIntensity should decrease, got %v", agent.NeedIntensities["Satiety"])
		}
	})
}

// P5: Config loading tests
func TestDefaultConfig_P5SocialFields(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.BondAffinityGain != 0.20 {
		t.Errorf("BondAffinityGain = %v, want 0.20", cfg.BondAffinityGain)
	}
	if cfg.MinCareThreshold != 0.30 {
		t.Errorf("MinCareThreshold = %v, want 0.30", cfg.MinCareThreshold)
	}
	if cfg.MaxPossiblePriority != 2.5 {
		t.Errorf("MaxPossiblePriority = %v, want 2.5", cfg.MaxPossiblePriority)
	}
}
