package flora

import (
	"math"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
)

const (
	probabilityMin                         = 0.0
	probabilityMax                         = 1.0
	nonNegativeFloor                       = 0.0
	lengthScaleBase                        = 1.0
	fullCircleRadians                      = 2 * math.Pi
	densityWeightNumerator                 = 1.0
	densityWeightBaseNeighbors             = 1
	seedlingLength                         = 0.0
	seedlingWidth                          = 0.0
	freshDeathStreak                       = 0
	statDexterity              core.StatID = "Dexterity"
	attrMoisture               core.Tag    = "moisture"
	attrTemperature            core.Tag    = "temperature"
	attrWidth                  core.Tag    = "width"
	attrLength                 core.Tag    = "length"
)

// ── floraContext: expr.Context adapter ────────────────────────────────────────

// floraContext adapts SiteInput + Plant to the expr.Context interface for §6 rule evaluation.
//
// §6 operands exposed:
//   - Stat "Dexterity"   → dexterity field (yield chance only; D7: read-only, never stored)
//   - Attr "moisture"    → SiteInput.Moisture ∈ [0,1]
//   - Attr "temperature" → SiteInput.Temperature in °C (CA3)
//   - Attr "width"       → Plant.Width (canopy spread; drives shade formulas)
//   - Attr "length"      → Plant.Length (height; may drive rate/suitability formulas)
//   - Attr other         → SiteInput.TerrainAttrs lookup (grainSize, slope, depth, …)
//
// Flora conditions use no predicates; Pred always returns false.
// The zero-value floraContext is valid (all attrs resolve to 0 / ok=false).
type floraContext struct {
	site      SiteInput
	plant     Plant
	dexterity float64 // actor Dexterity for yield rolls; 0 for growth/shade contexts
}

func (c floraContext) Stat(id core.StatID) float64 {
	if id == statDexterity {
		return c.dexterity
	}
	return 0
}

func (c floraContext) Attr(name core.Tag) (float64, bool) {
	switch name {
	case attrMoisture:
		return c.site.Moisture, true
	case attrTemperature:
		return c.site.Temperature, true
	case attrWidth:
		return c.plant.Width, true
	case attrLength:
		return c.plant.Length, true
	}
	if v, ok := c.site.TerrainAttrs[name]; ok {
		return v, true
	}
	return 0, false
}

func (c floraContext) Pred(_ string, _ core.Tag) bool {
	return false // flora conditions use no predicates
}

// Compile-time check that floraContext satisfies expr.Context.
var _ expr.Context = floraContext{}

// evalNum evaluates a *expr.Program that returns a numeric value.
// Returns 0 if prog is nil (nil program = absent formula → 0 per expr fixed-absence policy).
func evalNum(prog *expr.Program, ctx floraContext) float64 {
	if prog == nil {
		return 0
	}
	return prog.EvalNumber(ctx)
}

// densityWeight is the propagation density term (1k). K is the per-site carrying capacity
// (§6 over terrain attrs + climate, evaluated by the caller) — terrain-dependent density.
//
//	hasCapacity && K>0 : logistic weight max(0, 1−n/K); a patch's spawn rate falls to zero as
//	                     the same-species neighbor count n reaches K, regulating established
//	                     patch interiors toward density ≈K (a wetter/flatter site → larger K →
//	                     denser). Frontier plants with n<K still expand; K is not a global cap.
//	hasCapacity && K≤0 : density 0 — the species does not establish here (e.g. deep water when
//	                     the formula carries a (1−depth) factor). NOT the legacy runaway.
//	!hasCapacity       : legacy 1/(1+n) (no equilibrium; back-compat for species/fixtures that
//	                     omit carrying_capacity).
//
// n is clamped to ≥ 0. Pure and independent of RNG; Step's draw-count contract is documented
// at the call site (D12).
func densityWeight(n int, k float64, hasCapacity bool) float64 {
	if n < 0 {
		n = 0
	}
	if !hasCapacity {
		return densityWeightNumerator / float64(densityWeightBaseNeighbors+n)
	}
	if k <= 0 {
		return nonNegativeFloor
	}
	w := 1.0 - float64(n)/k
	if w < nonNegativeFloor {
		return nonNegativeFloor
	}
	return w
}

