// Package perception tests cover all SPEC Acceptance Criteria including deterministic
// ordering, LoS occlusion, smell formula, hearing range, config validation, and a
// multi-agent golden snapshot with all three senses exercised.
package perception

import (
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/spatial"
)

// ── Helper: mockWorldSnapshot ────────────────────────────────────────────────────────────────

// mockEntity holds position, tags, and opacity for a mock snapshot.
type mockEntity struct {
	pos    core.Vec2
	tags   []core.Tag
	opaque bool
}

// mockWorldSnapshot implements WorldSnapshot for testing.
type mockWorldSnapshot struct {
	entities map[core.ObjectID]mockEntity
	spatial  *spatial.SpatialHash
}

func newMockWorldSnapshot() *mockWorldSnapshot {
	return &mockWorldSnapshot{
		entities: make(map[core.ObjectID]mockEntity),
		spatial:  spatial.New(8.0),
	}
}

func (m *mockWorldSnapshot) add(id core.ObjectID, pos core.Vec2, tags []core.Tag, opaque bool) {
	m.entities[id] = mockEntity{pos: pos, tags: tags, opaque: opaque}
	m.spatial.Insert(id, pos)
}

func (m *mockWorldSnapshot) EntitiesInRadius(center core.Vec2, radius float64) []PerceivedEntity {
	ents := m.spatial.NearbyEntities(center, radius)
	res := make([]PerceivedEntity, len(ents))
	for i, e := range ents {
		d := center.Distance(e.Pos)
		res[i] = PerceivedEntity{
			ID:       e.ID,
			Pos:      e.Pos,
			Distance: d,
			Tags:     m.Tags(e.ID),
		}
	}
	// The spatial hash already returns sorted by ObjectID; but we sort again for
	// safety (the test helper should match the contract).
	sort.Slice(res, func(i, j int) bool {
		return res[i].ID < res[j].ID
	})
	return res
}

func (m *mockWorldSnapshot) Tags(id core.ObjectID) []core.Tag {
	if e, ok := m.entities[id]; ok {
		cp := make([]core.Tag, len(e.tags))
		copy(cp, e.tags)
		return cp
	}
	return nil
}

func (m *mockWorldSnapshot) IsOpaque(id core.ObjectID) bool {
	if e, ok := m.entities[id]; ok {
		return e.opaque
	}
	return false
}

// ── Test config for all tests ───────────────────────────────────────────────────────────────

var testCfg = PerceptionConfig{
	SightRadius:   18.0,
	SmellRadius:   10.0,
	HearingRadius: 14.0,
}

// ── Sight occlusion tests ───────────────────────────────────────────────────────────────────

func TestSight_Occlusion(t *testing.T) {
	// Observer at (0,0). Target behind an opaque entity.
	// Opaque at (5,0), target at (10,0) → target occluded.
	// Opaque at (5,0), target at (10,0) but opaque removed → target visible.
	world := newMockWorldSnapshot()
	world.add("opaque", core.Vec2{X: 5, Y: 0}, []core.Tag{"opaque"}, true)
	world.add("target", core.Vec2{X: 10, Y: 0}, nil, false)

	idx := spatial.New(8.0)
	idx.Insert("opaque", core.Vec2{X: 5, Y: 0})
	idx.Insert("target", core.Vec2{X: 10, Y: 0})

	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	// With opaque in the way, target should NOT be seen.
	seen := sensor.Sight(observer, world)
	for _, s := range seen {
		if s.ID == "target" {
			t.Fatal("target behind opaque entity was returned by Sight when it should be occluded")
		}
	}

	// Remove opaque from the world (but keep in hash — the world snapshot controls opacity).
	world.entities["opaque"] = mockEntity{pos: core.Vec2{X: 5, Y: 0}, tags: []core.Tag{"opaque"}, opaque: false}
	seen = sensor.Sight(observer, world)
	found := false
	for _, s := range seen {
		if s.ID == "target" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("target WAS expected to be visible after opaque was removed/went non-opaque")
	}
}

