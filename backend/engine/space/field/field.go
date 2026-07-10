// Package field is a deterministic scalar POTENTIAL field over a navmap snapshot, built by a multi-source
// weighted flood and sampled at CONTINUOUS positions to yield an intensity + gradient. It is the
// foundation of the fauna potential-field steering subsystem (docs/plans/fauna.md §4): a hazard field
// (source weight = terrain danger) yields a repulsion that SCALES WITH DANGER SEVERITY (deep water / a
// cliff push harder and farther than a shallow bank) — a continuous cost, never an absolute block, so a
// stronger drive/flight can overcome it (FM5). The same primitive later serves resource ATTRACTION
// (water for thirst, FM4): attraction = toward increasing intensity.
//
// It is an AUXILIARY INDEX (D11 — the navmap grid is an index; agent positions stay continuous float):
// this package snaps nothing into a position, it reads CellOf(p) to look up a value and returns a
// continuous direction. Pure query — never mutates the navmap.
//
// Determinism (D12): sources seed the max-heap in sorted (R,Q) order; the heap orders by (intensity
// desc, Cell.R, Cell.Q); neighbours come from navmap.Neighbors' fixed canonical order; the intensity
// map is never range-iterated for ordering. Same (navmap, sources, decay, passable) ⇒ byte-identical.
package field

import (
	"container/heap"
	"math"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
)

// Source is one weighted origin cell (a hazard cell's danger, a water cell's attractiveness, …). Weight
// is the intensity AT the cell; it decays linearly with distance-through-the-passable-graph and reaches
// 0 at Weight/decay world-units away — so a STRONGER source reaches FARTHER (a deep sea outranges a
// shallow bank). Weight ≤ 0 sources are ignored.
type Source struct {
	Cell   navmap.Cell
	Weight float64
}

// Field is an immutable scalar potential field over a navmap: at each cell, the MAX over sources of
// Weight − decay·dist (distance through the passable graph), floored at 0. Cells no source reaches are
// absent (intensity 0). Sampled at continuous positions (D11). Pure/read-only after Build.
type Field struct {
	m         *navmap.NavMap
	intensity map[navmap.Cell]float64
}

// Build runs a multi-source weighted max-flood from sources over m, expanding only through cells where
// passable(cell) is true, decaying intensity by decayPerUnit × geometric hex-step length and keeping the
// MAX contribution. A source is seeded at its Weight even if the cell is itself impassable (a cliff /
// deep-water hazard IS the origin). Empty/degenerate sources or decayPerUnit ≤ 0 ⇒ only the source cells
// carry intensity (no spread). passable == nil ⇒ navmap.Passable. Pure, D12.
func Build(m *navmap.NavMap, sources []Source, decayPerUnit float64, passable func(navmap.Cell) bool) *Field {
	if passable == nil {
		passable = m.Passable
	}
	intensity := make(map[navmap.Cell]float64, len(sources))

	// Seed sources in sorted (R,Q) order (D12), keeping the max weight per cell.
	srt := append([]Source(nil), sources...)
	sort.Slice(srt, func(i, j int) bool {
		if srt[i].Cell.R != srt[j].Cell.R {
			return srt[i].Cell.R < srt[j].Cell.R
		}
		return srt[i].Cell.Q < srt[j].Cell.Q
	})
	h := &maxHeap{}
	for _, s := range srt {
		if s.Weight <= 0 {
			continue
		}
		if cur, ok := intensity[s.Cell]; !ok || s.Weight > cur {
			intensity[s.Cell] = s.Weight
			*h = append(*h, fieldNode{cell: s.Cell, v: s.Weight})
		}
	}
	heap.Init(h)

	for h.Len() > 0 {
		cur := heap.Pop(h).(fieldNode)
		if cur.v < intensity[cur.cell] {
			continue // stale (a stronger contribution was already committed)
		}
		if decayPerUnit <= 0 {
			continue // no spread — sources only
		}
		center := m.CellCenter(cur.cell)
		for _, nb := range m.Neighbors(cur.cell) {
			if !passable(nb) {
				continue // danger does not spread through impassable terrain
			}
			nv := cur.v - decayPerUnit*center.Distance(m.CellCenter(nb))
			if nv <= 0 {
				continue // out of this source's reach
			}
			if old, ok := intensity[nb]; !ok || nv > old {
				intensity[nb] = nv
				heap.Push(h, fieldNode{cell: nb, v: nv})
			}
		}
	}
	return &Field{m: m, intensity: intensity}
}

// IntensityAt returns the field intensity at continuous p (0 where no source reaches). Index read, never
// an agent snap (D11).
func (f *Field) IntensityAt(p core.Vec2) float64 {
	return f.intensity[f.m.CellOf(p)]
}

// Gradient returns the UNIT direction of INCREASING intensity at p (toward the nearest/strongest source),
// from finite differences to p's cell neighbours in fixed order (D12). Zero where flat/degenerate.
// Repulsion = negate (away from danger); attraction (FM4) = as-is (toward the source).
func (f *Field) Gradient(p core.Vec2) core.Vec2 {
	c := f.m.CellOf(p)
	vc := f.intensity[c]
	center := f.m.CellCenter(c)
	var g core.Vec2
	for _, nb := range f.m.Neighbors(c) {
		dir := f.m.CellCenter(nb).Sub(center)
		n := math.Hypot(dir.X, dir.Y)
		if n == 0 {
			continue
		}
		// (intensity_nb − intensity_c) > 0 ⇒ nb is MORE dangerous ⇒ +gradient toward nb.
		w := (f.intensity[nb] - vc) / n
		g.X += w * dir.X
		g.Y += w * dir.Y
	}
	mag := math.Hypot(g.X, g.Y)
	if mag == 0 {
		return core.Vec2{}
	}
	return core.Vec2{X: g.X / mag, Y: g.Y / mag}
}

// Repulsion returns the away-from-danger steering vector at p: the local intensity times the UNIT
// away-direction (= intensity × −Gradient). Magnitude scales with danger SEVERITY (and proximity, since
// intensity decays with distance); zero where safe/flat. The fauna steer scales this by the species' §6
// hazard multiplier and blends it into the chosen heading (P_move1/FM5).
func (f *Field) Repulsion(p core.Vec2) core.Vec2 {
	g := f.Gradient(p)
	if g == (core.Vec2{}) {
		return core.Vec2{}
	}
	inten := f.IntensityAt(p)
	return core.Vec2{X: -g.X * inten, Y: -g.Y * inten}
}

// ── deterministic max-heap (D12: order by (v desc, R, Q), never push order) ──────

type fieldNode struct {
	cell navmap.Cell
	v    float64
}

type maxHeap []fieldNode

func (h maxHeap) Len() int { return len(h) }
func (h maxHeap) Less(i, j int) bool {
	if h[i].v != h[j].v {
		return h[i].v > h[j].v // MAX-heap: highest intensity popped first
	}
	if h[i].cell.R != h[j].cell.R {
		return h[i].cell.R < h[j].cell.R
	}
	return h[i].cell.Q < h[j].cell.Q
}
func (h maxHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(x any)   { *h = append(*h, x.(fieldNode)) }
func (h *maxHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
