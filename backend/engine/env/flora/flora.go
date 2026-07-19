// Package flora is the flora driver: a pure, deterministic transform over the set of live
// plant objects (trees, bushes, grass). Given the current flora state, the terrain/climate
// inputs at each plant's position (supplied as values by world), and the data-defined flora
// Rules, it produces per-object growth, propagation (new plants), and death (removals) deltas
// plus the shade parameters perception consumes.
//
// Forbidden imports (one-way wiring): engine/space/navmap, engine/env/climate, engine/world,
// engine/mind/perception, engine/mind/gates.
//
// D12 determinism invariants:
//   - All randomness comes from the injected *rng.RNG (propagation, yield, spawn test).
//   - No time.Now(), no global rand, no wall-clock.
//   - Plant set is iterated in sorted ObjectID order for Step, propagation, and Plants().
//   - Step is a pure function: it never mutates prev, inputs, or Rules.
package flora

import (
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── Identity & state ─────────────────────────────────────────────────────────

// SpeciesID names a flora species from content/objects.yaml object_kinds that carry a
// `flora:` block. It is core.Tag underlying so the content catalog validates it;
// flora never parses YAML (D10).
type SpeciesID = core.Tag

// Plant is one live flora object's flora-owned dynamic state. Pos is the continuous
// world coordinate (D11 — never snapped to a cell). Morphology is TWO continuous axes:
//
//	Length — plant HEIGHT in world units ≥ 0. Maturity proxy: discrete Stage is DERIVED
//	         from it via the species' stage thresholds, never stored. Drives yield qty.
//	Width  — plant girth/canopy spread in world units ≥ 0. Drives shade Radius/Opacity.
//	         Integrates with its OWN per-species §6 width-rate, independent of Length.
//
// DeathStreak is the hysteresis counter for sustained-unsuitability death (1b option a).
// Owner is empty for wild flora; set only for PLANTED flora (economy seam, inert in P1).
type Plant struct {
	ID          core.ObjectID
	Species     SpeciesID
	Pos         core.Vec2
	Length      float64      // continuous height ≥ 0; stages derived from it (D9)
	Width       float64      // continuous canopy ≥ 0; shade derived from it (D9)
	DeathStreak int          // consecutive flora-steps with suitability < θ (hysteresis)
	Owner       core.AgentID // zero/empty ⇒ wild (unowned); inert in P1 (RESOLVED 1f)
}

// SiteInput is the per-plant exogenous environment world samples at Plant.Pos and injects
// each flora step. Flora is a pure transform over VALUES — it does NOT import navmap or
// climate (RESOLVED 1h). NeighborCount is the count of plants within the species'
// propagation radius, supplied by world's spatial query (D11 spatial hash).
type SiteInput struct {
	Terrain       core.Tag             // terrain type at Pos
	TerrainAttrs  map[core.Tag]float64 // §5 terrain attribute vector (grainSize, slope, …)
	Moisture      float64              // climate Moisture at Pos ∈ [0,1]
	Temperature   float64              // climate Temperature at Pos in °C
	NeighborCount int                  // plants within propagation radius (density weighting)
}

// Shade is the per-plant occlusion PARAMETER perception reads to attenuate line-of-sight.
// Flora exposes the parameter ONLY (RESOLVED 1h); perception computes LoS occlusion.
// Radius/Opacity are DERIVED from Width + species §6 (RESOLVED 1d option b + §1 refinement).
// Opacity ∈ [0,1]; perception composes overlapping shades MULTIPLICATIVELY (RESOLVED 1d option c).
type Shade struct {
	ID      core.ObjectID
	Pos     core.Vec2
	Radius  float64 // shade radius in world units = §6(width, species); zero when flora-off
	Opacity float64 // light-blocking fraction ∈ [0,1] = §6(width, species); zero when flora-off
}

// StepDeltas is the world-applied result of one flora step.
// All three slices are in sorted ObjectID order (D12).
type StepDeltas struct {
	Spawned []Plant         // new plants (propagation); world adds to objects[] + spatial
	Died    []core.ObjectID // removed plants (§7 object-mortality); world removes from objects[]
	Grown   []GrowthDelta   // survivors whose Length/Width/DeathStreak changed this step
}

// GrowthDelta carries a survivor's new morphology state (absolute values, not increments,
// so apply is idempotent and order-free across survivors).
type GrowthDelta struct {
	ID          core.ObjectID
	Length      float64
	Width       float64
	DeathStreak int
}

// YieldItem is one produced item from a harvest.
type YieldItem struct {
	Item core.Tag // item_kind id (objects.yaml)
	Qty  int      // rolled quantity, scaled by plant length
}

// State is the whole flora field for one run: the live Plant set held in sorted ObjectID
// order (D12). Snapshot-serializable (data-contracts §6). Owned by engine/world (one per
// run); Step returns a fresh next and never mutates prev.
//
// rules is carried from the last Step call for lazy ShadeOf evaluation; nil = flora-off
// (initial state from New, or a deliberate flora-off run). ShadeOf returns zero-radius
// Shade when rules is nil or the species is unknown.
type State struct {
	plants []Plant                 // sorted ascending by ID (D12)
	idx    map[core.ObjectID]Plant // O(1) lookup by ID (single-key access, no range-iteration for logic)
	rules  *Rules                  // carried for ShadeOf; nil means flora-off
}

// ── Construction ─────────────────────────────────────────────────────────────

// New builds the initial flora State from an already-placed plant set.
// Placement (world-gen / scenario fixtures) is NOT this module's job (RESOLVED 1j).
// Pure; no RNG draw at construction. The resulting State has nil rules (flora-off until
// the first Step with a valid *Rules).
func New(plants []Plant) *State {
	sorted := make([]Plant, len(plants))
	copy(sorted, plants)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})
	idx := make(map[core.ObjectID]Plant, len(sorted))
	for _, p := range sorted {
		idx[p.ID] = p
	}
	return &State{plants: sorted, idx: idx, rules: nil}
}

