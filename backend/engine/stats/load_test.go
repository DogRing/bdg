package stats

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/core"
)

// ── AC: Loads from an injected io.Reader ─────────────────────────────────────

func TestLoad_ValidYAML(t *testing.T) {
	reg, err := Load(strings.NewReader(validYAML))
	if err != nil {
		t.Fatalf("Load(validYAML) returned error: %v", err)
	}

	// Must contain the shipped capabilities + dispositions at minimum.
	expectedIDs := []core.StatID{"Agility", "Greed", "Honesty", "Intelligence", "Strength"}
	if reg.Len() != len(expectedIDs) {
		t.Errorf("Len() = %d, want %d", reg.Len(), len(expectedIDs))
	}
	for _, id := range expectedIDs {
		if !reg.Has(id) {
			t.Errorf("Has(%q) = false, want true", id)
		}
	}
	// Verify kind classification.
	capIDs := reg.Kinds(Capability)
	found := make(map[core.StatID]bool, len(capIDs))
	for _, id := range capIDs {
		found[id] = true
	}
	for _, cap := range []core.StatID{"Strength", "Agility", "Intelligence"} {
		if !found[cap] {
			t.Errorf("Kinds(Capability) missing %q; got %v", cap, capIDs)
		}
	}
	dispIDs := reg.Kinds(Disposition)
	found2 := make(map[core.StatID]bool, len(dispIDs))
	for _, id := range dispIDs {
		found2[id] = true
	}
	for _, d := range []core.StatID{"Honesty", "Greed"} {
		if !found2[d] {
			t.Errorf("Kinds(Disposition) missing %q; got %v", d, dispIDs)
		}
	}
}

// ── AC: Unknown / unregistered StatID is rejected ────────────────────────────

func TestLoad_InvalidKind(t *testing.T) {
	_, err := Load(strings.NewReader(`
schema_version: 1
stats:
  - id: Foo
    kind: bogus
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
`))
	if err == nil {
		t.Fatal("Load with invalid kind: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error message should mention invalid kind: %v", err)
	}
}

func TestLoad_BadIDPattern(t *testing.T) {
	_, err := Load(strings.NewReader(`
schema_version: 1
stats:
  - id: "123bad"
    kind: capability
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
`))
	if err == nil {
		t.Fatal("Load with bad id pattern: expected error, got nil")
	}
}

func TestLoad_DuplicateID(t *testing.T) {
	_, err := Load(strings.NewReader(`
schema_version: 1
stats:
  - id: Strength
    kind: capability
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Strength
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.4, sd: 0.2 }
    inherit: 0.4
`))
	if err == nil {
		t.Fatal("Load with duplicate id: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention duplicate: %v", err)
	}
}

func TestLoad_EmptyStats(t *testing.T) {
	_, err := Load(strings.NewReader(`
schema_version: 1
stats: []
`))
	if err == nil {
		t.Fatal("Load with empty stats: expected error, got nil")
	}
}

// ── AC: Min <= Default <= Max enforced (semantic range check) ────────────────

func TestLoad_MinGreaterThanMax(t *testing.T) {
	_, err := Load(strings.NewReader(`
schema_version: 1
stats:
  - id: Foo
    kind: capability
    range: [0.8, 0.2]
    gen: { dist: normal, mean: 0.5, sd: 0.1 }
    inherit: 0.5
`))
	if err == nil {
		t.Fatal("Load with min>max: expected error, got nil")
	}
}

func TestLoad_DefaultOutsideRange(t *testing.T) {
	_, err := Load(strings.NewReader(`
schema_version: 1
stats:
  - id: Foo
    kind: capability
    range: [0.0, 1.0]
    default: 1.5
    gen: { dist: normal, mean: 0.5, sd: 0.1 }
    inherit: 0.5
`))
	if err == nil {
		t.Fatal("Load with default>max: expected error, got nil")
	}
}

func TestLoad_DefaultBelowMin(t *testing.T) {
	_, err := Load(strings.NewReader(`
schema_version: 1
stats:
  - id: Foo
    kind: capability
    range: [0.0, 1.0]
    default: -0.5
    gen: { dist: normal, mean: 0.5, sd: 0.1 }
    inherit: 0.5
`))
	if err == nil {
		t.Fatal("Load with default<min: expected error, got nil")
	}
}

func TestLoad_DefaultInRangeAccepted(t *testing.T) {
	_, err := Load(strings.NewReader(`
schema_version: 1
stats:
  - id: Foo
    kind: capability
    range: [0.0, 1.0]
    default: 0.0
    gen: { dist: normal, mean: 0.5, sd: 0.1 }
    inherit: 0.5
`))
	if err != nil {
		t.Fatalf("Load with default at min boundary: unexpected error: %v", err)
	}
}

func TestLoad_DefaultDefaultsToMidpoint(t *testing.T) {
	reg, err := Load(strings.NewReader(`
schema_version: 1
stats:
  - id: Foo
    kind: capability
    range: [2.0, 6.0]
    gen: { dist: normal, mean: 0.5, sd: 0.1 }
    inherit: 0.5
`))
	if err != nil {
		t.Fatalf("Load with no default: unexpected error: %v", err)
	}
	def, ok := reg.Def("Foo")
	if !ok {
		t.Fatal("Foo not found in registry")
	}
	want := 4.0 // midpoint of [2, 6]
	if def.Default != want {
		t.Errorf("default = %v, want midpoint %v", def.Default, want)
	}
}
