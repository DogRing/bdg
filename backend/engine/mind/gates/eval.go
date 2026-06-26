package gates

import "github.com/dogring/bdg/engine/kernel/core"

// ── Evaluation ─────────────────────────────────────────────────────────────

// Evaluate is the entry the planner calls per candidate action. It returns the
// action's gate verdict: Visible (the AND, across every gate whose tags match
// the action, of that gate's expr — a gate with a CostRule is always
// visibility-true), the per-gate Trace, and the aggregated CostMultiplier (the
// PRODUCT of every matching gate's emitted multiplier; default 1.0 when no
// CostRule fires). A gate with empty tags matches every action; an action
// matched by no gate is Visible with CostMultiplier 1.0.
func (reg *Registry) Evaluate(act Action, a AgentSnapshot) Result {
	visible := true
	costMult := 1.0
	var trace Trace

	for _, g := range reg.gates {
		if !gateMatchesAction(&g, act) {
			continue
		}

		// Evaluate the gate's expr.
		passed := evalExpr(&g.expr, act, a)

		// Determine contribution.
		mult := 1.0
		if g.costRule != nil {
			// Cost-rule gate: visibility is ALWAYS true (it never hides).
			// The multiplier fires only when the expr (gating predicate) is true.
			if passed {
				mult = g.costRule.Mult
				costMult *= mult
			}
			// Visibility verdict: always true for cost-rule gates.
			trace = append(trace, GateContribution{
				Gate:   g.id,
				Passed: true, // cost-rule gates never block visibility
				Mult:   mult,
			})
		} else {
			// Normal visibility gate.
			visible = visible && passed
			trace = append(trace, GateContribution{
				Gate:   g.id,
				Passed: passed,
				Mult:   1.0,
			})
		}
	}

	return Result{
		Visible:        visible,
		CostMultiplier: costMult,
		Trace:          trace,
	}
}

// gateMatchesAction reports whether gate g matches action act.
// A gate matches if the action carries ANY tag in the gate's tags list.
// An empty tags list matches every action.
func gateMatchesAction(g *gate, act Action) bool {
	if len(g.tags) == 0 {
		return true
	}
	actTags := make(map[core.Tag]struct{}, len(act.Tags))
	for _, t := range act.Tags {
		actTags[t] = struct{}{}
	}
	for _, gt := range g.tags {
		if _, ok := actTags[gt]; ok {
			return true
		}
	}
	return false
}

// evalExpr evaluates a GateExpr tree against the action and agent snapshot.
// Short-circuits but is side-effect-free, so the result is independent of
// short-circuit order.
func evalExpr(expr *GateExpr, act Action, a AgentSnapshot) bool {
	// Leaf: stat comparison (D8 — reads ToM[self]).
	if expr.Stat != "" {
		val := a.SelfStats.Get(expr.Stat)
		return cmpOp(val, expr.Op, expr.Value)
	}

	// Leaf: body-scalar comparison (NEW v3 — reads live Body).
	if expr.Body != "" {
		val := resolveBodyScalar(expr.Body, a)
		return cmpOp(val, expr.Op, expr.Value)
	}

	// Leaf: tag membership.
	if expr.Tag != "" {
		for _, t := range act.Tags {
			if t == expr.Tag {
				return true
			}
		}
		return false
	}

	// Composite: AND
	if len(expr.And) > 0 {
		for i := range expr.And {
			if !evalExpr(&expr.And[i], act, a) {
				return false // short-circuit
			}
		}
		return true
	}

	// Composite: OR
	if len(expr.Or) > 0 {
		for i := range expr.Or {
			if evalExpr(&expr.Or[i], act, a) {
				return true // short-circuit
			}
		}
		return false
	}

	// Composite: NOT
	if expr.Not != nil {
		return !evalExpr(expr.Not, act, a)
	}

	// Should not happen: empty expr node (no shape populated).
	return true
}

// resolveBodyScalar returns the live body scalar value from the agent snapshot
// for the given BodyScalar. Uses a sorted-key lookup (switch over the canonical
// set, not driven by the data value) for determinism (D12).
func resolveBodyScalar(bs BodyScalar, a AgentSnapshot) float64 {
	switch bs {
	case BodyStamina:
		return a.Stamina
	case BodyMood:
		return a.Mood
	case BodyAdrenaline:
		return a.Adrenaline
	case BodyUrgency:
		return a.Urgency
	default:
		return 0
	}
}

// cmpOp compares val against threshold using op.
func cmpOp(val float64, op Op, threshold float64) bool {
	switch op {
	case OpGE:
		return val >= threshold
	case OpGT:
		return val > threshold
	case OpLE:
		return val <= threshold
	case OpLT:
		return val < threshold
	case OpEQ:
		return val == threshold
	case OpNE:
		return val != threshold
	default:
		return false
	}
}
