// Package scent is the shared multi-channel scent field: an auxiliary spatial index
// over continuous space (D11 — the grid is an *index*, not the world). It owns deposit,
// wind-spread, commit (double-buffer), and read operations. World drives cadence; fauna
// (and later perception) read. No IO, no wall-clock, no RNG (D12).
package scent

import (
	"math"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── Balance-tunable constants ──────────────────────────────────────────────────
// These are named, never scattered literals. Changing them alters diffusion
// dynamics; they are design-balance values owned here until moved to content.

// spreadFraction is the fraction of a cell's intensity that diffuses out per
// Spread call. Range (0,1). Higher → wider/faster plume; lower → tighter.
const spreadFraction = 0.40

// spreadFalloff is the attenuation multiplier applied to the donated intensity
// before it reaches neighbors. < 1 ⇒ spread strictly decreases away from source
// ⇒ the gradient always points back toward the source.
const spreadFalloff = 0.60

// windBiasBase is the uniform base weight each neighbor receives before wind bias
// is added. Fixed at 1.0; the wind dot-product scales above/below this baseline.
const windBiasBase = 1.0

// ── Channel ───────────────────────────────────────────────────────────────────

// Channel selects one scent layer (F22 — multi-channel; one scalar intensity per
// channel per cell). The constant order is FIXED (array index / determinism, D12);
// a new channel appends, never re-orders.
type Channel uint8

const (
	ChanFood     Channel = iota // edible-flora / food scent (herbivore → Graze); tag scent:food
	ChanPrey                    // prey-animal scent (carnivore → Hunt);           tag scent:prey
	ChanPredator                // predator scent (early-warning → Wary + F45);    tag scent:predator
	ChanCarrion                 // carcass/rot scent (scavenger/predator → Feed);  tag scent:carrion
	NumChannels  = iota         // count (array width); not a Channel value
)

// ── Wind ──────────────────────────────────────────────────────────────────────

// Wind is the local wind VALUE world injects from climate (CA2). Dir is the
// direction the wind blows toward (radians); Mag is strength (≥ 0). Mag==0 ⇒
// isotropic short-range spread + neighbor-intensity read gradient (P1 neutral).
type Wind struct {
	Dir float64 // radians; direction wind blows toward
	Mag float64 // ≥ 0; 0 ⇒ no downwind transport (P1 climate-OFF)
}

// ── Internal types ────────────────────────────────────────────────────────────

// cellKey is a comparable integer cell coordinate (D12: map-iteration never
// drives logic; sorted-slice traversal used throughout).
type cellKey struct{ cx, cy int }

// cellVals holds one scalar intensity per channel for a single cell.
type cellVals [NumChannels]float64

// neighborOffsets lists the 8 Moore-neighborhood offsets in a fixed canonical
// order (sorted by dcy, then dcx). Fixed order ⇒ byte-identical spread regardless
// of deposit order (D12). The index into this array is stable across runs.
var neighborOffsets = [8][2]int{
	{-1, -1}, {0, -1}, {1, -1},
	{-1, 0}, {1, 0},
	{-1, 1}, {0, 1}, {1, 1},
}

// ── Grid ──────────────────────────────────────────────────────────────────────

// Grid is the uniform multi-channel scent field. DOUBLE-BUFFERED: committed is
// the stable read snapshot; pending accumulates deposits/spread for the next tick.
// Not safe for concurrent mutation (single-threaded apply phase; read phase is
// parallel-safe because it only reads committed).
type Grid struct {
	cellSize  float64
	committed map[cellKey]cellVals // stable; Read/IntensityAt see this
	pending   map[cellKey]cellVals // accumulates until Commit
}

// New creates an empty grid with the given square cell edge length (world units).
// Panics if cellSize ≤ 0. Both buffers start empty (intensity 0 everywhere).
func New(cellSize float64) *Grid {
	if cellSize <= 0 {
		panic("scent.New: cellSize must be > 0")
	}
	return &Grid{
		cellSize:  cellSize,
		committed: make(map[cellKey]cellVals),
		pending:   make(map[cellKey]cellVals),
	}
}

// cellOf converts a continuous Vec2 position to its integer cell key.
// Uses math.Floor per axis ⇒ negative-safe (D11: floor(-0.1/1.0) = -1, not 0).
func cellOf(pos core.Vec2, cellSize float64) cellKey {
	return cellKey{
		cx: int(math.Floor(pos.X / cellSize)),
		cy: int(math.Floor(pos.Y / cellSize)),
	}
}

// sortedKeys returns all keys of m in a deterministic sorted order (cy, cx
// ascending). Used wherever buffer traversal must be logic-driving (D12).
func sortedKeys(m map[cellKey]cellVals) []cellKey {
	keys := make([]cellKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].cy != keys[j].cy {
			return keys[i].cy < keys[j].cy
		}
		return keys[i].cx < keys[j].cx
	})
	return keys
}

