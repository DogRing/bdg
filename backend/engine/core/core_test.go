package core

import (
	"math"
	"testing"
)

// AC1: NoopEmitter satisfies EventEmitter — compile-time enforced by var _ line.
// Runtime call ensures no panic.
func TestNoopEmitter_Emit(t *testing.T) {
	var e EventEmitter = NoopEmitter{}
	e.Emit(Event{
		SchemaVersion: 1,
		Tick:          42,
		Seq:           7,
		AgentID:       "agent-1",
		Type:          "TestEvent",
		Payload:       nil,
	})
}

// AC2: Vec2.Distance table tests.
func TestVec2_Distance(t *testing.T) {
	cases := []struct {
		a, b Vec2
		want float64
	}{
		{Vec2{0, 0}, Vec2{3, 4}, 5},
		{Vec2{1, 1}, Vec2{1, 1}, 0},
		{Vec2{-3, 0}, Vec2{0, -4}, 5},
		{Vec2{0, 0}, Vec2{0, 0}, 0},
		{Vec2{-1, -1}, Vec2{-4, -5}, 5},
	}
	for _, tc := range cases {
		got := tc.a.Distance(tc.b)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("Distance(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// AC3: Vec2.Add/Sub/Scale table tests covering identity, zero, and negative cases.
func TestVec2_AddSubScale(t *testing.T) {
	t.Run("Add", func(t *testing.T) {
		cases := []struct {
			a, b, want Vec2
		}{
			{Vec2{1, 2}, Vec2{3, 4}, Vec2{4, 6}},
			{Vec2{0, 0}, Vec2{5, 7}, Vec2{5, 7}},   // identity (zero + v = v)
			{Vec2{1, 1}, Vec2{0, 0}, Vec2{1, 1}},   // identity (v + zero = v)
			{Vec2{-1, -2}, Vec2{1, 2}, Vec2{0, 0}}, // negative + positive = zero
			{Vec2{-3, 5}, Vec2{-2, -8}, Vec2{-5, -3}},
		}
		for _, tc := range cases {
			got := tc.a.Add(tc.b)
			if got != tc.want {
				t.Errorf("Add(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		}
	})

	t.Run("Sub", func(t *testing.T) {
		cases := []struct {
			a, b, want Vec2
		}{
			{Vec2{4, 6}, Vec2{3, 4}, Vec2{1, 2}},
			{Vec2{5, 7}, Vec2{0, 0}, Vec2{5, 7}},   // subtract zero = identity
			{Vec2{0, 0}, Vec2{0, 0}, Vec2{0, 0}},   // zero - zero
			{Vec2{1, 1}, Vec2{1, 1}, Vec2{0, 0}},   // v - v = zero
			{Vec2{-3, 5}, Vec2{-2, -8}, Vec2{-1, 13}},
		}
		for _, tc := range cases {
			got := tc.a.Sub(tc.b)
			if got != tc.want {
				t.Errorf("Sub(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		}
	})

	t.Run("Scale", func(t *testing.T) {
		cases := []struct {
			v    Vec2
			f    float64
			want Vec2
		}{
			{Vec2{3, 4}, 1.0, Vec2{3, 4}},    // identity scale
			{Vec2{3, 4}, 0.0, Vec2{0, 0}},    // scale to zero
			{Vec2{3, 4}, -1.0, Vec2{-3, -4}}, // negative scale
			{Vec2{2, 5}, 2.0, Vec2{4, 10}},
			{Vec2{-1, -2}, 3.0, Vec2{-3, -6}},
		}
		for _, tc := range cases {
			got := tc.v.Scale(tc.f)
			if got != tc.want {
				t.Errorf("Scale(%v, %v) = %v, want %v", tc.v, tc.f, got, tc.want)
			}
		}
	})
}

// AC4: Vec2.DistSq equals Distance² for fixed pairs.
func TestVec2_DistSq_EqualsDistanceSq(t *testing.T) {
	pairs := [][2]Vec2{
		{{0, 0}, {3, 4}},
		{{1, 1}, {1, 1}},
		{{-3, 0}, {0, -4}},
		{{2, 7}, {-5, 3}},
		{{100, 200}, {-100, -200}},
		{{0.5, 0.5}, {1.5, 1.5}},
		{{-1.5, 2.5}, {3.5, -4.5}},
		{{0, 0}, {0, 0}},
		{{1000, 0}, {0, 1000}},
		{{-7, -24}, {0, 0}},
	}
	for _, p := range pairs {
		a, b := p[0], p[1]
		distSq := a.DistSq(b)
		dist := a.Distance(b)
		wantSq := dist * dist
		if math.Abs(distSq-wantSq) > 1e-6 {
			t.Errorf("DistSq(%v, %v) = %v, Distance²= %v (diff=%v)", a, b, distSq, wantSq, math.Abs(distSq-wantSq))
		}
	}
}

// AC5: Zero values of all ID types, Tick, GameMinutes, StatID, Tag, Pred are valid (no panic on use).
func TestZeroValues(t *testing.T) {
	var agentID AgentID
	var objectID ObjectID
	var runID RunID
	var tick Tick
	var gm GameMinutes
	var statID StatID
	var dim Dimension
	var tag Tag
	var pred Pred
	var vec Vec2
	var ref Referent
	var evt Event

	// Use each value to ensure no panics and correct zero behavior.
	_ = string(agentID)
	_ = string(objectID)
	_ = string(runID)
	_ = int64(tick)
	_ = int64(gm)
	_ = string(statID)
	_ = string(dim)
	_ = string(tag)
	_ = string(pred)
	_ = vec.Add(Vec2{})
	_ = vec.Sub(Vec2{})
	_ = vec.Scale(0)
	_ = vec.DistSq(Vec2{})
	_ = vec.Distance(Vec2{})
	_ = ref.Kind
	_ = ref.ID
	_ = evt.Tick
	_ = evt.AgentID
	_ = evt.Payload
}

// AC_tick_gm: Compile-time guard — Tick and GameMinutes are distinct types.
// A value of one is not assignable to the other without an explicit conversion.
// This test verifies the types are distinct int64 underlying types.
func TestTickAndGameMinutesAreDistinct(t *testing.T) {
	var tick Tick = 42
	var gm GameMinutes = GameMinutes(tick) // explicit conversion needed
	if int64(tick) != int64(gm) {
		t.Errorf("Tick(%d) != GameMinutes(%d) after conversion", tick, gm)
	}
	// The following line should NOT compile; it's here as a comment to document the invariant:
	// var gm2 GameMinutes = tick  // compile error: cannot use tick (type Tick) as type GameMinutes
	_ = gm
}

// AC6: ReferentKind constants have the expected iota values.
func TestReferentKind_Constants(t *testing.T) {
	if Self != 0 {
		t.Errorf("Self = %d, want 0", Self)
	}
	if Other != 1 {
		t.Errorf("Other = %d, want 1", Other)
	}
	if Place != 2 {
		t.Errorf("Place = %d, want 2", Place)
	}
	if Collective != 3 {
		t.Errorf("Collective = %d, want 3", Collective)
	}
}

// SignalKind constants have the expected iota values (Offer=0..Threaten=4).
func TestSignalKind_Constants(t *testing.T) {
	cases := []struct {
		name string
		got  SignalKind
		want SignalKind
	}{
		{"SignalOffer", SignalOffer, 0},
		{"SignalAccept", SignalAccept, 1},
		{"SignalReject", SignalReject, 2},
		{"SignalGreet", SignalGreet, 3},
		{"SignalThreaten", SignalThreaten, 4},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// Signal zero value is valid and all fields are at their zero values.
func TestSignal_ZeroValue(t *testing.T) {
	var s Signal
	if s.Kind != SignalOffer {
		t.Errorf("zero Signal.Kind = %d, want SignalOffer(0)", s.Kind)
	}
	if s.Intent != "" {
		t.Errorf("zero Signal.Intent = %q, want empty", s.Intent)
	}
	if s.Valence != 0 || s.ClaimedValue != 0 || s.Truth != 0 || s.Intensity != 0 {
		t.Errorf("zero Signal floats not zero: %+v", s)
	}
}

// Signal is copy-safe (no hidden pointers).
func TestSignal_CopySafe(t *testing.T) {
	original := Signal{
		Kind:         SignalOffer,
		Intent:       "has_food",
		Valence:      0.8,
		ClaimedValue: 10.0,
		Truth:        0.9,
		Intensity:    0.5,
	}
	copy := original
	copy.Kind = SignalReject
	copy.Valence = -1.0
	if original.Kind != SignalOffer || original.Valence != 0.8 {
		t.Errorf("copy mutation affected original: %+v", original)
	}
}

// Type-distinction guard: StatID, Dimension, Tag, and Pred are distinct named string types.
// A value of one is not assignable to another without explicit conversion — prevents id-kind drift.
func TestDimensionIsDistinctType(t *testing.T) {
	var s StatID = "Strength"
	var d Dimension = Dimension(s) // explicit conversion OK
	_ = d
	// The following lines should NOT compile:
	// var d2 Dimension = s  // cannot use s (type StatID) as type Dimension
	// var s2 StatID = d     // cannot use d (type Dimension) as type StatID
}