func TestSight_Occlusion_OffSegment(t *testing.T) {
	// Opaque entity OFF the segment observer→target should NOT occlude.
	world := newMockWorldSnapshot()
	world.add("opaque", core.Vec2{X: 0, Y: 5}, []core.Tag{"opaque"}, true) // off to the side
	world.add("target", core.Vec2{X: 10, Y: 0}, nil, false)

	idx := spatial.New(8.0)
	idx.Insert("opaque", core.Vec2{X: 0, Y: 5})
	idx.Insert("target", core.Vec2{X: 10, Y: 0})

	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	seen := sensor.Sight(observer, world)
	found := false
	for _, s := range seen {
		if s.ID == "target" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("target should be visible when opaque is off the sight line")
	}
}

func TestSight_Occlusion_TargetIsOpaque(t *testing.T) {
	// A target tagged [opaque] should still be visible (its own opacity never occludes itself).
	world := newMockWorldSnapshot()
	world.add("target", core.Vec2{X: 10, Y: 0}, []core.Tag{"opaque"}, true)

	idx := spatial.New(8.0)
	idx.Insert("target", core.Vec2{X: 10, Y: 0})

	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	seen := sensor.Sight(observer, world)
	found := false
	for _, s := range seen {
		if s.ID == "target" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("target that is itself opaque should still be visible (own opacity does not occlude self)")
	}
}

func TestSight_Occlusion_CollinearTargetBeforeOpaque(t *testing.T) {
	// Target at (3,0) in front of opaque at (7,0). Target should be visible.
	world := newMockWorldSnapshot()
	world.add("target", core.Vec2{X: 3, Y: 0}, nil, false)
	world.add("opaque", core.Vec2{X: 7, Y: 0}, []core.Tag{"opaque"}, true)

	idx := spatial.New(8.0)
	idx.Insert("target", core.Vec2{X: 3, Y: 0})
	idx.Insert("opaque", core.Vec2{X: 7, Y: 0})

	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	seen := sensor.Sight(observer, world)
	found := false
	for _, s := range seen {
		if s.ID == "target" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("target in front of opaque should be visible")
	}
}

// ── Sight radius bound ──────────────────────────────────────────────────────────────────────

func TestSight_RadiusBound(t *testing.T) {
	world := newMockWorldSnapshot()
	world.add("atBoundary", core.Vec2{X: testCfg.SightRadius, Y: 0}, nil, false)
	world.add("beyond", core.Vec2{X: testCfg.SightRadius + 1, Y: 0}, nil, false)
	world.add("observerSelf", core.Vec2{X: 0, Y: 0}, nil, false) // observer's own entity

	idx := spatial.New(8.0)
	idx.Insert("atBoundary", core.Vec2{X: testCfg.SightRadius, Y: 0})
	idx.Insert("beyond", core.Vec2{X: testCfg.SightRadius + 1, Y: 0})
	idx.Insert("observerSelf", core.Vec2{X: 0, Y: 0})

	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}
	seen := sensor.Sight(observer, world)

	foundBoundary := false
	foundBeyond := false
	foundSelf := false
	for _, s := range seen {
		switch s.ID {
		case "atBoundary":
			foundBoundary = true
		case "beyond":
			foundBeyond = true
		case "observerSelf":
			foundSelf = true
		}
	}
	if !foundBoundary {
		t.Error("entity exactly at SightRadius should be returned")
	}
	if foundBeyond {
		t.Error("entity beyond SightRadius should NOT be returned")
	}
	if foundSelf {
		t.Error("observer's own entity (d=0) should be excluded")
	}
}

// ── Sight ordering ──────────────────────────────────────────────────────────────────────────

func TestSight_Ordering(t *testing.T) {
	world := newMockWorldSnapshot()
	// Entities at various distances, inserted in shuffled order.
	world.add("C", core.Vec2{X: 5, Y: 0}, nil, false)  // d=5
	world.add("A", core.Vec2{X: 3, Y: 0}, nil, false)  // d=3
	world.add("B", core.Vec2{X: 3, Y: 0}, nil, false)  // d=3 (tie with A)

	idx := spatial.New(8.0)
	idx.Insert("C", core.Vec2{X: 5, Y: 0})
	idx.Insert("A", core.Vec2{X: 3, Y: 0})
	idx.Insert("B", core.Vec2{X: 3, Y: 0})

	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}
	seen := sensor.Sight(observer, world)

	if len(seen) != 3 {
		t.Fatalf("expected 3 seen entities, got %d", len(seen))
	}
	// Order by Distance ASC; ties by ID ASC → A (d=3), B (d=3), C (d=5)
	expected := []core.ObjectID{"A", "B", "C"}
	for i, s := range seen {
		if s.ID != expected[i] {
			t.Errorf("position %d: expected %s, got %s", i, expected[i], s.ID)
		}
	}
}