// ── Deposit ───────────────────────────────────────────────────────────────────

// Deposit ADDS intensity (≥ 0, ∝ source magnitude) to channel ch at the cell
// containing pos in the PENDING buffer (accumulative — overlapping sources stack).
// Negative/zero intensity is silently ignored. pos is continuous (D11); cell =
// floor(pos/cellSize) per axis.
func (g *Grid) Deposit(ch Channel, pos core.Vec2, intensity float64) {
	if intensity <= 0 {
		return
	}
	key := cellOf(pos, g.cellSize)
	v := g.pending[key]
	v[ch] += intensity
	g.pending[key] = v
}

// ── Spread ────────────────────────────────────────────────────────────────────

// neighborWeights computes the 8 normalized diffusion weights for the Moore
// neighborhood under wind. With Wind.Mag==0 all weights are equal (1/8).
// With Mag>0 downwind neighbors receive higher weight (dot-product bias); upwind
// weight is clamped to 0. Result indices match neighborOffsets exactly.
func neighborWeights(wind Wind) [8]float64 {
	windX := math.Cos(wind.Dir) * wind.Mag
	windY := math.Sin(wind.Dir) * wind.Mag
	var w [8]float64
	var total float64
	for i, off := range neighborOffsets {
		dot := float64(off[0])*windX + float64(off[1])*windY
		raw := windBiasBase + dot
		if raw < 0 {
			raw = 0
		}
		w[i] = raw
		total += raw
	}
	if total > 0 {
		for i := range w {
			w[i] /= total
		}
	}
	return w
}

// Spread runs ONE deterministic fixed-order diffusion stencil over the PENDING
// buffer (F33): visiting on-cells in sorted cell-key order, it moves spreadFraction
// of each cell's channel intensity to its 8 Moore neighbors weighted by wind (more
// downwind when Mag>0, isotropic when Mag==0). Intensity is attenuated by
// spreadFalloff before reaching neighbors ⇒ gradient always points toward source.
// No RNG; fixed sorted-key traversal + fixed neighborOffsets order ⇒ byte-identical
// regardless of deposit order (D12).
func (g *Grid) Spread(wind Wind) {
	if len(g.pending) == 0 {
		return
	}
	// Snapshot pending so reads and writes don't interfere within one Spread call.
	snap := make(map[cellKey]cellVals, len(g.pending))
	for k, v := range g.pending {
		snap[k] = v
	}

	nw := neighborWeights(wind)

	// Sorted traversal (D12: never raw map-range for logic).
	keys := sortedKeys(snap)
	for _, key := range keys {
		sv := snap[key]
		for ch := 0; ch < NumChannels; ch++ {
			v := sv[ch]
			if v <= 0 {
				continue
			}
			donated := v * spreadFraction
			received := donated * spreadFalloff

			// Source cell loses the donated amount.
			cv := g.pending[key]
			cv[ch] -= donated
			if cv[ch] < 0 {
				cv[ch] = 0
			}
			g.pending[key] = cv

			// Distribute attenuated intensity to neighbors in fixed order.
			for i, off := range neighborOffsets {
				w := nw[i]
				if w <= 0 {
					continue
				}
				nk := cellKey{key.cx + off[0], key.cy + off[1]}
				nv := g.pending[nk]
				nv[ch] += received * w
				g.pending[nk] = nv
			}
		}
	}
}

// ── Commit ────────────────────────────────────────────────────────────────────

// Commit swaps PENDING into COMMITTED and clears the new pending buffer, realizing
// the 1-tick latency (F33): deposits/spread at tick T are visible to reads at T+1.
// Because pending is rebuilt from current emitters each cadence, a vanished source's
// scent is absent from the new pending and thus absent from committed after the next
// Commit (no stale accumulation). Deterministic; no RNG.
func (g *Grid) Commit() {
	g.committed = g.pending
	g.pending = make(map[cellKey]cellVals)
}

// ── Read ──────────────────────────────────────────────────────────────────────

// IntensityAt returns the committed intensity of channel ch at the single cell
// containing pos (O(1), no neighbor scan). This is the cheap per-tick F45 wake
// probe: `IntensityAt(ChanPredator, pos) > wakeThreshold`. Reads COMMITTED only
// (no pending); 0 before the first Commit.
func (g *Grid) IntensityAt(ch Channel, pos core.Vec2) float64 {
	key := cellOf(pos, g.cellSize)
	return g.committed[key][ch]
}

