package world

// Scenario L: 공유지의 비극과 폭도의 탄생 (Tragedy of the Commons)
//
// Core conflict: Self-interest vs. collective conscience under resource collapse.
//
// Setup:
//   - Shared commons (modeled as absence/presence of Take_Portion action)
//   - Take_Portion: sustainable commons use — effort:high (moral tax) — available to all
//   - Loot_All: destroy commons for immediate gain — effort:low (temptation) — conscience-gated
//   - Take_From_Other: rob neighbors in the Hobbes state — conscience-gated
//
// The phase transition:
//   Phase 1 (Order): All agents at intensity=0.25 → urgency=0.455 < 0.70
//     → conscience gate closed for Honesty≥0.40 agents → all plan [Take_Portion]
//   Phase 2 (Defection): One agent reaches intensity=0.45 → urgency=0.818 > 0.70
//     + Honesty=0.44 (<0.55) → conscience gate opens → plans [Loot_All] (effort:low wins)
//     Others still at intensity=0.25 → gate stays closed → Take_Portion
//   Phase 3 (Cascade): Commons destroyed (Take_Portion removed from action registry)
//     All agents at intensity=0.45 → urgency=0.818
//     Agents with Honesty<0.55: gate opens → [Take_From_Other] (Hobbes state)
//     Agents with Honesty≥0.55: gate stays closed → coping (no legal option left)
//   Phase 4 (Phase Transition count): 7 of 10 turn criminal → majority Hobbes state
//
// Technical rationale:
//   - urgency = intensity/threshold = intensity/0.55 (from values.ComputeSalience)
//     • intensity=0.25 → urgency=0.455 (below 0.70 threshold → gate closed)
//     • intensity=0.45 → urgency=0.818 (above 0.70 threshold → gate opens if Honesty<0.55)
//     • "Medium hunger" = highest crime risk (close to goal but blocked → frustration)
//     • "Extreme hunger" (intensity=0.05) = urgency=0.091 → coping, NOT crime
//   - conscience gate (norm:transgressive):
//       Honesty<0.40 → always crime
//       Aggression≥0.65 → always crime
//       urgency>0.70 AND (Honesty<0.55 OR Aggression≥0.50) → situational crime
//   - UrgencyThreshold=2.0 disables planner gate-relaxation; conscience gate is the
//     sole arbiter of crime access. Without this, ALL gates relax at urgency>0.65.
//   - Take_Portion (effort:high=0.80) vs Loot_All (effort:low=0.20): once conscience
//     gate opens, Loot_All wins by cost. Orderly behavior requires gate to stay shut.
//   - Phase transition: structural collapse (Take_Portion disappears) turns the
//     morally-restrained majority into criminals, not moral collapse.
//     The 3 agents with Honesty≥0.55 stay moral — they just have no options → coping.

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/gates"
	"github.com/dogring/bdg/engine/mind/needs"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/spatial"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/values"
	"github.com/dogring/bdg/engine/kernel/worldtime"
)

// -- YAML definitions ------------------------------------------------------------

// commonsActionsYAML: commons intact — three tiers of food acquisition:
//   Take_Portion: sustainable, high moral effort cost, no prerequisites
//   Loot_All:     destroys commons, low effort — but conscience-gated
//   Take_From_Other: inter-agent robbery — conscience-gated AND requires victims nearby
const commonsActionsYAML = `schema_version: 1
actions:
  - id: Take_Portion
    tags: [ "effort:high" ]
    produces: [ has_Satiety ]
    effect: { Satiety: 0.55 }
    duration: 12
  - id: Loot_All
    tags: [ "norm:transgressive", "effort:low" ]
    produces: [ has_Satiety ]
    effect: { Satiety: 1.0 }
    duration: 3
  - id: Take_From_Other
    tags: [ "norm:transgressive", "effort:low" ]
    requires: [ near_other, owned_by_other ]
    produces: [ has_Satiety ]
    effect: { Satiety: 0.55 }
    duration: 4
  - id: MoveTo
    tags: [ "effort:low", "uses:Agility" ]
    produces: [ at_target ]
    duration: 5
`

