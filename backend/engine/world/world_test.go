package world

import (
	"fmt"
	"sort"
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
	"github.com/dogring/bdg/engine/mind/tom"
	"github.com/dogring/bdg/engine/mind/values"
	"github.com/dogring/bdg/engine/kernel/worldtime"
)

// ── Test helpers ───────────────────────────────────────────────────────────────

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
  - id: Vindictiveness
    kind: disposition
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.3
`

type testFixture struct {
	world   *World
	regs    testRegs
	rootRNG *rng.RNG
	clock   worldtime.Clock
	svc     Services
	actReg  *actions.Registry
	emit    *recordingEmitter
}

type testRegs struct {
	stats   *stats.Registry
	needs   *needs.Registry
	values  *values.Config
	actions *actions.Registry
	gates   *gates.Registry
}

type recordingEmitter struct {
	events []core.Event
}

func (r *recordingEmitter) Emit(e core.Event) {
	r.events = append(r.events, e)
}

func mustLoadStats(t *testing.T, yamlStr string) *stats.Registry {
	t.Helper()
	reg, err := stats.Load(strings.NewReader(yamlStr))
	if err != nil {
		t.Fatalf("stats.Load: %v", err)
	}
	return reg
}

// newFixtureSeeded creates a fixture with a specific root RNG seed.
func newFixtureSeeded(t *testing.T, seed int64) *testFixture {
	t.Helper()

	statReg := mustLoadStats(t, testStatsYAML)

	needsYAML := `schema_version: 1
needs:
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Rest
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.45, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`
	balanceYAML := `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
  Rest: { decay_per_tick: 0.00045, satisfaction_threshold: 0.45 }
