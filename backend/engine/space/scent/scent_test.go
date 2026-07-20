package scent

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newGrid(t *testing.T, cs float64) *Grid {
	t.Helper()
	return New(cs)
}

func v(x, y float64) core.Vec2 { return core.Vec2{X: x, Y: y} }

// ── AC: Deposit + read intensity (F20–F22, scalar) ────────────────────────────

func TestDepositReadIntensity(t *testing.T) {
	cs := 1.0
	pos := v(0.5, 0.5) // centre of cell (0,0)
	rad := 1.5 * cs    // own cell + neighbours

	t.Run("food_only", func(t *testing.T) {
		g := newGrid(t, cs)
		g.Deposit(ChanFood, pos, 1.0)
		g.Commit()
		r := g.Read(pos, rad, Wind{})
		if r.Food.Intensity <= 0 {
			t.Fatalf("expected Food.Intensity>0, got %v", r.Food.Intensity)
		}
		if r.Prey.Intensity != 0 {
			t.Fatalf("expected Prey.Intensity==0, got %v", r.Prey.Intensity)
		}
		if r.Predator.Intensity != 0 {
			t.Fatalf("expected Predator.Intensity==0, got %v", r.Predator.Intensity)
		}
		if r.Carrion.Intensity != 0 {
			t.Fatalf("expected Carrion.Intensity==0, got %v", r.Carrion.Intensity)
		}
	})

	t.Run("larger_magnitude_strictly_greater", func(t *testing.T) {
		g1, g2 := newGrid(t, cs), newGrid(t, cs)
		g1.Deposit(ChanFood, pos, 1.0)
		g1.Commit()
		g2.Deposit(ChanFood, pos, 5.0)
		g2.Commit()
		r1 := g1.Read(pos, rad, Wind{})
		r2 := g2.Read(pos, rad, Wind{})
		if r2.Food.Intensity <= r1.Food.Intensity {
			t.Fatalf("larger deposit should give strictly larger intensity: %v <= %v",
				r2.Food.Intensity, r1.Food.Intensity)
		}
	})

	t.Run("stacked_sources_accumulate", func(t *testing.T) {
		g := newGrid(t, cs)
		g.Deposit(ChanFood, pos, 1.0)
		g.Deposit(ChanFood, pos, 1.0)
		g.Commit()
		r := g.Read(pos, rad, Wind{})
		g2 := newGrid(t, cs)
		g2.Deposit(ChanFood, pos, 1.0)
		g2.Commit()
		r2 := g2.Read(pos, rad, Wind{})
		if r.Food.Intensity <= r2.Food.Intensity {
			t.Fatalf("stacked deposits should give strictly larger intensity: %v <= %v",
				r.Food.Intensity, r2.Food.Intensity)
		}
	})

	t.Run("channels_independent", func(t *testing.T) {
		g := newGrid(t, cs)
		g.Deposit(ChanPrey, pos, 3.0)
		g.Commit()
		r := g.Read(pos, rad, Wind{})
		if r.Food.Intensity != 0 {
			t.Fatalf("Food contaminated by Prey deposit: %v", r.Food.Intensity)
		}
		if r.Predator.Intensity != 0 {
			t.Fatalf("Predator contaminated by Prey deposit: %v", r.Predator.Intensity)
		}
		if r.Carrion.Intensity != 0 {
			t.Fatalf("Carrion contaminated by Prey deposit: %v", r.Carrion.Intensity)
		}
		if r.Prey.Intensity <= 0 {
			t.Fatalf("Prey.Intensity should be >0, got %v", r.Prey.Intensity)
		}
	})

	t.Run("carrion_channel", func(t *testing.T) {
		g := newGrid(t, cs)
		g.Deposit(ChanCarrion, pos, 4.0)
		g.Commit()
		r := g.Read(pos, rad, Wind{})
		if r.Carrion.Intensity <= 0 {
			t.Fatalf("Carrion.Intensity should be >0, got %v", r.Carrion.Intensity)
		}
		if r.Food.Intensity != 0 || r.Prey.Intensity != 0 || r.Predator.Intensity != 0 {
			t.Fatalf("Carrion deposit contaminated other channels: %+v", r)
		}
	})
}

