package world

import (
	"fmt"
	"sort"
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// Scenario G — Village Chief Emergence (P6)
//
// Tests the reliance scan's rising-edge detection: when enough agents converge
// RelyOn[Safety] on a single holder, the scan emits RoleEmerged.
// Uses a minimal setup: directly sets RelyOn values on agents' ToMs rather than
// simulating the full theft → patrol → convergence chain.

// TestScenarioG_RoleEmerged_RisingEdge verifies that when a super-threshold share
// of agents point their strongest RelyOn[Safety] at one holder, the reliance scan
// emits exactly one RoleEmerged event for that holder+function.
func TestScenarioG_RoleEmerged_RisingEdge(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()

	// Spawn 5 agents: 4 villagers + 1 guard.
	villagerIDs := []core.AgentID{"villager_1", "villager_2", "villager_3", "villager_4"}
	guardID := core.AgentID("guard")

	for _, id := range villagerIDs {
		fx.world.Spawn(id, core.Vec2{}, cfg, rng.New(42))
	}
	fx.world.Spawn(guardID, core.Vec2{}, cfg, rng.New(43))

	// Set up the guard's self-belief so guard has a belief entry (not strictly
	// needed for the scan but matches realistic agent structure).
	guard, _ := fx.world.AgentOf(guardID)

	// Each villager's ToM[guard].RelyOn[Safety] = 0.8 (super-threshold).
	// Each villager's ToM[otherVillagers].RelyOn[Safety] = 0.1 (weak).
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)

		// Set RelyOn[Safety]=0.8 toward the guard.
		v.ToM.AdjustRelyOn(guardID, core.FuncSafety, 0.8)

		// Set weak RelyOn toward other villagers (so guard has the plurality).
		for _, other := range villagerIDs {
			if other != vid {
				v.ToM.AdjustRelyOn(other, core.FuncSafety, 0.1)
			}
		}

		// Also seed a belief about self with weak reliance (so self doesn't win).
		v.ToM.AdjustRelyOn(vid, core.FuncSafety, 0.05)
	}

	// Seed guard's beliefs too (guard doesn't rely on others for safety, but
	// needs beliefs for completeness).
	guard.ToM.AdjustRelyOn(guardID, core.FuncSafety, 0.0)
	for _, vid := range villagerIDs {
		guard.ToM.AdjustRelyOn(vid, core.FuncSafety, 0.0)
	}

	// Clear any events from setup.
	fx.emit.events = nil

	// Run the reliance scan directly.
	fx.world.relianceScan()

	// Assert exactly one RoleEmerged event for Safety → guard.
	var roleEvents []core.Event
	for _, ev := range fx.emit.events {
		if ev.Type == "RoleEmerged" {
			roleEvents = append(roleEvents, ev)
		}
	}

	if len(roleEvents) != 1 {
		t.Fatalf("expected exactly 1 RoleEmerged event, got %d", len(roleEvents))
	}

	ev := roleEvents[0]
	payload, ok := ev.Payload.(map[string]any)
	if !ok {
		t.Fatalf("expected map payload, got %T", ev.Payload)
	}

	if got := payload["function"]; got != "Safety" {
		t.Errorf("expected function=Safety, got %v", got)
	}
	if got := payload["holder"]; got != "guard" {
		t.Errorf("expected holder=guard, got %v", got)
	}
	share, ok := payload["reliance_share"].(float64)
	if !ok {
		t.Fatalf("expected float64 reliance_share, got %T", payload["reliance_share"])
	}
	// 4 villagers all point at guard → share = 4/5 = 0.8
	if share != 0.8 {
		t.Errorf("expected reliance_share=0.8, got %v", share)
	}
}

