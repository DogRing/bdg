package expr

import "github.com/dogring/bdg/engine/kernel/core"

// nodeKind enumerates every AST node variant.
type nodeKind int

const (
	nLit  nodeKind = iota // numeric literal
	nStat                 // Stat(statID)              → KindNum
	nAttr                 // Attr(attrName)            → KindNum
	nPred                 // Pred(predName, predArg)   → KindBool
	nNot                  // !left                     → KindBool
	nMul                  // left * right              → KindNum
	nDiv                  // left / right              → KindNum
	nAdd                  // left + right              → KindNum
	nSub                  // left - right              → KindNum
	nGT                   // left > right              → KindBool
	nLT                   // left < right              → KindBool
	nGTE                  // left >= right             → KindBool
	nLTE                  // left <= right             → KindBool
	nEQ                   // left == right             → KindBool
	nNEQ                  // left != right             → KindBool
	nAnd                  // left & right              → KindBool
	nOr                   // left | right              → KindBool
)

// node is an internal AST node. All fields are set based on kind.
// This type is never exported; Program is the opaque public handle.
type node struct {
	kind     nodeKind
	inferred Kind // set by typecheck; KindNum or KindBool

	// leaf payloads (one is set per kind)
	num      float64      // nLit
	statID   core.StatID  // nStat
	attrName core.Tag     // nAttr
	predName string       // nPred
	predArg  core.Tag     // nPred; "" for arity-0 predicates

	// children
	left  *node // nNot, nMul, nDiv, nAdd, nSub, nGT, nLT, nGTE, nLTE, nEQ, nNEQ, nAnd, nOr
	right *node // binary nodes only
}
