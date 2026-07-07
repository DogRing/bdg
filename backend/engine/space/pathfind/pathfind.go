// Package pathfind is a deterministic shortest-path query over a navmap snapshot.
//
// Given a start and goal in continuous space it returns a waypoint polyline (string-pulled, so it is
// NOT cell-stair-stepped) and the total traversal cost, routing around impassable footprints/terrain
// and preferring low-`wear` cells (navmap.StepCost already folds base cost × wear multiplier ×
// geometric length). It is a PURE query — it never mutates the navmap (wear deposit is the world's
// job). This is what makes "MoveTo cost reflects the real route" and "reroute around obstacles" true.
//
// Determinism (D12): the open set is a binary heap whose ties break by a FIXED cell ordering (f, then
// Cell.R, then Cell.Q) — never insertion order; neighbours come from navmap.Neighbors in its FIXED
// canonical order (flat-top hex — pathfind hardcodes no offsets); no map iteration affects the result.
// Same (navmap snapshot, start, goal, caps) ⇒ byte-identical path.
package pathfind

import (
	"container/heap"
	"math"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
)

// Caps describes which gated terrain the searcher may traverse, derived from the agent's available
// action tags / capabilities (e.g. can it Swim?). A cell whose navmap.RequiredTags is non-empty is
// traversable only if Caps.Tags contains ONE of them.
type Caps struct {
	Tags map[core.Tag]bool
}

// maxExpansions caps node expansions so the search always terminates and never freezes; on exhaustion
// Path/EstimateCost behave as "unreachable" (ok=false). The closed set already bounds the search to the
// finite cell count — this is a defensive ceiling, an algorithm parameter (not content), per the SPEC.
const maxExpansions = 1 << 22

// Path finds the cheapest route from start to goal over the navmap snapshot. ok=false when the goal is
// unreachable (walled off / impassable / no capability, or the expansion budget is exhausted). cost is
// the summed navmap.StepCost; waypoints is start→…→goal in continuous coordinates (string-pulled).
func Path(m *navmap.NavMap, start, goal core.Vec2, caps Caps) (waypoints []core.Vec2, cost float64, ok bool) {
	startCell := m.CellOf(start)
	goalCell := m.CellOf(goal)

	if !traversable(m, goalCell, caps) {
		return nil, 0, false // goal sits on an impassable/un-capable cell → unreachable
	}
	if startCell == goalCell {
		return []core.Vec2{start, goal}, start.Distance(goal) * m.BaseCost(startCell), true
	}

	// A* over cells. gScore/cameFrom/closed are keyed maps (never range-iterated for ordering, D12);
	// the open heap provides the deterministic visit order.
	gScore := map[navmap.Cell]float64{startCell: 0}
	cameFrom := map[navmap.Cell]navmap.Cell{}
	closed := map[navmap.Cell]bool{}

	open := &openHeap{}
	heap.Init(open)
	heap.Push(open, node{cell: startCell, f: heuristic(m, startCell, goal)})

	reached := false
	expansions := 0
	for open.Len() > 0 {
		cur := heap.Pop(open).(node)
		if cur.cell == goalCell {
			reached = true
			break
		}
		if closed[cur.cell] {
			continue // stale heap entry (a cheaper one was already processed)
		}
		closed[cur.cell] = true
		expansions++
		if expansions > maxExpansions {
			return nil, 0, false // budget exhausted → behave as unreachable (never freeze)
		}

		curG := gScore[cur.cell]
		for _, nb := range m.Neighbors(cur.cell) {
			if closed[nb] || !traversable(m, nb, caps) {
				continue
			}
			step := m.StepCost(cur.cell, nb)
			if math.IsInf(step, 1) {
				continue
			}
			tentative := curG + step
			if g, seen := gScore[nb]; !seen || tentative < g {
				gScore[nb] = tentative
				cameFrom[nb] = cur.cell
				heap.Push(open, node{cell: nb, f: tentative + heuristic(m, nb, goal)})
			}
		}
	}
	if !reached {
		return nil, 0, false
	}

	cellPath := reconstruct(cameFrom, startCell, goalCell)
	cost = gScore[goalCell]

	// Raw waypoints: the true start, each intermediate cell centre, the true goal. Then string-pull.
	raw := make([]core.Vec2, 0, len(cellPath))
	raw = append(raw, start)
	for _, c := range cellPath[1 : len(cellPath)-1] {
		raw = append(raw, m.CellCenter(c))
	}
	raw = append(raw, goal)
	return stringPull(m, raw, caps), cost, true
}