// ── SpeciesRule & YieldRule ───────────────────────────────────────────────────

// SpeciesRule is one species' compiled flora spec (the NewRules input).
// All *expr.Program fields are compiled by platform/config via expr.Parse; flora only evaluates.
type SpeciesRule struct {
	Suitability      *expr.Program // → scalar ∈ [0,1]; suitability driver for both growth axes
	LengthRate       *expr.Program // → height growth rate (world units per flora step)
	WidthRate        *expr.Program // → canopy growth rate (world units per flora step)
	ShadeRadius      *expr.Program // shade radius = §6(width) → ShadeOf
	ShadeOpacity     *expr.Program // shade opacity = §6(width) → ShadeOf, clamped [0,1]
	Stages           []float64     // strictly ascending Length thresholds; len+1 stage indices
	YieldStage       int           // min derived stage for harvest yields (default 0)
	PropagateStage   int           // min derived stage for propagation (default 0)
	PropRadius       *expr.Program // propagation radius in world units
	PropChance       *expr.Program // base propagation probability (scaled by suitability + density)
	CarryingCapacity *expr.Program // K = §6(terrain attrs, climate) evaluated PER SITE (1k, terrain-
	//   dependent): densityWeight(n)=max(0,1−n/K) → a patch saturates at n≈K (wetter/flatter → denser).
	//   nil → legacy densityWeight(n)=1/(1+n) (no equilibrium; back-compat; flora-off unaffected).
	//   Evaluates ≤0 at a site → density 0 there (species won't establish, e.g. water via (1−depth)).
	//   MUST NOT read neighbor_count (circular — it is the divisor of n); platform/config rejects that.
	DeathThreshold  float64     // θ: suitability below this counts toward DeathStreak (1b)
	DeathHysteresis int         // consecutive sub-θ steps before object-mortality (1b); 0 = no death
	Yields          []YieldRule // yield table → Yield
}

// YieldRule is one compiled yield-table row (flora 1e).
// Chance is the §6(Dexterity) success probability.
// QtyMin/QtyMax are the base [min,max] before length scaling (SPEC §1 refinement).
type YieldRule struct {
	Item           core.Tag
	Chance         *expr.Program // §6(Dexterity) → probability ∈ [0,1]
	QtyMin, QtyMax int
}

// ── Rules ─────────────────────────────────────────────────────────────────────

// Rules is the compiled, immutable per-species flora table.
// An empty bySpecies map (no species entries) produces flora-off behavior.
// Opaque; constructed only via NewRules.
type Rules struct {
	bySpecies map[SpeciesID]SpeciesRule // keyed by species; single key lookups only (D12)
}

// NewRules builds the immutable per-species flora table from COMPILED inputs.
// platform/config parses content/objects.yaml `flora:` blocks, compiles each §6 formula
// via expr.Parse, validates species/item ids + strictly-ascending stage thresholds, then
// calls this. flora never parses YAML (D10). A nil or empty map produces flora-off Rules.
func NewRules(species map[SpeciesID]SpeciesRule) *Rules {
	m := make(map[SpeciesID]SpeciesRule, len(species))
	for k, v := range species {
		v.Stages = append([]float64(nil), v.Stages...)
		v.Yields = append([]YieldRule(nil), v.Yields...)
		m[k] = v
	}
	return &Rules{bySpecies: m}
}

// ── Rules accessors ───────────────────────────────────────────────────────────

// Suitability evaluates the species' §6 suitability formula over the site → scalar ∈ [0,1].
// Returns 0 for an unknown species or nil Suitability program (flora-off; no growth/death).
// Caller domain: suitability drives BOTH growth axes (Length and Width each advance by
// their own rate × this scalar); below the species' death threshold θ it counts toward DeathStreak.
func (r *Rules) Suitability(sp SpeciesID, in SiteInput) float64 {
	if r == nil {
		return 0
	}
	sr, ok := r.bySpecies[sp]
	if !ok {
		return 0
	}
	ctx := floraContext{site: in}
	v := evalNum(sr.Suitability, ctx)
	if v < probabilityMin {
		return probabilityMin
	}
	if v > probabilityMax {
		return probabilityMax
	}
	return v
}

