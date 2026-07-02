// Package gates owns the immutable Registry of Gate definitions (built from
// content/gates.yaml) and the recursive predicate-tree evaluator. Each Gate is
// {id, tags, expr}: matched to actions BY TAG (D4) — never by per-action function.
// A Gate's boolean expr decides whether an action is visible to the agent. An
// action is visible iff EVERY matching gate's expr is true (hard AND across
// matching gates). Decisions read ToM[self] (D8), never Real Stats.
//
// P3 (schema_version 3) adds: body-scalar leaves (Stamina, Mood, Adrenaline,
// Urgency — live Body state, not beliefs), an optional CostRule per gate (the
// adrenaline cost discount), and the CostMultiplier product channel on Result.
package gates

import (
	"fmt"
	"io"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/stats"
	"gopkg.in/yaml.v3"
)

// ── Types ─────────────────────────────────────────────────────────────────

// GateID names a gate definition (canonical id from content/gates.yaml).
type GateID string

// Op is a stat or body-scalar comparison operator used by a GateExpr leaf.
type Op uint8

const (
	OpGE Op = iota // ">="
	OpGT           // ">"
	OpLE           // "<="
	OpLT           // "<"
	OpEQ           // "=="
	OpNE           // "!="
)

// BodyScalar names a dynamic Body field a body-scalar leaf compares (glossary §Dynamics).
// CANONICAL strings, cross-checked by platform/config; resolved against AgentSnapshot
// by a sorted-key lookup table (D12).
type BodyScalar string

const (
	BodyStamina    BodyScalar = "Stamina"    // [0, StaminaMax]
	BodyMood       BodyScalar = "Mood"       // signed
	BodyAdrenaline BodyScalar = "Adrenaline" // [0, AdrMax]
	BodyUrgency    BodyScalar = "Urgency"    // [0,1]
)

// knownBodyScalars is the set of valid BodyScalar values checked at load time.
var knownBodyScalars = map[BodyScalar]bool{
	BodyStamina:    true,
	BodyMood:       true,
	BodyAdrenaline: true,
	BodyUrgency:    true,
}

// GateExpr is one node of a recursive boolean predicate tree. EXACTLY ONE shape
// is populated per node:
//   - stat-comparison leaf: Stat != "" — cmp(SelfStats[Stat], Op, Value) (D8)
//   - body-scalar leaf:     Body != "" — cmp(AgentSnapshot body field, Op, Value) (NEW v3)
//   - tag-membership leaf:  Tag != ""  — true iff the candidate Action carries exactly Tag
//   - composite:            And / Or non-nil, or Not non-nil
type GateExpr struct {
	// leaf: ToM[self] stat comparison (D8)
	Stat  core.StatID
	Op    Op
	Value float64

	// leaf: live Body-scalar comparison (NEW v3)
	Body BodyScalar // empty unless this is a body-scalar leaf

	// leaf: tag membership
	Tag core.Tag

	// composite
	And []GateExpr
	Or  []GateExpr
	Not *GateExpr
}

// CostRule is the per-gate cost-multiplier rule (NEW v3). A gate with a non-nil
// CostRule emits Mult (instead of the default 1.0) for a matched action whose
// expr (the rule's gating predicate) is true. Gates WITHOUT a CostRule never
// touch CostMultiplier (it stays 1.0). A cost-rule gate's visibility verdict is
// ALWAYS true (it never hides an action).
type CostRule struct {
	Mult float64 // the multiplier emitted when this gate's expr is true (e.g. 0.50)
}

// AgentSnapshot is the READ-ONLY view a gate evaluates against, supplied by the
// caller (the planner / agent) for the agent currently deliberating. Gates MUST NOT
// mutate it. Stat leaves read SelfStats = ToM[self] (D8); body-scalar leaves read
// the live Body fields (NEW v3).
type AgentSnapshot struct {
	SelfStats stats.Stats                // ToM[self] — stat values decisions read (D8)
	Known     map[core.ObjectID]struct{} // membership only; used by planner's known-check

	// Live Body scalars (NEW v3) — read by body-scalar leaves and the adrenaline CostRule.
	Stamina    float64 // glossary §Dynamics; [0, StaminaMax]
	Mood       float64 // signed
	Adrenaline float64 // [0, AdrMax]
	Urgency    float64 // [0,1]
}

// Action is the minimal action view a gate reads: its Tags and the target it would act on.
type Action struct {
	Tags   []core.Tag
	Target core.ObjectID
}

// Result bundles one candidate's gate verdict for the planner.
type Result struct {
	Visible        bool    // AND of every matching gate's expr
	CostMultiplier float64 // PRODUCT of matching gates' cost rules; default 1.0 (NEW v3)
	Trace          Trace   // per matching-gate verdicts, ordered by GateID (D12)
}

// Trace is the per-gate breakdown for the why-trace (ordered by GateID for determinism).
type Trace []GateContribution

// GateContribution is one matching gate's verdict.
type GateContribution struct {
	Gate   GateID
	Passed bool    // this gate's expr evaluated true (the visibility predicate)
	Mult   float64 // the multiplier this gate contributed (1.0 if it has no CostRule) (NEW v3)
}

// ── Internal types ─────────────────────────────────────────────────────────