// EstimateCost is a cheap admissible lower bound on the true Path cost (Euclidean distance × the
// navmap's minimum per-unit cost factor) — used for nearest-by-path target ranking without a full
// search. It is NEVER greater than the true Path cost (so callers can prune safely). caps does not
// affect a lower bound, so it is accepted only for signature symmetry with Path.
func EstimateCost(m *navmap.NavMap, start, goal core.Vec2, caps Caps) float64 {
	_ = caps
	return start.Distance(goal) * m.MinCostFactor()
}

// ── internals ────────────────────────────────────────────────────────────────

// traversable reports whether cell c may be entered: it must be navmap-Passable (terrain passable, no
// footprint, in bounds) AND, if it carries RequiredTags, Caps must satisfy one of them.
func traversable(m *navmap.NavMap, c navmap.Cell, caps Caps) bool {
	if !m.Passable(c) {
		return false
	}
	req := m.RequiredTags(c)
	if len(req) == 0 {
		return true
	}
	for _, t := range req {
		if caps.Tags[t] {
			return true
		}
	}
	return false
}

// heuristic is the admissible A* estimate from cell c to the goal: Euclidean world distance (a lower
// bound on the true grid path length) × the navmap's minimum per-unit cost factor. Admissible ⇒ A*
// yields optimal paths. Uses CellCenter so it needs no private navmap geometry.
func heuristic(m *navmap.NavMap, c navmap.Cell, goal core.Vec2) float64 {
	return m.CellCenter(c).Distance(goal) * m.MinCostFactor()
}

// reconstruct walks cameFrom from goalCell back to startCell, returning the cell path start→…→goal.
func reconstruct(cameFrom map[navmap.Cell]navmap.Cell, startCell, goalCell navmap.Cell) []navmap.Cell {
	rev := []navmap.Cell{goalCell}
	for c := goalCell; c != startCell; {
		prev := cameFrom[c]
		rev = append(rev, prev)
		c = prev
	}
	// reverse in place → start..goal
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// stringPull greedily removes intermediate waypoints whose corner is not needed: it keeps a point only
// when the straight segment from the last kept anchor to the NEXT point is not clear (would clip an
// untraversable cell). Result is a continuous polyline, not a cell-stair-step. start and goal are always
// kept. Deterministic (fixed sampling, no map iteration).
func stringPull(m *navmap.NavMap, pts []core.Vec2, caps Caps) []core.Vec2 {
	if len(pts) <= 2 {
		return pts
	}
	out := []core.Vec2{pts[0]}
	anchor := 0
	for i := 1; i < len(pts)-1; i++ {
		if !lineClear(m, pts[anchor], pts[i+1], caps) {
			out = append(out, pts[i]) // pts[i] is a required corner
			anchor = i
		}
	}
	out = append(out, pts[len(pts)-1]) // always keep the goal
	return out
}

// lineClear samples the segment a→b at sub-cell intervals and reports whether every sampled cell is
// traversable. The sample step is half a cell (derived from CellCenter of two adjacent cells) so the
// walk cannot skip a cell. Sampling-based LoS is adequate for P1 string-pulling (the underlying A* path
// is already valid; this only prunes clearly-redundant points).
func lineClear(m *navmap.NavMap, a, b core.Vec2, caps Caps) bool {
	cellSize := m.CellCenter(navmap.Cell{Q: 1, R: 0}).X - m.CellCenter(navmap.Cell{Q: 0, R: 0}).X
	if cellSize <= 0 {
		cellSize = 1
	}
	d := b.Sub(a)
	dist := math.Hypot(d.X, d.Y)
	n := int(math.Ceil(dist/(cellSize*0.5))) + 1
	for k := 0; k <= n; k++ {
		t := float64(k) / float64(n)
		p := a.Add(d.Scale(t))
		if !traversable(m, m.CellOf(p), caps) {
			return false
		}
	}
	return true
}

// ── deterministic open set (binary heap; ties by Cell Y then X) ───────────────

type node struct {
	cell navmap.Cell
	f    float64
}

type openHeap []node

func (h openHeap) Len() int { return len(h) }

// Less orders by f-score, breaking ties by a FIXED cell ordering (R then Q — canonical hex) so the pop
// order — and hence the resulting path — is reproducible regardless of push order (D12).
func (h openHeap) Less(i, j int) bool {
	if h[i].f != h[j].f {
		return h[i].f < h[j].f
	}
	if h[i].cell.R != h[j].cell.R {
		return h[i].cell.R < h[j].cell.R
	}
	return h[i].cell.Q < h[j].cell.Q
}

func (h openHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *openHeap) Push(x any)   { *h = append(*h, x.(node)) }
func (h *openHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
