package world

import (
	"testing"

	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// Scenario: resource-conflict resentment (P3 — BLOCKER-3 wiring).
//
// Two agents contend the SAME resource via Forage (conflictStat = Agility). The
// stronger agent wins; the loser is Interrupted AND recorded as a resentment
// trigger pointing at the winner. On the next tick's plan phase the loser —
// while in Latent coping — accrues Resentment toward the winner. The winner,
// having no trigger, is unaffected. This exercises the real wiring that
// replaced the ResentmentTriggers stub: world conflict → trigger → snapshot →
// agent.accrueResentment.
func TestScenarioResentment_ConflictLoserAccrues(t *testing.T) {
	fx := newFixtureSeeded(t, 42)

	cfg := agent.DefaultConfig()
	winner := fx.world.Spawn("agent_win", core.Vec2{X: 0, Y: 0}, cfg, rng.New(1))
	loser := fx.world.Spawn("agent_lose", core.Vec2{X: 1, Y: 1}, cfg, rng.New(2))

	// Forage uses Agility (conflictStat). Pin so the winner deterministically wins.
	winner.RealStats["Agility"] = 80
	loser.RealStats["Agility"] = 30

	// Both Forage the same berry bush this tick → a resource conflict.
	intents := []agent.Intent{
		{Kind: agent.IntentStart, Agent: "agent_lose", Action: "Forage", Target: "berry_bush_0", Tick: 0},
		{Kind: agent.IntentStart, Agent: "agent_win", Action: "Forage", Target: "berry_bush_0", Tick: 0},
	}
	groups := fx.world.buildConflictGroups(intents)

	// ── 1. The world converts the conflict into a loser→winner trigger ──────
	triggers := fx.world.conflictResentmentTriggers(intents, groups)
	if got := triggers["agent_lose"]; len(got) != 1 || got[0] != "agent_win" {
		t.Fatalf("loser trigger = %v, want [agent_win]", got)
	}
	if got := triggers["agent_win"]; len(got) != 0 {
		t.Fatalf("winner should accrue no trigger, got %v", got)
	}

	// Commit the triggers as Tick()'s apply phase does, then expose via snapshot.
	fx.world.resentmentTriggers = triggers
	snap := newSnapshot(fx.world)
	if got := snap.ResentmentTriggers("agent_lose"); len(got) != 1 || got[0] != "agent_win" {
		t.Fatalf("snapshot loser triggers = %v, want [agent_win]", got)
	}
	if got := snap.ResentmentTriggers("agent_win"); got != nil {
		t.Fatalf("snapshot winner triggers = %v, want nil", got)
	}

	// ── 2. A Latent loser consumes the trigger and accrues Resentment ───────
	// Keep the loser stable so its own tick does NOT replan (which would reset
	// Coping to Idle before accrual): a non-empty plan + a dominant Goal that
	// stays the top priority (mediateGoal returns "no change").
	loser.Coping = agent.Latent
	loser.Latent = []agent.LatentGoal{{Dim: "Satiety", Since: 0, Intensity: 0.5}}
	loser.Goal = "Satiety"
	loser.NeedIntensities["Satiety"] = 0.9 // dominant, unmet → stays top priority
	loser.Plan = planner.Plan{Actions: []actions.ActionID{"Forage"}, Horizon: 1}
	loser.PlanIdx = 0

	before := loser.Resentment
	_ = loser.Tick(snap, fx.world.tick, rng.New(7), fx.svc, fx.emit)

	if loser.Coping != agent.Latent {
		t.Fatalf("loser left Latent (Coping=%v) before accrual — replan reset it", loser.Coping)
	}
	if loser.Resentment <= before {
		t.Fatalf("loser Resentment did not accrue: before=%.4f after=%.4f", before, loser.Resentment)
	}

	// ── 3. The winner, with no trigger, accrues nothing ─────────────────────
	if winner.Resentment != 0 {
		t.Fatalf("winner Resentment changed without a trigger: %.4f", winner.Resentment)
	}
}

// TestConflictResentment_NoConflictNoTrigger guards the negative: a single
// uncontested intent on a target produces no resentment triggers.
func TestConflictResentment_NoConflictNoTrigger(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()
	fx.world.Spawn("agent_solo", core.Vec2{}, cfg, rng.New(1))

	intents := []agent.Intent{
		{Kind: agent.IntentStart, Agent: "agent_solo", Action: "Forage", Target: "berry_bush_0", Tick: 0},
	}
	groups := fx.world.buildConflictGroups(intents)
	triggers := fx.world.conflictResentmentTriggers(intents, groups)
	if len(triggers) != 0 {
		t.Fatalf("no conflict should yield no triggers, got %v", triggers)
	}
}
