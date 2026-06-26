package expr

import "github.com/dogring/bdg/engine/kernel/core"

// Program is a compiled, immutable §6 formula.
// Produced ONLY by Parse; engine modules hold it opaquely and never reconstruct it.
// Concurrency-safe: EvalNumber/EvalBool are read-only tree walks; no shared mutable state.
type Program struct {
	root       *node
	resultKind Kind
	// cached sorted, de-duplicated read-sets (set once by Parse, never mutated)
	reads      []core.StatID
	readsAttrs []core.Tag
	readsPreds []string
}

// ResultKind returns the statically inferred result type of the whole Program.
func (p *Program) ResultKind() Kind { return p.resultKind }

// Reads returns the sorted, de-duplicated StatIDs this Program references.
// Returns a copy so callers cannot mutate the cached slice.
func (p *Program) Reads() []core.StatID {
	out := make([]core.StatID, len(p.reads))
	copy(out, p.reads)
	return out
}

// ReadsAttrs returns the sorted, de-duplicated attribute names (core.Tag) this Program
// references via Context.Attr.
func (p *Program) ReadsAttrs() []core.Tag {
	out := make([]core.Tag, len(p.readsAttrs))
	copy(out, p.readsAttrs)
	return out
}

// ReadsPreds returns the sorted, de-duplicated predicate names this Program calls via
// Context.Pred.
func (p *Program) ReadsPreds() []string {
	out := make([]string, len(p.readsPreds))
	copy(out, p.readsPreds)
	return out
}

// EvalNumber evaluates a numeric Program against ctx and returns the scalar result.
// Precondition: p.ResultKind() == KindNum (enforced at load time by Parse; panics on misuse).
// Pure, deterministic, no RNG, no IO. Arithmetic edges (div-0, NaN) resolve to 0.
func (p *Program) EvalNumber(ctx Context) float64 {
	if p.resultKind != KindNum {
		panic("expr: EvalNumber called on a KindBool Program (programming error; caught at load)")
	}
	return evalNode(p.root, ctx).Num
}

// EvalBool evaluates a boolean Program against ctx and returns the verdict.
// Precondition: p.ResultKind() == KindBool (enforced at load time by Parse; panics on misuse).
// Pure, deterministic, no RNG, no IO.
func (p *Program) EvalBool(ctx Context) bool {
	if p.resultKind != KindBool {
		panic("expr: EvalBool called on a KindNum Program (programming error; caught at load)")
	}
	return evalNode(p.root, ctx).Bool
}
