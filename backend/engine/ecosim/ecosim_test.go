// Package ecosim_test is the G5 ecosystem integration test harness tests.
// Tests run the full 2000-tick loop and verify determinism, terrain containment,
// and emergent locomotion behaviour using no external IO (pure simulation).
package ecosim

import (
	"fmt"
	"math"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
)

const (
	testSeed  = int64(42)
	testTicks = 2000
)

// wolfInitPos is wolf's starting position (used in movement assertions).
var wolfInitPos = [2]float64{15, 35}

// TestEcosimRuns2000Ticks is the primary integration test:
//   - Runs the G5 ecosystem for exactly 2000 ticks with no panic.
//   - Verifies all animals remain within world bounds.
//   - Verifies fish stay in the river corridor (x ∈ [RiverXMin, RiverXMax]).
//   - Verifies wolf has moved from its initial position (it is always ACTIVE, D12).
//   - Prints a per-50-tick summary for human inspection of emergent behaviour.
func TestEcosimRuns2000Ticks(t *testing.T) {
	w := NewWorldState(testSeed)

	type summary struct {
		tick         int
		wolfX, wolfY float64
		wolfAction   string
		deerCentX    float64
		deerCentY    float64
		wolfToDeer   float64
		meanDeerFear float64
		fishXMin     float64
		fishXMax     float64
	}
	var summaries []summary

	for tick := 0; tick < testTicks; tick++ {
		w.TickOnce()

		if (tick+1)%50 == 0 {
			wolf := findAnimal(w, "an:w1")
			deerCX, deerCY := deerCentroid(w)
			s := summary{
				tick:         tick + 1,
				wolfX:        wolf.Pos.X,
				wolfY:        wolf.Pos.Y,
				wolfAction:   string(wolf.CurrentAction),
				deerCentX:    deerCX,
				deerCentY:    deerCY,
				wolfToDeer:   dist(wolf.Pos.X, wolf.Pos.Y, deerCX, deerCY),
				meanDeerFear: meanDeerFear(w),
				fishXMin:     fishXMin(w),
				fishXMax:     fishXMax(w),
			}
			summaries = append(summaries, s)
		}
	}

	// ── Print per-50-tick summary ─────────────────────────────────────────────
	t.Logf("=== Ecosystem 2000-tick summary (seed=%d) ===", testSeed)
	t.Logf("%-6s  %-16s  %-16s  %-8s  %-9s  %-8s  %-8s",
		"tick", "wolf(x,y)", "deer-centroid", "wolf→deer", "deer-fear", "fish-Xmin", "fish-Xmax")
	for _, s := range summaries {
		t.Logf("%-6d  (%-5.1f,%-5.1f)     (%-5.1f,%-5.1f)     %-8.2f  %-9.4f  %-8.2f  %-8.2f",
			s.tick, s.wolfX, s.wolfY, s.deerCentX, s.deerCentY,
			s.wolfToDeer, s.meanDeerFear, s.fishXMin, s.fishXMax)
	}
	t.Logf("=== %d ticks completed ===", testTicks)

	// ── Assertion 1: All animals within world bounds ──────────────────────────
	for _, a := range w.Animals {
		if a.Pos.X < WorldMin || a.Pos.X >= WorldMax || a.Pos.Y < WorldMin || a.Pos.Y >= WorldMax {
			t.Errorf("animal %s out of world bounds at (%.3f, %.3f)", a.ID, a.Pos.X, a.Pos.Y)
		}
	}

	// ── Assertion 2: Fish terrain containment (impassable = soil/sand/mountain/bare_rock) ──
	// NavAdapter maps continuous pos → cell → terrainAt cell center; fish cannot
	// enter soil (x<40 or x>50, y<85). River cells span x∈[40,50] (NavCell=2.0).
	// Fish can reach y<85 only within x∈[40,50] (river); outside is soil.
	for _, a := range w.Animals {
		if a.Species != "fish" {
			continue
		}
		// A fish position must lie in the river corridor [RiverXMin, RiverXMax].
		// It cannot cross to soil or mountain (impassable). The sea at y≥85 is only
		// reachable through soil (x<40 or x>50, y≥85 outside river), which is blocked.
		if a.Pos.X < RiverXMin || a.Pos.X > RiverXMax {
			t.Errorf("fish %s escaped river: pos=(%.3f, %.3f), x must be in [%.0f,%.0f]",
				a.ID, a.Pos.X, a.Pos.Y, RiverXMin, RiverXMax)
		}
	}

	// ── Assertion 3: Wolf moved from initial position (wolf is always ACTIVE) ──
	wolf := findAnimal(w, "an:w1")
	wolfDist := dist(wolf.Pos.X, wolf.Pos.Y, wolfInitPos[0], wolfInitPos[1])
	if wolfDist < 2.0 {
		t.Errorf("wolf did not move after %d ticks: still near (%.1f,%.1f), dist=%.4f",
			testTicks, wolfInitPos[0], wolfInitPos[1], wolfDist)
	}
	t.Logf("wolf final pos=(%.2f, %.2f), displacement from start=%.2f", wolf.Pos.X, wolf.Pos.Y, wolfDist)

	// ── Assertion 4: Drives are bounded [0,1] ─────────────────────────────────
	for _, a := range w.Animals {
		for dr, v := range a.Drives {
			if v < -0.001 || v > 1.001 {
				t.Errorf("animal %s drive %s out of [0,1]: %.6f", a.ID, dr, v)
			}
		}
	}

	// ── Assertion 5: Bear and goat started in mountain; must not be in sea ────
	// Bear+goat are impassable on sea; they cannot enter it.
	for _, a := range w.Animals {
		if a.Species == "bear" || a.Species == "goat" {
			terrain := TerrainAtPos(a.Pos)
			if terrain == "sea" {
				t.Errorf("%s %s ended up in sea at (%.3f,%.3f)", a.Species, a.ID, a.Pos.X, a.Pos.Y)
			}
		}
	}
}