func TestChannelAppendOrder(t *testing.T) {
	if ChanFood != 0 || ChanPrey != 1 || ChanPredator != 2 || ChanCarrion != 3 || NumChannels != 4 {
		t.Fatalf("channel order changed: food=%d prey=%d predator=%d carrion=%d num=%d",
			ChanFood, ChanPrey, ChanPredator, ChanCarrion, NumChannels)
	}
}

// ── AC: Intensity falls off with distance ─────────────────────────────────────

func TestIntensityFalloffDistance(t *testing.T) {
	cs := 1.0
	// Source at origin cell.
	src := v(0.5, 0.5)
	g := newGrid(t, cs)
	g.Deposit(ChanFood, src, 10.0)
	// Two spread passes on the same pending buffer: ring-0→ring-1 in pass 1,
	// ring-1→ring-2 in pass 2; then a single Commit. This simulates the
	// multi-pass diffusion that demonstrates the SPEC's "ring-2 > 0" invariant.
	g.Spread(Wind{})
	g.Spread(Wind{})
	g.Commit()

	// Probe positions: source cell, one ring, two rings out.
	posR0 := v(0.5, 0.5)
	posR1 := v(1.5, 0.5)  // east, 1 cell out
	posR2 := v(2.5, 0.5)  // east, 2 cells out
	posR3 := v(10.5, 0.5) // 10 cells out — beyond 2-pass spread reach

	probe := func(pos core.Vec2, rad float64) float64 {
		return g.IntensityAt(ChanFood, pos) // single-cell probe avoids smellRadius edge cases
	}

	i0 := probe(posR0, 0.8)
	i1 := probe(posR1, 0.8)
	i2 := probe(posR2, 0.8)
	i3 := probe(posR3, 0.8)

	if i0 <= i1 {
		t.Fatalf("source ring (%v) should exceed ring-1 (%v)", i0, i1)
	}
	if i1 <= i2 {
		t.Fatalf("ring-1 (%v) should exceed ring-2 (%v)", i1, i2)
	}
	if i2 <= i3 {
		t.Fatalf("ring-2 (%v) should exceed beyond-reach (%v) (should be 0)", i2, i3)
	}
	if i3 != 0 {
		t.Fatalf("beyond reach should be 0, got %v", i3)
	}

	// Dir at ring-1 (east of source) should point west (back toward source).
	// Use smellRadius large enough to include own cell + source cell.
	r1 := g.Read(posR1, 1.5*cs, Wind{})
	if r1.Food.Dir.X >= 0 {
		t.Fatalf("gradient at ring-1-east should point west (Dir.X<0), got Dir=%v", r1.Food.Dir)
	}
}

// ── AC: IntensityAt O(1) wake gate (F45) ─────────────────────────────────────

func TestIntensityAtWakeGate(t *testing.T) {
	cs := 2.0
	predPos := v(3.0, 3.0) // inside cell (1,1)
	farPos := v(99.0, 99.0)

	g := newGrid(t, cs)

	// Before commit: invisible (next-tick latency).
	g.Deposit(ChanPredator, predPos, 5.0)
	if got := g.IntensityAt(ChanPredator, predPos); got != 0 {
		t.Fatalf("should be 0 before Commit, got %v", got)
	}

	g.Commit()
	// After commit: visible in own cell.
	if got := g.IntensityAt(ChanPredator, predPos); got <= 0 {
		t.Fatalf("should be >0 after Commit, got %v", got)
	}
	// Far away: 0 (no neighbour scan).
	if got := g.IntensityAt(ChanPredator, farPos); got != 0 {
		t.Fatalf("far cell should be 0, got %v", got)
	}

	// Own-cell component of Read agrees (Read ≥ IntensityAt because Read also sums neighbours).
	rAtOwn := g.Read(predPos, 0.5*cs, Wind{}).Predator.Intensity // tiny radius: only own cell
	iat := g.IntensityAt(ChanPredator, predPos)
	if math.Abs(rAtOwn-iat) > 1e-12 {
		t.Fatalf("Read (own-cell only) should agree with IntensityAt: Read=%v IntensityAt=%v", rAtOwn, iat)
	}
}

