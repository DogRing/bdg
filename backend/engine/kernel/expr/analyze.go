package expr

import (
	"fmt"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── Static type inference ────────────────────────────────────────────────────

func typecheck(n *node) error {
	switch n.kind {
	case nLit:
		n.inferred = KindNum
	case nStat, nAttr:
		n.inferred = KindNum
	case nPred:
		n.inferred = KindBool

	case nNot:
		if err := typecheck(n.left); err != nil {
			return err
		}
		if n.left.inferred != KindBool {
			return fmt.Errorf("expr: '!' requires a Bool operand, got Num")
		}
		n.inferred = KindBool

	case nMul, nDiv, nAdd, nSub:
		if err := typecheck(n.left); err != nil {
			return err
		}
		if err := typecheck(n.right); err != nil {
			return err
		}
		if n.left.inferred != KindNum {
			return fmt.Errorf("expr: arithmetic operator requires Num left operand, got Bool")
		}
		if n.right.inferred != KindNum {
			return fmt.Errorf("expr: arithmetic operator requires Num right operand, got Bool")
		}
		n.inferred = KindNum

	case nGT, nLT, nGTE, nLTE, nEQ, nNEQ:
		if err := typecheck(n.left); err != nil {
			return err
		}
		if err := typecheck(n.right); err != nil {
			return err
		}
		if n.left.inferred != KindNum {
			return fmt.Errorf("expr: comparison requires Num left operand, got Bool")
		}
		if n.right.inferred != KindNum {
			return fmt.Errorf("expr: comparison requires Num right operand, got Bool")
		}
		n.inferred = KindBool

	case nAnd, nOr:
		if err := typecheck(n.left); err != nil {
			return err
		}
		if err := typecheck(n.right); err != nil {
			return err
		}
		if n.left.inferred != KindBool {
			return fmt.Errorf("expr: '&'/'|' requires Bool left operand, got Num")
		}
		if n.right.inferred != KindBool {
			return fmt.Errorf("expr: '&'/'|' requires Bool right operand, got Num")
		}
		n.inferred = KindBool
	}
	return nil
}

// ── Reads collection (sorted, de-duplicated) ─────────────────────────────────

func collectReads(root *node) (stats []core.StatID, attrs []core.Tag, predNames []string) {
	statsSet := make(map[core.StatID]struct{})
	attrsSet := make(map[core.Tag]struct{})
	predsSet := make(map[string]struct{})
	walkCollect(root, statsSet, attrsSet, predsSet)

	for id := range statsSet {
		stats = append(stats, id)
	}
	sort.Slice(stats, func(i, j int) bool { return string(stats[i]) < string(stats[j]) })

	for name := range attrsSet {
		attrs = append(attrs, name)
	}
	sort.Slice(attrs, func(i, j int) bool { return string(attrs[i]) < string(attrs[j]) })

	for name := range predsSet {
		predNames = append(predNames, name)
	}
	sort.Strings(predNames)

	return stats, attrs, predNames
}

func walkCollect(n *node,
	stats map[core.StatID]struct{},
	attrs map[core.Tag]struct{},
	preds map[string]struct{},
) {
	if n == nil {
		return
	}
	switch n.kind {
	case nStat:
		stats[n.statID] = struct{}{}
	case nAttr:
		attrs[n.attrName] = struct{}{}
	case nPred:
		preds[n.predName] = struct{}{}
	}
	walkCollect(n.left, stats, attrs, preds)
	walkCollect(n.right, stats, attrs, preds)
}