// ── Smell no-LoS ────────────────────────────────────────────────────────────────────────────

func TestSmell_NoLOS(t *testing.T) {
	world := newMockWorldSnapshot()
	world.add("scentedBehind", core.Vec2{X: 8, Y: 0}, []core.Tag{"scented"}, false)
	world.add("opaque", core.Vec2{X: 7, Y: 0}, []core.Tag{"opaque"}, true) // wall between

	idx := spatial.New(8.0)
	idx.Insert("scentedBehind", core.Vec2{X: 8, Y: 0})
	idx.Insert("opaque", core.Vec2{X: 7, Y: 0})

	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}
	signals := sensor.Smell(observer, world)

	found := false
	for _, sig := range signals {
		if sig.ID == "scentedBehind" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("scented entity behind opaque wall should still be smelled (smell ignores LoS)")
	}
}

// ── Smell formula ───────────────────────────────────────────────────────────────────────────

func TestSmell_Formula(t *testing.T) {
	world := newMockWorldSnapshot()
	// Entity at observer position (d=0): strength = 1/(1+0) = 1.0
	world.add("atZero", core.Vec2{X: 0, Y: 0}, []core.Tag{"scented"}, false)
	// Entity at d=1: strength = 1/(1+1) = 0.5
	world.add("atOne", core.Vec2{X: 1, Y: 0}, []core.Tag{"scented"}, false)
	// Entity at d=√3 (~1.732): strength = 1/(1+3) = 0.25
	world.add("atSqrt3", core.Vec2{X: math.Sqrt(3), Y: 0}, []core.Tag{"scented"}, false)
	// Entity beyond SmellRadius (d=11 > 10)
	world.add("beyond", core.Vec2{X: 11, Y: 0}, []core.Tag{"scented"}, false)
	// Entity not scented but in range
	world.add("notScented", core.Vec2{X: 3, Y: 0}, nil, false)

	idx := spatial.New(8.0)
	idx.Insert("atZero", core.Vec2{X: 0, Y: 0})
	idx.Insert("atOne", core.Vec2{X: 1, Y: 0})
	idx.Insert("atSqrt3", core.Vec2{X: math.Sqrt(3), Y: 0})
	idx.Insert("beyond", core.Vec2{X: 11, Y: 0})
	idx.Insert("notScented", core.Vec2{X: 3, Y: 0})

	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}
	signals := sensor.Smell(observer, world)

	// Build lookup.
	strengths := make(map[core.ObjectID]float64)
	for _, sig := range signals {
		strengths[sig.ID] = sig.Strength
	}

	tests := []struct {
		id       core.ObjectID
		expected float64
		present  bool
	}{
		{"atZero", 1.0, true},
		{"atOne", 0.5, true},
		{"atSqrt3", 0.25, true},
		{"beyond", 0, false},
		{"notScented", 0, false},
	}

	for _, tc := range tests {
		s, found := strengths[tc.id]
		if tc.present && !found {
			t.Errorf("%s should be present but was not", tc.id)
			continue
		}
		if !tc.present && found {
			t.Errorf("%s should NOT be present but was (strength=%v)", tc.id, s)
			continue
		}
		if tc.present {
			// Compare with epsilon.
			diff := math.Abs(s - tc.expected)
			if diff > 1e-12 {
				t.Errorf("%s strength: expected %v, got %v (diff=%v)", tc.id, tc.expected, s, diff)
			}
		}
	}
}

// ── Smell ordering ──────────────────────────────────────────────────────────────────────────