// ── Snapshot / serialization (data-contracts §6) ─────────────────────────────

// Plants returns the live Plant set in D12-sorted (ascending ObjectID) order, for the
// periodic-full serialization channel (data-contracts §6). Shade is NOT serialized
// (derived; perception recomputes from Pos+Width).
func (s *State) Plants() []Plant {
	out := make([]Plant, len(s.plants))
	copy(out, s.plants)
	return out
}

// PlantByID returns the plant with the given ID via the O(1) index (single-key access, no
// range-iteration, D12). ok=false if the ID is unknown. Lets callers look up one plant without
// the full Plants() copy — e.g. a spatial-hash neighbour query that then reads plant Width.
func (s *State) PlantByID(id core.ObjectID) (Plant, bool) {
	p, ok := s.idx[id]
	return p, ok
}

// GrazeLength crops a plant's Length by `amount` (world-Length units of biomass eaten), clamped at 0,
// and returns the amount ACTUALLY removed (≤ the plant's prior Length). It is the fauna→flora depletion
// seam (PD2, docs/plans/fauna.md §5): a grazing herbivore consumes biomass so the plant shrinks — weaker
// food scent (mag = Length+Width) and less recovery — then regrows via the ordinary growth Step, which
// integrates LengthRate up from the reduced Length. Width (cover/shade) is deliberately untouched:
// grazing crops edible height, not canopy. This is an IN-PLACE, world-owned mutation BETWEEN Steps (like
// decay lots), NOT a Step output — Step's purity (never mutating prev) is unaffected. Deterministic: the
// plant is found by ascending-ID binary search over the sorted `plants` slice (no map-iteration, D12);
// the idx value is resynced from the slice. No-op (returns 0) for an unknown id or amount ≤ 0.
func (s *State) GrazeLength(id core.ObjectID, amount float64) float64 {
	if s == nil || amount <= 0 {
		return 0
	}
	i := sort.Search(len(s.plants), func(i int) bool { return s.plants[i].ID >= id })
	if i >= len(s.plants) || s.plants[i].ID != id {
		return 0
	}
	removed := amount
	if removed > s.plants[i].Length {
		removed = s.plants[i].Length
	}
	if removed <= 0 {
		return 0
	}
	s.plants[i].Length -= removed
	s.idx[id] = s.plants[i]
	return removed
}

// ── Shade parameters (lazy, per-demand; RESOLVED 1g) ─────────────────────────

// ShadeOf returns the Shade parameter for one plant (lazy — computed on demand, not in
// the bulk Step). Returns ok=false if id is unknown.
//
// With nil rules (flora-off), or when the species is absent from Rules (unknown species),
// returns zero-radius Shade so perception LoS is unaffected (RESOLVED 1g neutrality).
// Flora performs NO LoS test, NO shade summation, NO tile field (D11 + RESOLVED 1d).
func (s *State) ShadeOf(id core.ObjectID) (Shade, bool) {
	p, ok := s.idx[id]
	if !ok {
		return Shade{}, false
	}
	// flora-off: zero-radius shade (perception LoS unaffected)
	if s.rules == nil {
		return Shade{ID: id, Pos: p.Pos, Radius: 0, Opacity: 0}, true
	}
	sr, hasSR := s.rules.bySpecies[p.Species]
	if !hasSR {
		// Unknown species in this Rules: zero shade (content validated at load by platform/config)
		return Shade{ID: id, Pos: p.Pos, Radius: 0, Opacity: 0}, true
	}
	// Shade uses only Plant.Width; SiteInput is zero-value (shade formulas reference only width).
	ctx := newFloraContext(sr, SiteInput{}, p)
	radius := evalNum(sr.ShadeRadius, ctx)
	if radius < 0 {
		radius = 0
	}
	opacity := evalNum(sr.ShadeOpacity, ctx)
	if opacity < 0 {
		opacity = 0
	} else if opacity > 1 {
		opacity = 1
	}
	return Shade{ID: id, Pos: p.Pos, Radius: radius, Opacity: opacity}, true
}
