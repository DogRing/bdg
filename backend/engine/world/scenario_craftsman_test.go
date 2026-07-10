package world

// Scenario H: 소심한 장인 (The Timid Craftsman)
//
// An agent with world-class Intelligence (actual=90) but chronically low
// self-perception (ToM[self][Intelligence]=20). This creates a self-sealing
// trap: they plan Craft_Basic (effort:med, cost=0.50) instead of Craft_Advanced
// (effort:low, cost=0.20), working harder for lesser outcomes.
//
// Observation points:
//  1. Low self-estimate (Intel=20) fails the Craft_Advanced gate (requires ≥70) →
//     craftsman uses the expensive Craft_Basic plan, underperforming their ability.
//  2. Calibrated self-estimate (Intel=85) passes the gate → Craft_Advanced (cheaper)
//     is chosen: the craftsman works smarter and produces more value.
//  3. Information asymmetry (exploitation gap): the exploiter observes the craftsman
//     in action and discovers actual Intelligence=90, while the craftsman's own
//     self-estimate stays at 20. The exploiter knows the craftsman is undervaluing
//     themselves — exactly the condition that makes sustained exploitation possible.
//  4. Determinism (D12).
//
// Technical rationale:
//   - Craft_Basic: produces has_Satiety, effort:med (cost=0.50), no gate.
//   - Craft_Advanced: produces has_Satiety, effort:low (cost=0.20), uses:Intelligence
//     → master_craftsman gate requires Intelligence ≥ 70.
//   - When gate passes: planner picks Craft_Advanced (0.20 < 0.50) ✓
//   - When gate fails: only Craft_Basic available (0.50) ✓
//   - D8: self-perception underestimation is self-sealing — craftsman never plans
//     actions that would provide intelligence-calibration evidence.

import (
	"slices"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/kernel/worldtime"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/gates"
	"github.com/dogring/bdg/engine/mind/needs"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/values"
	"github.com/dogring/bdg/engine/space/spatial"
)

// ── Craftsman fixture YAML ────────────────────────────────────────────────────

// Craft_Basic: brute-force approach — always available, moderate cost.
// Craft_Advanced: intelligent approach — cheaper but requires Intelligence ≥ 70.
// The planner naturally picks Craft_Advanced when the gate passes (lower cost).
const craftsmanActionsYAML = `schema_version: 1
actions:
  - id: Craft_Basic
    tags: [ "effort:med" ]
    produces: [ has_Satiety ]
    effect: { Satiety: 0.50 }
    duration: 12
  - id: Craft_Advanced
    tags: [ "effort:low", "uses:Intelligence" ]
    produces: [ has_Satiety ]
    effect: { Satiety: 0.70 }
    duration: 10
  - id: MoveTo
    tags: [ "effort:low", "uses:Agility" ]
    produces: [ at_target ]
    duration: 5
`

const craftsmanNeedsYAML = `schema_version: 1
needs:
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`

const craftsmanBalanceYAML = `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
values:
  weights:
    Satiety: 1.00
`

// Craft_Advanced is gated on Intelligence ≥ 70. Craftsman with self-estimate=20
// cannot reach this gate; craftsman with self-estimate=85 passes.
const craftsmanGatesYAML = `schema_version: 3
gates:
  - id: master_craftsman
    tags: [ "uses:Intelligence" ]
    expr: { stat: Intelligence, op: ">=", value: 70 }
`

// ── Fixture constructor ───────────────────────────────────────────────────────