func TestSmell_Ordering(t *testing.T) {
	world := newMockWorldSnapshot()
	// A: d=0 → strength=1.0
	// B: d=0 → strength=1.0 (tie)
	// C: d=1 → strength=0.5
	world.add("C", core.Vec2{X: 1, Y: 0}, []core.Tag{"scented"}, false)
	world.add("A", core.Vec2{X: 0, Y: 0}, []core.Tag{"scented"}, false)
	world.add("B", core.Vec2{X: 0, Y: 0}, []core.Tag{"scented"}, false)

	idx := spatial.New(8.0)
	idx.Insert("C", core.Vec2{X: 1, Y: 0})
	idx.Insert("A", core.Vec2{X: 0, Y: 0})
	idx.Insert("B", core.Vec2{X: 0, Y: 0})

	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}
	signals := sensor.Smell(observer, world)

	if len(signals) != 3 {
		t.Fatalf("expected 3 scent signals, got %d", len(signals))
	}
	// Strength DESC, ties by ID ASC → A (1.0), B (1.0), C (0.5)
	expected := []core.ObjectID{"A", "B", "C"}
	for i, sig := range signals {
		if sig.ID != expected[i] {
			t.Errorf("position %d: expected %s, got %s (strength=%v)", i, expected[i], sig.ID, sig.Strength)
		}
	}
}

// ── Hearing range ───────────────────────────────────────────────────────────────────────────

