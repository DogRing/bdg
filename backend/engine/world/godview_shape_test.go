package world

import (
	"encoding/json"
	"testing"

	"github.com/dogring/bdg/engine/mind/tom"
)

// TestStateJSON_GodViewContractShape guards the snake_case snapshot contract that
// the read API / god-view depends on (data-contracts §1). A regression here is what
// silently 404'd GET /api/god/agent/{id}/divergence against real snapshots: the
// engine emitted PascalCase ("Agents"/"ID"/"RealStats") and lacked tom_digest, so
// the parser (which expects "agents"/"id"/"real_stats" + "tom_digest") never matched.
func TestStateJSON_GodViewContractShape(t *testing.T) {
	fx := newFixtureSeeded(t, 1)
	spawnTwoAgents(t, fx, 1)

	// agent_a forms a cross-agent belief about agent_b → tom_digest must capture it,
	// which is what feeds the "others" channel (D6) of the divergence response.
	a, ok := fx.world.AgentOf("agent_a")
	if !ok {
		t.Fatal("agent_a missing")
	}
	a.ToM.Observe("agent_b", tom.StatEvidence{Stat: "Strength", Observed: 0.8, Weight: 0.6, Tick: 1})

	blob, err := json.Marshal(fx.world.State())
	if err != nil {
		t.Fatalf("marshal State: %v", err)
	}
	var ws map[string]any
	if err := json.Unmarshal(blob, &ws); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	agents, ok := ws["agents"].([]any)
	if !ok || len(agents) == 0 {
		t.Fatalf("snapshot missing snake_case \"agents\" array: %s", blob)
	}
	a0, _ := agents[0].(map[string]any)
	for _, k := range []string{"id", "real_stats", "self_est_stats"} {
		if _, ok := a0[k]; !ok {
			t.Errorf("agent digest missing %q (have %v)", k, gvKeys(a0))
		}
	}

	// tom_digest[agent_a][agent_b].est_stats.Strength.mean must exist.
	td, ok := ws["tom_digest"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot missing \"tom_digest\": %s", blob)
	}
	obs, ok := td["agent_a"].(map[string]any)
	if !ok {
		t.Fatalf("tom_digest missing observer agent_a (have %v)", gvKeys(td))
	}
	subj, ok := obs["agent_b"].(map[string]any)
	if !ok {
		t.Fatalf("tom_digest[agent_a] missing subject agent_b (have %v)", gvKeys(obs))
	}
	est, ok := subj["est_stats"].(map[string]any)
	if !ok {
		t.Fatalf("tom_digest entry missing est_stats (have %v)", gvKeys(subj))
	}
	strength, ok := est["Strength"].(map[string]any)
	if !ok {
		t.Fatalf("est_stats missing Strength dist (have %v)", gvKeys(est))
	}
	if _, ok := strength["mean"]; !ok {
		t.Errorf("StatDist missing snake_case \"mean\" (have %v)", strength)
	}
}

func gvKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
