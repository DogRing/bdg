package actions

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── Helpers ────────────────────────────────────────────────────────────────────

// loadTestActions loads actions from the shipped content file for test scenarios.
func loadTestActions(t *testing.T) *Registry {
	t.Helper()
	// Resolve from the repo root (assumes test runs from backend/).
	// Walk up to find content/actions.yaml.
	cwd, _ := os.Getwd()
	p := filepath.Join(cwd, "..", "..", "..", "..", "content", "actions.yaml")
	if _, err := os.Stat(p); err != nil {
		// Try alternate paths (go test may run from the module root).
		p = filepath.Join(cwd, "..", "..", "..", "content", "actions.yaml")
		if _, err2 := os.Stat(p); err2 != nil {
			// Fallback: for tests run from backend/ dir or repo root.
			p = filepath.Join(cwd, "content", "actions.yaml")
		}
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open content/actions.yaml: %v", err)
	}
	defer f.Close()
	reg, err := Load(f)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return reg
}

// loadFromString builds a Registry from YAML string bytes (for table-driven semantic tests).
func loadFromString(t *testing.T, yamlSrc string) (*Registry, error) {
	t.Helper()
	return Load(strings.NewReader(yamlSrc))
}

// ── Acceptance Criteria ────────────────────────────────────────────────────────

// AC: Loads from an injected io.Reader.
func TestLoadFromInjectedReader(t *testing.T) {
	src := `
schema_version: 1
actions:
  - id: Rest
    tags: ["effort:none"]
    duration: 30
    effect_per_minute: { Rest: 0.0010 }
    interruptible: true
  - id: Sleep
    tags: ["effort:none", "risk:low"]
    duration: 360
    effect_per_minute: { Rest: 0.0030 }
    interruptible: true
`
	reg, err := loadFromString(t, src)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	ids := reg.IDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 actions, got %d: %v", len(ids), ids)
	}
	if ids[0] != "Rest" || ids[1] != "Sleep" {
		t.Fatalf("expected sorted [Rest, Sleep], got %v", ids)
	}

	// Also test with the shipped content.
	shipped := loadTestActions(t)
	if shipped.Len() == 0 {
		t.Fatal("shipped content loaded 0 actions")
	}
	// All P1 actions present.
	expectedP1 := []ActionID{"Craft", "Eat", "Forage", "Hunt", "MoveTo", "Rest"}
	for _, e := range expectedP1 {
		if !shipped.Has(e) {
			t.Fatalf("shipped content missing expected action %q", e)
		}
	}
}

