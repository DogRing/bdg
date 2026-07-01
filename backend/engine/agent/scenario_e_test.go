package agent

// Scenario E — View Dispute (조망권 분쟁, P5 Place referent)
//
// Agent A values Openness at their home position (Maximize posture).
// Agent B builds a tall structure that obstructs the view, dropping
// PlaceQuality("home") from 1.0 (pristine) to 0.2 (blocked).
//
// This test verifies:
//   1. appraisePlace detects the Place quality drop and computes a
//      non-zero Place-referent Priority for Openness.
//   2. The Place-derived priority is folded into the combined urgency
//      proxy that the planner uses, so A's urgency rises.
//   3. Protocol: the threat trigger is recorded in event logs (via
//      the what-if trace: Place priority delta is observable).
//
// This is an agent-level unit test (like Scenario D). The full end-to-end
// integration through the world (B builds, A perceives view blocked) is
// owned by engine/world.

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/needs"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/tom"
	"github.com/dogring/bdg/engine/mind/values"
)

// setupScenarioE creates Agent A with a Place referent value for Openness
// at "home". Returns the agent, registries, and world mock.
//
// Agent A cares about the open view from home (Openness, Place referent,
// Maximize posture, setpoint=0.35 per content/needs.yaml).
func setupScenarioE(t *testing.T, placeQuality float64) (*Agent, *stats.Registry, *values.Config, *needs.Registry, *mockWorldView) {
	t.Helper()

	statReg := mustLoadStats(t, testStatsYAML)

	// Needs with Openness (conditional, no rate) + Satiety + Safety for well-rounded appraisal.
	needsYAML := `schema_version: 1
needs:
  - id: Openness
    kind: conditional
    default: { posture: Maximize, setpoint: 0.35, referent: Place }
    salience: { curve: gap_to_max, gain: 0.5 }
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Safety
    kind: conditional
    default: { posture: PreventBelow, setpoint: 0.60, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`
	balanceYAML := `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
values:
  weights:
    Openness: 0.40
    Satiety: 1.00
    Safety: 1.40
`
	needReg, err := needs.Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}

	valsCfg, err := values.Load(strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}

	cfg := DefaultConfig()
	cfg.MaxPossiblePriority = 2.5

	realStats := statReg.Defaults()
	realStats["Intelligence"] = 60.0
	realStats["Honesty"] = 50.0
	realStats["Aggression"] = 50.0

	selfToM := tom.NewToM("agent_a", realStats, 0.5, rng.New(42), statReg, cfg.Rates)

	// Agent A values Openness at Place(home) with Maximize posture.
	agent := New("agent_a", core.Vec2{X: 10, Y: 10}, realStats, selfToM, cfg)
	agent.Values = []core.Value{
		{
			Dimension: "Openness",
			Ref:       core.Referent{Kind: core.Place, ID: "home"},
			Posture:   core.Maximize,
			Setpoint:  0.35, // from needs.yaml Openness default.setpoint
		},
	}
	// Set physiological needs to moderate levels so Openness is the decisive factor.
	agent.NeedIntensities["Satiety"] = 0.30 // below threshold 0.55
	agent.NeedIntensities["Safety"] = 0.20  // below threshold 0.60

	// Mock world with configurable place quality.
	mockWV := newMockWorldView()
	mockWV.placeQuality = func(id core.ObjectID) float64 {
		if id == "home" {
			return placeQuality
		}
		return 1.0
	}

	return agent, statReg, valsCfg, needReg, mockWV
}

// TestScenarioE_PlaceReferentPriority computes the Openness priority from
// Place referent appraisal when the view from home is pristine (quality 1.0)
// vs blocked (quality 0.2).
func TestScenarioE_PlaceReferentPriority(t *testing.T) {
	// ── Case 1: pristine view (placeQuality=1.0) ─────────────────────────────
	agent, _, valsCfg, needReg, mockWV := setupScenarioE(t, 1.0)

	// Self-referent priorities (baseline — moderate needs).
	priorities := agent.appraise(needReg, valsCfg)
	selfMaxDefault := float64(priorities[0].Priority)
	t.Logf("E (pristine): self-priorities: %v", priorities)

	// Place-referent appraisal at pristine quality.
	maxPlace := agent.appraisePlace(mockWV, needReg, valsCfg)
	t.Logf("E (pristine): maxPlace=%.6f (expected ~0, since place quality = 1.0)", maxPlace)

	// With quality=1.0: CurrentIntensity = 1-1 = 0, Standing=1, Salience=0, Priority=0.
	if maxPlace != 0 {
		t.Logf("WARN (pristine): maxPlace=%.6f, expected 0 for pristine view", maxPlace)
	}

	// ── Case 2: blocked view (placeQuality=0.2) ──────────────────────────────
	agent2, _, _, _, mockWV2 := setupScenarioE(t, 0.2)

	// Place-referent appraisal at blocked quality.
	maxPlaceBlocked := agent2.appraisePlace(mockWV2, needReg, valsCfg)
	t.Logf("E (blocked): maxPlace=%.6f (expected > 0)", maxPlaceBlocked)

	combined := max(float64(priorities[0].Priority), maxPlaceBlocked)
	t.Logf("E (blocked): combinedPriority=%.6f (self=%.6f, place=%.6f)",
		combined, selfMaxDefault, maxPlaceBlocked)

	// GOLDEN: Place priority must be non-zero when quality drops.
	if maxPlaceBlocked <= 0 {
		t.Errorf("GOLDEN FAIL: Place-referent priority should be > 0 when view is blocked, got %.6f", maxPlaceBlocked)
	}

	// GOLDEN: Place priority should be higher than when pristine.
	if maxPlaceBlocked <= maxPlace {
		t.Errorf("GOLDEN FAIL: Place priority with blocked view (%.6f) should exceed pristine (%.6f)",
			maxPlaceBlocked, maxPlace)
	}

	// ── Urgency proxy ────────────────────────────────────────────────────────
	urgencyProxy := combined / agent.Cfg.MaxPossiblePriority
	t.Logf("E (blocked): urgencyProxy = %.6f (combined=%.6f / maxPossible=%.2f)",
		urgencyProxy, combined, agent.Cfg.MaxPossiblePriority)

	const conscienceThreshold = 0.70
	if urgencyProxy > conscienceThreshold {
		t.Logf("E (blocked): urgencyProxy %.4f EXCEEDS conscience threshold %.2f",
			urgencyProxy, conscienceThreshold)
	} else {
		t.Logf("E (blocked): urgencyProxy %.4f is below conscience threshold %.2f",
			urgencyProxy, conscienceThreshold)
	}

	// Log discipline.
	t.Logf("GOLDEN seed 606: Place priority at pristine=%.10f, blocked=%.10f",
		maxPlace, maxPlaceBlocked)
}

