package stats

import (
	"os"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/core"
)

// ── AC: Has / unknown StatID ─────────────────────────────────────────────────

func TestHas_Unknown(t *testing.T) {
	reg := mustLoadYAML(t, validYAML)
	if reg.Has("NotAStat") {
		t.Error("Has(NotAStat) = true, want false")
	}
}

// ── AC: IDs() ordering is deterministic (D12) ────────────────────────────────

func TestIDs_DeterministicOrder(t *testing.T) {
	reg1 := mustLoadYAML(t, validYAML)
	reg2 := mustLoadYAML(t, validYAML)

	ids1 := reg1.IDs()
	ids2 := reg2.IDs()

	if len(ids1) != len(ids2) {
		t.Fatalf("length mismatch: %d vs %d", len(ids1), len(ids2))
	}
	for i := range ids1 {
		if ids1[i] != ids2[i] {
			t.Errorf("mismatch at %d: %q vs %q", i, ids1[i], ids2[i])
		}
	}

	// Check lexicographic order for our content.
	want := []core.StatID{"Agility", "Greed", "Honesty", "Intelligence", "Strength"}
	for i, id := range ids1 {
		if id != want[i] {
			t.Errorf("IDs()[%d] = %q, want %q", i, id, want[i])
		}
	}

	// Verify Kinds(Capability) == [Agility, Intelligence, Strength]
	wantCap := []core.StatID{"Agility", "Intelligence", "Strength"}
	gotCap := reg1.Kinds(Capability)
	if len(gotCap) != len(wantCap) {
		t.Fatalf("Kinds(Capability) len = %d, want %d; got %v", len(gotCap), len(wantCap), gotCap)
	}
	for i := range wantCap {
		if gotCap[i] != wantCap[i] {
			t.Errorf("Kinds(Capability)[%d] = %q, want %q", i, gotCap[i], wantCap[i])
		}
	}
}

func TestIDs_StableAcrossCalls(t *testing.T) {
	reg := mustLoadYAML(t, validYAML)
	first := reg.IDs()
	second := reg.IDs()
	if len(first) != len(second) {
		t.Fatal("different lengths between calls")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("IDs() changed between calls at %d: %q vs %q", i, first[i], second[i])
		}
	}
}

// ── AC: Defaults() returns one entry per defined stat ────────────────────────

func TestDefaults_AllStatsPresent(t *testing.T) {
	reg := mustLoadYAML(t, validYAML)
	defs := reg.Defaults()

	if len(defs) != reg.Len() {
		t.Errorf("Defaults() has %d entries, want %d (Len)", len(defs), reg.Len())
	}
	for _, id := range reg.IDs() {
		v, ok := defs[id]
		if !ok {
			t.Errorf("Defaults() missing id %q", id)
			continue
		}
		d, _ := reg.Def(id)
		if v != d.Default {
			t.Errorf("Defaults()[%q] = %v, want %v", id, v, d.Default)
		}
		if v < d.Min || v > d.Max {
			t.Errorf("Defaults()[%q] = %v outside [%v, %v]", id, v, d.Min, d.Max)
		}
	}
}

// ── AC: gen precedence ───────────────────────────────────────────────────────

func TestDef_CarriesGenAndDefault(t *testing.T) {
	reg := mustLoadYAML(t, validYAML)
	def, ok := reg.Def("Strength")
	if !ok {
		t.Fatal("Strength not found")
	}
	if def.Default != 0.5 {
		t.Errorf("Strength.Default = %v, want 0.5", def.Default)
	}
	if def.Gen.Dist != "normal" {
		t.Errorf("Strength.Gen.Dist = %q, want normal", def.Gen.Dist)
	}
	if def.Gen.Mean != 0.5 {
		t.Errorf("Strength.Gen.Mean = %v, want 0.5", def.Gen.Mean)
	}
	if def.Gen.SD != 0.15 {
		t.Errorf("Strength.Gen.SD = %v, want 0.15", def.Gen.SD)
	}
	if def.Inherit != 0.5 {
		t.Errorf("Strength.Inherit = %v, want 0.5", def.Inherit)
	}
}

func TestDef_LabelFallsBackToID(t *testing.T) {
	reg := mustLoadYAML(t, validYAML)
	// Greed has no explicit label in validYAML.
	def, ok := reg.Def("Greed")
	if !ok {
		t.Fatal("Greed not found")
	}
	if def.Label != "Greed" {
		t.Errorf("Greed.Label = %q, want 'Greed'", def.Label)
	}
	// Strength has explicit label.
	def2, ok2 := reg.Def("Strength")
	if !ok2 {
		t.Fatal("Strength not found")
	}
	if def2.Label != "Strength" {
		t.Errorf("Strength.Label = %q, want 'Strength'", def2.Label)
	}
}

// ── AC: Immutable after initialization ───────────────────────────────────────