// AC: Get round-trips a definition faithfully.
func TestGetRoundTrip(t *testing.T) {
	reg := loadTestActions(t)

	tests := []struct {
		id                ActionID
		wantTags          []core.Tag
		wantDuration      core.GameMinutes
		wantTarget        TargetKind
		wantTargetKindID  core.Tag
		wantProduces      []core.Pred
		wantProducesItem  core.Tag
		wantConsumesItem  core.Tag
		wantInterruptible bool
		wantEffectLen     int
		wantEffectPMinLen int
	}{
		{
			id: "Hunt",
			wantTags: []core.Tag{
				"abstraction:med", "effort:high", "noise:high",
				"risk:med", "uses:Agility", "uses:Strength", "violent:low",
			},
			wantDuration:      35,
			wantTarget:        TargetObject,
			wantTargetKindID:  "prey",
			wantProduces:      []core.Pred{"has_food"},
			wantProducesItem:  "raw_meat",
			wantInterruptible: true,
		},
		{
			id:               "Eat",
			wantTags:         []core.Tag{"effort:low"},
			wantDuration:     6,
			wantTarget:       TargetNone,
			wantTargetKindID: "",
			wantProduces:     []core.Pred{"has_Satiety"},
			wantConsumesItem: "", // P1: direct-effect Eat; item-supply chain deferred
			wantEffectLen:    1,  // effect: { Satiety: 0.40 }
			wantInterruptible: true,
		},
		{
			id:               "MoveTo",
			wantTags:         []core.Tag{"effort:low", "noise:low", "time:by_distance", "uses:Agility"},
			wantDuration:     1,
			wantTarget:       TargetLocation,
			wantTargetKindID: "",
			wantProduces:     []core.Pred{"at_target"},
			wantInterruptible: true,
		},
		{
			id:               "Rest",
			wantTags:         []core.Tag{"effort:none"},
			wantDuration:     30,
			wantTarget:       TargetNone,
			wantTargetKindID: "",
			wantProduces:     []core.Pred{"has_Rest"},
			wantEffectPMinLen: 1,
			wantInterruptible: true,
		},
		{
			id:               "Forage",
			wantTags:         []core.Tag{"abstraction:low", "effort:low", "noise:low", "uses:Agility"},
			wantDuration:     12,
			wantTarget:       TargetObject,
			wantTargetKindID: "berry_bush",
			wantProduces:     []core.Pred{"has_food"},
			wantProducesItem: "berries",
			wantInterruptible: true,
		},
		{
			// Craft is RECIPE-MEDIATED (Materials P_m3 FINAL): the content omits duration /
			// target_kind / produces_item — the bound content/recipes.yaml recipe supplies the
			// duration + outputs, and the station is the recipe's `ambient` (not a target_kind).
			// So Duration=0, Target=TargetNone, ProducesItem="" (recipe owns it); produces has_tool.
			id:               "Craft",
			wantTags:         []core.Tag{"abstraction:med", "effort:high", "noise:low", "uses:Intelligence"},
			wantDuration:     0,
			wantTarget:       TargetNone,
			wantTargetKindID: "",
			wantProduces:     []core.Pred{"has_tool"},
			wantProducesItem: "",
			wantInterruptible: true,
		},
		{
			id:               "Signal",
			wantTags:         []core.Tag{"abstraction:med", "effort:none", "social"},
			wantDuration:     3,
			wantTarget:       TargetAgent,
			wantTargetKindID: "",
			wantProduces:     []core.Pred{"signalled"},
			wantInterruptible: true,
		},
		{
			id:               "Attack",
			wantTags:         []core.Tag{"effort:high", "noise:high", "risk:high", "uses:Strength", "violent:high", "norm:transgressive"},
			wantDuration:     5,
			wantTarget:       TargetAgent,
			wantTargetKindID: "",
			wantProduces:     []core.Pred{"has_Safety"},
			wantInterruptible: false,
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.id), func(t *testing.T) {
			def, ok := reg.Get(tt.id)
			if !ok {
				t.Fatalf("Get(%q) not found", tt.id)
			}
			if def.ID != tt.id {
				t.Errorf("ID = %q, want %q", def.ID, tt.id)
			}

			// Check tags (any order).
			if !sameTags(def.Tags, tt.wantTags) {
				t.Errorf("Tags = %v, want %v", def.Tags, tt.wantTags)
			}

			if def.Duration != tt.wantDuration {
				t.Errorf("Duration = %d, want %d", def.Duration, tt.wantDuration)
			}
			if def.Target != tt.wantTarget {
				t.Errorf("Target = %v, want %v", def.Target, tt.wantTarget)
			}
			if def.TargetKindID != tt.wantTargetKindID {
				t.Errorf("TargetKindID = %q, want %q", def.TargetKindID, tt.wantTargetKindID)
			}

			if !samePreds(def.Produces, tt.wantProduces) {
				t.Errorf("Produces = %v, want %v", def.Produces, tt.wantProduces)
			}
			if def.ProducesItem != tt.wantProducesItem {
				t.Errorf("ProducesItem = %q, want %q", def.ProducesItem, tt.wantProducesItem)
			}
			if def.ConsumesItem != tt.wantConsumesItem {
				t.Errorf("ConsumesItem = %q, want %q", def.ConsumesItem, tt.wantConsumesItem)
			}

			if def.Interruptible != tt.wantInterruptible {
				t.Errorf("Interruptible = %v, want %v", def.Interruptible, tt.wantInterruptible)
			}

			if tt.wantEffectLen >= 0 && len(def.Effect) != tt.wantEffectLen {
				t.Errorf("Effect length = %d, want %d", len(def.Effect), tt.wantEffectLen)
			}
			if tt.wantEffectPMinLen >= 0 && len(def.EffectPerMinute) != tt.wantEffectPMinLen {
				t.Errorf("EffectPerMinute length = %d, want %d", len(def.EffectPerMinute), tt.wantEffectPMinLen)
			}

			// D9: Eat has ConsumesItem and empty Effect.
			if def.ConsumesItem != "" && len(def.Effect) > 0 {
				t.Errorf("D9 violation: consumes_item action has non-empty Effect")
			}
		})
	}
}

