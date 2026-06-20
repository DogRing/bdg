package planner

import (
	"sort"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/gates"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/tom"
)

// searchState carries the mutable state for one Plan call's GOAP+HTN search.
// It is created fresh per Plan call and discarded after.
type searchState struct {
	actionsReg       *actions.Registry
	gatesReg         *gates.Registry
	statsReg         *stats.Registry
	agent            AgentSnapshot
	urgencyThreshold float64
	sortedTagKeys    []core.Tag
	tagCosts         map[core.Tag]float64
	nodesExpanded    int
	maxNodes         int
	maxDepth         int
	maxActions       int
	relaxed          bool
	candidates       []Candidate // competing root candidates
	chosenActions    []actions.ActionID
}

// findBestSequence finds the cheapest valid action sequence for a target predicate
// given the agent's current state. Returns (actions, cost, error).
// This is the core GOAP backward-chaining + HTN forward-decomposition loop.
func (s *searchState) findBestSequence(pred core.Pred) ([]actions.ActionID, float64, error) {
	// Check if the predicate is already satisfied in the current world state.
	for _, p := range s.agent.SatisfiedFacts {
		if p == pred {
			return nil, 0, nil // already true; no actions needed
		}
	}

	producers := s.actionsReg.Producers(pred)
	if len(producers) == 0 {
		return nil, 0, ErrUnreachable // no producers for this predicate
	}

	// Evaluate all producers in IDs() order (D12).
	type evalResult struct {
		actionID actions.ActionID
		cost     float64
		visible  bool
	}
	results := make([]evalResult, 0, len(producers))

	for _, aid := range producers {
		// Budget: nodes expanded.
		s.nodesExpanded++
		if s.nodesExpanded > s.maxNodes {
			return nil, 0, ErrBudgetExceeded
		}

		adef, ok := s.actionsReg.Get(aid)
		if !ok {
			continue
		}

		// Compute tag-derived cost.
		cst := computeCost(adef.Tags, s.sortedTagKeys, s.tagCosts)

		// Evaluate gate visibility.
		gateAct := gates.Action{
			Tags:   adef.Tags,
			Target: "", // planner does not resolve targets at search time
		}
		gateSnap := gates.AgentSnapshot{
			SelfStats:  tomStatDistMeans(s.agent.SelfModel.EstStats),
			Known:      s.agent.Known,
			Stamina:    s.agent.Stamina,
			Mood:       s.agent.Mood,
			Adrenaline: s.agent.Adrenaline,
			Urgency:    s.agent.Urgency,
		}
		gateResult := s.gatesReg.Evaluate(gateAct, gateSnap)
		visible := gateResult.Visible

		// Apply CostMultiplier to tag-derived cost (NEW P3).
		cst *= gateResult.CostMultiplier

		// Apply dynamic relaxation (SPEC § Dynamic visibility).
		if !visible && s.agent.Urgency > s.urgencyThreshold {
			visible = true
			s.relaxed = true
		}

		results = append(results, evalResult{
			actionID: aid,
			cost:     cst,
			visible:  visible,
		})
	}

	// Among visible producers, select the one with the cheapest total sequence.
	// For each visible producer, find the best sequence that satisfies its preconditions.
	type candidateSeq struct {
		actions []actions.ActionID
		cost    float64
		choice  evalResult
	}

	var best *candidateSeq

	for _, r := range results {
		if !r.visible {
			continue
		}

		adef, _ := s.actionsReg.Get(r.actionID)

		// HTN decompose preconditions: resolve Requires (ALL must hold) and
		// RequiresAny (ANY must hold). If a Requires predicate is not already
		// satisfied, recursively find a producer for it.
		preSeq, preCost, err := s.decomposePreconditions(adef)
		if err != nil {
			if err == ErrBudgetExceeded {
				return nil, 0, err
			}
			// If preconditions are unreachable, skip this producer.
			continue
		}

		// Check plan length budget.
		totalLen := len(preSeq) + 1 // +1 for this action itself
		if totalLen > s.maxActions {
			continue // skip: would exceed MaxActions; ErrBudgetExceeded check at final plan construction
		}

		totalCost := preCost + r.cost

		// Build the full sequence: prereqs first, then this action last.
		seq := make([]actions.ActionID, 0, totalLen)
		seq = append(seq, preSeq...)
		seq = append(seq, r.actionID)

		if best == nil || totalCost < best.cost || (totalCost == best.cost && r.actionID < best.choice.actionID) {
			// This is a candidate for the root goal; record it.
			best = &candidateSeq{
				actions: seq,
				cost:    totalCost,
				choice:  r,
			}
		}
	}

	if best == nil {
		return nil, 0, ErrUnreachable
	}

	// Record candidates for the root goal trace.
	s.candidates = append(s.candidates, Candidate{
		Action:  best.choice.actionID,
		Cost:    best.choice.cost,
		Visible: best.choice.visible,
		Chosen:  true,
	})

	// Also record non-chosen visible candidates.
	for _, r := range results {
		if r.actionID != best.choice.actionID && r.visible {
			s.candidates = append(s.candidates, Candidate{
				Action:  r.actionID,
				Cost:    r.cost,
				Visible: r.visible,
				Chosen:  false,
			})
		}
	}

	return best.actions, best.cost, nil
}

