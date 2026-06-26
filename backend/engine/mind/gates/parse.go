package gates

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/stats"
	"gopkg.in/yaml.v3"
)

// ── Raw YAML types ────────────────────────────────────────────────────────

// rawDocument is the on-disk shape of content/gates.yaml (schema_version 3).
type rawDocument struct {
	SchemaVersion int       `yaml:"schema_version"`
	Gates         []rawGate `yaml:"gates"`
}

type rawGate struct {
	ID       string       `yaml:"id"`
	Tags     []string     `yaml:"tags"`
	Expr     yaml.Node    `yaml:"expr"`
	CostRule *rawCostRule `yaml:"cost_rule"` // NEW v3: optional
}

// rawCostRule is the on-disk shape of a gate's cost_rule block.
type rawCostRule struct {
	Mult float64 `yaml:"mult"`
}

// ── Expr parsing ───────────────────────────────────────────────────────────

// opFromYAML converts a YAML op string to our Op constant.
func opFromYAML(s string) (Op, error) {
	switch s {
	case ">=":
		return OpGE, nil
	case ">":
		return OpGT, nil
	case "<=":
		return OpLE, nil
	case "<":
		return OpLT, nil
	case "==":
		return OpEQ, nil
	case "!=":
		return OpNE, nil
	default:
		return 0, fmt.Errorf("unknown operator %q", s)
	}
}

// parseExpr parses a yaml.Node (a mapping node) into GateExpr, validating
// that exactly one shape is populated and that stat/body references exist.
func parseExpr(node *yaml.Node, reg *stats.Registry) (GateExpr, error) {
	if node.Kind != yaml.MappingNode {
		return GateExpr{}, fmt.Errorf("expr node must be a mapping, got kind %d", node.Kind)
	}

	// Build a map of key → value node from the mapping node's key-value pairs.
	kv := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		if keyNode.Kind != yaml.ScalarNode {
			return GateExpr{}, fmt.Errorf("expr mapping key must be scalar, got kind %d", keyNode.Kind)
		}
		kv[keyNode.Value] = valNode
	}

	// Each shape is mutually exclusive — track which one we detect.
	shapes := 0
	var expr GateExpr

	// ── stat leaf ─────────────────────────────────────────────────
	if statStr, hasStat := kv["stat"]; hasStat {
		opStr, hasOp := kv["op"]
		valNode, hasVal := kv["value"]
		if !hasOp || !hasVal {
			return GateExpr{}, fmt.Errorf("stat leaf requires 'stat', 'op', and 'value'")
		}
		shapes++
		expr.Stat = core.StatID(statStr.Value)

		// Validate stat exists in registry.
		if !reg.Has(expr.Stat) {
			return GateExpr{}, fmt.Errorf("stat leaf references unknown stat %q", expr.Stat)
		}

		var err error
		expr.Op, err = opFromYAML(opStr.Value)
		if err != nil {
			return GateExpr{}, fmt.Errorf("stat leaf for %q: %w", expr.Stat, err)
		}

		if err := valNode.Decode(&expr.Value); err != nil {
			return GateExpr{}, fmt.Errorf("stat leaf for %q: invalid value: %w", expr.Stat, err)
		}
	}

	// ── body-scalar leaf (NEW v3) ─────────────────────────────────
	if bodyStr, hasBody := kv["body"]; hasBody {
		opStr, hasOp := kv["op"]
		valNode, hasVal := kv["value"]
		if !hasOp || !hasVal {
			return GateExpr{}, fmt.Errorf("body-scalar leaf requires 'body', 'op', and 'value'")
		}
		shapes++
		bs := BodyScalar(bodyStr.Value)
		if !knownBodyScalars[bs] {
			return GateExpr{}, fmt.Errorf("body-scalar leaf references unknown body %q", bs)
		}
		expr.Body = bs

		var err error
		expr.Op, err = opFromYAML(opStr.Value)
		if err != nil {
			return GateExpr{}, fmt.Errorf("body-scalar leaf for %q: %w", bs, err)
		}

		if err := valNode.Decode(&expr.Value); err != nil {
			return GateExpr{}, fmt.Errorf("body-scalar leaf for %q: invalid value: %w", bs, err)
		}
	}

	// ── tag leaf ─────────────────────────────────────────────────
	if tagNode, hasTag := kv["tag"]; hasTag {
		shapes++
		expr.Tag = core.Tag(tagNode.Value)
	}

	// ── and ─────────────────────────────────────────────────────
	if andNode, hasAnd := kv["and"]; hasAnd {
		shapes++
		children, err := parseExprList(andNode, reg)
		if err != nil {
			return GateExpr{}, fmt.Errorf("and: %w", err)
		}
		expr.And = children
	}

	// ── or ──────────────────────────────────────────────────────
	if orNode, hasOr := kv["or"]; hasOr {
		shapes++
		children, err := parseExprList(orNode, reg)
		if err != nil {
			return GateExpr{}, fmt.Errorf("or: %w", err)
		}
		expr.Or = children
	}

	// ── not ─────────────────────────────────────────────────────
	if notNode, hasNot := kv["not"]; hasNot {
		shapes++
		child, err := parseExpr(notNode, reg)
		if err != nil {
			return GateExpr{}, fmt.Errorf("not: %w", err)
		}
		expr.Not = &child
	}

	if shapes == 0 {
		return GateExpr{}, fmt.Errorf("expr node has no recognized shape: keys %v", keysOf(kv))
	}
	if shapes > 1 {
		return GateExpr{}, fmt.Errorf("expr node has %d shapes, exactly one required", shapes)
	}

	return expr, nil
}

// parseExprList parses a YAML sequence node into a []GateExpr.
func parseExprList(node *yaml.Node, reg *stats.Registry) ([]GateExpr, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("expected sequence node, got kind %d", node.Kind)
	}
	result := make([]GateExpr, len(node.Content))
	for i, child := range node.Content {
		var err error
		result[i], err = parseExpr(child, reg)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
	}
	return result, nil
}

// keysOf returns sorted string keys of a map for error messages.
func keysOf(m map[string]*yaml.Node) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// collectRefs recursively collects all StatIDs from stat leaves and BodyScalars
// from body-scalar leaves in an expr tree.
func collectRefs(expr *GateExpr, statSet map[core.StatID]struct{}, bodySet map[BodyScalar]struct{}) {
	if expr.Stat != "" {
		statSet[expr.Stat] = struct{}{}
	}
	if expr.Body != "" {
		bodySet[expr.Body] = struct{}{}
	}
	for i := range expr.And {
		collectRefs(&expr.And[i], statSet, bodySet)
	}
	for i := range expr.Or {
		collectRefs(&expr.Or[i], statSet, bodySet)
	}
	if expr.Not != nil {
		collectRefs(expr.Not, statSet, bodySet)
	}
}