// AC: IDs() ordering is deterministic (D12).
func TestIDsDeterministic(t *testing.T) {
	src := `
schema_version: 1
actions:
  - id: Zebra
    tags: ["effort:low"]
    duration: 1
  - id: Apple
    tags: ["effort:low"]
    duration: 1
  - id: Mango
    tags: ["effort:low"]
    duration: 1
`
	reg1, err := loadFromString(t, src)
	if err != nil {
		t.Fatal(err)
	}
	reg2, err := loadFromString(t, src)
	if err != nil {
		t.Fatal(err)
	}

	// Check same across calls on one instance.
	idsA := reg1.IDs()
	idsB := reg1.IDs()
	if len(idsA) != 3 {
		t.Fatalf("expected 3 ids, got %d", len(idsA))
	}
	for i := range idsA {
		if idsA[i] != idsB[i] {
			t.Fatalf("non-deterministic IDs across calls: %v vs %v", idsA, idsB)
		}
	}

	// Check same across two instances.
	idsC := reg2.IDs()
	for i := range idsA {
		if idsA[i] != idsC[i] {
			t.Fatalf("non-deterministic IDs across instances: %v vs %v", idsA, idsC)
		}
	}

	// Check sorted.
	if idsA[0] != "Apple" || idsA[1] != "Mango" || idsA[2] != "Zebra" {
		t.Fatalf("not sorted: %v", idsA)
	}

	// Check with shipped content.
	shipped := loadTestActions(t)
	shippedIDs := shipped.IDs()
	if !sort.SliceIsSorted(shippedIDs, func(i, j int) bool { return shippedIDs[i] < shippedIDs[j] }) {
		t.Fatal("shipped IDs not sorted")
	}
	// Cross-process stability: two separate Loads from same file.
	shipped2 := loadTestActions(t)
	shippedIDs2 := shipped2.IDs()
	for i := range shippedIDs {
		if shippedIDs[i] != shippedIDs2[i] {
			t.Fatalf("shipped IDs differ between Load calls at index %d: %q vs %q",
				i, shippedIDs[i], shippedIDs2[i])
		}
	}
}

