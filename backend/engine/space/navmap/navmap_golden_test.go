// Package navmap_test — AC4 (decay golden), AC8–AC11, and resolved accessor tests.
// Shares fixtures from navmap_test.go (same package).
package navmap_test

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
)

// ── AC4: Decay fades trails (golden) ──────────────────────────────────────────

type goldenDecayEntry struct {
	Tick int               `json:"tick"`
	Wear []navmap.WearCell `json:"wear"`
}

const decayGoldenPath = "testdata/golden/decay.json"

func TestDecayFadesTrails(t *testing.T) {
	t.Parallel()
	// WearDecay=1, WearMax=5, initial deposit=4: fades to 0 in exactly 4 Decay ticks.
	cfg := navmap.Config{
		CellSize: 10,
		MinX:     0, MinY: 0, MaxX: 100, MaxY: 100,
		WearDecay:   1,
		WearMax:     5,
		WearCostMin: 0.3,
	}
	m := navmap.New(cfg, func(_ core.Vec2) navmap.TerrainID { return "plain" }, testTypes)

	cell := navmap.Cell{X: 2, Y: 3}
	m.Deposit([]navmap.Cell{cell}, 4.0)

	var entries []goldenDecayEntry
	entries = append(entries, goldenDecayEntry{Tick: 0, Wear: m.ActiveWear()})
	for i := 1; i <= 5; i++ {
		m.Decay()
		entries = append(entries, goldenDecayEntry{Tick: i, Wear: m.ActiveWear()})
	}

	// Logical assertions (independent of golden file).
	for _, e := range entries {
		switch {
		case e.Tick >= 4:
			if len(e.Wear) != 0 {
				t.Errorf("tick %d: expected empty ActiveWear (fully faded), got %v", e.Tick, e.Wear)
			}
		default:
			if len(e.Wear) != 1 {
				t.Errorf("tick %d: expected 1 wear entry, got %v", e.Tick, e.Wear)
			} else if e.Tick > 0 {
				want := 4.0 - float64(e.Tick)
				if math.Abs(e.Wear[0].Wear-want) > 1e-9 {
					t.Errorf("tick %d: wear = %v, want %v", e.Tick, e.Wear[0].Wear, want)
				}
			}
		}
	}

	// Golden file: seed on first run; compare on subsequent runs.
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	existing, readErr := os.ReadFile(decayGoldenPath)
	if readErr != nil {
		if err2 := os.MkdirAll("testdata/golden", 0o755); err2 != nil {
			t.Fatalf("mkdir: %v", err2)
		}
		if err2 := os.WriteFile(decayGoldenPath, raw, 0o644); err2 != nil {
			t.Fatalf("write golden: %v", err2)
		}
		t.Logf("seeded golden file %s", decayGoldenPath)
		return
	}
	if string(existing) != string(raw) {
		t.Errorf("golden mismatch.\nGot:\n%s\nWant:\n%s", raw, existing)
	}
}

// ── AC8: Outcome-neutral pre-activation ───────────────────────────────────────

func TestOutcomeNeutralPreActivation(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()
	cell := navmap.Cell{X: 3, Y: 3}
	src := navmap.Cell{X: 2, Y: 3}

	// Never calling SetTerrain: TerrainOverrides must be empty.
	if ovr := m.TerrainOverrides(); len(ovr) != 0 {
		t.Errorf("TerrainOverrides without SetTerrain = %v", ovr)
	}

	m.Deposit([]navmap.Cell{cell}, 5.0)
	aw := m.ActiveWear()
	if len(aw) != 1 || aw[0].Cell != cell {
		t.Errorf("ActiveWear = %v, want [{%v 5}]", aw, cell)
	}

	// StepCost uses base terrain (plain); unchanged by absence of SetTerrain.
	wearMult := 1.0 - (5.0/testCfg.WearMax)*(1.0-testCfg.WearCostMin)
	want := 1.0 * wearMult * 10.0
	if got := m.StepCost(src, cell); math.Abs(got-want) > 1e-9 {
		t.Errorf("StepCost = %v, want %v", got, want)
	}
}