// TestEcosimDeterminism verifies that two runs with the same seed produce
// byte-identical state at every sampled checkpoint (D12 compliance).
// Runs 2000 ticks twice and compares DigestState at ticks 500, 1000, 1500, 2000.
func TestEcosimDeterminism(t *testing.T) {
	checkpoints := []int{500, 1000, 1500, 2000}
	digestsA := make(map[int]string, len(checkpoints))
	digestsB := make(map[int]string, len(checkpoints))
	checkSet := make(map[int]bool, len(checkpoints))
	for _, c := range checkpoints {
		checkSet[c] = true
	}

	// Run A.
	wA := NewWorldState(testSeed)
	for tick := 0; tick < testTicks; tick++ {
		wA.TickOnce()
		if checkSet[tick+1] {
			digestsA[tick+1] = DigestState(wA.Animals)
		}
	}

	// Run B (fresh state, same seed).
	wB := NewWorldState(testSeed)
	for tick := 0; tick < testTicks; tick++ {
		wB.TickOnce()
		if checkSet[tick+1] {
			digestsB[tick+1] = DigestState(wB.Animals)
		}
	}

	// Compare.
	allMatch := true
	for _, c := range checkpoints {
		a, b := digestsA[c], digestsB[c]
		match := a == b
		if !match {
			allMatch = false
		}
		t.Logf("tick %4d: %s %s", c, shortDigest(a), map[bool]string{true: "== MATCH", false: "!= MISMATCH"}[match])
	}
	if !allMatch {
		t.Errorf("determinism broken: run A and B produced different state digests")
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// findAnimal returns a pointer to the Animal with the given ID in w.Animals.
// Panics if not found (test-only helper; a missing ID is a test-setup bug).
func findAnimal(w *WorldState, id string) *fauna.Animal {
	for i := range w.Animals {
		if string(w.Animals[i].ID) == id {
			return &w.Animals[i]
		}
	}
	panic(fmt.Sprintf("ecosim_test: animal %q not found", id))
}

// deerCentroid returns the mean position of all deer.
func deerCentroid(w *WorldState) (cx, cy float64) {
	n := 0
	for _, a := range w.Animals {
		if a.Species == "deer" {
			cx += a.Pos.X
			cy += a.Pos.Y
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return cx / float64(n), cy / float64(n)
}

// meanDeerFear returns the mean fear drive across all deer.
func meanDeerFear(w *WorldState) float64 {
	n := 0
	sum := 0.0
	for _, a := range w.Animals {
		if a.Species == "deer" {
			sum += a.Drives["fear"]
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// fishXMin returns the minimum X of all fish positions.
func fishXMin(w *WorldState) float64 {
	min := math.MaxFloat64
	for _, a := range w.Animals {
		if a.Species == "fish" && a.Pos.X < min {
			min = a.Pos.X
		}
	}
	return min
}

// fishXMax returns the maximum X of all fish positions.
func fishXMax(w *WorldState) float64 {
	max := -math.MaxFloat64
	for _, a := range w.Animals {
		if a.Species == "fish" && a.Pos.X > max {
			max = a.Pos.X
		}
	}
	return max
}

// dist returns the Euclidean distance between two 2D points.
func dist(x1, y1, x2, y2 float64) float64 {
	dx, dy := x2-x1, y2-y1
	return math.Sqrt(dx*dx + dy*dy)
}

// shortDigest returns the first 16 hex chars of a digest for readable logging.
func shortDigest(d string) string {
	if len(d) <= 16 {
		return d
	}
	return d[:16] + "..."
}