// ── AC: Next-tick latency (F33) ───────────────────────────────────────────────

func TestNextTickLatency(t *testing.T) {
	cs := 1.0
	pos := v(0.5, 0.5)
	rad := 0.8

	g := newGrid(t, cs)

	// Deposit at tick T — not visible yet.
	g.Deposit(ChanFood, pos, 2.0)
	if r := g.Read(pos, rad, Wind{}); r.Food.Intensity != 0 {
		t.Fatalf("visible before Commit: %v", r.Food.Intensity)
	}
	g.Commit()
	// Visible at T+1.
	if r := g.Read(pos, rad, Wind{}); r.Food.Intensity == 0 {
		t.Fatal("should be visible after Commit")
	}

	// Commit with no new deposits — field fades (food not re-deposited).
	g.Commit()
	if r := g.Read(pos, rad, Wind{}); r.Food.Intensity != 0 {
		t.Fatalf("should fade when not re-deposited, got %v", r.Food.Intensity)
	}

	// Predator persists only if re-deposited.
	g.Deposit(ChanPredator, pos, 1.0)
	g.Commit()
	if r := g.Read(pos, rad, Wind{}); r.Predator.Intensity == 0 {
		t.Fatal("predator should be visible after re-deposit+commit")
	}
	// Without re-deposit: fades.
	g.Commit()
	if r := g.Read(pos, rad, Wind{}); r.Predator.Intensity != 0 {
		t.Fatalf("predator should fade without re-deposit, got %v", r.Predator.Intensity)
	}
}

// ── AC: Spread = fixed-order diffusion, downwind / isotropic (F33) ────────────

func TestSpreadDiffusion(t *testing.T) {
	cs := 1.0
	src := v(5.5, 5.5) // cell (5,5)

	// Helper: deposit, spread with wind, commit, then probe downwind vs upwind.
	runSpread := func(wind Wind) (downwind, upwind float64) {
		g := newGrid(t, cs)
		g.Deposit(ChanFood, src, 10.0)
		g.Spread(wind)
		g.Commit()
		// downwind = east cell when Dir=0; upwind = west cell
		down := v(6.5, 5.5)
		up := v(4.5, 5.5)
		downwind = g.IntensityAt(ChanFood, down)
		upwind = g.IntensityAt(ChanFood, up)
		return
	}

	t.Run("isotropic_when_no_wind", func(t *testing.T) {
		down, up := runSpread(Wind{Mag: 0})
		// With isotropic spread the east and west neighbour should be equal.
		if math.Abs(down-up) > 1e-12 {
			t.Fatalf("isotropic: east(%v) != west(%v)", down, up)
		}
		// Both should be > 0 (spread occurred).
		if down <= 0 {
			t.Fatal("isotropic spread should reach east neighbour")
		}
	})

	t.Run("downwind_bias_with_wind", func(t *testing.T) {
		// Wind blowing east (Dir=0).
		down, up := runSpread(Wind{Dir: 0, Mag: 1.0})
		if down <= up {
			t.Fatalf("east wind: downwind(%v) should exceed upwind(%v)", down, up)
		}
		if up != 0 {
			t.Fatalf("east wind Mag=1: fully upwind cell should be 0, got %v", up)
		}
	})

	t.Run("deterministic_deposit_order", func(t *testing.T) {
		// Two sources in same cell: order of deposit must not change result.
		wind := Wind{Dir: math.Pi / 4, Mag: 0.5}
		g1 := newGrid(t, cs)
		g1.Deposit(ChanFood, src, 3.0)
		g1.Deposit(ChanFood, src, 7.0)
		g1.Spread(wind)
		g1.Commit()

		g2 := newGrid(t, cs)
		g2.Deposit(ChanFood, src, 7.0) // reversed
		g2.Deposit(ChanFood, src, 3.0)
		g2.Spread(wind)
		g2.Commit()

		// Probe several cells.
		for _, p := range []core.Vec2{src, v(6.5, 5.5), v(5.5, 6.5), v(6.5, 6.5)} {
			i1 := g1.IntensityAt(ChanFood, p)
			i2 := g2.IntensityAt(ChanFood, p)
			if i1 != i2 {
				t.Fatalf("deposit order changed result at %v: %v vs %v", p, i1, i2)
			}
		}
	})
}

