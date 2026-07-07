// Package navmap_test — AC4 (decay golden), AC8–AC11, and resolved accessor tests.
// Shares fixtures from navmap_test.go (same package). Hex (docs/hex-grid.md): cells are flat-top
// axial (q,r); cells are chosen in-bounds (centre within bounds) and, where terrain matters, by the
// column x-band. adjLen (√3·CellSize) is the adjacent-hex geometric length.
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

	cell := navmap.Cell{Q: 2, R: 3}
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
	cell := navmap.Cell{Q: 3, R: 3}
	src := navmap.Cell{Q: 2, R: 3} // adjacent (dir -1,0)

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
	want := 1.0 * wearMult * adjLen
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
	m.SetTerrain([]navmap.Cell{{Q: 1, R: 1}}, "nope")
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
		m.Deposit([]navmap.Cell{{Q: 1, R: 0}, {Q: 3, R: 2}, {Q: 0, R: 5}}, 7.0)
		m.Deposit([]navmap.Cell{{Q: 3, R: 2}}, 2.0) // (3,2) total = 9
		m.Decay()
		m.SetTerrain([]navmap.Cell{{Q: 2, R: 1}, {Q: 4, R: 3}}, "forest")
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

// ── AC11: Bounds (hex: a cell is in-bounds iff its CENTRE is within bounds) ────

func TestBounds(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()
	inBounds := m.CellOf(core.Vec2{X: 50, Y: 50})
	if !m.Passable(inBounds) {
		t.Error("in-bounds plain cell should be passable")
	}

	// Cells whose flat-top centres fall outside [0,100)×[0,100): x=1.5·10·q, y=√3·10·(q/2+r).
	oob := []navmap.Cell{
		{Q: -1, R: 0}, // x=-15
		{Q: 0, R: -1}, // y=-17.3
		{Q: 7, R: 0},  // x=105
		{Q: 0, R: 6},  // y≈103.9
		{Q: -5, R: -5},
	}
	for _, c := range oob {
		if m.Passable(c) {
			t.Errorf("out-of-bounds cell %v should be impassable", c)
		}
		if !math.IsInf(m.StepCost(inBounds, c), 1) {
			t.Errorf("StepCost into out-of-bounds %v should be +Inf", c)
		}
	}

	// CellOf at the grid origin (MinX,MinY) is axial (0,0).
	if c := m.CellOf(core.Vec2{X: 0, Y: 0}); c.Q != 0 || c.R != 0 {
		t.Errorf("CellOf(0,0) = %v, want {0,0}", c)
	}
	// A point well beyond bounds maps to a hex whose centre is out of bounds → impassable.
	// (A point exactly at MaxX rounds to the last in-bounds column centre, so use a far point.)
	if c := m.CellOf(core.Vec2{X: 150, Y: 150}); m.Passable(c) {
		t.Errorf("cell well past bounds (%v) should be out-of-bounds/impassable", c)
	}
}

// ── FootprintBlocked: footprint-only accessor (RESOLVED #FA-navmap) ───────────

func TestFootprintBlocked(t *testing.T) {
	t.Parallel()
	// x≥50 → water, else plain.
	m := newMapFn(func(p core.Vec2) navmap.TerrainID {
		if p.X >= 50 {
			return "water"
		}
		return "plain"
	})
	waterCell := m.CellOf(core.Vec2{X: 75, Y: 50})

	// Water is terrain-impassable but NOT FootprintBlocked.
	if m.FootprintBlocked(waterCell) {
		t.Error("water terrain cell must NOT be FootprintBlocked (terrain ≠ footprint)")
	}
	if m.Passable(waterCell) {
		t.Error("water terrain cell must not be Passable")
	}

	// Wall stamp → FootprintBlocked true.
	wallCell := m.CellOf(core.Vec2{X: 25, Y: 25})
	m.StampFootprint([]navmap.Cell{wallCell}, false)
	if !m.FootprintBlocked(wallCell) {
		t.Error("wall-stamped cell should be FootprintBlocked")
	}

	// Un-stamp → FootprintBlocked false.
	m.StampFootprint([]navmap.Cell{wallCell}, true)
	if m.FootprintBlocked(wallCell) {
		t.Error("un-stamped cell should not be FootprintBlocked")
	}

	// A plain cell with no stamp → not FootprintBlocked.
	if m.FootprintBlocked(navmap.Cell{Q: 1, R: 1}) {
		t.Error("unstamped plain cell should not be FootprintBlocked")
	}
}

// ── BaseCost: per-cell terrain base cost (RESOLVED #FA-navmap) ────────────────

func TestBaseCost(t *testing.T) {
	t.Parallel()
	// Column x-bands (x = 1.5·10·q): q≤1 (x≤15) plain, q∈{2,3} (x∈{30,45}) forest, q≥4 (x≥60) swamp.
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
		{navmap.Cell{Q: 1, R: 0}, 1.0}, // x=15 → plain
		{navmap.Cell{Q: 3, R: 0}, 2.0}, // x=45 → forest
		{navmap.Cell{Q: 5, R: 0}, 3.0}, // x=75 → swamp
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
	cell := navmap.Cell{Q: 1, R: 0}
	m.SetTerrain([]navmap.Cell{cell}, "forest")
	if got := m.BaseCost(cell); math.Abs(got-2.0) > 1e-9 {
		t.Errorf("BaseCost after SetTerrain(forest) = %v, want 2.0", got)
	}

	// Footprint does NOT affect BaseCost (layers are independent). Wall on a plain cell (x<30).
	wall := navmap.Cell{Q: 1, R: 2} // x=15 → plain
	m.StampFootprint([]navmap.Cell{wall}, false)
	if got := m.BaseCost(wall); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("BaseCost of wall-stamped plain cell = %v, want 1.0", got)
	}

	// Out-of-bounds returns 0 (undefined; callers check bounds before BaseCost).
	if got := m.BaseCost(navmap.Cell{Q: -1, R: 0}); got != 0 {
		t.Errorf("BaseCost out-of-bounds = %v, want 0", got)
	}
}