values:
  weights:
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
  - id: RestAction
    tags: [effort:none]
    duration: 10
    produces: [has_Rest]
    effect_per_minute: { Rest: 0.01 }
  - id: Forage
    tags: [effort:med, uses:Agility]
    duration: 12
    produces: [has_food]
  - id: Eat
    tags: [effort:low]
    duration: 6
    requires: [has_food]
    produces: [has_Satiety]
    effect: { Satiety: 0.5 }
  - id: MoveTo
    tags: [effort:low, uses:Agility]
    duration: 5
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

	plannerCfg := planner.PlannerConfig{
		Budget:             planner.Budget{MaxDepth: 6, MaxActions: 16, MaxNodes: 256},
		BaseHorizonTicks:   720,
		UrgencyThreshold:   0.65,
		LookaheadThreshold: 0.4, // content/balance.yaml intelligence.lookahead_threshold (P5)
		TagCosts: map[core.Tag]float64{
			"effort:low": 0.20, "effort:med": 0.50, "effort:none": 0.0,
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

// spawnTwoAgents spawns two agents at fixed positions with deterministic RNGs.
func spawnTwoAgents(t *testing.T, fx *testFixture, seed int64) {
	t.Helper()
	cfg := agent.DefaultConfig()
	fx.world.Spawn("agent_a", core.Vec2{X: 10, Y: 10}, cfg, rng.New(seed))
	fx.world.Spawn("agent_b", core.Vec2{X: 20, Y: 20}, cfg, rng.New(seed+1))
}

// ── State digest helpers ───────────────────────────────────────────────────────

func worldDigest(w *World) string {
	ws := w.State()
	var b strings.Builder
	fmt.Fprintf(&b, "tick=%d\n", int64(ws.Tick))

	for _, d := range ws.Agents {
		b.WriteString("agent ")
		b.WriteString(string(d.ID))
		fmt.Fprintf(&b, " pos=%.4f,%.4f", d.Pos.X, d.Pos.Y)
		fmt.Fprintf(&b, " stamina=%.6f mood=%.6f adr=%.6f",
			d.Stamina, d.Mood, d.Adrenaline)
		fmt.Fprintf(&b, " goal=%s coping=%d plan=[",
			string(d.Goal), int(d.Coping))
		for i, a := range d.PlanActions {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(a)
		}
		b.WriteString("]")
		b.WriteString(" stats={")
		first := true
		for _, sid := range sortStatIDs(d.RealStats) {
			if !first {
				b.WriteByte(',')
			}
			first = false
			fmt.Fprintf(&b, "%s=%.6f", string(sid), d.RealStats.Get(sid))
		}
		b.WriteString("}")
		b.WriteString(" needs={")
		first = true
		for _, dim := range sortDims(d.NeedIntensities) {
			if !first {
				b.WriteByte(',')
			}
			first = false
			fmt.Fprintf(&b, "%s=%.6f", string(dim), d.NeedIntensities[dim])
		}
		b.WriteString("}")
		if d.SelfEstStats != nil {
			b.WriteString(" tom={")
			first = true
			for _, sid := range sortSDistIDs(d.SelfEstStats) {
				sd := d.SelfEstStats[sid]
				if !first {
					b.WriteByte(',')
				}
				first = false
				fmt.Fprintf(&b, "%s=%.6f", string(sid), sd.Mean)
			}
			b.WriteString("}")
		}
		b.WriteString("\n")
	}

	for _, obj := range ws.Objects {
		fmt.Fprintf(&b, "object %s pos=%.4f,%.4f\n",
			string(obj.ID), obj.Pos.X, obj.Pos.Y)
	}

	b.WriteString("rng=")
	if len(ws.RNGState.Data) >= 20 {
		b.WriteString(ws.RNGState.Data[:20])
	}
	b.WriteString("\n")
	return b.String()
}

func sortStatIDs(s stats.Stats) []core.StatID {
	ids := make([]core.StatID, 0, len(s))
	for sid := range s {
		ids = append(ids, sid)
	}
	sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
	return ids
}

func sortSDistIDs(m map[core.StatID]tom.StatDist) []core.StatID {
	ids := make([]core.StatID, 0, len(m))
	for sid := range m {
		ids = append(ids, sid)
	}
	sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
	return ids
}

func sortDims(m map[core.Dimension]float64) []core.Dimension {
	ids := make([]core.Dimension, 0, len(m))
	for dim := range m {
		ids = append(ids, dim)
	}
	sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
	return ids
}

// ── Unit tests ─────────────────────────────────────────────────────────────────

func TestNew(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	if fx.world.CurrentTick() != 0 {
		t.Errorf("expected tick 0, got %v", fx.world.CurrentTick())
	}
	if len(fx.world.AgentIDs()) != 0 {
		t.Errorf("expected 0 agents, got %v", len(fx.world.AgentIDs()))
	}
}

func TestSpawn(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()
	spawnRNG := rng.New(100)

	a := fx.world.Spawn("agent_1", core.Vec2{X: 10, Y: 10}, cfg, spawnRNG)
	if a == nil {
		t.Fatal("expected non-nil agent")
	}
	if a.ID != "agent_1" {
		t.Errorf("expected ID agent_1, got %v", a.ID)
	}
	if ids := fx.world.AgentIDs(); len(ids) != 1 || ids[0] != "agent_1" {
		t.Errorf("expected agent_1 in AgentIDs, got %v", ids)
	}
	if pos, ok := fx.world.spatial.PosOf(core.ObjectID("agent_1")); !ok || pos.X != 10 || pos.Y != 10 {
		t.Errorf("agent not at expected position: %v, %v", pos, ok)
	}
	if _, ok := a.ToM.Self("agent_1"); !ok {
		t.Error("expected ToM[self] to exist")
	}
}

func TestSpawnDeterminism(t *testing.T) {
	fx1 := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()
	a1 := fx1.world.Spawn("agent_1", core.Vec2{}, cfg, rng.New(42))

	fx2 := newFixtureSeeded(t, 42)
	a2 := fx2.world.Spawn("agent_1", core.Vec2{}, cfg, rng.New(42))

	for _, sid := range fx1.regs.stats.IDs() {
		if a1.RealStats.Get(sid) != a2.RealStats.Get(sid) {
			t.Errorf("stat %v mismatch: %v vs %v", sid, a1.RealStats.Get(sid), a2.RealStats.Get(sid))
		}
	}
}

func TestPlaceObject(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	supply := map[core.Dimension]float64{"Satiety": 0.5}
	fx.world.PlaceObject("berry_bush_1", "berry_bush", core.Vec2{X: 5, Y: 5}, supply)

	entities := fx.world.spatial.NearbyEntities(core.Vec2{X: 5, Y: 5}, 1.0)
	found := false
	for _, e := range entities {
		if e.ID == "berry_bush_1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("object not found in spatial hash")
	}
}

func TestRemoveObject(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	fx.world.PlaceObject("obj_1", "rock", core.Vec2{}, nil)
	fx.world.RemoveObject("obj_1")
	if _, ok := fx.world.spatial.PosOf("obj_1"); ok {
		t.Error("object should be removed from spatial hash")
	}
}

func TestConflictResolution_HigherStatWins(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()
	a1 := fx.world.Spawn("agent_1", core.Vec2{X: 0, Y: 0}, cfg, rng.New(1))
	a2 := fx.world.Spawn("agent_2", core.Vec2{X: 1, Y: 1}, cfg, rng.New(2))
	a1.RealStats["Agility"] = 80
	a2.RealStats["Agility"] = 30

	intents := []agent.Intent{
		{Kind: agent.IntentStart, Agent: "agent_1", Action: "MoveTo", Target: "obj_1", Tick: 0},
		{Kind: agent.IntentStart, Agent: "agent_2", Action: "MoveTo", Target: "obj_1", Tick: 0},
	}
	groups := fx.world.buildConflictGroups(intents)
	if fx.world.isConflictLoser(0, intents[0], groups, intents) {
		t.Error("agent_1 (higher stat) should win")
	}
	if !fx.world.isConflictLoser(1, intents[1], groups, intents) {
		t.Error("agent_2 (lower stat) should lose")
	}
}

func TestConflictResolution_TieBreakByAgentID(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()
	a1 := fx.world.Spawn("agent_a", core.Vec2{}, cfg, rng.New(1))
	a2 := fx.world.Spawn("agent_b", core.Vec2{}, cfg, rng.New(2))
	a1.RealStats["Agility"] = 50
	a2.RealStats["Agility"] = 50

	intents := []agent.Intent{
		{Kind: agent.IntentStart, Agent: "agent_a", Action: "MoveTo", Target: "obj_1", Tick: 0},
		{Kind: agent.IntentStart, Agent: "agent_b", Action: "MoveTo", Target: "obj_1", Tick: 0},
	}
	groups := fx.world.buildConflictGroups(intents)
	if fx.world.isConflictLoser(0, intents[0], groups, intents) {
		t.Error("agent_a (lower AgentID) should win tie")
	}
	if !fx.world.isConflictLoser(1, intents[1], groups, intents) {
		t.Error("agent_b (higher AgentID) should lose tie")
	}
}

func TestOutcomeResolution_AgainstRealStats(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()
	a := fx.world.Spawn("agent_1", core.Vec2{}, cfg, rng.New(1))
	a.RealStats["Agility"] = 80

	intent := agent.Intent{Kind: agent.IntentStart, Agent: "agent_1", Action: "Forage", Tick: 0}
	outcome := fx.world.resolveOutcome(intent, agent.Succeeded, rng.New(0))
	if outcome.Status == agent.Failed {
		t.Errorf("high-stat agent should succeed, got %v", outcome.Status)
	}
}

func TestOutcomeResolution_LowStatsFail(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()
	a := fx.world.Spawn("agent_1", core.Vec2{}, cfg, rng.New(1))
	a.RealStats["Agility"] = 5

	intent := agent.Intent{Kind: agent.IntentStart, Agent: "agent_1", Action: "Forage", Tick: 0}
	outcome := fx.world.resolveOutcome(intent, agent.Succeeded, rng.New(0))
	if outcome.Status != agent.Failed {
		t.Errorf("low-stat agent should fail, got %v", outcome.Status)
	}
}

func TestTick_EmitsTickDone(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()
	fx.world.Spawn("agent_1", core.Vec2{X: 10, Y: 10}, cfg, rng.New(1))
	fx.emit.events = nil
	fx.world.Tick()

	foundTickDone := false
	for _, ev := range fx.emit.events {
		if ev.Type == "TickDone" {
			foundTickDone = true
			break
		}
	}
	if !foundTickDone {
		t.Error("expected TickDone event")
	}
}

// ── Golden determinism test ────────────────────────────────────────────────────

// TestDeterminismGolden_Seed1_10Ticks runs 10 ticks with seed 1 twice and asserts
// byte-identical world-state digests.
func TestDeterminismGolden_Seed1_10Ticks(t *testing.T) {
	const seed = int64(1)
	const ticks = 10

	fxA := newFixtureSeeded(t, seed)
	spawnTwoAgents(t, fxA, seed)
	for range ticks {
		fxA.world.Tick()
	}
	digestA := worldDigest(fxA.world)

	fxB := newFixtureSeeded(t, seed)
	spawnTwoAgents(t, fxB, seed)
	for range ticks {
		fxB.world.Tick()
	}
	digestB := worldDigest(fxB.world)

	if digestA != digestB {
		t.Errorf("GOLDEN DETERMINISM FAILED at seed=%d ticks=%d", seed, ticks)
		t.Logf("DIGEST A:\n%s", digestA)
		t.Logf("DIGEST B:\n%s", digestB)
	} else {
		t.Logf("GOLDEN DETERMINISM PASSED: seed=%d, %d ticks, %d bytes",
			seed, ticks, len(digestA))
		t.Logf("GOLDEN DIGEST:\n%s", digestA)
	}
}

// ── Resume invariant test ──────────────────────────────────────────────────────

// TestResumeInvariant_Tick5Snapshot_Tick10Matches verifies the resume invariant
// (testing.md §1): capturing state at tick 5 via State(), restoring into a fresh
// world via RestoreState(), and running to tick 10 produces byte-identical state
// to running continuously from 0 to 10.
func TestResumeInvariant_Tick5Snapshot_Tick10Matches(t *testing.T) {
	const seed = int64(7)

	// Path A: run 10 ticks continuously.
	fxA := newFixtureSeeded(t, seed)
	spawnTwoAgents(t, fxA, seed)
	for range 10 {
		fxA.world.Tick()
	}
	digestA := worldDigest(fxA.world)

	// Path B: run 5 ticks, capture state, resume, run 5 more.
	fxB := newFixtureSeeded(t, seed)
	spawnTwoAgents(t, fxB, seed)
	for range 5 {
		fxB.world.Tick()
	}
	stateAt5 := fxB.world.State()

	// Restore into a fresh world C.
	fxC := newFixtureSeeded(t, seed)
	spawnTwoAgents(t, fxC, seed)
	fxC.world.RestoreState(stateAt5)

	if got := fxC.world.CurrentTick(); got != 5 {
		t.Fatalf("after RestoreState: expected tick 5, got %v", got)
	}

	for range 5 {
		fxC.world.Tick()
	}

	if got := fxC.world.CurrentTick(); got != 10 {
		t.Fatalf("after resume+5 ticks: expected tick 10, got %v", got)
	}

	digestC := worldDigest(fxC.world)

	if digestA != digestC {
		t.Errorf("RESUME INVARIANT FAILED: continuous 0→10 diverges from resume 5→10")
		t.Logf("DIGEST A (continuous 0→10):\n%s", digestA)
		t.Logf("DIGEST C (resumed 5→10):\n%s", digestC)
	} else {
		t.Logf("RESUME INVARIANT PASSED: continuous 0→10 = resume 5→10 (%d bytes)", len(digestA))
	}
}