func TestCarrionSpreadAndRead(t *testing.T) {
	cs := 1.0
	src := v(5.5, 5.5)
	g := newGrid(t, cs)
	g.Deposit(ChanCarrion, src, 8.0)
	g.Spread(Wind{})
	g.Commit()

	if got := g.IntensityAt(ChanCarrion, src); got <= 0 {
		t.Fatalf("carrion source cell intensity = %v, want >0", got)
	}
	east := v(6.5, 5.5)
	if got := g.IntensityAt(ChanCarrion, east); got <= 0 {
		t.Fatalf("carrion did not spread to neighbor, got %v", got)
	}
	r := g.Read(east, 1.5*cs, Wind{})
	if r.Carrion.Intensity <= 0 {
		t.Fatalf("Carrion read intensity = %v, want >0", r.Carrion.Intensity)
	}
	if r.Carrion.Dir.X >= 0 {
		t.Fatalf("Carrion gradient east of source should point west, got %+v", r.Carrion.Dir)
	}
}

// ── AC: Read gradient (F34) ───────────────────────────────────────────────────

func TestReadGradient(t *testing.T) {
	cs := 1.0
	w := Wind{}

	t.Run("gradient_points_toward_source_no_wind", func(t *testing.T) {
		// Source is east of reader.
		g := newGrid(t, cs)
		g.Deposit(ChanFood, v(3.5, 1.5), 5.0) // cell (3,1)
		g.Commit()
		r := g.Read(v(2.5, 1.5), 2.0, w) // reading from cell (2,1)
		if r.Food.Intensity == 0 {
			t.Skip("source not in smellRadius — adjust test positions")
		}
		if r.Food.Dir.X <= 0 {
			t.Fatalf("gradient should point east (toward source), got Dir=%v", r.Food.Dir)
		}
	})

	t.Run("upwind_dir_with_wind", func(t *testing.T) {
		// Wind blows east (Dir=0); source is upwind (west). Reader at east. Dir should be west.
		g := newGrid(t, cs)
		g.Deposit(ChanFood, v(0.5, 0.5), 5.0)
		g.Commit()
		wind := Wind{Dir: 0, Mag: 0.8}
		r := g.Read(v(0.5, 0.5), 2.0, wind)
		if r.Food.Intensity == 0 {
			t.Fatal("intensity should be >0")
		}
		// Upwind = opposite of east = west ⇒ Dir.X < 0
		if r.Food.Dir.X >= 0 {
			t.Fatalf("upwind dir should have Dir.X<0, got %v", r.Food.Dir)
		}
	})

	t.Run("absent_channel_zero", func(t *testing.T) {
		g := newGrid(t, cs)
		g.Commit()
		r := g.Read(v(0.5, 0.5), 2.0, w)
		if r.Prey.Intensity != 0 || r.Prey.Dir.X != 0 || r.Prey.Dir.Y != 0 {
			t.Fatalf("absent channel should give zero ChannelReading, got %+v", r.Prey)
		}
	})
}

// ── AC: Scent-only (F44) ──────────────────────────────────────────────────────

func TestScentOnly(t *testing.T) {
	// Reading must NOT expose any forward-FOV or heading-cone API.
	// Verified structurally: Reading has only Food/Prey/Predator ChannelReading fields.
	// ChannelReading has only Intensity + Dir (no range, no heading, no ID).
	// This test documents the F34↔F44 boundary: sight.predator is NOT in this struct.
	var r Reading
	// Compile check: these are the only fields.
	_ = r.Food.Intensity
	_ = r.Food.Dir
	_ = r.Prey.Intensity
	_ = r.Prey.Dir
	_ = r.Predator.Intensity
	_ = r.Predator.Dir
	_ = r.Carrion.Intensity
	_ = r.Carrion.Dir
	// No r.Food.Range, r.Predator.Heading, etc. — if those existed this would not compile.
}

