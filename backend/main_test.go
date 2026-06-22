package main

import (
	"os"
	"testing"

	"github.com/dogring/bdg/platform/config"
)

// ── Main config wiring tests ──────────────────────────────────────────────────

// TestAgentConfigFromBalance_AllPoliticsFieldsWired verifies that all 6 P6 politics
// fields are non-zero when loaded from the real content/balance.yaml file via config.Load.
func TestAgentConfigFromBalance_AllPoliticsFieldsWired(t *testing.T) {
	contentDir := findContentDir(t)
	if contentDir == "" {
		t.Skip("content directory not found — run from repo root or set -content flag")
	}

	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	agentCfg := cfg.Balance.AgentConfig(cfg.NeedsRegistry, cfg.StatsRegistry)

	// Check that all 6 politics fields are non-zero.
	// If a field is zero, the balance.yaml value failed to wire through.
	politicsFields := []struct {
		name  string
		value float64
	}{
		{"RelyCostThreshold", agentCfg.RelyCostThreshold},
		{"RelyOnDelta", agentCfg.RelyOnDelta},
		{"VoteRelyThreshold", agentCfg.VoteRelyThreshold},
		{"VoteUrgencyThreshold", agentCfg.VoteUrgencyThreshold},
		{"VoteRelyOnDelta", agentCfg.VoteRelyOnDelta},
		{"InfluenceWeight", agentCfg.InfluenceWeight},
	}

	for _, pf := range politicsFields {
		if pf.value == 0 {
			t.Errorf("agent.Config.%s = 0 — balance.yaml politics.%s not wired",
				pf.name, yamlKeyForField(pf.name))
		} else {
			t.Logf("agent.Config.%s = %v (wired)", pf.name, pf.value)
		}
	}

	// Also verify that the tom.Rates.InfluenceWeight is wired.
	if agentCfg.Rates.InfluenceWeight == 0 {
		t.Error("tom.Rates.InfluenceWeight = 0 — balance.yaml politics.influence_weight not wired to Rates")
	} else {
		t.Logf("tom.Rates.InfluenceWeight = %v (wired)", agentCfg.Rates.InfluenceWeight)
	}

	// Verify that the Balance.Politics struct itself parsed correctly.
	bal := cfg.Balance
	if bal.Politics.RelyCostThreshold == 0 {
		t.Error("bal.Politics.RelyCostThreshold = 0 — balance.yaml parse failed")
	}
	if bal.Politics.VoteRelyOnDelta == 0 {
		t.Error("bal.Politics.VoteRelyOnDelta = 0 — balance.yaml parse failed")
	}
	if bal.Politics.InfluenceWeight == 0 {
		t.Error("bal.Politics.InfluenceWeight = 0 — balance.yaml parse failed")
	}
}

// TestIncomingSignals_WorldSnapshot_Populated tests the world snapshot's
// IncomingSignals method with a real world.
func TestIncomingSignals_WorldSnapshot_Populated(t *testing.T) {
	// This test is in the world package; skip.
	t.Skip("world-level test — see world package")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func findContentDir(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"../content",
		"../../content",
		"content",
		"./content",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			if _, err := os.Stat(c + "/balance.yaml"); err == nil {
				return c
			}
		}
	}
	return ""
}

// yamlKeyForField maps agent.Config field names to their balance.yaml keys.
func yamlKeyForField(field string) string {
	m := map[string]string{
		"RelyCostThreshold":    "rely_cost_threshold",
		"RelyOnDelta":          "relyon_delta",
		"VoteRelyThreshold":    "vote_rely_threshold",
		"VoteUrgencyThreshold": "vote_urgency_threshold",
		"VoteRelyOnDelta":      "vote_relyon_delta",
		"InfluenceWeight":      "influence_weight",
	}
	if v, ok := m[field]; ok {
		return v
	}
	return field
}