// TestScenarioG_NoDuplicateOnConvergedTicks verifies that the rising-edge
// debounce works: running the scan again with the same RelyOn distribution does
// NOT emit a duplicate RoleEmerged.
func TestScenarioG_NoDuplicateOnConvergedTicks(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()

	villagerIDs := []core.AgentID{"v1", "v2", "v3"}
	guardID := core.AgentID("guard")

	for _, id := range villagerIDs {
		fx.world.Spawn(id, core.Vec2{}, cfg, rng.New(100))
	}
	fx.world.Spawn(guardID, core.Vec2{}, cfg, rng.New(101))

	// Set up all villagers relying on guard for Safety.
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(guardID, core.FuncSafety, 0.9)
		// Weak self-reliance.
		v.ToM.AdjustRelyOn(vid, core.FuncSafety, 0.05)
	}

	guard, _ := fx.world.AgentOf(guardID)
	guard.ToM.AdjustRelyOn(guardID, core.FuncSafety, 0.0)
	for _, vid := range villagerIDs {
		guard.ToM.AdjustRelyOn(vid, core.FuncSafety, 0.0)
	}

	fx.emit.events = nil

	// First scan: should emit.
	fx.world.relianceScan()
	firstCount := countRoleEvents(fx.emit.events)
	if firstCount != 1 {
		t.Fatalf("expected 1 RoleEmerged on first scan, got %d", firstCount)
	}

	// Clear events and scan again (same distribution — converged state).
	fx.emit.events = nil
	fx.world.relianceScan()
	secondCount := countRoleEvents(fx.emit.events)
	if secondCount != 0 {
		t.Errorf("expected 0 duplicate RoleEmerged on converged scan, got %d", secondCount)
	}
}

// TestScenarioG_Succession_NewHolderEmitted verifies that a holder change
// (succession) emits a new RoleEmerged.
func TestScenarioG_Succession_NewHolderEmitted(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()

	villagerIDs := []core.AgentID{"v1", "v2", "v3"}
	guardA := core.AgentID("guard_a")
	guardB := core.AgentID("guard_b")

	for _, id := range villagerIDs {
		fx.world.Spawn(id, core.Vec2{}, cfg, rng.New(200))
	}
	fx.world.Spawn(guardA, core.Vec2{}, cfg, rng.New(201))
	fx.world.Spawn(guardB, core.Vec2{}, cfg, rng.New(202))

	// Phase 1: all villagers rely on guardA.
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(guardA, core.FuncSafety, 0.9)
	}
	for _, g := range []core.AgentID{guardA, guardB} {
		gv, _ := fx.world.AgentOf(g)
		gv.ToM.AdjustRelyOn(g, core.FuncSafety, 0.0)
		for _, vid := range villagerIDs {
			gv.ToM.AdjustRelyOn(vid, core.FuncSafety, 0.0)
		}
	}

	fx.emit.events = nil
	fx.world.relianceScan()
	phase1Count := countRoleEvents(fx.emit.events)
	if phase1Count != 1 {
		t.Fatalf("expected 1 RoleEmerged for guardA, got %d", phase1Count)
	}
	ev1 := lastRoleEvent(fx.emit.events)
	if holder := extractString(ev1.Payload, "holder"); holder != "guard_a" {
		t.Errorf("expected holder=guard_a, got %s", holder)
	}

	// Phase 2: villagers shift reliance to guardB.
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(guardA, core.FuncSafety, -0.9) // remove
		v.ToM.AdjustRelyOn(guardB, core.FuncSafety, 0.9)  // add to B
	}

	fx.emit.events = nil
	fx.world.relianceScan()
	phase2Count := countRoleEvents(fx.emit.events)
	if phase2Count != 1 {
		t.Fatalf("expected 1 RoleEmerged for guardB (succession), got %d", phase2Count)
	}
	ev2 := lastRoleEvent(fx.emit.events)
	if holder := extractString(ev2.Payload, "holder"); holder != "guard_b" {
		t.Errorf("expected holder=guard_b, got %s", holder)
	}
}