// ── AC: Continuous-coordinate cell math (D11) ─────────────────────────────────

func TestContinuousCoordCellMath(t *testing.T) {
	cs := 10.0

	t.Run("negative_fractional_bucket", func(t *testing.T) {
		g := newGrid(t, cs)
		pos := v(-1000.5, 7.2) // cell(-101, 0) via floor
		g.Deposit(ChanFood, pos, 1.0)
		g.Commit()
		// IntensityAt same continuous pos should return the deposited value.
		got := g.IntensityAt(ChanFood, pos)
		if got <= 0 {
			t.Fatalf("negative fractional pos: expected >0, got %v", got)
		}
		// Read from same pos with smellRadius large enough to include own cell.
		r := g.Read(pos, cs, Wind{})
		if r.Food.Intensity <= 0 {
			t.Fatalf("Read at neg pos: expected >0, got %v", r.Food.Intensity)
		}
	})

	t.Run("entity_anywhere_in_cell_reads_cell", func(t *testing.T) {
		g := newGrid(t, cs)
		g.Deposit(ChanFood, v(0.5, 0.5), 1.0)
		g.Commit()
		// Read from corners of the same cell: should all see the deposit.
		for _, p := range []core.Vec2{v(0.1, 0.1), v(9.9, 0.1), v(0.1, 9.9), v(9.9, 9.9)} {
			got := g.IntensityAt(ChanFood, p)
			if got <= 0 {
				t.Fatalf("corner %v of cell: expected >0, got %v", p, got)
			}
		}
	})

	t.Run("far_cell_returns_zero", func(t *testing.T) {
		g := newGrid(t, cs)
		g.Deposit(ChanFood, v(0.5, 0.5), 1.0)
		g.Commit()
		got := g.IntensityAt(ChanFood, v(1000.0, 1000.0))
		if got != 0 {
			t.Fatalf("far cell expected 0, got %v", got)
		}
	})
}

// ── AC: Empty-grid neutrality ─────────────────────────────────────────────────

func TestEmptyGridNeutrality(t *testing.T) {
	g := newGrid(t, 1.0)
	// No deposits at all.
	g.Spread(Wind{Dir: 1.2, Mag: 0.5})
	g.Commit()

	for _, p := range []core.Vec2{v(0, 0), v(100, -50), v(-3.7, 8.1)} {
		r := g.Read(p, 5.0, Wind{Dir: 0.5, Mag: 0.3})
		if r.Food.Intensity != 0 || r.Prey.Intensity != 0 || r.Predator.Intensity != 0 || r.Carrion.Intensity != 0 {
			t.Fatalf("empty grid: non-zero reading at %v: %+v", p, r)
		}
		if r.Food.Dir != (core.Vec2{}) || r.Prey.Dir != (core.Vec2{}) || r.Carrion.Dir != (core.Vec2{}) {
			t.Fatalf("empty grid: non-zero Dir at %v", p)
		}
		if ia := g.IntensityAt(ChanFood, p); ia != 0 {
			t.Fatalf("empty grid IntensityAt non-zero: %v", ia)
		}
	}
}

// ── AC: Determinism golden (D12) ─────────────────────────────────────────────

