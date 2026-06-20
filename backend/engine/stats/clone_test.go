package stats

import "testing"

// ── AC: Stats.Clone() is a deep copy ─────────────────────────────────────────

func TestStats_Clone(t *testing.T) {
	original := Stats{"Strength": 0.8, "Agility": 0.3}
	clone := original.Clone()

	// Mutate the clone.
	clone["Strength"] = 0.0
	delete(clone, "Agility")

	// Original must be unchanged.
	if original["Strength"] != 0.8 {
		t.Errorf("original Strength changed to %v after clone mutation", original["Strength"])
	}
	if _, ok := original["Agility"]; !ok {
		t.Error("original Agility vanished after clone mutation")
	}
}

func TestStats_CloneNil(t *testing.T) {
	var s Stats
	c := s.Clone()
	if c != nil {
		t.Error("Clone() of nil Stats should return nil")
	}
}