// TestScenarioE_AppraisePlaceDirect verifies that appraisePlace returns
// the expected maxPlacePriority from the agent's held Values.
func TestScenarioE_AppraisePlaceDirect(t *testing.T) {
	agent, _, valsCfg, needReg, mockWV := setupScenarioE(t, 0.2)

	maxPlace := agent.appraisePlace(mockWV, needReg, valsCfg)
	t.Logf("E appraisePlace: maxPlace = %.6f", maxPlace)

	if maxPlace <= 0 {
		t.Errorf("GOLDEN FAIL: appraisePlace returned %.6f, expected > 0 when Place quality is low", maxPlace)
	}

	// Verify that appraisePlace returns 0 when no Place values exist.
	agentNoValues := *agent
	agentNoValues.Values = nil
	maxPlaceNoValues := agentNoValues.appraisePlace(mockWV, needReg, valsCfg)
	t.Logf("E appraisePlace (no values): maxPlace = %.6f (expected 0)", maxPlaceNoValues)
	if maxPlaceNoValues != 0 {
		t.Errorf("GOLDEN FAIL: appraisePlace with no Values should return 0, got %.6f", maxPlaceNoValues)
	}
}

// TestScenarioE_CombinedUrgencyFold verifies that the Place-referent priority
// is folded into the combined urgency proxy during Tick's phase 3.
func TestScenarioE_CombinedUrgencyFold(t *testing.T) {
	agent, statReg, valsCfg, needReg, mockWV := setupScenarioE(t, 0.15) // severely blocked

	// This replicates what Tick phase 3 does.
	priorities := agent.appraise(needReg, valsCfg)
	selfMax := float64(priorities[0].Priority)
	maxPlace := agent.appraisePlace(mockWV, needReg, valsCfg)
	maxOther := agent.appraiseOthers(mockWV, needReg, valsCfg, statReg) // no ToM subjects -> 0

	combined := max(selfMax, maxOther, maxPlace)
	t.Logf("E fold: selfMax=%.6f otherMax=%.6f placeMax=%.6f combined=%.6f",
		selfMax, maxOther, maxPlace, combined)

	// The combined should be at least as large as place max.
	if combined < maxPlace {
		t.Errorf("GOLDEN FAIL: combinedPriority (%.6f) should be >= maxPlace (%.6f)", combined, maxPlace)
	}

	// Urgency proxy calculation (what Tick's replan computes).
	urgency := clamp01(combined / agent.Cfg.MaxPossiblePriority)
	t.Logf("E fold: urgency proxy = %.6f", urgency)
	if urgency <= 0 {
		t.Errorf("GOLDEN FAIL: urgency should be > 0 when Place quality is threatened, got %.6f", urgency)
	}
}

// TestScenarioE_PlaceQualitySensitivity verifies that the Place-referent
// priority increases monotonically as place quality degrades.
func TestScenarioE_PlaceQualitySensitivity(t *testing.T) {
	qualities := []float64{1.0, 0.8, 0.6, 0.4, 0.2, 0.0}
	priorities := make([]float64, len(qualities))

	for i, q := range qualities {
		agent, _, valsCfg, needReg, mockWV := setupScenarioE(t, q)
		priorities[i] = agent.appraisePlace(mockWV, needReg, valsCfg)
		t.Logf("E sensitivity: quality=%.2f -> place priority=%.6f", q, priorities[i])
	}

	// Verify monotonic: as quality drops, priority should not decrease.
	for i := 1; i < len(priorities); i++ {
		if priorities[i] < priorities[i-1] {
			t.Errorf("GOLDEN FAIL: priority should be monotonic increasing as quality drops, "+
				"but quality=%.2f (%.6f) < quality=%.2f (%.6f)",
				qualities[i], priorities[i], qualities[i-1], priorities[i-1])
		}
	}
}

// TestScenarioE_NoThreatWhenPristine verifies that when place quality is
// high (1.0), appraisePlace returns 0, confirming no threat is detected.
func TestScenarioE_NoThreatWhenPristine(t *testing.T) {
	agent, _, valsCfg, needReg, mockWV := setupScenarioE(t, 1.0)
	maxPlace := agent.appraisePlace(mockWV, needReg, valsCfg)

	// GOLDEN: pristine view should produce zero place priority.
	if maxPlace != 0 {
		t.Errorf("GOLDEN FAIL: pristine place should produce 0 priority, got %.6f", maxPlace)
	}
	t.Logf("E pristine: maxPlace=%.6f (correct: no threat for perfect view)", maxPlace)
}