// TestScenarioG_BelowThreshold_NoEmission verifies that when the share is below
// the threshold, no RoleEmerged is emitted.
func TestScenarioG_BelowThreshold_NoEmission(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()

	v1 := core.AgentID("v1")
	v2 := core.AgentID("v2")
	v3 := core.AgentID("v3")
	target := core.AgentID("target")

	fx.world.Spawn(v1, core.Vec2{}, cfg, rng.New(300))
	fx.world.Spawn(v2, core.Vec2{}, cfg, rng.New(301))
	fx.world.Spawn(v3, core.Vec2{}, cfg, rng.New(302))
	fx.world.Spawn(target, core.Vec2{}, cfg, rng.New(303))

	// Only 1 out of 4 agents (25%) relies on target — below 0.5 threshold.
	va, _ := fx.world.AgentOf(v1)
	va.ToM.AdjustRelyOn(target, core.FuncSafety, 0.8)

	// v2 and v3 have weak RelyOn on themselves (they don't rely on anyone
	// strongly). Target has NO RelyOn on anyone for Safety (meaning they
	// don't vote). So votes: target=1 (from v1), v2=1, v3=1.
	// Max=1, share=1/4=0.25 < 0.5 → no emission.
	vb, _ := fx.world.AgentOf(v2)
	vb.ToM.AdjustRelyOn(v2, core.FuncSafety, 0.05)
	vc, _ := fx.world.AgentOf(v3)
	vc.ToM.AdjustRelyOn(v3, core.FuncSafety, 0.05)

	// Do NOT set any RelyOn on target (target has no self-reliance or other-reliance).

	fx.emit.events = nil
	fx.world.relianceScan()

	if count := countRoleEvents(fx.emit.events); count != 0 {
		t.Errorf("expected 0 RoleEmerged (share below threshold), got %d", count)
	}
}

// TestScenarioG_Determinism_IdenticalSeed verifies the scan is D12-deterministic.
func TestScenarioG_Determinism_IdenticalSeed(t *testing.T) {
	run := func() []core.Event {
		fx := newFixtureSeeded(t, 99)
		cfg := agent.DefaultConfig()

		ids := []core.AgentID{"b", "a", "c"} // intentionally out of order
		holder := core.AgentID("z")
		for _, id := range ids {
			fx.world.Spawn(id, core.Vec2{}, cfg, rng.New(400))
		}
		fx.world.Spawn(holder, core.Vec2{}, cfg, rng.New(401))

		for _, id := range ids {
			a, _ := fx.world.AgentOf(id)
			a.ToM.AdjustRelyOn(holder, core.FuncSafety, 0.8)
			a.ToM.AdjustRelyOn(id, core.FuncSafety, 0.05)
		}
		h, _ := fx.world.AgentOf(holder)
		h.ToM.AdjustRelyOn(holder, core.FuncSafety, 0.0)
		for _, id := range ids {
			h.ToM.AdjustRelyOn(id, core.FuncSafety, 0.0)
		}

		fx.emit.events = nil
		fx.world.relianceScan()
		out := make([]core.Event, len(fx.emit.events))
		copy(out, fx.emit.events)
		return out
	}

	eventsA := run()
	eventsB := run()

	if len(eventsA) != len(eventsB) {
		t.Fatalf("determinism: event count mismatch: %d vs %d", len(eventsA), len(eventsB))
	}
	for i := range eventsA {
		if eventsA[i].Type != eventsB[i].Type {
			t.Errorf("determinism: event[%d] type mismatch: %s vs %s", i, eventsA[i].Type, eventsB[i].Type)
		}
	}
}

// TestScenarioG_StateRoundTrip_EmergedRolesPreserved verifies that emergedRoles
// survive a capture/restore cycle.
func TestScenarioG_StateRoundTrip_EmergedRolesPreserved(t *testing.T) {
	const seed = int64(42)

	fx := newFixtureSeeded(t, seed)
	cfg := agent.DefaultConfig()

	vIDs := []core.AgentID{"x", "y"}
	holder := core.AgentID("chief")
	for _, id := range append(vIDs, holder) {
		fx.world.Spawn(id, core.Vec2{}, cfg, rng.New(500))
	}

	for _, vid := range vIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(holder, core.FuncSafety, 0.9)
	}
	ch, _ := fx.world.AgentOf(holder)
	ch.ToM.AdjustRelyOn(holder, core.FuncSafety, 0.0)
	for _, vid := range vIDs {
		ch.ToM.AdjustRelyOn(vid, core.FuncSafety, 0.0)
	}

	fx.world.relianceScan()

	// Capture state.
	state := fx.world.State()

	// Restore into a fresh world with same config.
	fx2 := newFixtureSeeded(t, seed)
	for _, id := range append(vIDs, holder) {
		fx2.world.Spawn(id, core.Vec2{}, cfg, rng.New(500))
	}
	fx2.world.RestoreState(state)

	// Verify emergedRoles map matches.
	if len(fx.world.emergedRoles) != len(fx2.world.emergedRoles) {
		t.Fatalf("emergedRoles length mismatch: %d vs %d",
			len(fx.world.emergedRoles), len(fx2.world.emergedRoles))
	}
	for fn, holder := range fx.world.emergedRoles {
		h2, ok := fx2.world.emergedRoles[fn]
		if !ok {
			t.Errorf("function %s missing from restored world", fn)
		} else if h2 != holder {
			t.Errorf("function %s holder mismatch: %s vs %s", fn, holder, h2)
		}
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────────

func countRoleEvents(events []core.Event) int {
	var count int
	for _, ev := range events {
		if ev.Type == "RoleEmerged" {
			count++
		}
	}
	return count
}

func lastRoleEvent(events []core.Event) core.Event {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "RoleEmerged" {
			return events[i]
		}
	}
	return core.Event{}
}

