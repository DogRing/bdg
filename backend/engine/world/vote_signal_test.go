package world

import (
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/tom"
)

// ── Vote signal world-level tests ─────────────────────────────────────────────

// TestApplyVoteSignal_BroadcastsToAllAgents verifies that when a Vote signal is
// applied by the world, ALL agents (except the voter) have their RelyOn adjusted
// toward the voted holder, and the signal appears in IncomingSignals.
func TestApplyVoteSignal_BroadcastsToAllAgents(t *testing.T) {
	fx := newAgentFixture(t)

	// Spawn two listener agents.
	listener1 := spawnTestAgent(fx, "listener_1", 50)
	listener2 := spawnTestAgent(fx, "listener_2", 50)

	// Spawn a voted holder (non-voter, will be the Target).
	_ = spawnTestAgent(fx, "provider", 50)

	// Spawn the voter.
	_ = spawnTestAgent(fx, "voter", 50)

	// Ensure all listeners have the voted holder in their ToM.
	// (AdjustRelyOn seeds the belief if needed.)
	for _, listener := range []*agent.Agent{listener1, listener2} {
		listener.ToM.AdjustRelyOn("provider", core.FuncSafety, 0.0)
	}

	// Tick once to set up snapshot state and clear pendingSignals.
	fx.world.Tick()

	// Manually call applySignal with a Vote intent (simulates the apply phase).
	voteIntent := agent.Intent{
		Kind:  agent.IntentSignal,
		Agent: core.AgentID("voter"),
		Tick:  fx.world.CurrentTick(),
		Signal: &agent.Signal{
			Kind:      agent.SignalKind("Vote"),
			Target:    core.AgentID("provider"),
			Function:  core.FuncSafety,
			Intensity: 0.8,
		},
	}
	fx.world.applySignal(voteIntent)

	// Check that both listeners have RelyOn adjusted toward provider.
	for _, listenerID := range []core.AgentID{"listener_1", "listener_2"} {
		listener, ok := fx.world.AgentOf(listenerID)
		if !ok {
			t.Fatalf("listener %s not found", listenerID)
		}
		b, ok := listener.ToM.Self("provider")
		if !ok {
			t.Fatalf("listener %s has no belief about provider", listenerID)
		}
		relyOn := b.RelyOn[core.FuncSafety]
		if relyOn <= 0 {
			t.Errorf("listener %s RelyOn[FuncSafety] toward provider = %.4f, want > 0",
				listenerID, relyOn)
		}
		// The delta should be the agent's VoteRelyOnDelta.
		expected := listener.Cfg.VoteRelyOnDelta
		if relyOn != expected {
			t.Errorf("listener %s RelyOn[FuncSafety] = %.4f, want %.4f",
				listenerID, relyOn, expected)
		}
	}

	// Check that the voter's RelyOn was NOT adjusted (voter excludes self).
	voterAgent, ok := fx.world.AgentOf("voter")
	if !ok {
		t.Fatal("voter not found")
	}
	voterBelief, ok := voterAgent.ToM.Self("provider")
	if ok && voterBelief.RelyOn != nil {
		if relyOn := voterBelief.RelyOn[core.FuncSafety]; relyOn > 0 {
			t.Errorf("voter's own RelyOn[FuncSafety] toward provider = %.4f, want 0 (should be excluded)",
				relyOn)
		}
	}

	// Check IncomingSignals on a NEW snapshot taken after the applySignal call.
	// We need to force a new snapshot to see the pendingSignals.
	// Instead, directly check the world's pendingSignals map.
	if len(fx.world.pendingSignals) == 0 {
		t.Fatal("pendingSignals is empty after Vote broadcast")
	}
	for _, listenerID := range []core.AgentID{"listener_1", "listener_2"} {
		signals := fx.world.pendingSignals[listenerID]
		if len(signals) == 0 {
			t.Errorf("listener %s has no signals in pendingSignals after vote broadcast", listenerID)
			continue
		}
		foundVote := false
		for _, sig := range signals {
			if sig.Kind == core.SignalVote && sig.Function == core.FuncSafety && sig.Target == "provider" {
				foundVote = true
				break
			}
		}
		if !foundVote {
			t.Errorf("listener %s did not receive Vote signal for provider/Safety in pendingSignals", listenerID)
		}
	}

	// Voter should NOT receive its own signal.
	voterSignals := fx.world.pendingSignals["voter"]
	for _, sig := range voterSignals {
		if sig.Kind == core.SignalVote && sig.Source == "voter" {
			t.Errorf("voter received its own Vote signal in pendingSignals — should be excluded")
		}
	}
}

// ── Test helpers ──────────────────────────────────────────────────────────────

// agentFixture is a lightweight world with config for vote signal tests.
type agentFixture struct {
	world    *World
	statReg  *stats.Registry
	rootRNG  *rng.RNG
	emit     *recordingEmitter
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	fx := newFixtureSeeded(t, 42)
	return &agentFixture{
		world:   fx.world,
		statReg: fx.regs.stats,
		rootRNG: fx.rootRNG,
		emit:    fx.emit,
	}
}

// spawnTestAgent spawns an agent with the given Intelligence stat level.
func spawnTestAgent(fx *agentFixture, id core.AgentID, intelligence float64) *agent.Agent {
	realStats := fx.statReg.Defaults()
	realStats[core.StatID("Intelligence")] = intelligence
	realStats[core.StatID("Strength")] = 50
	realStats[core.StatID("Agility")] = 50
	realStats[core.StatID("Honesty")] = 50
	realStats[core.StatID("Aggression")] = 30

	cfg := agent.DefaultConfig()
	cfg.VoteRelyOnDelta = 0.10

	selfToM := tom.NewToM(id, realStats, 0.5, rng.New(int64(id[0])), fx.statReg, cfg.Rates)

	a := agent.New(id, core.Vec2{X: 5, Y: 5}, realStats, selfToM, cfg)
	fx.world.agents[id] = a
	fx.world.agentIDs = append(fx.world.agentIDs, id)
	return a
}