// LengthRate evaluates the species' §6 length-rate formula (height growth rate).
// Returns 0 for an unknown species or nil LengthRate program.
func (r *Rules) LengthRate(sp SpeciesID, in SiteInput) float64 {
	if r == nil {
		return 0
	}
	sr, ok := r.bySpecies[sp]
	if !ok {
		return 0
	}
	return evalNum(sr.LengthRate, floraContext{site: in})
}

// WidthRate evaluates the species' §6 width-rate formula (canopy growth rate).
// Returns 0 for an unknown species or nil WidthRate program.
func (r *Rules) WidthRate(sp SpeciesID, in SiteInput) float64 {
	if r == nil {
		return 0
	}
	sr, ok := r.bySpecies[sp]
	if !ok {
		return 0
	}
	return evalNum(sr.WidthRate, floraContext{site: in})
}

// PropRadius evaluates the species' §6 propagation-radius formula using the same context
// Step uses before seed dispersal. Returns 0 for an unknown species or nil PropRadius program.
func (r *Rules) PropRadius(sp SpeciesID, plant Plant, in SiteInput) float64 {
	if r == nil {
		return 0
	}
	sr, ok := r.bySpecies[sp]
	if !ok {
		return 0
	}
	return evalNum(sr.PropRadius, floraContext{site: in, plant: plant})
}

// Stage maps continuous Length (HEIGHT = maturity proxy) to the species' DERIVED discrete
// stage index via the species' stage thresholds over length (RESOLVED 1b option c + §1).
// Returns 0 (seedling) for an unknown species or a length below all thresholds.
// Stage increases monotonically with length; two plants with equal Length but different
// Width share the same Stage (Width does not influence stage — D9, §1 refinement).
func (r *Rules) Stage(sp SpeciesID, length float64) int {
	if r == nil {
		return 0
	}
	sr, ok := r.bySpecies[sp]
	if !ok {
		return 0
	}
	stage := 0
	for i, threshold := range sr.Stages {
		if length >= threshold {
			stage = i + 1
		}
	}
	return stage
}

// Yield rolls the species' yield table for a harvest of one plant at the given LENGTH and
// dexterity (the actor's capability stat value, READ ONLY — flora never trains it, D7).
//
// Documented functional form for qty scaling with length (SPEC leaves this to implementer;
// monotone + deterministic required):
//
//	lengthScale = 1.0 + math.Floor(length)   (1 at seedling, 2 at length≥1, 3 at length≥2, …)
//	effectiveMax = round(QtyMax × lengthScale)
//	qty = QtyMin + Uniform[0, max(0, effectiveMax − QtyMin)]
//
// This is monotone (larger length → larger effectiveMax → more yield expected) and integer-valued.
// A taller tree yields more wood; effectiveMax is always ≥ QtyMin.
//
// Returns nil for an unknown species, or when Stage(length) < YieldStage (immature — no yield).
// Pure given the same rnd draw sequence (D12).
func (r *Rules) Yield(sp SpeciesID, length, dexterity float64, rnd *rng.RNG) []YieldItem {
	if r == nil {
		return nil
	}
	sr, ok := r.bySpecies[sp]
	if !ok {
		return nil
	}
	if r.Stage(sp, length) < sr.YieldStage {
		return nil // immature: no yield (RESOLVED 1e + §1 refinement)
	}
	ctx := floraContext{dexterity: dexterity}
	// Qty scaling: monotone increasing with length (documented choice above).
	lengthScale := lengthScaleBase + math.Floor(length)
	var items []YieldItem
	for _, yr := range sr.Yields {
		chance := evalNum(yr.Chance, ctx)
		if chance < probabilityMin {
			chance = probabilityMin
		} else if chance > probabilityMax {
			chance = probabilityMax
		}
		if rnd.Float64() >= chance {
			continue // miss
		}
		effectiveMax := int(math.Round(float64(yr.QtyMax) * lengthScale))
		effectiveMin := yr.QtyMin
		if effectiveMax < effectiveMin {
			effectiveMax = effectiveMin
		}
		var qty int
		if effectiveMax <= effectiveMin {
			qty = effectiveMin
		} else {
			qty = effectiveMin + rnd.Intn(effectiveMax-effectiveMin+1)
		}
		items = append(items, YieldItem{Item: yr.Item, Qty: qty})
	}
	return items
}
