package agent

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

// Scenario D: Other-referent urgency — parent cares for child (P5)

// setupScenarioD creates a parent agent with a cared-for child in ToM.
// The parent has high Intelligence (0.8) so it uses the high-foresight
// (unmet-need proxy) branch of DeriveReferentInput. The child's belief
// has low EstStats (welfare near min) so the Other-referent Safety
// priority should exceed the parent's own Safety priority.
func setupScenarioD(t *testing.T, highAffinity bool, perceivedIntel float64) (*Agent, *stats.Registry, *values.Config, *needs.Registry) {
	t.Helper()

	statReg := mustLoadStats(t, testStatsYAML)

	// Needs with Safety threshold.
	needsYAML := `schema_version: 1
needs:
  - id: Safety
    kind: conditional
    default: { posture: PreventBelow, setpoint: 0.60, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`
	balanceYAML := `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
values:
  weights:
    Safety: 1.40
    Satiety: 1.00
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
	cfg.BondAffinityGain = 0.625 // yields 1.5x bond multiplier at Affinity 0.8 (1 + 0.8*0.625 = 1.5)
	cfg.MinCareThreshold = 0.30
	cfg.MaxPossiblePriority = 2.5

	realStats := statReg.Defaults()
	realStats["Intelligence"] = perceivedIntel * 100.0
	realStats["Honesty"] = 50.0
	realStats["Aggression"] = 30.0

	selfToM := tom.NewToM("parent", realStats, 0.5, rng.New(42), statReg, cfg.Rates)

	// Seed child belief with many Observe iterations to force EstStats near targets.
	// Each call: mean += beta * Weight * (observed - mean). With beta=0.08, Weight=1.0,
	// fifty iterations bring the mean within 0.015 * initial_gap of the target (D12: deterministic).
	lowStats := map[core.StatID]float64{
		"Strength":     10,
		"Agility":      5,
		"Intelligence": 20,
		"Honesty":      80,
	}
	for i := 0; i < 50; i++ {
		for sID, val := range lowStats {
			selfToM.Observe("child", tom.StatEvidence{Stat: sID, Observed: val, Weight: 1.0, Tick: 1})
		}
	}

	var affinity float64
	if highAffinity {
		affinity = 0.8
	} else {
		affinity = 0.1
	}
	selfToM.AdjustAffinity("child", affinity)

	agent := New("parent", core.Vec2{X: 10, Y: 10}, realStats, selfToM, cfg)
	agent.NeedIntensities["Safety"] = 0.30
	agent.NeedIntensities["Satiety"] = 0.10

	return agent, statReg, valsCfg, needReg
}

// TestScenarioD_OtherReferentPriority computes the Other-referent Safety priority
// and verifies that with high Affinity + high Intelligence, the Other-priority
// exceeds the Self-priority, and that the combined urgency proxy would trigger
// conscience loosening.
func TestScenarioD_OtherReferentPriority(t *testing.T) {
	agent, statReg, valsCfg, needReg := setupScenarioD(t, true, 0.8)

	safetyDef, ok := needReg.Def("Safety")
	if !ok {
		t.Fatal("Safety not in registry")
	}

	// Self Safety priority.
	selfSafetyIntensity := agent.NeedIntensities["Safety"]
	selfStanding := values.ComputeStanding(safetyDef, selfSafetyIntensity)
	selfSalience := values.ComputeSalience(selfStanding)
	safetyWeight := valsCfg.Weight("Safety")
	selfPriority := float64(values.ComputePriority(selfSalience, safetyWeight))
	t.Logf("D: Self.Safety: intensity=%.3f standing=%.3f salience=%.3f weight=%.2f priority=%.3f",
		selfSafetyIntensity, float64(selfStanding), float64(selfSalience), safetyWeight, selfPriority)

	// Other Safety priority (child).
	childBelief, ok := agent.ToM.Self("child")
	if !ok {
		t.Fatal("child belief not in ToM")
	}
	t.Logf("D: Child belief: Affinity=%.3f EstStats={S:%v A:%v I:%v H:%v}",
		childBelief.Affinity,
		childBelief.EstStats["Strength"].Mean,
		childBelief.EstStats["Agility"].Mean,
		childBelief.EstStats["Intelligence"].Mean,
		childBelief.EstStats["Honesty"].Mean,
	)

	moodStatID := resolveOtherMoodStatID(statReg)
	perceivedIntel := agent.normalizedIntelligence(statReg)

	ref := core.Referent{Kind: core.Other, ID: "child"}
	ri := values.DeriveReferentInput(
		ref, "Safety", agent.NeedIntensities["Safety"],
		safetyDef, childBelief, 0, nil,
		perceivedIntel, moodStatID, valsCfg,
	)
	otherStanding := values.ComputeStanding(safetyDef, ri.CurrentIntensity)
	otherSalience := values.ComputeSalience(otherStanding)
	bondMultiplier := 1.0 + childBelief.Affinity*agent.Cfg.BondAffinityGain
	otherPriority := float64(values.ComputePriority(otherSalience, safetyWeight*bondMultiplier))

	t.Logf("D: Other.Safety(child): ri.CurrentIntensity=%.3f standing=%.3f salience=%.3f bond=%.2f priority=%.3f",
		ri.CurrentIntensity, float64(otherStanding), float64(otherSalience), bondMultiplier, otherPriority)

	// GOLDEN: Priority(Other.Safety, C) > Priority(Self.Safety, A)
	if otherPriority <= selfPriority {
		t.Errorf("GOLDEN FAIL: Other.Safety priority (%.3f) should exceed Self.Safety priority (%.3f)",
			otherPriority, selfPriority)
	}

	// Combined urgency proxy.
	maxPossible := agent.Cfg.MaxPossiblePriority
	combinedPriority := max(selfPriority, otherPriority)
	urgencyProxy := combinedPriority / maxPossible
	t.Logf("D: Combined priority=%.3f / MaxPossible=%.2f = urgencyProxy=%.3f",
		combinedPriority, maxPossible, urgencyProxy)

	const conscienceThreshold = 0.70
	selfUrgencyOnly := selfPriority / maxPossible
	t.Logf("D: Self-only urgency proxy: %.3f (below threshold=0.70)", selfUrgencyOnly)
	t.Logf("D: Combined urgency proxy:  %.3f", urgencyProxy)

	if urgencyProxy <= conscienceThreshold {
		t.Errorf("GOLDEN FAIL: combined urgency proxy (%.3f) should exceed conscience threshold (%.3f)",
			urgencyProxy, conscienceThreshold)
	}
}

// TestScenarioD_LowAffinityNoOtherReferent verifies that when Affinity is below
// MinCareThreshold, appraiseOthers does NOT contribute to urgency.
func TestScenarioD_LowAffinityNoOtherReferent(t *testing.T) {
	agent, statReg, valsCfg, needReg := setupScenarioD(t, false, 0.8)

	childBelief, ok := agent.ToM.Self("child")
	if !ok {
		t.Fatal("child belief not in ToM")
	}
	t.Logf("D low-affinity: child Affinity=%.3f (<= MinCareThreshold=%.3f)",
		childBelief.Affinity, agent.Cfg.MinCareThreshold)

	mockWV := newMockWorldView()
	maxOther := agent.appraiseOthers(mockWV, needReg, valsCfg, statReg)
	t.Logf("D low-affinity: maxOther from appraiseOthers = %.3f (expected 0)", maxOther)

	if maxOther != 0 {
		t.Errorf("GOLDEN FAIL: appraiseOthers with low Affinity (%.3f) should return 0, got %.3f",
			childBelief.Affinity, maxOther)
	}
}

// TestScenarioD_AppraiseOthersDirect verifies that appraiseOthers produces
// the expected maxOtherPriority from ToM beliefs.
func TestScenarioD_AppraiseOthersDirect(t *testing.T) {
	agent, statReg, valsCfg, needReg := setupScenarioD(t, true, 0.8)

	mockWV := newMockWorldView()
	maxOther := agent.appraiseOthers(mockWV, needReg, valsCfg, statReg)

	t.Logf("D appraiseOthers: maxOther = %.6f", maxOther)

	if maxOther <= 0 {
		t.Errorf("GOLDEN FAIL: appraiseOthers returned %.6f, expected > 0 for cared-for child in distress", maxOther)
	}

	priorities := agent.appraise(needReg, valsCfg)
	selfPriority := float64(priorities[0].Priority)
	t.Logf("D appraiseOthers: selfPriority=%.6f, maxOther=%.6f", selfPriority, maxOther)

	combinedPriority := max(selfPriority, maxOther)
	maxPossible := agent.Cfg.MaxPossiblePriority
	urgencyProxy := combinedPriority / maxPossible
	t.Logf("D appraiseOthers: urgencyProxy=%.6f (combined=%.6f / maxPossible=%.2f)",
		urgencyProxy, combinedPriority, maxPossible)

	if urgencyProxy <= 0.70 {
		t.Errorf("GOLDEN FAIL: urgency proxy (%.6f) should exceed conscience threshold 0.70 when caring for distressed Other",
			urgencyProxy)
	}
}