// decomposePreconditions decomposes the preconditions of an ActionDef recursively
// (HTN). Requires predicates must ALL be satisfied; RequiresAny predicates must
// have at least one satisfied.
func (s *searchState) decomposePreconditions(adef actions.ActionDef) ([]actions.ActionID, float64, error) {
	// Check MaxDepth via recursion tracking. We use depth 1 for the action itself
	// plus depth for each recursion.
	return s.decomposePreconditionsAtDepth(adef, 0)
}

func (s *searchState) decomposePreconditionsAtDepth(adef actions.ActionDef, depth int) ([]actions.ActionID, float64, error) {
	if depth >= s.maxDepth {
		return nil, 0, ErrBudgetExceeded
	}

	// Collect all precondition predicates: Requires (ALL) + RequiresAny (ANY).
	// We need to find a sequence that satisfies Requires AND at least one of RequiresAny.
	// For Requires, we try to produce each predicate.
	// For RequiresAny, we only need one.

	// We'll build a plan for Requires predicates first (they are mandatory).
	// Then try to satisfy at least one RequiresAny predicate.

	type predSeq struct {
		pred    core.Pred
		actions []actions.ActionID
		cost    float64
	}

	// Resolve Requires (ALL must hold).
	var prereqSeq []actions.ActionID
	prereqCost := 0.0

	for _, pred := range adef.Requires {
		seq, cst, err := s.findBestSequence(pred)
		if err != nil {
			return nil, 0, err
		}
		if seq == nil {
			// nil with no error = predicate already in SatisfiedFacts; 0-cost, skip.
			continue
		}
		prereqSeq = append(prereqSeq, seq...)
		prereqCost += cst
	}

	// Check MaxActions early (partial).
	if len(prereqSeq) > s.maxActions {
		return nil, 0, ErrBudgetExceeded
	}

	// If there are RequiresAny predicates, try each and pick the cheapest.
	if len(adef.RequiresAny) > 0 {
		var bestAny struct {
			actions []actions.ActionID
			cost    float64
			found   bool
		}

		for _, pred := range adef.RequiresAny {
			seq, cst, err := s.findBestSequence(pred)
			if err != nil {
				if err == ErrBudgetExceeded {
					return nil, 0, err
				}
				// Unreachable — skip.
				continue
			}
			if seq == nil {
				// nil with no error = already in SatisfiedFacts; 0-cost solution.
				bestAny.found = true
				bestAny.cost = 0
				bestAny.actions = nil
				break // 0-cost is optimal; no need to check further
			}

			if !bestAny.found || cst < bestAny.cost || (cst == bestAny.cost && len(seq) < len(bestAny.actions)) {
				bestAny.actions = seq
				bestAny.cost = cst
				bestAny.found = true
			}
		}

		if !bestAny.found {
			return nil, 0, ErrUnreachable
		}

		prereqSeq = append(prereqSeq, bestAny.actions...)
		prereqCost += bestAny.cost
	}

	// Check MaxActions after preconditions.
	if len(prereqSeq) > s.maxActions {
		return nil, 0, ErrBudgetExceeded
	}

	return prereqSeq, prereqCost, nil
}

// tomStatDistMeans extracts the per-stat Mean values from a tom.Belief's EstStats
// into the stats.Stats shape that gates.AgentSnapshot expects (map[StatID]float64).
// This is the adaptation point where the planner projects tom.Belief → gate-side Stats.
func tomStatDistMeans(estStats map[core.StatID]tom.StatDist) stats.Stats {
	s := make(stats.Stats, len(estStats))
	for sid, sd := range estStats {
		s[sid] = sd.Mean
	}
	return s
}

// sortDimensionPriority defensively re-sorts the values slice by Priority
// descending and Dimension lexicographically ascending (D12). This guarantees
// deterministic ordering even if the caller's sort was unreliable.
func sortDimensionPriority(vals []DimensionPriority) {
	sort.SliceStable(vals, func(i, j int) bool {
		if vals[i].Priority != vals[j].Priority {
			return vals[i].Priority > vals[j].Priority // higher Priority first
		}
		return string(vals[i].Dim) < string(vals[j].Dim) // tie-break by Dim lexicographic
	})
}