// gate is the internal representation of one gate definition.
type gate struct {
	id       GateID
	tags     []core.Tag // empty means "matches every action"
	expr     GateExpr
	costRule *CostRule // nil for normal visibility gates (NEW v3)
}

// ── Registry ───────────────────────────────────────────────────────────────

// Registry is the immutable, read-only set of gate definitions. After Load it
// never changes (no setters, no exported mutable fields). Safe to share across
// goroutines in the read/plan phase.
type Registry struct {
	gates         []gate   // sorted by id
	ids           []GateID // sorted lexicographically
	readsSet      map[core.StatID]struct{}
	readsList     []core.StatID           // stat reads in lexicographic order
	readsBodySet  map[BodyScalar]struct{} // NEW v3
	readsBodyList []BodyScalar            // NEW v3: sorted body-scalar reads
}

// Load parses the gates document from r (the bytes of content/gates.yaml — the
// path is injected by platform/config, NEVER a file path here, keeping the
// engine IO-free) together with the already-loaded stats.Registry. It builds
// each gate's GateExpr tree + optional CostRule and performs SEMANTIC validation:
// every StatID referenced by a stat leaf exists in reg, every Body in a
// body-scalar leaf is a known BodyScalar, exactly one shape per node, and
// CostRule.Mult > 0.
func Load(r io.Reader, reg *stats.Registry) (*Registry, error) {
	var doc rawDocument
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("gates.Load: yaml decode: %w", err)
	}

	if len(doc.Gates) == 0 {
		return nil, fmt.Errorf("gates.Load: gates list is empty; at least one gate required")
	}

	gates := make([]gate, 0, len(doc.Gates))
	readsSet := make(map[core.StatID]struct{})
	readsBodySet := make(map[BodyScalar]struct{})

	for i, rg := range doc.Gates {
		if rg.ID == "" {
			return nil, fmt.Errorf("gates.Load: entry %d: missing id", i)
		}

		// Parse tags (may be nil/empty).
		var tags []core.Tag
		for _, t := range rg.Tags {
			tags = append(tags, core.Tag(t))
		}

		// Parse the expr tree.
		expr, err := parseExpr(&rg.Expr, reg)
		if err != nil {
			return nil, fmt.Errorf("gates.Load: entry %d (%q): expr: %w", i, rg.ID, err)
		}

		// Parse optional cost_rule (NEW v3).
		var costRule *CostRule
		if rg.CostRule != nil {
			if rg.CostRule.Mult <= 0 {
				return nil, fmt.Errorf("gates.Load: entry %d (%q): cost_rule.mult must be > 0, got %v", i, rg.ID, rg.CostRule.Mult)
			}
			costRule = &CostRule{Mult: rg.CostRule.Mult}
		}

		// Collect stat and body references from this gate's expr tree.
		collectRefs(&expr, readsSet, readsBodySet)

		gates = append(gates, gate{
			id:       GateID(rg.ID),
			tags:     tags,
			expr:     expr,
			costRule: costRule,
		})
	}

	// Sort gates by id lexicographically for deterministic ordering (D12).
	sort.Slice(gates, func(i, j int) bool {
		return string(gates[i].id) < string(gates[j].id)
	})

	// Build sorted ids list.
	ids := make([]GateID, len(gates))
	for i, g := range gates {
		ids[i] = g.id
	}

	// Build reads list sorted lexicographically by StatID for determinism (D12).
	readsList := make([]core.StatID, 0, len(readsSet))
	for sid := range readsSet {
		readsList = append(readsList, sid)
	}
	sort.Slice(readsList, func(i, j int) bool {
		return string(readsList[i]) < string(readsList[j])
	})

	// Build body reads list sorted by BodyScalar (NEW v3, D12).
	readsBodyList := make([]BodyScalar, 0, len(readsBodySet))
	for bs := range readsBodySet {
		readsBodyList = append(readsBodyList, bs)
	}
	sort.Slice(readsBodyList, func(i, j int) bool {
		return string(readsBodyList[i]) < string(readsBodyList[j])
	})

	return &Registry{
		gates:         gates,
		ids:           ids,
		readsSet:      readsSet,
		readsList:     readsList,
		readsBodySet:  readsBodySet,
		readsBodyList: readsBodyList,
	}, nil
}

// ── Introspection ──────────────────────────────────────────────────────────

// IDs returns ALL gate ids in canonical fixed order (sorted lexicographically).
// This is the ONE ordering used to iterate gates (D12); Trace entries follow it.
func (reg *Registry) IDs() []GateID {
	c := make([]GateID, len(reg.ids))
	copy(c, reg.ids)
	return c
}

// Reads returns the union of StatIDs referenced by any gate's stat leaves, in
// lexicographic StatID order, so callers can pre-fill an AgentSnapshot's
// SelfStats vector.
func (reg *Registry) Reads() []core.StatID {
	c := make([]core.StatID, len(reg.readsList))
	copy(c, reg.readsList)
	return c
}

// ReadsBody returns the union of BodyScalars referenced by any gate's body-scalar
// leaves or CostRules, sorted, so callers know which live Body fields the
// snapshot must carry (NEW v3).
func (reg *Registry) ReadsBody() []BodyScalar {
	c := make([]BodyScalar, len(reg.readsBodyList))
	copy(c, reg.readsBodyList)
	return c
}