func TestHearing_Range(t *testing.T) {
	sensor := NewSensor(spatial.New(8.0), testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	events := []SoundEvent{
		{SourceID: "atBoundary", ActionID: "TestAction", Pos: core.Vec2{X: testCfg.HearingRadius, Y: 0}},
		{SourceID: "beyond", ActionID: "TestAction", Pos: core.Vec2{X: testCfg.HearingRadius + 1, Y: 0}},
		{SourceID: "behindWall", ActionID: "TestAction", Pos: core.Vec2{X: 5, Y: 0}},
	}

	heard := sensor.Hearing(observer, events)

	foundBoundary := false
	foundBehindWall := false
	foundBeyond := false
	for _, e := range heard {
		switch e.SourceID {
		case "atBoundary":
			foundBoundary = true
			if e.Distance != testCfg.HearingRadius {
				t.Errorf("atBoundary distance: expected %v, got %v", testCfg.HearingRadius, e.Distance)
			}
		case "behindWall":
			foundBehindWall = true
		case "beyond":
			foundBeyond = true
		}
	}
	if !foundBoundary {
		t.Error("event exactly at HearingRadius should be heard")
	}
	if !foundBehindWall {
		t.Error("event behind opaque wall should still be heard (no LoS check)")
	}
	if foundBeyond {
		t.Error("event beyond HearingRadius should NOT be heard")
	}
}

// ── Hearing ordering ────────────────────────────────────────────────────────────────────────

func TestHearing_Ordering(t *testing.T) {
	sensor := NewSensor(spatial.New(8.0), testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	events := []SoundEvent{
		{SourceID: "C", ActionID: "A", Pos: core.Vec2{X: 10, Y: 0}},  // d=10
		{SourceID: "A", ActionID: "A", Pos: core.Vec2{X: 5, Y: 0}},   // d=5
		{SourceID: "B", ActionID: "A", Pos: core.Vec2{X: 5, Y: 0}},   // d=5 (tie)
		{SourceID: "D", ActionID: "A", Pos: core.Vec2{X: 20, Y: 0}},  // beyond range
	}

	heard := sensor.Hearing(observer, events)
	if len(heard) != 3 {
		t.Fatalf("expected 3 heard events, got %d", len(heard))
	}
	// Distance ASC, ties by SourceID ASC → A (d=5), B (d=5), C (d=10)
	expected := []core.ObjectID{"A", "B", "C"}
	for i, e := range heard {
		if e.SourceID != expected[i] {
			t.Errorf("position %d: expected %s, got %s", i, expected[i], e.SourceID)
		}
	}

	// Verify input slice was not mutated.
	if len(events) != 4 {
		t.Error("input events slice was mutated")
	}
	if events[3].SourceID != "D" {
		t.Error("input events slice contents were modified")
	}
}

func TestHearing_InputNotMutated(t *testing.T) {
	sensor := NewSensor(spatial.New(8.0), testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	events := []SoundEvent{
		{SourceID: "A", ActionID: "Test", Pos: core.Vec2{X: 5, Y: 0}, Distance: 999},
	}
	_ = sensor.Hearing(observer, events)
	if events[0].Distance != 999 {
		t.Error("input event Distance was mutated: Hearing should only set Distance on returned copies")
	}
}

// ── Config load ─────────────────────────────────────────────────────────────────────────────

func TestLoadConfig_Valid(t *testing.T) {
	yaml := `perception:
  sight_radius: 18.0
  smell_radius: 10.0
  hearing_radius: 14.0
`
	cfg, err := LoadConfig(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SightRadius != 18.0 {
		t.Errorf("SightRadius: expected 18.0, got %v", cfg.SightRadius)
	}
	if cfg.SmellRadius != 10.0 {
		t.Errorf("SmellRadius: expected 10.0, got %v", cfg.SmellRadius)
	}
	if cfg.HearingRadius != 14.0 {
		t.Errorf("HearingRadius: expected 14.0, got %v", cfg.HearingRadius)
	}
}

func TestLoadConfig_MissingBlock(t *testing.T) {
	yaml := `world:
  tick_minutes: 1
`
	_, err := LoadConfig(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing perception block, got nil")
	}
	if !strings.Contains(err.Error(), "perception:") {
		t.Errorf("error should mention 'perception:', got %v", err)
	}
}

func TestLoadConfig_ZeroRadius(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"zero sight", "perception:\n  sight_radius: 0\n  smell_radius: 10.0\n  hearing_radius: 14.0\n"},
		{"neg sight", "perception:\n  sight_radius: -1\n  smell_radius: 10.0\n  hearing_radius: 14.0\n"},
		{"zero smell", "perception:\n  sight_radius: 18.0\n  smell_radius: 0\n  hearing_radius: 14.0\n"},
		{"zero hearing", "perception:\n  sight_radius: 18.0\n  smell_radius: 10.0\n  hearing_radius: 0\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfig(strings.NewReader(tc.yaml))
			if err == nil {
				t.Fatal("expected error for zero/negative radius, got nil")
			}
		})
	}
}

// ── No persistent state ─────────────────────────────────────────────────────────────────────

func TestNoPersistentState(t *testing.T) {
	sensor := NewSensor(spatial.New(8.0), testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	// First call with events.
	events := []SoundEvent{
		{SourceID: "A", ActionID: "Test", Pos: core.Vec2{X: 5, Y: 0}},
	}
	heard1 := sensor.Hearing(observer, events)
	if len(heard1) != 1 {
		t.Fatal("expected 1 heard event on first call")
	}

	// Second call with empty slice.
	heard2 := sensor.Hearing(observer, nil)
	if len(heard2) != 0 {
		t.Fatal("expected empty result on second call (no events persisted)")
	}
}

func TestStatelessDeterminism(t *testing.T) {
	// Two identically-configured sensors produce identical results.
	idx := spatial.New(8.0)
	idx.Insert("target", core.Vec2{X: 5, Y: 0})

	world := newMockWorldSnapshot()
	world.add("target", core.Vec2{X: 5, Y: 0}, []core.Tag{"scented"}, false)

	s1 := NewSensor(idx, testCfg)
	s2 := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	sight1 := s1.Sight(observer, world)
	sight2 := s2.Sight(observer, world)
	if len(sight1) != len(sight2) {
		t.Fatalf("sight lengths differ: %d vs %d", len(sight1), len(sight2))
	}
	for i := range sight1 {
		if sight1[i].ID != sight2[i].ID || sight1[i].Distance != sight2[i].Distance {
			t.Errorf("Sight result %d differs: %v vs %v", i, sight1[i], sight2[i])
		}
	}
}

// ── Golden snapshot ─────────────────────────────────────────────────────────────────────────

func TestGoldenSnapshot(t *testing.T) {
	// 3-agent scenario:
	//   observer at (0,0)
	//   targetA at (5,0) — visible (in front of opaque), scented (d=5)
	//   opaque at (7,0) — blocks sight to targetB
	//   targetB at (10,0) — occluded behind opaque, scented (d=10, at SmellRadius boundary)
	//   scentFar at (8,0) — scented (d=8, within SmellRadius=10)
	//   soundNear at (5,0) — produced by action "Hunt" (d=5, within HearingRadius=14)
	//   soundFar at (20,0) — produced by action "Hunt" (d=20, beyond HearingRadius=14)
	observer := core.Vec2{X: 0, Y: 0}
	targetAPos := core.Vec2{X: 5, Y: 0}
	opaquePos := core.Vec2{X: 7, Y: 0}
	targetBPos := core.Vec2{X: 10, Y: 0}
	scentFarPos := core.Vec2{X: 8, Y: 0}
	soundNearPos := core.Vec2{X: 5, Y: 0}
	soundFarPos := core.Vec2{X: 20, Y: 0}

	world := newMockWorldSnapshot()
	world.add("targetA", targetAPos, []core.Tag{"scented"}, false)
	world.add("opaque", opaquePos, []core.Tag{"opaque"}, true)
	world.add("targetB", targetBPos, []core.Tag{"scented"}, false)
	world.add("scentFar", scentFarPos, []core.Tag{"scented"}, false)

	idx := spatial.New(8.0)
	idx.Insert("targetA", targetAPos)
	idx.Insert("opaque", opaquePos)
	idx.Insert("targetB", targetBPos)
	idx.Insert("scentFar", scentFarPos)

	sensor := NewSensor(idx, testCfg)

	// Sight
	sight := sensor.Sight(observer, world)

	// Smell
	smell := sensor.Smell(observer, world)

	// Hearing
	events := []SoundEvent{
		{SourceID: "soundNear", ActionID: actions.ActionID("Hunt"), Pos: soundNearPos},
		{SourceID: "soundFar", ActionID: actions.ActionID("Hunt"), Pos: soundFarPos},
	}
	hearing := sensor.Hearing(observer, events)

	// Build golden summary as a string for stable comparison.
	var sb strings.Builder
	sb.WriteString("SIGHT:\n")
	for _, s := range sight {
		sb.WriteString(string(s.ID) + "\n")
	}
	sb.WriteString("SMELL:\n")
	for _, s := range smell {
		sb.WriteString(string(s.ID) + "\n")
	}
	sb.WriteString("HEARING:\n")
	for _, h := range hearing {
		sb.WriteString(string(h.SourceID) + "\n")
	}

	expected := `SIGHT:
targetA
opaque
SMELL:
targetA
scentFar
targetB
HEARING:
soundNear
`

	got := sb.String()
	if got != expected {
		t.Errorf("Golden snapshot mismatch:\n--- expected ---\n%s\n--- got ---\n%s", expected, got)
	}
}

// ── Additional SPEC invariants ──────────────────────────────────────────────────────────────

func TestNewSensor_NilPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewSensor with nil index should panic")
		}
	}()
	NewSensor(nil, testCfg)
}