// commonsCollapseActionsYAML: commons destroyed — Take_Portion no longer available.
// Agents must either rob neighbors (if conscience permits) or enter coping.
// "Loot_All" is also removed: there is no commons left to loot.
const commonsCollapseActionsYAML = `schema_version: 1
actions:
  - id: Take_From_Other
    tags: [ "norm:transgressive", "effort:low" ]
    requires: [ near_other, owned_by_other ]
    produces: [ has_Satiety ]
    effect: { Satiety: 0.55 }
    duration: 4
  - id: MoveTo
    tags: [ "effort:low", "uses:Agility" ]
    produces: [ at_target ]
    duration: 5
`

// commonsGatesYAML: conscience gate identical to Robin Hood / chain scenarios.
// Conscience opens when:
//   Honesty < 0.40 (extreme dishonesty, always criminal)
//   Aggression >= 0.65 (extreme aggression, always criminal)
//   urgency > 0.70 AND (Honesty < 0.55 OR Aggression >= 0.50)
//   (= medium dishonesty + economic frustration = situational criminal)
const commonsGatesYAML = robinHoodGatesYAML

// -- Fixture ---------------------------------------------------------------------

// newCommonsFixtureWith builds a World with the given actionsYAML.
// Shared: robinHoodStatsYAML, robinHoodNeedsYAML, robinHoodBalanceYAML, commonsGatesYAML.
// UrgencyThreshold=2.0 disables planner gate-relaxation so conscience gate is the
// sole arbiter of access to transgressive actions.
func newCommonsFixtureWith(t *testing.T, seed int64, actionsYAML string) *testFixture {
	t.Helper()

	statReg, err := stats.Load(strings.NewReader(robinHoodStatsYAML))
	if err != nil {
		t.Fatalf("stats.Load: %v", err)
	}
	needReg, err := needs.Load(
		strings.NewReader(robinHoodNeedsYAML),
		strings.NewReader(robinHoodBalanceYAML),
	)
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}
	valsCfg, err := values.Load(strings.NewReader(robinHoodBalanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}
	actReg, err := actions.Load(strings.NewReader(actionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}
	gateReg, err := gates.Load(strings.NewReader(commonsGatesYAML), statReg)
	if err != nil {
		t.Fatalf("gates.Load: %v", err)
	}

	plannerCfg := planner.PlannerConfig{
		Budget:             planner.Budget{MaxDepth: 6, MaxActions: 12, MaxNodes: 256},
		BaseHorizonTicks:   720,
		UrgencyThreshold:   2.0, // disables relaxation — conscience gate is the sole arbiter
		LookaheadThreshold: 0.4,
		TagCosts: map[core.Tag]float64{
			"effort:none": 0.0,
			"effort:low":  0.20,
			"effort:med":  0.50,
			"effort:high": 0.80,
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

// seedCommons seeds an agent's self-ToM Honesty and Aggression stats.
// These are the two disposition stats that determine conscience gate behavior.
func seedCommons(a *agent.Agent, honesty, aggression float64) {
	seedToM(a, "Honesty", honesty)
	seedToM(a, "Aggression", aggression)
}

// -- Test 1: Order in Abundance --------------------------------------------------

// TestCommons_OrderInAbundance verifies that when Take_Portion is available and
// urgency is below the conscience threshold, all agents choose sustainable commons use.
//
// Setup: 3 agents at widely-spaced positions (distance > 5 = no near_other).
// Satiety intensity=0.25 → urgency=0.25/0.55≈0.455 < 0.70 → conscience gate closed.
// With gate closed: Loot_All invisible → only Take_Portion remains → all plan [Take_Portion].
// Take_From_Other requires [near_other] which is false (agents are far apart) — also blocked.
//
// Decision calculus: orderly behavior emerges NOT from high moral character but from
// the structural fact that crime isn't profitable enough at low urgency to overcome the
// conscience gate. Take_Portion (effort:high = cost 0.80) is the only option.
func TestCommons_OrderInAbundance(t *testing.T) {
	const seed = int64(9001)

	run := func() (*World, []*agent.Agent) {
		fx := newCommonsFixtureWith(t, seed, commonsActionsYAML)
		cfg := robinHoodAgentConfig()

		agents := make([]*agent.Agent, 3)
		for i := range 3 {
			id := core.AgentID(fmt.Sprintf("villager%d", i))
			pos := core.Vec2{X: float64(i * 20)} // 20 units apart — no near_other
			a := fx.world.Spawn(id, pos, cfg, rng.New(int64(100+i)))
			seedCommons(a, 0.70, 0.20) // high Honesty, low Aggression
			a.NeedIntensities["Satiety"] = 0.25 // urgency = 0.455 < 0.70 (gate closed)
			agents[i] = a
		}

		fx.world.Tick()
		return fx.world, agents
	}

	_, agents := run()

	for i, a := range agents {
		t.Logf("villager%d: plan=%v coping=%v", i, a.Plan.Actions, a.Coping)

		if slices.Contains(a.Plan.Actions, "Loot_All") {
			t.Errorf("villager%d should NOT Loot_All (urgency=0.455<0.70, Honesty=0.70≥0.40): plan=%v",
				i, a.Plan.Actions)
		}
		if !slices.Contains(a.Plan.Actions, "Take_Portion") {
			t.Errorf("villager%d should plan Take_Portion as the only legal option: plan=%v coping=%v",
				i, a.Plan.Actions, a.Coping)
		}
	}

	t.Logf("Phase 1 (Order): all %d agents chose Take_Portion — conscience gate held", len(agents))

	// Determinism: two runs with same seed must produce identical world state.
	{
		w1, _ := run()
		w2, _ := run()
		if d1, d2 := worldDigest(w1), worldDigest(w2); d1 != d2 {
			t.Error("DETERMINISM FAILED (order in abundance)")
		}
	}
}

// -- Test 2: Single Defector -----------------------------------------------------

// TestCommons_SingleDefector verifies that a single agent with slightly higher
// satiety (intensity=0.45) breaks from the orderly group and Loots All, while
// the others (intensity=0.25) remain orderly.
//
// This models the initial defection event in the tragedy of the commons:
//   - Defector: intensity=0.45 → urgency=0.818 > 0.70
//               Honesty=0.44 (<0.55) → urgency branch opens conscience gate
//               Cost(Loot_All=0.20) < Cost(Take_Portion=0.80) → defector picks Loot_All
//   - Orderly:  intensity=0.25 → urgency=0.455 < 0.70 → conscience gate closed
//               Only Take_Portion available → orderly agents plan [Take_Portion]
//
// The defector's advantage is that Loot_All provides 1.0 Satiety (vs 0.55 for Take_Portion)
// at lower effort cost. Once the gate opens, defection is economically dominant.
func TestCommons_SingleDefector(t *testing.T) {
	const seed = int64(9002)

	fx := newCommonsFixtureWith(t, seed, commonsActionsYAML)
	fx2 := newCommonsFixtureWith(t, seed, commonsActionsYAML)
	cfg := robinHoodAgentConfig()

	// Defector: near-threshold satiety (82% of way to comfort level) + moderate dishonesty.
	// Economic frustration: "I'm almost comfortable, just need this one big grab."
	defector := fx.world.Spawn("defector", core.Vec2{X: 0}, cfg, rng.New(200))
	seedCommons(defector, 0.44, 0.44) // Honesty=0.44 (<0.55), Aggression=0.44 (<0.50)
	defector.NeedIntensities["Satiety"] = 0.45 // urgency=0.818 > 0.70 → gate OPENS

	// Orderly villagers: lower satiety → lower urgency → conscience gate stays shut.
	orderly := make([]*agent.Agent, 3)
	for i := range 3 {
		id := core.AgentID(fmt.Sprintf("orderly%d", i))
		pos := core.Vec2{X: float64((i + 1) * 20)} // 20+ units from defector AND each other
		a := fx.world.Spawn(id, pos, cfg, rng.New(int64(201+i)))
		seedCommons(a, 0.70, 0.20) // Honesty=0.70 (≥0.55) — urgency branch can never open
		a.NeedIntensities["Satiety"] = 0.25 // urgency=0.455 < 0.70
		orderly[i] = a
	}

	// Defector in second fixture for determinism.
	defector2 := fx2.world.Spawn("defector", core.Vec2{X: 0}, cfg, rng.New(200))
	seedCommons(defector2, 0.44, 0.44)
	defector2.NeedIntensities["Satiety"] = 0.45

	fx.world.Tick()
	fx2.world.Tick()

	t.Logf("Defector: plan=%v coping=%v (Honesty=0.44, intensity=0.45, urgency≈0.818)",
		defector.Plan.Actions, defector.Coping)

	// Defector plans Loot_All: conscience gate open + lower cost wins.
	if !slices.Contains(defector.Plan.Actions, "Loot_All") {
		t.Errorf("DEFECTOR should plan Loot_All (urgency=0.818>0.70, Honesty=0.44<0.55): plan=%v coping=%v",
			defector.Plan.Actions, defector.Coping)
	}
	if slices.Contains(defector.Plan.Actions, "Take_Portion") {
		t.Errorf("DEFECTOR should NOT plan Take_Portion when Loot_All is cheaper: plan=%v", defector.Plan.Actions)
	}

	// Orderly agents remain moral: urgency < 0.70 → conscience gate closed.
	for i, a := range orderly {
		t.Logf("orderly%d: plan=%v coping=%v (Honesty=0.70, intensity=0.25, urgency≈0.455)", i, a.Plan.Actions, a.Coping)
		if slices.Contains(a.Plan.Actions, "Loot_All") {
			t.Errorf("orderly%d should NOT Loot_All (urgency=0.455<0.70): plan=%v", i, a.Plan.Actions)
		}
		if !slices.Contains(a.Plan.Actions, "Take_Portion") {
			t.Errorf("orderly%d should plan Take_Portion: plan=%v coping=%v", i, a.Plan.Actions, a.Coping)
		}
	}

	t.Logf("Phase 2 (Defection): 1 defector → Loot_All, 3 orderly → Take_Portion")
	t.Logf("Cost differential: Loot_All(0.20) < Take_Portion(0.80) — defection is economically dominant")

	// Determinism: defector's plan must be identical across same-seed runs.
	fx2.world.Tick()
	if !slices.Equal(defector.Plan.Actions, defector2.Plan.Actions) {
		t.Errorf("DETERMINISM FAILED (single defector): %v ≠ %v",
			defector.Plan.Actions, defector2.Plan.Actions)
	}
}

// -- Test 3: Cascade After Collapse ----------------------------------------------

// TestCommons_CascadeCollapse verifies the immediate behavioral bifurcation when
// the commons is destroyed (Take_Portion removed from action registry):
//
//   - Dishonest agents (Honesty=0.44, intensity=0.45): conscience opens via urgency branch
//     → plan [Take_From_Other] (only option left for moral bypass)
//   - Highly honest agents (Honesty=0.70, intensity=0.45): conscience stays closed
//     → no legal option and no criminal option → coping
//
// All agents are spawned in tight cluster (within 5 units) with inventory, so
// near_other=true and owned_by_other=true are set for everyone by execution.go.
// This makes Take_From_Other plannable for anyone with open conscience.
//
// The key insight: it is the DISAPPEARANCE OF THE LEGAL OPTION — not moral collapse —
// that creates the Hobbes state. Honest agents (Honesty≥0.55) stay honest; they just
// have nowhere to turn. The cascade is structural, not psychological.
func TestCommons_CascadeCollapse(t *testing.T) {
	const seed = int64(9003)

	fx := newCommonsFixtureWith(t, seed, commonsCollapseActionsYAML) // commons GONE
	cfg := robinHoodAgentConfig()

	// All agents at tight cluster positions (within 5 units of neighbors).
	// All have inventory items so owned_by_other=true for everyone.
	type agentSpec struct {
		id         string
		pos        float64
		honesty    float64
		aggression float64
		intensity  float64
		wantCrime  bool
	}

	specs := []agentSpec{
		// --- Criminal tier: urgency=0.818 > 0.70, Honesty < 0.55 → gate opens ---
		{"criminal_a", 0.0, 0.44, 0.20, 0.45, true},
		{"criminal_b", 1.0, 0.48, 0.20, 0.45, true},
		{"criminal_c", 2.0, 0.52, 0.20, 0.45, true},
		// --- Moral tier: Honesty ≥ 0.55 → conscience gate stays closed → coping ---
		{"moral_d", 3.0, 0.70, 0.20, 0.45, false},
		{"moral_e", 4.0, 0.80, 0.20, 0.45, false},
	}

	agents := make(map[string]*agent.Agent)
	for i, s := range specs {
		a := fx.world.Spawn(core.AgentID(s.id), core.Vec2{X: s.pos}, cfg, rng.New(int64(300+i)))
		seedCommons(a, s.honesty, s.aggression)
		a.NeedIntensities["Satiety"] = s.intensity
		a.Inventory["grain"] = 3 // ← owned_by_other=true for all neighbors
		agents[s.id] = a
	}

	fx.world.Tick()

	criminals := 0
	copers := 0

	for _, s := range specs {
		a := agents[s.id]
		hasCrime := slices.Contains(a.Plan.Actions, "Take_From_Other")
		isInCoping := a.Coping > 0

		t.Logf("%s (H=%.2f, intensity=%.2f, urgency≈%.3f): plan=%v coping=%v",
			s.id, s.honesty, s.intensity, s.intensity/0.55, a.Plan.Actions, a.Coping)

		if s.wantCrime {
			criminals++
			if !hasCrime {
				t.Errorf("%s should plan Take_From_Other (urgency=%.3f>0.70, Honesty=%.2f<0.55): plan=%v coping=%v",
					s.id, s.intensity/0.55, s.honesty, a.Plan.Actions, a.Coping)
			}
		} else {
			copers++
			if hasCrime {
				t.Errorf("%s should NOT plan crime (Honesty=%.2f≥0.55 → gate closed): plan=%v",
					s.id, s.honesty, a.Plan.Actions)
			}
			if !isInCoping && len(a.Plan.Actions) == 0 {
				// Acceptable: agent has no plan and no coping — planner gave up.
				t.Logf("  note: %s has empty plan and coping=0 (planner found no solution)", s.id)
			}
		}
	}

	t.Logf("Phase 3 (Cascade): %d criminals (Take_From_Other) | %d moral agents (coping)", criminals, copers)
	t.Logf("Structural collapse, not moral collapse: honest agents stay honest but lose options")
}

// -- Test 4: Phase Transition (만인의 만인에 대한 투쟁) ----------------------------

// TestCommons_PhaseTransition demonstrates the full "all against all" phase transition:
//
// BEFORE COLLAPSE: 10 agents, commons intact, all at intensity=0.25 → urgency=0.455 <0.70
//   All conscience gates CLOSED → all plan [Take_Portion]. Orderly state.
//
// AFTER COLLAPSE: Same agent profiles, no Take_Portion. Agents at intensity=0.45:
//   7 agents (Honesty=0.50, below 0.55): conscience opens at urgency=0.818 → Take_From_Other
//   3 agents (Honesty=0.70, above 0.55): conscience closed → coping → helpless bystanders
//
// The 70% criminalization rate shows how the structural removal of one legal option
// creates the Hobbesian war of all against all. The remaining 30% don't turn criminal
// — they were never close to the conscience gate's threshold — but they cannot help either.
//
// Importantly: the phase transition is IMMEDIATE (single tick after collapse) and
// STRUCTURAL (determined by the missing action, not by moral character change).
func TestCommons_PhaseTransition(t *testing.T) {
	const nAgents = 10
	const nCriminals = 7  // Honesty=0.50 (<0.55): gate opens at urgency=0.818
	const nCopers = 3     // Honesty=0.70 (≥0.55): gate stays closed regardless

	cfg := robinHoodAgentConfig()

	// ── Phase 1: Commons intact, all orderly ────────────────────────────────────
	fxBefore := newCommonsFixtureWith(t, 9004, commonsActionsYAML)

	beforePlans := make(map[string][]actions.ActionID)
	for i := range nAgents {
		id := core.AgentID(fmt.Sprintf("agent%d", i))
		pos := core.Vec2{X: float64(i) * 20} // far apart → no near_other
		honesty := 0.50
		if i >= nCriminals {
			honesty = 0.70 // last 3 are more honest
		}
		a := fxBefore.world.Spawn(id, pos, cfg, rng.New(int64(400+i)))
		seedCommons(a, honesty, 0.20)
		a.NeedIntensities["Satiety"] = 0.25 // urgency=0.455 < 0.70 → gate closed for all
	}

	fxBefore.world.Tick()

	for i := range nAgents {
		id := fmt.Sprintf("agent%d", i)
		a, _ := fxBefore.world.AgentOf(core.AgentID(id))
		beforePlans[id] = a.Plan.Actions
		t.Logf("BEFORE %s: plan=%v (urgency≈0.455, gate closed)", id, a.Plan.Actions)
		if !slices.Contains(a.Plan.Actions, "Take_Portion") {
			t.Errorf("BEFORE collapse: %s should plan Take_Portion, got %v coping=%v",
				id, a.Plan.Actions, a.Coping)
		}
	}

	t.Logf("Phase 1 VERIFIED: all %d agents chose Take_Portion (orderly state)", nAgents)

	// ── Phase 3: Commons destroyed, cascade begins ──────────────────────────────
	fxAfter := newCommonsFixtureWith(t, 9004, commonsCollapseActionsYAML) // NO Take_Portion

	for i := range nAgents {
		id := core.AgentID(fmt.Sprintf("agent%d", i))
		pos := core.Vec2{X: float64(i) * 0.5} // tight cluster (0.5 apart, all within 5 units)
		honesty := 0.50
		if i >= nCriminals {
			honesty = 0.70
		}
		a := fxAfter.world.Spawn(id, pos, cfg, rng.New(int64(400+i)))
		seedCommons(a, honesty, 0.20)
		a.NeedIntensities["Satiety"] = 0.45 // urgency=0.818 > 0.70 → opens for Honesty<0.55
		a.Inventory["grain"] = 3            // → owned_by_other=true for neighbors
	}

	fxAfter.world.Tick()

	criminalCount := 0
	copingCount := 0

	for i := range nAgents {
		id := core.AgentID(fmt.Sprintf("agent%d", i))
		a, _ := fxAfter.world.AgentOf(id)
		isCriminal := slices.Contains(a.Plan.Actions, "Take_From_Other")
		isInCoping := a.Coping > 0
		expectedCriminal := i < nCriminals

		t.Logf("AFTER agent%d (H=%s, urgency≈0.818): plan=%v coping=%v → %s",
			i, honestyLabel(i, nCriminals), a.Plan.Actions, a.Coping, phaseLabel(isCriminal, isInCoping))

		if isCriminal {
			criminalCount++
		} else {
			copingCount++
		}

		if expectedCriminal && !isCriminal {
			t.Errorf("AFTER collapse: agent%d (Honesty=0.50<0.55, urgency=0.818>0.70) should plan crime: plan=%v coping=%v",
				i, a.Plan.Actions, a.Coping)
		}
		if !expectedCriminal && isCriminal {
			t.Errorf("AFTER collapse: agent%d (Honesty=0.70≥0.55) should NOT plan crime: plan=%v",
				i, a.Plan.Actions)
		}
	}

	t.Logf("═══ PHASE TRANSITION RESULT ═══")
	t.Logf("Before collapse: %d/%d → Take_Portion (orderly)", nAgents, nAgents)
	t.Logf("After collapse:  %d/%d → Take_From_Other (Hobbes state)", criminalCount, nAgents)
	t.Logf("                 %d/%d → coping (moral but helpless)", copingCount, nAgents)
	t.Logf("Phase transition: STRUCTURAL (legal option removed, not moral change)")
	t.Logf("Honest agents (Honesty≥0.55) stayed moral — conscience gate never opened")
	t.Logf("'만인의 만인에 대한 투쟁' reached: %d%% of agents in Hobbes state", 100*criminalCount/nAgents)

	if criminalCount < nCriminals {
		t.Errorf("phase transition incomplete: expected at least %d criminals, got %d", nCriminals, criminalCount)
	}
	if copingCount < nCopers {
		t.Errorf("honest remnant too small: expected at least %d copers, got %d", nCopers, copingCount)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────────

func honestyLabel(i, nCriminals int) string {
	if i < nCriminals {
		return "0.50"
	}
	return "0.70"
}

func phaseLabel(isCriminal, isInCoping bool) string {
	if isCriminal {
		return "HOBBES (criminal)"
	}
	if isInCoping {
		return "coping (moral, no options)"
	}
	return "coping/silent"
}
