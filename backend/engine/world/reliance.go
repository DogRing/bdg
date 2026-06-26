package world

import (
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── Emergent reliance-cluster detection (D2 — detection only, no role type) ─────

// relianceScan is the cluster-detection step run after every apply phase.
// It scans every agent's ToM[h].RelyOn[f] edges to detect whether a super-
// threshold share of the village relies on a single holder for a Function.
// Emits RoleEmerged on the RISING EDGE only (first tick the share crosses
// cfg.RoleConvergenceThreshold), for new-function-first-time and succession
// (holder change) alike. Drops below threshold clear the emerged entry so
// re-convergence can fire again.
//
// Deterministic: functions and agents are iterated in sorted order (D12);
// argmax holder ties break by lower AgentID. No RNG.
func (w *World) relianceScan() {
	if len(w.agents) == 0 {
		return
	}
	agentCount := len(w.agents)

	// ── 1. Collect every Function referenced across all agents' RelyOn edges ──
	fnSet := make(map[core.Function]bool)
	for _, agentID := range w.agentIDs {
		a := w.agents[agentID]
		for _, subjectID := range a.ToM.Subjects() {
			b, ok := a.ToM.Self(subjectID)
			if !ok {
				continue
			}
			for fn := range b.RelyOn {
				fnSet[fn] = true
			}
		}
	}

	if len(fnSet) == 0 {
		return
	}

	// Sort functions for deterministic iteration (D12).
	functions := make([]core.Function, 0, len(fnSet))
	for fn := range fnSet {
		functions = append(functions, fn)
	}
	sort.Slice(functions, func(i, j int) bool {
		return string(functions[i]) < string(functions[j])
	})

	// ── 2. Per-Function: count votes (who each agent relies on most) ──────────
	for _, fn := range functions {
		// votes: holderAgentID → count of agents whose strongest RelyOn[fn] points there.
		votes := make(map[core.AgentID]int)

		for _, agentID := range w.agentIDs {
			a := w.agents[agentID]
			bestHolder := core.AgentID("")
			bestVal := float64(0)

			// Iterate all subjects (ToM.Subjects is sorted, D12).
			for _, subjectID := range a.ToM.Subjects() {
				b, ok := a.ToM.Self(subjectID)
				if !ok {
					continue
				}
				val := b.RelyOn[fn]
				if val > bestVal {
					bestVal = val
					bestHolder = subjectID
				} else if val == bestVal && val > 0 {
					// Tie: lower AgentID wins (D12).
					if string(subjectID) < string(bestHolder) {
						bestHolder = subjectID
					}
				}
			}

			if bestHolder != "" && bestVal > 0 {
				votes[bestHolder]++
			}
		}

		if len(votes) == 0 {
			// No votes for this function — ensure it is cleared from emerged set.
			delete(w.emergedRoles, fn)
			continue
		}

		// ── 3. Find the plurality holder (argmax) ────────────────────────────
		holder := core.AgentID("")
		maxVotes := -1
		for _, hid := range sortedVoteKeys(votes) {
			c := votes[hid]
			if c > maxVotes {
				maxVotes = c
				holder = hid
			} else if c == maxVotes {
				// Tie: lower AgentID wins (D12).
				if string(hid) < string(holder) {
					holder = hid
				}
			}
		}

		// ── 4. Check against threshold ───────────────────────────────────────
		share := float64(maxVotes) / float64(agentCount)
		prevHolder, wasEmerged := w.emergedRoles[fn]

		if share >= w.cfg.RoleConvergenceThreshold {
			if !wasEmerged || prevHolder != holder {
				// Rising edge: either first time or succession (holder change).
				w.emergedRoles[fn] = holder

				w.emit.Emit(core.Event{
					SchemaVersion: 1,
					Tick:          w.tick,
					AgentID:       "",
					Type:          "RoleEmerged",
					Payload: map[string]any{
						"function":       string(fn),
						"holder":         string(holder),
						"reliance_share": share,
					},
				})
			}
			// Already emerged with same holder → no event (debounce).
		} else {
			// Below threshold: clear from emerged set (allow re-emergence later).
			delete(w.emergedRoles, fn)
		}
	}
}

// sortedVoteKeys returns the AgentID keys of a votes map in sorted order (D12).
func sortedVoteKeys(m map[core.AgentID]int) []core.AgentID {
	keys := make([]core.AgentID, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i]) < string(keys[j])
	})
	return keys
}

// ── Compile-time guard: NO Role type exists (D2) ────────────────────────────────
// Uncommenting the line below must fail to compile:
// var _ core.Role // D2: no Role type in engine/world