func TestSmell_NonScentedExcluded(t *testing.T) {
	world := newMockWorldSnapshot()
	world.add("noScent", core.Vec2{X: 3, Y: 0}, []core.Tag{"someOtherTag"}, false)

	idx := spatial.New(8.0)
	idx.Insert("noScent", core.Vec2{X: 3, Y: 0})

	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	signals := sensor.Smell(observer, world)
	if len(signals) != 0 {
		t.Fatal("non-scented entity should not appear in smell results")
	}
}

func TestSight_TagsCopied(t *testing.T) {
	world := newMockWorldSnapshot()
	world.add("entity", core.Vec2{X: 5, Y: 0}, []core.Tag{"opaque", "test"}, false)

	idx := spatial.New(8.0)
	idx.Insert("entity", core.Vec2{X: 5, Y: 0})

	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	seen := sensor.Sight(observer, world)
	if len(seen) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(seen))
	}
	if len(seen[0].Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d tags: %v", len(seen[0].Tags), seen[0].Tags)
	}
	// Modify the returned tags to verify they are a copy.
	seen[0].Tags[0] = "modified"
	// The world should still have the original tags.
	origTags := world.Tags("entity")
	if origTags[0] != "opaque" {
		t.Error("Tags returned from Sight should be a copy; mutating them should not affect world snapshot")
	}
}

func TestHearing_EmptyInput(t *testing.T) {
	sensor := NewSensor(spatial.New(8.0), testCfg)
	observer := core.Vec2{X: 0, Y: 0}

	heard := sensor.Hearing(observer, nil)
	if heard != nil {
		t.Error("Hearing(nil) should return nil")
	}
	heard = sensor.Hearing(observer, []SoundEvent{})
	if heard != nil {
		t.Error("Hearing([]) should return nil")
	}
}

func TestSight_EmptyWorld(t *testing.T) {
	world := newMockWorldSnapshot()
	idx := spatial.New(8.0)
	sensor := NewSensor(idx, testCfg)
	observer := core.Vec2{X: 0, Y: 0}
	seen := sensor.Sight(observer, world)
	if seen != nil {
		t.Error("Sight in empty world should return nil")
	}
}