// Read returns the multi-channel reading at pos over the COMMITTED buffer,
// scanning the cell containing pos plus all neighbor cells whose center falls
// within smellRadius (world units) of pos (F34). For each channel:
//   - Intensity = aggregate (sum) of committed cell intensities within range.
//   - Dir = unit vector toward higher intensity (= toward source):
//   - Wind.Mag>0 → upwind direction (-cos(wind.Dir), -sin(wind.Dir)), F34(c).
//   - Wind.Mag==0 → neighbor-intensity gradient (weighted centroid of cell centers).
//   - Dir is the zero vector when Intensity==0.
//
// PURE (committed buffer only); safe during the parallel read/score phase.
// Scent-only (F44): no forward-FOV sight — sight.predator comes from the parent's
// spatial-hash query, not this grid.
func (g *Grid) Read(pos core.Vec2, smellRadius float64, wind Wind) Reading {
	center := cellOf(pos, g.cellSize)
	rings := int(math.Ceil(smellRadius/g.cellSize)) + 1

	var totals [NumChannels]float64
	var gradX, gradY [NumChannels]float64

	// Iterate fixed nested-loop range (not map iteration): deterministic (D12).
	// The own cell (dcx==0, dcy==0) is ALWAYS included per the SPEC ("the cell containing
	// pos plus neighbor cells within smellRadius") — an entity at a cell corner must still
	// read its own cell regardless of smellRadius.
	for dcy := -rings; dcy <= rings; dcy++ {
		for dcx := -rings; dcx <= rings; dcx++ {
			nk := cellKey{center.cx + dcx, center.cy + dcy}
			v, ok := g.committed[nk]
			if !ok {
				continue
			}
			// Cell center in world coordinates (used for gradient direction).
			ccx := (float64(nk.cx) + 0.5) * g.cellSize
			ccy := (float64(nk.cy) + 0.5) * g.cellSize
			// Neighbour cells (not own) must be within smellRadius.
			if dcx != 0 || dcy != 0 {
				dist := math.Sqrt((ccx-pos.X)*(ccx-pos.X) + (ccy-pos.Y)*(ccy-pos.Y))
				if dist > smellRadius {
					continue
				}
			}
			for ch := 0; ch < NumChannels; ch++ {
				intensity := v[ch]
				if intensity <= 0 {
					continue
				}
				totals[ch] += intensity
				// Gradient: weight each cell center by its intensity.
				// Points toward regions of higher intensity (= toward source).
				gradX[ch] += intensity * (ccx - pos.X)
				gradY[ch] += intensity * (ccy - pos.Y)
			}
		}
	}

	chanReading := func(ch Channel) ChannelReading {
		intensity := totals[ch]
		if intensity == 0 {
			return ChannelReading{}
		}
		var dir core.Vec2
		if wind.Mag > 0 {
			// Upwind = opposite of wind direction = toward scent source.
			dir = core.Vec2{X: -math.Cos(wind.Dir), Y: -math.Sin(wind.Dir)}
		} else {
			gx, gy := gradX[ch], gradY[ch]
			mag := math.Sqrt(gx*gx + gy*gy)
			if mag > 0 {
				dir = core.Vec2{X: gx / mag, Y: gy / mag}
			}
		}
		return ChannelReading{Intensity: intensity, Dir: dir}
	}

	return Reading{
		Food:     chanReading(ChanFood),
		Prey:     chanReading(ChanPrey),
		Predator: chanReading(ChanPredator),
		Carrion:  chanReading(ChanCarrion),
	}
}

// ── Public result types ───────────────────────────────────────────────────────

// Reading is the per-channel scent result at one position. The parent fills §6
// operands from it (scent.food/prey/predator/carrion = Intensity; steer dir per channel).
type Reading struct {
	Food     ChannelReading
	Prey     ChannelReading
	Predator ChannelReading // Intensity → scent.predator (early-warning/Wary); NOT sight.predator (F44)
	Carrion  ChannelReading // Intensity → scent.carrion (carcass/rot homing)
}

// ChannelReading is one channel's intensity + gradient direction at a read position.
type ChannelReading struct {
	Intensity float64   // aggregate scalar concentration (0 ⇒ absent); §6 operand
	Dir       core.Vec2 // unit direction toward higher intensity (= toward source; upwind if wind,
	// else neighbor-intensity gradient). Zero vector when Intensity==0. Parent reverses for Flee.
}