// ── AC9: SetTerrain unknown id panics ─────────────────────────────────────────

func TestSetTerrainUnknownIDPanics(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()
	defer func() {
		if r := recover(); r == nil {
			t.Error("SetTerrain with unknown TerrainID should panic")
		}
	}()
	m.SetTerrain([]navmap.Cell{{X: 1, Y: 1}}, "nope")
}

// ── AC10: Determinism golden ───────────────────────────────────────────────────

type deterministicGoldenEntry struct {
	Wear      []navmap.WearCell    `json:"wear"`
	Overrides []navmap.TerrainCell `json:"overrides"`
}

const detGoldenPath = "testdata/golden/determinism.json"

func TestDeterminism(t *testing.T) {
	t.Parallel()

	run := func() deterministicGoldenEntry {
		m := newMapAllPlain()
		m.Deposit([]navmap.Cell{{X: 1, Y: 0}, {X: 3, Y: 2}, {X: 0, Y: 5}}, 7.0)
		m.Deposit([]navmap.Cell{{X: 3, Y: 2}}, 2.0) // (3,2) total = 9
		m.Decay()
		m.SetTerrain([]navmap.Cell{{X: 2, Y: 1}, {X: 4, Y: 3}}, "forest")
		m.Decay()
		return deterministicGoldenEntry{Wear: m.ActiveWear(), Overrides: m.TerrainOverrides()}
	}

	// Two in-process runs must be byte-identical (no map-iteration leakage).
	r1, r2 := run(), run()
	raw1, _ := json.Marshal(r1)
	raw2, _ := json.Marshal(r2)
	if string(raw1) != string(raw2) {
		t.Errorf("non-deterministic: runs differ\nRun1: %s\nRun2: %s", raw1, raw2)
	}

	// Golden file comparison.
	existing, readErr := os.ReadFile(detGoldenPath)
	if readErr != nil {
		if err2 := os.MkdirAll("testdata/golden", 0o755); err2 != nil {
			t.Fatalf("mkdir: %v", err2)
		}
		pretty, _ := json.MarshalIndent(r1, "", "  ")
		if err2 := os.WriteFile(detGoldenPath, pretty, 0o644); err2 != nil {
			t.Fatalf("write golden: %v", err2)
		}
		t.Logf("seeded golden file %s", detGoldenPath)
		return
	}
	var existingParsed deterministicGoldenEntry
	if err := json.Unmarshal(existing, &existingParsed); err != nil {
		t.Fatalf("parse existing golden: %v", err)
	}
	existingNorm, _ := json.Marshal(existingParsed)
	if string(existingNorm) != string(raw1) {
		t.Errorf("golden mismatch.\nGot: %s\nWant: %s", raw1, existingNorm)
	}
}

// ── AC11: Bounds ──────────────────────────────────────────────────────────────

func TestBounds(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()
	inBounds := navmap.Cell{X: 5, Y: 5}
	if !m.Passable(inBounds) {
		t.Error("in-bounds plain cell should be passable")
	}

	oob := []navmap.Cell{
		{X: -1, Y: 0}, {X: 0, Y: -1},
		{X: 10, Y: 0}, {X: 0, Y: 10}, // 10×10=100 ≥ MaxX/MaxY=100 → out
		{X: -5, Y: -5},
	}
	for _, c := range oob {
		if m.Passable(c) {
			t.Errorf("out-of-bounds cell %v should be impassable", c)
		}
		if !math.IsInf(m.StepCost(inBounds, c), 1) {
			t.Errorf("StepCost into out-of-bounds %v should be +Inf", c)
		}
	}

	// CellOf corner cases.
	if c := m.CellOf(core.Vec2{X: 0, Y: 0}); c.X != 0 || c.Y != 0 {
		t.Errorf("CellOf(0,0) = %v, want {0,0}", c)
	}
	if c := m.CellOf(core.Vec2{X: 99, Y: 99}); c.X != 9 || c.Y != 9 {
		t.Errorf("CellOf(99,99) = %v, want {9,9}", c)
	}
	if c := m.CellOf(core.Vec2{X: 100, Y: 0}); m.Passable(c) {
		t.Errorf("cell at MaxX (%v) should be out-of-bounds/impassable", c)
	}
}

