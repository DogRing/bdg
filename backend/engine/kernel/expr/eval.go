package expr

import "math"

// evalNode performs a pure, deterministic tree walk over the AST.
// No RNG, no IO, no error return, no panic.
// Fixed arithmetic-edge policy (D12, #6):
//   - division by zero   → 0
//   - any NaN result     → 0
//   - missing Attr ok=false → 0 (numeric)
//   - no domain clamp    (caller's responsibility)
//
// Logical & and | use Go's natural short-circuit evaluation; because Context
// methods are pure for a given snapshot, the result is order-independent.
func evalNode(n *node, ctx Context) Value {
	switch n.kind {

	// ── Leaves ───────────────────────────────────────────────────────────────

	case nLit:
		return Value{Kind: KindNum, Num: n.num}

	case nStat:
		return Value{Kind: KindNum, Num: ctx.Stat(n.statID)}

	case nAttr:
		v, ok := ctx.Attr(n.attrName)
		if !ok {
			v = 0 // fixed policy: missing attribute → 0
		}
		return Value{Kind: KindNum, Num: v}

	case nPred:
		// arity-0 predicates (isOwner) are called with predArg == "".
		return Value{Kind: KindBool, Bool: ctx.Pred(n.predName, n.predArg)}

	// ── Unary ─────────────────────────────────────────────────────────────────

	case nNot:
		return Value{Kind: KindBool, Bool: !evalNode(n.left, ctx).Bool}

	// ── Arithmetic ────────────────────────────────────────────────────────────

	case nMul:
		l := evalNode(n.left, ctx).Num
		r := evalNode(n.right, ctx).Num
		return Value{Kind: KindNum, Num: safeNum(l * r)}

	case nDiv:
		l := evalNode(n.left, ctx).Num
		r := evalNode(n.right, ctx).Num
		if r == 0 {
			return Value{Kind: KindNum, Num: 0} // div-by-zero → 0
		}
		return Value{Kind: KindNum, Num: safeNum(l / r)}

	case nAdd:
		l := evalNode(n.left, ctx).Num
		r := evalNode(n.right, ctx).Num
		return Value{Kind: KindNum, Num: safeNum(l + r)}

	case nSub:
		l := evalNode(n.left, ctx).Num
		r := evalNode(n.right, ctx).Num
		return Value{Kind: KindNum, Num: safeNum(l - r)}

	// ── Comparison (Num × Num → Bool) ─────────────────────────────────────────

	case nGT:
		return Value{Kind: KindBool, Bool: evalNode(n.left, ctx).Num > evalNode(n.right, ctx).Num}
	case nLT:
		return Value{Kind: KindBool, Bool: evalNode(n.left, ctx).Num < evalNode(n.right, ctx).Num}
	case nGTE:
		return Value{Kind: KindBool, Bool: evalNode(n.left, ctx).Num >= evalNode(n.right, ctx).Num}
	case nLTE:
		return Value{Kind: KindBool, Bool: evalNode(n.left, ctx).Num <= evalNode(n.right, ctx).Num}
	case nEQ:
		return Value{Kind: KindBool, Bool: evalNode(n.left, ctx).Num == evalNode(n.right, ctx).Num}
	case nNEQ:
		return Value{Kind: KindBool, Bool: evalNode(n.left, ctx).Num != evalNode(n.right, ctx).Num}

	// ── Logical (Bool × Bool → Bool) ─────────────────────────────────────────

	case nAnd:
		return Value{Kind: KindBool, Bool: evalNode(n.left, ctx).Bool && evalNode(n.right, ctx).Bool}
	case nOr:
		return Value{Kind: KindBool, Bool: evalNode(n.left, ctx).Bool || evalNode(n.right, ctx).Bool}

	default:
		// Unreachable: typecheck ensures all node kinds are valid before eval.
		return Value{Kind: KindNum, Num: 0}
	}
}

// safeNum maps NaN and ±Inf to 0; finite values pass through unchanged.
func safeNum(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}
