package decay

import (
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// Step advances the whole decay field ONE decay step (= N ticks; world owns the multi-rate
// cadence and only calls Step when tick % N == 0). It is a PURE function of
// (prev, inputs, elapsedTicks, rules, rng): it does NOT mutate prev, and returns the deltas
// world applies in the apply phase. For each lot, in sorted ObjectID order (D12):
//
//	effectiveRate = rules.BaseRate(Kind) · rules.Accel(Kind, inputs[ID]) · inputs[ID].StorageRateMult  (Dm2(a))
//	DecayAge'     = DecayAge + float64(elapsedTicks) · effectiveRate                                     (Dm1(a))
//	State'        = rules.StateAt(Kind, DecayAge')   (derived; flora Length→Stage parity)
//
// A lot whose derived State increased this step emits a TransitionDelta; the state it ENTERED
// emits its transform products (TransformOut, Q3) and, if that state is terminal gone (the last
// state, no transforms by content contract), the lot is added to Gone (the only mass removal, D9).
// A lot whose State is unchanged but DecayAge advanced emits an AgeDelta.
//
// Several thresholds may be crossed in one step if elapsedTicks·effectiveRate is large: the
// final derived State is used; every transform on each state passed THROUGH is emitted in
// ascending state order so no product is skipped (D9 locality).
//
// inputs[lot.ID] is the EnvInput for that lot; a missing entry is a world-contract bug (panic,
// like flora's missing SiteInput) — every live perishable lot must have its environment sampled.
// rng is the per-step seeded fork world supplies (reserved — discrete-threshold decay is
// deterministic and draws nothing, but kept in the signature for shape parity; NOT drawn from
// in P_m2).
func Step(
	prev *State,
	inputs map[core.ObjectID]EnvInput,
	elapsedTicks int64,
	rules *Rules,
	_ *rng.RNG, // reserved, unused in P_m2 (deterministic thresholds; future stochastic variant)
) (next *State, deltas StepDeltas) {
	// prev.lots is already sorted ascending by ID (D12 invariant of State), so iterating
	// prev.lots directly gives the D12-required sorted order. We never range over inputs map
	// for logic (D12 — no map-iteration for logic).
	nextLots := make([]Lot, 0, len(prev.lots))

	for _, lot := range prev.lots {
		// World-contract guard: every live lot must have its environment sampled.
		in, ok := inputs[lot.ID]
		if !ok {
			panic("decay.Step: missing EnvInput for lot " + string(lot.ID))
		}

		// ── decay-off: empty Rules or unknown kind → lot unchanged, no age advance ──
		if rules == nil || len(rules.byKind) == 0 {
			nextLots = append(nextLots, lot)
			continue
		}
		kr, hasKind := rules.byKind[lot.Kind]
		if !hasKind {
			// Unknown kind in this Rules set (platform/config validates at load).
			// Treat as decay-off for this individual lot.
			nextLots = append(nextLots, lot)
			continue
		}

		// ── effectiveRate = baseRate · accel · StorageRateMult (Dm2(a)) ──
		effectiveRate := rules.BaseRate(lot.Kind) *
			rules.Accel(lot.Kind, in) *
			in.StorageRateMult

		// ── DecayAge' = DecayAge + elapsedTicks · effectiveRate (Dm1(a)) ──
		newAge := lot.DecayAge + float64(elapsedTicks)*effectiveRate

		// ── Derive old and new state from DecayAge (D9 — state never stored) ──
		oldState := stateFromKind(kr, lot.DecayAge)
		newState := stateFromKind(kr, newAge)

		if newState > oldState {
			// ── State transition: emit transforms for every crossed state in ascending order ──
			// D9 locality: crossing multiple states in one step emits EVERY passed-through
			// state's transform (ascending state index), so no product is skipped.
			for crossedState := oldState + 1; crossedState <= newState; crossedState++ {
				if crossedState < 0 || crossedState >= len(kr.States) {
					break
				}
				sr := kr.States[crossedState]
				for _, tr := range sr.Transform {
					deltas.Transformed = append(deltas.Transformed, TransformOut{
						SourceID: lot.ID,
						State:    crossedState,
						Item:     tr.Item,
						Qty:      tr.Qty * lot.Qty, // Dm5(a): scales with lot qty
					})
				}
			}

			// Record the transition delta (carries the new absolute DecayAge).
			deltas.Transitioned = append(deltas.Transitioned, TransitionDelta{
				ID:       lot.ID,
				DecayAge: newAge,
				State:    newState,
			})

			// Terminal state: the last state in the kind's States slice.
			// Per content contract (enforced by platform/config), the terminal gone state
			// has no transform products — it only removes mass (D9, the ONLY mass removal).
			terminal := len(kr.States) - 1
			if newState >= terminal {
				deltas.Gone = append(deltas.Gone, lot.ID)
				continue // do not add to nextLots: lot is gone
			}

			// Non-terminal transition: lot survived, carry forward with updated DecayAge.
			nextLots = append(nextLots, Lot{
				ID:       lot.ID,
				Kind:     lot.Kind,
				Qty:      lot.Qty,
				DecayAge: newAge,
			})

		} else if newAge != lot.DecayAge {
			// ── Age advanced, no state change: emit AgeDelta ──
			deltas.Aged = append(deltas.Aged, AgeDelta{
				ID:       lot.ID,
				DecayAge: newAge,
			})
			nextLots = append(nextLots, Lot{
				ID:       lot.ID,
				Kind:     lot.Kind,
				Qty:      lot.Qty,
				DecayAge: newAge,
			})

		} else {
			// ── No change (effectiveRate == 0 or elapsedTicks == 0) ──
			nextLots = append(nextLots, lot)
		}
	}

	// ── Build next State (sorted by ID, D12) ──
	// nextLots was built by iterating sorted prev.lots, so it is already sorted.
	// Sort unconditionally for correctness under all future refactors.
	sort.Slice(nextLots, func(i, j int) bool {
		return nextLots[i].ID < nextLots[j].ID
	})
	nextIdx := make(map[core.ObjectID]int, len(nextLots))
	for i, l := range nextLots {
		nextIdx[l.ID] = i
	}

	// ── Sort all delta slices (D12 — output always in ascending ObjectID order) ──
	// Aged and Transitioned: by ID.
	sort.Slice(deltas.Aged, func(i, j int) bool {
		return deltas.Aged[i].ID < deltas.Aged[j].ID
	})
	sort.Slice(deltas.Transitioned, func(i, j int) bool {
		return deltas.Transitioned[i].ID < deltas.Transitioned[j].ID
	})
	// Transformed: primary sort by SourceID (ascending ObjectID order, D12);
	// secondary sort by State (ascending — "ascending state order" per D9 locality spec);
	// tertiary by Item string (fully deterministic tiebreaker within same state).
	sort.Slice(deltas.Transformed, func(i, j int) bool {
		a, b := &deltas.Transformed[i], &deltas.Transformed[j]
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		if a.State != b.State {
			return a.State < b.State
		}
		return a.Item < b.Item
	})
	sort.Slice(deltas.Gone, func(i, j int) bool {
		return deltas.Gone[i] < deltas.Gone[j]
	})

	return &State{lots: nextLots, idx: nextIdx}, deltas
}

// stateFromKind derives the discrete state index from a continuous decayAge using the
// kind's ordered thresholds. Returns 0 when the kind has no states or decayAge is below
// all thresholds. Used internally by Step.
func stateFromKind(kr KindRule, decayAge float64) int {
	state := 0
	for i, sr := range kr.States {
		if decayAge >= sr.Threshold {
			state = i
		}
	}
	return state
}