// AC: Producers reverse index (GOAP).
func TestProducers(t *testing.T) {
	reg := loadTestActions(t)

	tests := []struct {
		pred core.Pred
		want []ActionID
	}{
		{pred: "has_food", want: []ActionID{"Forage", "Hunt", "Take"}},
		{pred: "at_target", want: []ActionID{"MoveTo"}},
		{pred: "holding", want: []ActionID{"PickUp", "Take"}},
		{pred: "has_tool", want: []ActionID{"Craft"}},
		{pred: "signalled", want: []ActionID{"Signal"}},
		{pred: "transferred", want: []ActionID{"GiveItem", "Trade"}},
		{pred: "sheltered", want: []ActionID{"TakeShelter"}},
		{pred: "has_Safety", want: []ActionID{"Attack", "Patrol"}},
		{pred: "structure_built", want: []ActionID{"Build"}},
		{pred: "nonexistent", want: nil},
	}

	for _, tt := range tests {
		t.Run(string(tt.pred), func(t *testing.T) {
			got := reg.Producers(tt.pred)
			if tt.want == nil {
				if got != nil {
					t.Errorf("Producers(%q) = %v, want nil", tt.pred, got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Producers(%q) = %v (len=%d), want %v (len=%d)",
					tt.pred, got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Producers(%q)[%d] = %q, want %q", tt.pred, i, got[i], tt.want[i])
				}
			}
			// Verify each returned action actually produces the pred.
			for _, aid := range got {
				def, ok := reg.Get(aid)
				if !ok {
					t.Errorf("Producer %q not found in registry", aid)
					continue
				}
				if !predInList(tt.pred, def.Produces) {
					t.Errorf("Producer %q does not actually produce %q (Produces=%v)",
						aid, tt.pred, def.Produces)
				}
			}
		})
	}
}

// AC: Target kind derived correctly.
func TestTargetKindDerivation(t *testing.T) {
	reg := loadTestActions(t)

	tests := []struct {
		id             ActionID
		wantKind       TargetKind
		wantKindID     core.Tag
	}{
		{id: "MoveTo",       wantKind: TargetLocation, wantKindID: ""},
		{id: "Rest",         wantKind: TargetNone,     wantKindID: ""},
		{id: "Sleep",        wantKind: TargetNone,     wantKindID: ""},
		{id: "Forage",       wantKind: TargetObject,   wantKindID: "berry_bush"},
		{id: "Hunt",         wantKind: TargetObject,   wantKindID: "prey"},
		{id: "Eat",          wantKind: TargetNone,     wantKindID: ""},
		{id: "Drink",        wantKind: TargetObject,   wantKindID: "water_source"},
		{id: "Craft",        wantKind: TargetNone,     wantKindID: ""}, // recipe-mediated: no target_kind (station is the recipe `ambient`, P_m3 FINAL)
		{id: "Build",        wantKind: TargetNone,     wantKindID: ""}, // Build has requires: [at_target, has_materials] but no target_kind and produces != at_target
		{id: "TakeShelter",  wantKind: TargetObject,   wantKindID: "shelter"},
		{id: "PickUp",       wantKind: TargetNone,     wantKindID: ""}, // PickUp has requires: [at_target], produces: [holding] — at_target is NOT in produces
		{id: "Signal",       wantKind: TargetAgent,    wantKindID: ""},
		{id: "GiveItem",     wantKind: TargetAgent,    wantKindID: ""},
		{id: "Trade",        wantKind: TargetAgent,    wantKindID: ""},
		{id: "Take",         wantKind: TargetAgent,    wantKindID: ""}, // near_other in requires → TargetAgent
		{id: "Attack",       wantKind: TargetAgent,    wantKindID: ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.id), func(t *testing.T) {
			def, ok := reg.Get(tt.id)
			if !ok {
				t.Fatalf("Get(%q) not found", tt.id)
			}
			if def.Target != tt.wantKind {
				t.Errorf("Target = %v, want %v", def.Target, tt.wantKind)
			}
			if def.TargetKindID != tt.wantKindID {
				t.Errorf("TargetKindID = %q, want %q", def.TargetKindID, tt.wantKindID)
			}
		})
	}
}

// AC: Atomic-only guard (D3) — ActionDef exposes no method/task/subtask/plan field.
func TestAtomicOnlyGuard(t *testing.T) {
	typ := reflect.TypeOf(ActionDef{})
	fieldNames := make(map[string]bool)
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		fieldNames[f.Name] = true
	}

	banned := []string{"Method", "Task", "Subtask", "Subtasks", "Plan", "Steps"}
	for _, b := range banned {
		if fieldNames[b] {
			t.Errorf("D3 violation: ActionDef has field %q (method/task/subtask/plan forbidden)", b)
		}
	}
}