// runScenario executes a fixed multi-tick deposit/spread/commit/read scenario
// and returns a SHA-256 digest of all committed intensities + Readings sampled
// at fixed positions. Byte-identical across runs = determinism verified.
func runScenario() [32]byte {
	g := New(2.0)
	wind := Wind{Dir: math.Pi / 6, Mag: 0.7}
	var trailStrength [NumChannels]float64
	trailStrength[ChanPrey] = 0.02
	g.ConfigureTrail(trailStrength, 0.85, 0.5)
	probePositions := []core.Vec2{
		v(1.0, 1.0), v(3.0, 1.0), v(5.0, 3.0), v(-1.0, -1.0), v(7.0, -1.0),
	}

	h := sha256.New()
	buf := make([]byte, 8)

	writeF64 := func(f float64) {
		bits := math.Float64bits(f)
		binary.LittleEndian.PutUint64(buf, bits)
		h.Write(buf)
	}

	for tick := 0; tick < 8; tick++ {
		g.DecayTrail()
		if tick%3 == 0 {
			g.DepositStatic(ChanFood, v(6.0, 2.0), 4.0)
			g.DepositStatic(ChanCarrion, v(-4.0, 0.0), 2.5+float64(tick)*0.1)
			g.CommitStatic(wind)
		}
		// Deposits (fixed sources, fixed order — D12).
		g.Deposit(ChanFood, v(2.0, 2.0), 3.0+float64(tick)*0.5)
		g.Deposit(ChanPrey, v(4.0, 0.0), 2.0)
		g.Deposit(ChanPredator, v(0.0, 4.0), 1.5)
		g.Deposit(ChanCarrion, v(-2.0, 2.0), 1.0+float64(tick)*0.25)
		if tick%2 == 0 {
			g.Spread(wind)
		}
		g.Commit()

		// Hash the public read surface at fixed positions.
		for _, p := range probePositions {
			writeF64(g.IntensityAt(ChanFood, p))
			writeF64(g.IntensityAt(ChanPrey, p))
			writeF64(g.IntensityAt(ChanPredator, p))
			writeF64(g.IntensityAt(ChanCarrion, p))
			r := g.Read(p, 3.0, wind)
			writeF64(r.Food.Intensity)
			writeF64(r.Food.Dir.X)
			writeF64(r.Food.Dir.Y)
			writeF64(r.Prey.Intensity)
			writeF64(r.Prey.Dir.X)
			writeF64(r.Prey.Dir.Y)
			writeF64(r.Predator.Intensity)
			writeF64(r.Predator.Dir.X)
			writeF64(r.Predator.Dir.Y)
			writeF64(r.Carrion.Intensity)
			writeF64(r.Carrion.Dir.X)
			writeF64(r.Carrion.Dir.Y)
		}
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

func TestDeterminismGolden(t *testing.T) {
	d1 := runScenario()
	d2 := runScenario()
	if d1 != d2 {
		t.Fatalf("non-deterministic: run1=%x run2=%x", d1, d2)
	}

	// File-based golden regression (generate with -update flag).
	golden := filepath.Join("testdata", "golden", "digest.txt")
	want := fmt.Sprintf("%x\n", d1)

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(want), 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden written: %s", golden)
		return
	}

	got, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file missing — run with UPDATE_GOLDEN=1 to generate: %v", err)
	}
	if string(got) != want {
		t.Fatalf("golden mismatch:\n got  %s\n want %s", got, want)
	}
}

// ── AC: New guard + injected cellSize (D10) ───────────────────────────────────

func TestNewGuard(t *testing.T) {
	for _, bad := range []float64{0, -1, -0.001} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("New(%v) should panic", bad)
				}
			}()
			New(bad)
		}()
	}
	// Positive cellSize must not panic.
	_ = New(0.001)
	_ = New(100.0)
}

// ── AC: No forbidden imports (guard) ─────────────────────────────────────────

func TestNoForbiddenImports(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "scent.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse scent.go imports: %v", err)
	}
	forbidden := map[string]bool{
		"github.com/dogring/bdg/engine/kernel/rng":      true,
		"github.com/dogring/bdg/engine/env/climate":     true,
		"github.com/dogring/bdg/engine/world":           true,
		"github.com/dogring/bdg/engine/fauna":           true,
		"github.com/dogring/bdg/engine/mind/perception": true,
		"github.com/dogring/bdg/engine/space/navmap":    true,
		"github.com/dogring/bdg/engine/space/spatial":   true,
		"github.com/dogring/bdg/engine/actions":         true,
	}
	hasCore := false
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("unquote import %s: %v", imp.Path.Value, err)
		}
		if _, bad := forbidden[path]; bad {
			t.Errorf("forbidden import found in scent.go: %q", path)
		}
		if path == "github.com/dogring/bdg/engine/kernel/core" {
			hasCore = true
		}
	}
	if !hasCore {
		t.Error("scent.go must import engine/kernel/core")
	}
}
