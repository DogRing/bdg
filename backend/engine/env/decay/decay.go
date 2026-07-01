// Package decay is the decay driver: a pure, deterministic transform over the set of perishable
// item lots (wherever they live — floor, inventory, or storage — owner-agnostic, Dm4). Given the
// prior decay state, the per-lot environment ({Temperature, Moisture, StorageRateMult}, supplied as
// VALUES by world), and the data-defined decay Rules, it advances each lot's continuous DecayAge,
// fires discrete state transitions (fresh→stale→rotten→gone) when DecayAge crosses the next
// state's threshold, and emits the transform products a transition produces (D9 locality).
//
// Forbidden imports (one-way wiring): engine/env/climate, engine/env/flora, engine/world,
// engine/space/navmap, engine/agent, engine/mind/gates. Only core, expr, rng are allowed.
//
// D12 determinism invariants:
//   - No time.Now(), no global rand, no wall-clock.
//   - Lot set is iterated in sorted ObjectID order for Step, transitions, and Lots().
//   - Step is a pure function: it never mutates prev, inputs, or Rules.
//   - The inputs map is read by sorted lot id, NOT by map-range (D12).
package decay

import (
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── Identity & state ──────────────────────────────────────────────────────────

// KindID names an item_kind from content/objects.yaml that carries a `decay:` block.
// core.Tag underlying so the content catalog validates it; decay never parses YAML (D10).
type KindID = core.Tag

// Lot is one perishable item lot's decay-owned dynamic state — the decay UNIT (Dm5(a)).
// A lot is a group of Qty identical-kind items that age together, identified by a stable
// instance id (NOT an agent + kind), so a lot decays identically whether it sits on the
// floor, in a Body.Inventory, or in storage (Dm4(a) owner-agnostic). Lots NEVER auto-merge
// in P_m2: a freshly produced lot is a NEW instance with DecayAge=0.
//
//	Kind     — the decaying item_kind (selects the Rules entry).
//	Qty      — the lot quantity (Dm5(a)): all Qty items share this lot's single DecayAge/State.
//	DecayAge — the continuous effective-decay-time accumulator (Dm1(a)): monotone ≥ 0, advanced
//	           by DecayAge += elapsedTicks · effectiveRate per Step. The discrete State is
//	           DERIVED from DecayAge via the kind's thresholds (never stored, mirrors flora
//	           Length→Stage; D9).
type Lot struct {
	ID       core.ObjectID // stable instance id (world owns the id space)
	Kind     KindID
	Qty      int     // lot quantity (Dm5(a)); all share one DecayAge
	DecayAge float64 // continuous effective-decay-time accumulator (Dm1(a)), in effective-time units
}

// EnvInput is the per-lot exogenous environment world samples at the lot's position and injects
// each decay step. Decay is a pure transform over VALUES — it does NOT import climate.
//
// Temperature/Moisture are the SHAPE of the climate output (climate.CellState:
// Temperature in °C, Moisture normalized [0,1]), passed as plain numbers — the accel §6
// Context operand names match the climate/flora Context vocabulary exactly
// ("temperature", "moisture").
//
// StorageRateMult is the storage-structure rate multiplier. world computes it from the
// structure the lot is stored in and injects it as a value — decay does NOT model storage
// structures. It composes MULTIPLICATIVELY: effectiveRate = baseRate · accel(T,M) · StorageRateMult (Dm2(a)).
type EnvInput struct {
	Temperature     float64 // climate Temperature at the lot's Pos in °C (accel operand "temperature")
	Moisture        float64 // climate Moisture at the lot's Pos ∈ [0,1] (accel operand "moisture")
	StorageRateMult float64 // storage-structure rate multiplier (≥ 0; 1 = neutral, < 1 = preserved, 0 = halted)
}

// State is the whole decay field for one run: the live Lot set held in sorted ObjectID order
// (D12). Snapshot-serializable (data-contracts §8). Owned by engine/world (one per run);
// Step returns a fresh next and never mutates prev (so plan-snapshot and apply-write don't alias,
// mirroring flora.State).
type State struct {
	lots []Lot                 // sorted ascending by ID (D12)
	idx  map[core.ObjectID]int // O(1) lookup: ID → index in lots slice
}

// AgeDelta carries a survivor's new ABSOLUTE DecayAge (not an increment, so apply is idempotent
// and order-free across survivors, mirroring flora.GrowthDelta).
type AgeDelta struct {
	ID       core.ObjectID
	DecayAge float64 // new absolute effective-decay-time accumulator
}

// TransitionDelta carries a lot that entered a new discrete State this step.
// State is the derived index into the Kind's ordered states (NOT stored on Lot; world records
// it for render/value). The per-state supply OVERRIDE is read from Rules.SupplyAt by the consumer.
type TransitionDelta struct {
	ID       core.ObjectID
	DecayAge float64 // the new absolute accumulated measure (this lot also advanced; not also in Aged)
	State    int     // derived discrete state index it ENTERED (0 = fresh …)
}

// TransformOut is a transform product a transition produces (Q3 — D9 locality, NOT vanish).
// world places Qty of Item into the same location the source lot occupied. SourceID names the
// lot that produced it. The produced Qty scales with the SOURCE LOT'S Qty (Dm5(a)); the source
// lot's items are consumed by the transition.
type TransformOut struct {
	SourceID core.ObjectID // the decaying lot that transitioned and produced this
	State    int           // the derived state index whose transform emitted this (the state ENTERED)
	Item     KindID        // the produced item_kind (objects.yaml decay.states[].transform[].item)
	Qty      int           // produced quantity = transform[].qty · sourceLot.Qty (Dm5(a))
}

// StepDeltas is the world-applied result of one decay step. All slices are in sorted ObjectID
// order (D12). A lot appears in at most one of {Aged, Transitioned} (Transitioned implies its
// DecayAge also advanced — the new DecayAge is carried on the TransitionDelta, so Aged is the
// no-state-change subset only); a transitioning lot may additionally contribute to
// Transformed/Gone.
type StepDeltas struct {
	Aged         []AgeDelta        // lots whose DecayAge advanced with NO state change this step
	Transitioned []TransitionDelta // lots that crossed into a new discrete State this step
	Transformed  []TransformOut    // transform products emitted on a transition (Q3; world adds them)
	Gone         []core.ObjectID   // lots that reached the terminal gone state (removed by world)
}

// ── Construction ──────────────────────────────────────────────────────────────

// New builds the initial decay State from an already-placed perishable-lot set. Placement
// (which lots exist, where) is world's, not this module's. Pure; no RNG draw at construction.
func New(lots []Lot) *State {
	sorted := make([]Lot, len(lots))
	copy(sorted, lots)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})
	idx := make(map[core.ObjectID]int, len(sorted))
	for i, l := range sorted {
		idx[l.ID] = i
	}
	return &State{lots: sorted, idx: idx}
}

// ── Snapshot / serialization (data-contracts §8) ─────────────────────────────

// Lots returns the live Lot set in D12-sorted (ascending ObjectID) order, for the
// periodic-full serialization channel (data-contracts §8).
func (s *State) Lots() []Lot {
	out := make([]Lot, len(s.lots))
	copy(out, s.lots)
	return out
}