// ── FootprintBlocked: footprint-only accessor (RESOLVED #FA-navmap) ───────────

func TestFootprintBlocked(t *testing.T) {
	t.Parallel()
	waterCell := navmap.Cell{X: 5, Y: 5}
	m := newMapFn(func(p core.Vec2) navmap.TerrainID {
		if p.X >= 50 && p.X < 60 && p.Y >= 50 && p.Y < 60 {
			return "water"
		}
		return "plain"
	})

	// Water is terrain-impassable but NOT FootprintBlocked.
	if m.FootprintBlocked(waterCell) {
		t.Error("water terrain cell must NOT be FootprintBlocked (terrain ≠ footprint)")
	}
	if m.Passable(waterCell) {
		t.Error("water terrain cell must not be Passable")
	}

	// Wall stamp → FootprintBlocked true.
	wallCell := navmap.Cell{X: 3, Y: 3}
	m.StampFootprint([]navmap.Cell{wallCell}, false)
	if !m.FootprintBlocked(wallCell) {
		t.Error("wall-stamped cell should be FootprintBlocked")
	}

	// Un-stamp → FootprintBlocked false.
	m.StampFootprint([]navmap.Cell{wallCell}, true)
	if m.FootprintBlocked(wallCell) {
		t.Error("un-stamped cell should not be FootprintBlocked")
	}

	// Plain cell with no stamp → not FootprintBlocked.
	if m.FootprintBlocked(navmap.Cell{X: 1, Y: 1}) {
		t.Error("unstamped plain cell should not be FootprintBlocked")
	}
}

// ── BaseCost: per-cell terrain base cost (RESOLVED #FA-navmap) ────────────────

func TestBaseCost(t *testing.T) {
	t.Parallel()
	m := newMapFn(func(p core.Vec2) navmap.TerrainID {
		switch {
		case p.X < 30:
			return "plain" // BaseCost=1
		case p.X < 60:
			return "forest" // BaseCost=2
		default:
			return "swamp" // BaseCost=3
		}
	})

	cases := []struct {
		cell navmap.Cell
		want float64
	}{
		{navmap.Cell{X: 1, Y: 0}, 1.0},
		{navmap.Cell{X: 4, Y: 0}, 2.0},
		{navmap.Cell{X: 7, Y: 0}, 3.0},
	}
	for _, c := range cases {
		got := m.BaseCost(c.cell)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("BaseCost(%v) = %v, want %v", c.cell, got, c.want)
		}
		if got < 1.0 {
			t.Errorf("BaseCost(%v) = %v < 1.0, violates ≥1 invariant", c.cell, got)
		}
	}

	// SetTerrain changes BaseCost.
	cell := navmap.Cell{X: 1, Y: 0}
	m.SetTerrain([]navmap.Cell{cell}, "forest")
	if got := m.BaseCost(cell); math.Abs(got-2.0) > 1e-9 {
		t.Errorf("BaseCost after SetTerrain(forest) = %v, want 2.0", got)
	}

	// Footprint does NOT affect BaseCost (layers are independent).
	wall := navmap.Cell{X: 2, Y: 2}
	m.StampFootprint([]navmap.Cell{wall}, false)
	if got := m.BaseCost(wall); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("BaseCost of wall-stamped plain cell = %v, want 1.0", got)
	}

	// Out-of-bounds returns 0 (undefined; callers check bounds before BaseCost).
	if got := m.BaseCost(navmap.Cell{X: -1, Y: 0}); got != 0 {
		t.Errorf("BaseCost out-of-bounds = %v, want 0", got)
	}
}