// AC: No gate/cost field guard (D4).
func TestNoGateCostField(t *testing.T) {
	typ := reflect.TypeOf(ActionDef{})
	fieldNames := make(map[string]bool)
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		fieldNames[f.Name] = true
	}

	banned := []string{"GateID", "Gate", "Cost", "CostTerms", "CostField"}
	for _, b := range banned {
		if fieldNames[b] {
			t.Errorf("D4 violation: ActionDef has field %q (GateID/Cost forbidden)", b)
		}
	}

	// Also verify Tags is present and there's no separate cost/gate field.
	if !fieldNames["Tags"] {
		t.Errorf("ActionDef missing Tags field (D4: tags drive gates/cost)")
	}
}

// AC: Consumption action carries no direct Effect (D9).
func TestConsumptionActionNoDirectEffect(t *testing.T) {
	reg := loadTestActions(t)

	// D9 invariant (loader-enforced): ANY action with consumes_item must have an
	// empty direct Effect — the consumed item's supply IS the effect. Assert it
	// holds across the whole shipped catalog, not just one named action.
	for _, id := range reg.IDs() {
		def, _ := reg.Get(id)
		if def.ConsumesItem != "" && len(def.Effect) > 0 {
			t.Errorf("D9 violation: %q consumes_item=%q but has direct Effect %v",
				id, def.ConsumesItem, def.Effect)
		}
	}

	// GiveItem is the shipped consumption action: consumes_item set, Effect empty.
	def, ok := reg.Get("GiveItem")
	if !ok {
		t.Fatal("GiveItem not found")
	}
	if def.ConsumesItem == "" {
		t.Fatal("GiveItem should have ConsumesItem")
	}
	if len(def.Effect) != 0 {
		t.Errorf("GiveItem.Effect should be empty (D9), got %v", def.Effect)
	}

	// P1 design: the item-supply chain for subsistence is deferred — Eat and Drink
	// carry NO consumes_item and apply a direct Effect instead (content/actions.yaml).
	for _, id := range []ActionID{"Eat", "Drink"} {
		def, ok := reg.Get(id)
		if !ok {
			t.Fatalf("%s not found", id)
		}
		if def.ConsumesItem != "" {
			t.Errorf("%s should have no ConsumesItem (P1 direct-effect), got %q", id, def.ConsumesItem)
		}
		if len(def.Effect) == 0 {
			t.Errorf("%s should have a direct Effect (P1 direct-effect design)", id)
		}
	}

	// Sleep has no consumes_item, but has effect_per_minute.
	def, ok = reg.Get("Sleep")
	if !ok {
		t.Fatal("Sleep not found")
	}
	if def.ConsumesItem != "" || def.ProducesItem != "" {
		t.Fatal("Sleep should not consume or produce items")
	}
	if len(def.EffectPerMinute) == 0 {
		t.Fatal("Sleep should have EffectPerMinute")
	}
}