func TestImmutable_DefaultsIsCopy(t *testing.T) {
	reg := mustLoadYAML(t, validYAML)
	first := reg.Defaults()
	first["Strength"] = 999.0
	second := reg.Defaults()
	if second["Strength"] == 999.0 {
		t.Error("mutating returned Defaults changed the registry's backing data")
	}
}

func TestImmutable_IDsIsCopy(t *testing.T) {
	reg := mustLoadYAML(t, validYAML)
	first := reg.IDs()
	if len(first) > 0 {
		first[0] = "MUTATED"
	}
	second := reg.IDs()
	if second[0] == "MUTATED" {
		t.Error("mutating returned IDs() slice changed the registry's backing data")
	}
}

func TestImmutable_KindsIsCopy(t *testing.T) {
	reg := mustLoadYAML(t, validYAML)
	capIDs := reg.Kinds(Capability)
	if len(capIDs) > 0 {
		capIDs[0] = "MUTATED"
	}
	capIDs2 := reg.Kinds(Capability)
	if capIDs2[0] == "MUTATED" {
		t.Error("mutating returned Kinds slice changed the registry's backing data")
	}
}

// ── AC: Clamp constrains out-of-range values and drops unknown ids ───────────

func TestClamp_DropsUnknown(t *testing.T) {
	reg := mustLoadYAML(t, validYAML)
	s := Stats{"Strength": 0.8, "NotAStat": 0.5}
	clamped := reg.Clamp(s)
	if _, ok := clamped["NotAStat"]; ok {
		t.Error("Clamp should drop unknown stat NotAStat")
	}
	if v, ok := clamped["Strength"]; !ok || v != 0.8 {
		t.Errorf("Clamp should keep known stat Strength, got %v", v)
	}
}

func TestClamp_ConstrainsValues(t *testing.T) {
	reg := mustLoadYAML(t, validYAML)
	s := Stats{
		"Strength":     1.5,  // above max
		"Agility":      -0.5, // below min
		"Intelligence": 0.5,  // in range — should be unchanged
	}
	clamped := reg.Clamp(s)
	if clamped["Strength"] != 1.0 {
		t.Errorf("Strength clamped to %v, want 1.0", clamped["Strength"])
	}
	if clamped["Agility"] != 0.0 {
		t.Errorf("Agility clamped to %v, want 0.0", clamped["Agility"])
	}
	if clamped["Intelligence"] != 0.5 {
		t.Errorf("Intelligence clamped to %v, want 0.5", clamped["Intelligence"])
	}
}

func TestClamp_DropsMissingKnownStat(t *testing.T) {
	// Known stat not present in s should not appear in output.
	reg := mustLoadYAML(t, validYAML)
	s := Stats{"Strength": 0.8}
	clamped := reg.Clamp(s)
	if _, ok := clamped["Agility"]; ok {
		t.Error("Clamp should not include Agility (not in input)")
	}
	if _, ok := clamped["Strength"]; !ok || clamped["Strength"] != 0.8 {
		t.Errorf("Strength = %v, want 0.8", clamped["Strength"])
	}
}

func TestClamp_NilInput(t *testing.T) {
	reg := mustLoadYAML(t, validYAML)
	clamped := reg.Clamp(nil)
	if clamped == nil {
		t.Error("Clamp(nil) should return non-nil map")
	}
	if len(clamped) != 0 {
		t.Errorf("Clamp(nil) len = %d, want 0", len(clamped))
	}
}

// ── AC: Def.Clamp ────────────────────────────────────────────────────────────

func TestDef_Clamp(t *testing.T) {
	d := Def{Min: -1.0, Max: 1.0}
	cases := []struct {
		v    float64
		want float64
	}{
		{0.0, 0.0},
		{1.0, 1.0},
		{-1.0, -1.0},
		{2.0, 1.0},
		{-2.0, -1.0},
		{0.5, 0.5},
	}
	for _, tc := range cases {
		got := d.Clamp(tc.v)
		if got != tc.want {
			t.Errorf("Clamp(%v) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

// ── AC: No literal stat name in engine/stats source (grep guard) ─────────────

func TestNoHardcodedStatNames(t *testing.T) {
	// Check the package's own source files (excluding this test file).
	// The SPEC says no literal "Strength", "Agility", etc. in engine/stats source.
	forbidden := []string{
		`"Strength"`,
		`"Agility"`,
		`"Intelligence"`,
		`"Aggression"`,
		`"Impulsivity"`,
		`"Honesty"`,
		`"Greed"`,
		`"Sociability"`,
		`"Vindictiveness"`,
		`"RiskAversion"`,
	}
	// Read stats.go from the same directory as this test file.
	src, err := os.ReadFile("stats.go")
	if err != nil {
		t.Fatalf("read stats.go: %v", err)
	}
	content := string(src)
	for _, name := range forbidden {
		if strings.Contains(content, name) {
			t.Errorf("stats.go: contains hardcoded stat name %s (D7 violation)", name)
		}
	}
}