func extractString(payload any, key string) string {
	m, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

// TestScenarioG_Golden_Determinism records a golden digest for Scenario G.
// It runs the scan twice from identical setup and asserts byte-identical events.
func TestScenarioG_Golden_Determinism(t *testing.T) {
	const seed = int64(42)

	run := func() string {
		fx := newFixtureSeeded(t, seed)
		cfg := agent.DefaultConfig()

		// Spawn agents in specific order (D12: IDs sorted internally).
		ids := []core.AgentID{"a", "b", "c", "d", "e"}
		holder := core.AgentID("chief")
		for _, id := range ids {
			fx.world.Spawn(id, core.Vec2{}, cfg, rng.New(800))
		}
		fx.world.Spawn(holder, core.Vec2{}, cfg, rng.New(801))

		// Set up reliance: a,b,c,d (4/5 = 80%) on chief for Safety.
		// Agent 'e' relies on itself.
		for _, id := range ids {
			a, _ := fx.world.AgentOf(id)
			if id == "e" {
				a.ToM.AdjustRelyOn(id, core.FuncSafety, 0.8) // self-reliant
			} else {
				a.ToM.AdjustRelyOn(holder, core.FuncSafety, 0.8)
			}
			// Weak fallback on others.
			for _, other := range ids {
				if other != id {
					a.ToM.AdjustRelyOn(other, core.FuncSafety, 0.05)
				}
			}
		}
		ch, _ := fx.world.AgentOf(holder)
		ch.ToM.AdjustRelyOn(holder, core.FuncSafety, 0.0)
		for _, id := range ids {
			ch.ToM.AdjustRelyOn(id, core.FuncSafety, 0.0)
		}

		fx.emit.events = nil
		fx.world.relianceScan()

		// Build a deterministic digest of the events.
		sortedEvents := make([]core.Event, len(fx.emit.events))
		copy(sortedEvents, fx.emit.events)
		// Already in emission order (which is deterministic), but sort by
		// type+seq to be defensive.
		sort.Slice(sortedEvents, func(i, j int) bool {
			if sortedEvents[i].Type != sortedEvents[j].Type {
				return sortedEvents[i].Type < sortedEvents[j].Type
			}
			return sortedEvents[i].Seq < sortedEvents[j].Seq
		})

		var out string
		for _, ev := range sortedEvents {
			out += ev.Type
			if ev.Type == "RoleEmerged" {
				m, _ := ev.Payload.(map[string]any)
				out += "|" + toString(m["function"])
				out += "|" + toString(m["holder"])
				out += "|" + fmtFloat(toFloat(m["reliance_share"]))
			}
			out += "\n"
		}
		return out
	}

	digestA := run()
	digestB := run()

	if digestA != digestB {
		t.Errorf("GOLDEN DETERMINISM FAILED\nA:\n%s\nB:\n%s", digestA, digestB)
	} else {
		t.Logf("GOLDEN DETERMINISM PASSED\n%s", digestA)
	}
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}

func toFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

func fmtFloat(f float64) string {
	return fmt.Sprintf("%.6f", f)
}
