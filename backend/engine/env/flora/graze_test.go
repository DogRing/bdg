package flora_test

import (
	"testing"

	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/kernel/core"
)

// PD2 flora depletion (P_fa4b, docs/plans/fauna.md §5). GrazeLength is the fauna→flora depletion seam: a
// grazing herbivore crops a plant's Length (biomass), which weakens its food scent (mag = Length+Width)
// and its next recovery, then the plant regrows via the ordinary growth Step. These tests pin the
// mutation contract in isolation.

// A normal graze crops Length by the amount and returns exactly that; Width is untouched (grazing eats
// edible height, not canopy) and BOTH read surfaces (Plants slice + PlantByID index) reflect the change.
func TestGrazeLengthCropsAndStaysConsistent(t *testing.T) {
	s := flora.New([]flora.Plant{plant("g", "grass", core.Vec2{}, 5.0, 2.0)})
	removed := s.GrazeLength("g", 1.5)
	if removed != 1.5 {
		t.Errorf("removed = %.3f, want 1.5", removed)
	}
	p, ok := s.PlantByID("g")
	if !ok || p.Length != 3.5 {
		t.Errorf("PlantByID Length = %.3f (ok=%v), want 3.5", p.Length, ok)
	}
	if p.Width != 2.0 {
		t.Errorf("Width must be untouched by grazing: got %.3f, want 2.0", p.Width)
	}
	plants := s.Plants()
	if plants[0].Length != 3.5 {
		t.Errorf("Plants() slice Length = %.3f, want 3.5 (idx/slice out of sync)", plants[0].Length)
	}
}

// Grazing more than the plant has crops it to 0 and returns only what was actually there (≤ prior Length)
// — never a negative Length. This is why an overgrazed plant feeds a herbivore less (local famine).
func TestGrazeLengthCapsAtAvailableBiomass(t *testing.T) {
	s := flora.New([]flora.Plant{plant("g", "grass", core.Vec2{}, 0.8, 1.0)})
	removed := s.GrazeLength("g", 5.0) // wants more than the 0.8 available
	if removed != 0.8 {
		t.Errorf("removed = %.3f, want 0.8 (all that was there)", removed)
	}
	p, _ := s.PlantByID("g")
	if p.Length != 0 {
		t.Errorf("over-grazed plant Length = %.3f, want 0 (clamped, never negative)", p.Length)
	}
	// A fully-grazed stub gives nothing more.
	if again := s.GrazeLength("g", 1.0); again != 0 {
		t.Errorf("grazing a Length-0 stub must return 0, got %.3f", again)
	}
}

// No-op guards: an unknown ID and a non-positive amount both return 0 and mutate nothing.
func TestGrazeLengthNoOps(t *testing.T) {
	s := flora.New([]flora.Plant{plant("g", "grass", core.Vec2{}, 4.0, 1.0)})
	if r := s.GrazeLength("nope", 1.0); r != 0 {
		t.Errorf("unknown id must return 0, got %.3f", r)
	}
	if r := s.GrazeLength("g", 0); r != 0 {
		t.Errorf("amount 0 must return 0, got %.3f", r)
	}
	if r := s.GrazeLength("g", -2.0); r != 0 {
		t.Errorf("negative amount must return 0, got %.3f", r)
	}
	if p, _ := s.PlantByID("g"); p.Length != 4.0 {
		t.Errorf("no-op grazes must leave Length untouched: got %.3f, want 4.0", p.Length)
	}
}

// Determinism: with multiple plants, GrazeLength finds the right one by ID (binary search over the sorted
// slice) and leaves the others alone — same call, same result.
func TestGrazeLengthTargetsByIDDeterministically(t *testing.T) {
	newState := func() *flora.State {
		return flora.New([]flora.Plant{
			plant("a", "grass", core.Vec2{}, 3.0, 1.0),
			plant("b", "grass", core.Vec2{X: 1}, 4.0, 1.0),
			plant("c", "grass", core.Vec2{X: 2}, 5.0, 1.0),
		})
	}
	s1, s2 := newState(), newState()
	if s1.GrazeLength("b", 2.0) != s2.GrazeLength("b", 2.0) {
		t.Fatal("same graze must return the same removed amount")
	}
	pb, _ := s1.PlantByID("b")
	pa, _ := s1.PlantByID("a")
	pc, _ := s1.PlantByID("c")
	if pb.Length != 2.0 {
		t.Errorf("target b Length = %.3f, want 2.0", pb.Length)
	}
	if pa.Length != 3.0 || pc.Length != 5.0 {
		t.Errorf("non-target plants must be untouched: a=%.3f (want 3.0), c=%.3f (want 5.0)", pa.Length, pc.Length)
	}
}