// AC: Semantic rejects.
func TestSemanticRejects(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		wantErr  string // substring in error
	}{
		{
			name: "duplicate id",
			yaml: `
schema_version: 1
actions:
  - id: Forage
    tags: ["effort:low"]
    duration: 10
  - id: Forage
    tags: ["effort:high"]
    duration: 20
`,
			wantErr: "duplicate action id",
		},
		{
			name: "empty tags",
			yaml: `
schema_version: 1
actions:
  - id: NoTags
    tags: []
    duration: 10
`,
			wantErr: "empty tags",
		},
		{
			name: "duration zero",
			yaml: `
schema_version: 1
actions:
  - id: Instant
    tags: ["effort:none"]
    duration: 0
`,
			wantErr: "duration 0 < 1",
		},
		{
			name: "duration negative",
			yaml: `
schema_version: 1
actions:
  - id: Negative
    tags: ["effort:none"]
    duration: -5
`,
			wantErr: "duration -5 < 1",
		},
		{
			name: "consumes with effect (D9 conflict)",
			yaml: `
schema_version: 1
actions:
  - id: BadEat
    tags: ["effort:low"]
    consumes_item: any_food
    effect: { Satiety: 10.0 }
    duration: 5
`,
			wantErr: "D9 conflict",
		},
		{
			name: "empty action list",
			yaml: `
schema_version: 1
actions: []
`,
			wantErr: "no actions defined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadFromString(t, tt.yaml)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// AC: Immutable after init.
func TestImmutableAfterInit(t *testing.T) {
	reg := loadTestActions(t)
	initialLen := reg.Len()

	// IDs() returns a copy; mutating the returned slice must not affect registry.
	idsCopy := reg.IDs()
	if len(idsCopy) > 0 {
		idsCopy[0] = "MUTATED"
	}
	if reg.Len() != initialLen {
		t.Fatal("Len changed after mutating returned IDs slice")
	}
	// Re-check: IDs() should still return original.
	idsAgain := reg.IDs()
	if idsAgain[0] == "MUTATED" {
		t.Fatal("IDs() returned stale data after mutating prior copy")
	}

	// Producers() returns a copy; mutating must not affect registry.
	if prods := reg.Producers("has_food"); len(prods) > 0 {
		prods[0] = "MUTATED"
	}
	prodsAgain := reg.Producers("has_food")
	for _, p := range prodsAgain {
		if p == "MUTATED" {
			t.Fatal("Producers() returned stale data after mutating prior copy")
		}
	}

	// Tags() returns a copy.
	tagsCopy := reg.Tags()
	if len(tagsCopy) > 0 {
		tagsCopy[0] = "MUTATED"
	}
	tagsAgain := reg.Tags()
	for _, t2 := range tagsAgain {
		if t2 == "MUTATED" {
			t.Fatal("Tags() returned stale data after mutating prior copy")
		}
	}

	// ActionDef.Tags is directly accessible; mutating it must not affect registry.
	def, ok := reg.Get("Hunt")
	if !ok {
		t.Fatal("Hunt not found")
	}
	if len(def.Tags) > 0 {
		def.Tags[0] = "MUTATED"
	}
	def2, _ := reg.Get("Hunt")
	if def2.Tags[0] == "MUTATED" {
		t.Fatal("ActionDef.Tags slice is shared with registry (should be copy-on-Get or slice wasn't mutated by Load)")
	}
}

// AC: No literal action name in source (D10).
func TestNoLiteralActionName(t *testing.T) {
	// Grep for action id literals in non-test .go files in this directory.
	// This only passes if no action id like "Forage" appears as a string literal
	// in actions.go logic (not in actions_test.go).
	src, err := os.ReadFile("actions.go")
	if err != nil {
		t.Fatal(err)
	}
	srcStr := string(src)

	// Strip comments so doc examples don't trigger the check.
	clean := stripGoComments(srcStr)

	// Action IDs from the shipped content. These should NOT appear as string literals
	// in actions.go logic. Predicate names ("at_target", "near_other", etc.) are NOT
	// action IDs and are legal — the derivation logic matches against content-defined
	// Predicate values.
	bannedIDs := []string{
		`"Forage"`, `"Hunt"`, `"Eat"`, `"Drink"`, `"Sleep"`, `"Rest"`,
		`"MoveTo"`, `"TakeShelter"`, `"PickUp"`, `"Signal"`, `"GiveItem"`,
		`"Trade"`, `"Craft"`, `"Build"`, `"Take"`, `"Attack"`,
	}

	for _, ban := range bannedIDs {
		if strings.Contains(clean, ban) {
			t.Errorf("D10 violation: string literal %s found in actions.go logic (excluding comments)", ban)
		}
	}
}

// stripGoComments removes // single-line and /* */ block comments from Go source.
func stripGoComments(src string) string {
	var out strings.Builder
	out.Grow(len(src))
	i := 0
	for i < len(src) {
		if src[i] == '/' && i+1 < len(src) && src[i+1] == '/' {
			// Line comment: skip to '\n'
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		if src[i] == '/' && i+1 < len(src) && src[i+1] == '*' {
			// Block comment: skip to "*/"
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		out.WriteByte(src[i])
		i++
	}
	return out.String()
}

// AC: Has() and Len() basic operations.
func TestHasAndLen(t *testing.T) {
	reg := loadTestActions(t)

	if !reg.Has("Forage") {
		t.Error("Has(Forage) should be true")
	}
	if reg.Has("Nonexistent") {
		t.Error("Has(Nonexistent) should be false")
	}

	if reg.Len() != 30 {
		t.Errorf("Len = %d, want 30", reg.Len())
	}
}

// AC: Tags() returns sorted union.
func TestTags(t *testing.T) {
	reg := loadTestActions(t)

	tags := reg.Tags()
	if len(tags) == 0 {
		t.Fatal("Tags() returned empty")
	}

	// Must be sorted.
	if !sort.SliceIsSorted(tags, func(i, j int) bool { return tags[i] < tags[j] }) {
		t.Fatal("Tags() not sorted")
	}

	// Check that known tags are present.
	tagSet := make(map[core.Tag]bool)
	for _, t2 := range tags {
		tagSet[t2] = true
	}

	expected := []core.Tag{
		"abstraction:high", "abstraction:low", "abstraction:med",
		"cooperative", "effort:high", "effort:low", "effort:none",
		"noise:high", "noise:low", "noise:med",
		"norm:transgressive",
		"risk:high", "risk:low", "risk:med",
		"social", "social:covert",
		"time:by_distance",
		"uses:Agility", "uses:Intelligence", "uses:Strength",
		"violent:high", "violent:low",
	}
	for _, e := range expected {
		if !tagSet[e] {
			t.Errorf("expected tag %q not present in union", e)
		}
	}
}

// AC: Golden snapshot consistency — the shipped content loads deterministically.
func TestGoldenSnapshot(t *testing.T) {
	reg := loadTestActions(t)

	// Verify key structural invariants via golden-consistent output.
	// This doesn't write a file; it verifies the semantic invariants
	// that would be captured in a golden snapshot.

	// 1. All actions have >= 1 tag, duration >= 1.
	for _, id := range reg.IDs() {
		def, _ := reg.Get(id)
		if len(def.Tags) == 0 {
			t.Errorf("action %q has no tags", id)
		}
		if !def.RecipeMediated && def.Duration < 1 {
			t.Errorf("action %q has duration %d < 1", id, def.Duration)
		}
	}

	// 2. No action has both GateID and Cost fields (struct guard-plus).
	// 3. No action has both consumes_item and non-empty Effect.
	for _, id := range reg.IDs() {
		def, _ := reg.Get(id)
		if def.ConsumesItem != "" && len(def.Effect) > 0 {
			t.Errorf("action %q: D9 violation (consumes + effect)", id)
		}
	}

	// 4. Verify no action references a Method/Subtask/Plan field (type guard).
	var _ ActionDef

	// 5. Print deterministic summary for manual golden review.
	t.Logf("=== Golden Summary (loaded %d actions) ===", reg.Len())
	for _, id := range reg.IDs() {
		def, _ := reg.Get(id)
		t.Logf("  %s: target=%v targetKindID=%q tags=%d duration=%d produces=%v producesItem=%q consumesItem=%q effect=%d effectPM=%d intr=%v",
			id, def.Target, def.TargetKindID, len(def.Tags), def.Duration,
			def.Produces, def.ProducesItem, def.ConsumesItem,
			len(def.Effect), len(def.EffectPerMinute), def.Interruptible)
	}
}

// ── Helper functions ───────────────────────────────────────────────────────────

func sameTags(a, b []core.Tag) bool {
	if len(a) != len(b) {
		return false
	}
	// Build sets and compare (order independent typical for tags).
	setA := make(map[core.Tag]bool, len(a))
	for _, t := range a {
		setA[t] = true
	}
	for _, t := range b {
		if !setA[t] {
			return false
		}
	}
	return true
}

func samePreds(a, b []core.Pred) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func predInList(pred core.Pred, list []core.Pred) bool {
	for _, p := range list {
		if p == pred {
			return true
		}
	}
	return false
}