func newCraftsmanFixture(t *testing.T, seed int64) *testFixture {
	t.Helper()

	statReg, err := stats.Load(strings.NewReader(testStatsYAML))
	if err != nil {
		t.Fatalf("stats.Load: %v", err)
	}
	needReg, err := needs.Load(
		strings.NewReader(craftsmanNeedsYAML),
		strings.NewReader(craftsmanBalanceYAML),
	)
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}
	valsCfg, err := values.Load(strings.NewReader(craftsmanBalanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}
	actReg, err := actions.Load(strings.NewReader(craftsmanActionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}
	gateReg, err := gates.Load(strings.NewReader(craftsmanGatesYAML), statReg)
	if err != nil {
		t.Fatalf("gates.Load: %v", err)
	}

	plannerCfg := planner.PlannerConfig{
		Budget:             planner.Budget{MaxDepth: 6, MaxActions: 16, MaxNodes: 256},
		BaseHorizonTicks:   720,
		UrgencyThreshold:   0.65,
		LookaheadThreshold: 0.4,
		TagCosts: map[core.Tag]float64{
			"effort:low":  0.20,
			"effort:med":  0.50,
			"effort:high": 0.80,
			"effort:none": 0.0,
		},
	}
	pl := planner.New(actReg, gateReg, needReg, statReg, plannerCfg)

	sp := spatial.New(8.0)
	sensor := perception.NewSensor(sp, perception.PerceptionConfig{
		SightRadius: 18.0, SmellRadius: 10.0, HearingRadius: 14.0,
	})

	svc := Services{
		Sensor:  sensor,
		Planner: pl,
		Values:  valsCfg,
		Needs:   needReg,
		Stats:   statReg,
		Actions: actReg,
	}

	rootRNG := rng.New(seed)
	clock, _ := worldtime.NewClock(worldtime.DefaultConfig())
	emit := &recordingEmitter{}
	w := New(DefaultConfig(), clock, rootRNG, svc, actReg, emit)

	return &testFixture{
		world:   w,
		regs:    testRegs{stats: statReg, needs: needReg, values: valsCfg, actions: actReg, gates: gateReg},
		rootRNG: rootRNG,
		clock:   clock,
		svc:     svc,
		actReg:  actReg,
		emit:    emit,
	}
}

// ── Test 1: Low self-estimate blocks advanced craft ───────────────────────────

// TestCraftsman_LowSelfEstimate_ForcesBasicCraft verifies that a craftsman with
// actual Intelligence=90 but ToM[self][Intelligence]=20 cannot plan Craft_Advanced
// (gate requires ≥70) and falls back to the expensive Craft_Basic (cost=0.50).
//
// This models D8's self-sealing trap: the craftsman never exercises their talent,
// so they never receive evidence that would calibrate their self-estimate upward.
func TestCraftsman_LowSelfEstimate_ForcesBasicCraft(t *testing.T) {
	const seed = int64(5001)

	run := func() (*World, *agent.Agent) {
		fx := newCraftsmanFixture(t, seed)
		cfg := agent.DefaultConfig()

		// Actual Intelligence=90 but self-perceived as only 20 (D8: underestimation).
		craftsman := fx.world.Spawn("craftsman", core.Vec2{X: 0, Y: 0}, cfg, rng.New(10))
		seedToM(craftsman, "Intelligence", 20) // ToM[self][Intel]=20 < gate threshold 70

		// Satiety critical → Satiety is the goal → planner evaluates Craft actions.
		craftsman.NeedIntensities["Satiety"] = 0.50

		for range 5 {
			fx.world.Tick()
		}
		return fx.world, craftsman
	}

	worldA, craftsmanA := run()
	worldB, _ := run()

	// Craftsman should use Craft_Basic (only option when Advanced gate fails).
	hasBasic := slices.Contains(craftsmanA.Plan.Actions, "Craft_Basic") ||
		craftsmanA.NeedIntensities["Satiety"] < 0.50 // executed
	hasAdvanced := slices.Contains(craftsmanA.Plan.Actions, "Craft_Advanced")

	if hasAdvanced {
		t.Errorf("Craftsman (ToM[self][Intel]=20) should NOT plan Craft_Advanced "+
			"(gate requires 70): plan=%v", craftsmanA.Plan.Actions)
	}
	if !hasBasic {
		t.Errorf("Craftsman should use Craft_Basic when Advanced gate fails: "+
			"plan=%v satiety=%.4f", craftsmanA.Plan.Actions, craftsmanA.NeedIntensities["Satiety"])
	}
	t.Logf("Low self-estimate craftsman: plan=%v satiety=%.4f → forced to Craft_Basic (expensive)",
		craftsmanA.Plan.Actions, craftsmanA.NeedIntensities["Satiety"])

	// D12.
	assertWorldDigestsEqual(t, "craftsman low self-estimate", worldA, worldB)
}

// ── Test 2: Calibrated self-estimate unlocks advanced craft ──────────────────

// TestCraftsman_HighSelfEstimate_UnlocksAdvancedCraft verifies that the same
// craftsman with ToM[self][Intelligence]=85 (accurately reflecting actual=90)
// plans Craft_Advanced — the cheaper, smarter option (cost=0.20 vs 0.50).
//
// Same agent, same actual ability; only self-perception differs. This is the
// counterfactual that reveals the cost of D8 underestimation.
func TestCraftsman_HighSelfEstimate_UnlocksAdvancedCraft(t *testing.T) {
	const seed = int64(5002)

	run := func() (*World, *agent.Agent) {
		fx := newCraftsmanFixture(t, seed)
		cfg := agent.DefaultConfig()

		craftsman := fx.world.Spawn("craftsman", core.Vec2{X: 0, Y: 0}, cfg, rng.New(20))
		seedToM(craftsman, "Intelligence", 85) // accurate self-estimate ≥ gate 70

		craftsman.NeedIntensities["Satiety"] = 0.50

		for range 5 {
			fx.world.Tick()
		}
		return fx.world, craftsman
	}

	worldA, craftsmanA := run()
	worldB, _ := run()

	// Craftsman plans Craft_Advanced (cheaper, gate passes at 85 ≥ 70).
	hasAdvanced := slices.Contains(craftsmanA.Plan.Actions, "Craft_Advanced") ||
		craftsmanA.NeedIntensities["Satiety"] < 0.50
	hasBasic := slices.Contains(craftsmanA.Plan.Actions, "Craft_Basic")

	if !hasAdvanced {
		t.Errorf("Craftsman (ToM[self][Intel]=85) should plan Craft_Advanced "+
			"(gate passes, cheaper): plan=%v satiety=%.4f",
			craftsmanA.Plan.Actions, craftsmanA.NeedIntensities["Satiety"])
	}
	if hasBasic {
		t.Errorf("Craftsman should NOT fall back to expensive Craft_Basic when "+
			"Advanced is available: plan=%v", craftsmanA.Plan.Actions)
	}
	t.Logf("Calibrated craftsman: plan=%v satiety=%.4f → Craft_Advanced (efficient)",
		craftsmanA.Plan.Actions, craftsmanA.NeedIntensities["Satiety"])

	// D12.
	assertWorldDigestsEqual(t, "craftsman high self-estimate", worldA, worldB)
}

// ── Test 3: Information asymmetry enables exploitation ────────────────────────

// TestCraftsman_ExploiterSeesActualCapability verifies that the gap between the
// craftsman's self-estimate and the exploiter's observed belief constitutes the
// structural condition for exploitation.
//
// After the exploiter observes the craftsman's actual Intelligence=90 (via direct
// observation), they know the craftsman is extremely capable. But the craftsman's
// own self-estimate stays at 20. This asymmetry means:
//   - The exploiter knows the craftsman could do Craft_Advanced (high-value work)
//   - The craftsman thinks they can only do Craft_Basic (low-value work)
//   - The exploiter can extract high-value work at Craft_Basic prices indefinitely.
//
// This documents D8's "self-sealing underestimation" as an exploitable condition
// (not a bug — it is a design invariant).
func TestCraftsman_ExploiterSeesActualCapability(t *testing.T) {
	fx := newCraftsmanFixture(t, 5003)
	cfg := agent.DefaultConfig()

	craftsmanID := core.AgentID("craftsman")
	exploiterID := core.AgentID("exploiter")

	craftsman := fx.world.Spawn(craftsmanID, core.Vec2{X: 0}, cfg, rng.New(30))
	exploiter := fx.world.Spawn(exploiterID, core.Vec2{X: 5}, cfg, rng.New(31))

	// Craftsman's own self-estimate: chronically low (D8 underestimation).
	seedToM(craftsman, "Intelligence", 20)

	// Exploiter observes craftsman's actual Intelligence=90 through interaction.
	// seedOtherToM converges exploiter.ToM[craftsman][Intelligence] toward 90.
	seedOtherToM(exploiter, craftsmanID, "Intelligence", 90)

	// Retrieve both beliefs.
	craftsmanSelf, _ := craftsman.ToM.Self(craftsmanID)
	exploiterView, _ := exploiter.ToM.Self(craftsmanID)

	craftsmanSelfMean := craftsmanSelf.EstStats["Intelligence"].Mean
	exploiterViewMean := exploiterView.EstStats["Intelligence"].Mean

	t.Logf("Craftsman self-estimate: ToM[self][Intelligence]=%.2f", craftsmanSelfMean)
	t.Logf("Exploiter's view:        ToM[craftsman][Intelligence]=%.2f", exploiterViewMean)
	t.Logf("Asymmetry gap: %.2f points (exploiter knows craftsman is %.1f× more capable than craftsman thinks)",
		exploiterViewMean-craftsmanSelfMean, exploiterViewMean/craftsmanSelfMean)

	// Exploiter's belief should be high (≥70, approaching actual 90).
	if exploiterViewMean < 70 {
		t.Errorf("Exploiter should have high ToM[craftsman][Intel] (≥70 from observation=90), got %.2f",
			exploiterViewMean)
	}

	// Craftsman's self-estimate should be low (≤30, seeded at 20).
	if craftsmanSelfMean > 30 {
		t.Errorf("Craftsman self-estimate should stay low (≤30, D8 self-sealing), got %.2f",
			craftsmanSelfMean)
	}

	// The gap must be large enough to constitute a structural advantage (≥ 40 points).
	gap := exploiterViewMean - craftsmanSelfMean
	if gap < 40 {
		t.Errorf("Exploitation gap too small: exploiter(%.2f) - craftsman(%.2f) = %.2f (need ≥40)",
			exploiterViewMean, craftsmanSelfMean, gap)
	}
}

// ── Test 4: Determinism ───────────────────────────────────────────────────────

// TestCraftsman_Determinism verifies that the full craftsman scenario — low vs
// high self-estimate + exploiter observation — is byte-identical across two runs
// (D12).
func TestCraftsman_Determinism(t *testing.T) {
	const seed = int64(5004)

	run := func() *World {
		fx := newCraftsmanFixture(t, seed)
		cfg := agent.DefaultConfig()

		timid := fx.world.Spawn("timid", core.Vec2{X: 0}, cfg, rng.New(40))
		seedToM(timid, "Intelligence", 20) // low self-estimate
		timid.NeedIntensities["Satiety"] = 0.50

		confident := fx.world.Spawn("confident", core.Vec2{X: 5}, cfg, rng.New(41))
		seedToM(confident, "Intelligence", 85) // calibrated self-estimate
		confident.NeedIntensities["Satiety"] = 0.50

		exploiter := fx.world.Spawn("exploiter", core.Vec2{X: 10}, cfg, rng.New(42))
		seedOtherToM(exploiter, core.AgentID("timid"), "Intelligence", 90)

		for range 30 {
			fx.world.Tick()
		}
		return fx.world
	}

	assertScenarioDeterministic(t, "craftsman scenario", run)
}
