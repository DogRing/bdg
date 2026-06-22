// Package tom owns each agent's Theory of Mind: a per-subject Belief (= ToM[X])
// holding a per-stat estimate (mean + uncertainty) of every other agent it knows,
// and of itself (ToM[self], D8). This is the reputation substrate.
//
// Reputation is NEVER stored as a single value (D6) — it is the distribution of
// ToM[C] across observers, derived on demand by ReputationDist.
package tom

import (
	"math"
	"sort"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/stats"
)

// SubjectID is who a belief is *about*. It aliases core.AgentID; self-belief is
// keyed by the owning agent's own id (ToM[self], D8).
type SubjectID = core.AgentID

// Function names a service an agent relies on another to provide (glossary: Reliance edge).
// Canonical declaration is core.Function; this alias makes map keys interoperate.
type Function = core.Function

// StatDist is the mean/variance summary of a stat estimate.
// Variance >= 0 encodes uncertainty: high variance = low confidence (a fresh /
// hearsay belief); it shrinks toward 0 as direct evidence accumulates.
type StatDist struct {
	Mean     float64 `json:"mean"`
	Variance float64 `json:"variance"`
}

// Belief is one subject-model (glossary: Belief = ToM[X]).
// Fields: EstStats, Trust, Affinity, LastSeen, Ledger, RelyOn.
// Ledger update logic is out of scope here (this module only carries the storage).
// RelyOn is updated via AdjustRelyOn; the DECISION of when/whom is engine/agent's.
type Belief struct {
	EstStats map[core.StatID]StatDist // per-stat estimate (mean + uncertainty)
	Trust    float64                  // credibility weight applied to THIS subject's claims when it gossips; [0,1]
	Affinity float64                  // signed relational stance; updated by engine/agent
	LastSeen core.Tick                // tick of the last direct observation
	Ledger   any                      // reserved: social-exchange ledger; populated by engine/agent
	RelyOn   map[Function]float64     // P6: reliance edges — how much this observer relies on subject per Function; [0,1]
}

// ToM is one agent's whole theory-of-mind: subject id -> Belief, INCLUDING self.
// It wraps a map for storage/lookup; per D12 code MUST NOT range over the
// underlying map for logic — iterate via Subjects() (sorted) instead.
// The self field records which SubjectID is the owning agent (ToM[self], D8),
// and rates/reg store the injected configuration for all methods.
type ToM struct {
	self            core.AgentID    // owning agent's ID; used to enforce D8
	belief          map[SubjectID]Belief
	rates           Rates           // injected rate constants
	reg             *stats.Registry // stat definitions for clamping and initial estimates
	selfPerception  float64         // perception capability (typically Intelligence) governing initial uncertainty for unknown subjects
}

// StatEvidence is one direct-observation datum: the outcome the observer
// attributes to a stat.
type StatEvidence struct {
	Stat     core.StatID
	Observed float64 // the value implied by the observed action/outcome (in stat units)
	Weight   float64 // precision of this evidence in [0,1]; scales the update (beta * Weight)
	Tick     core.Tick
}

// Rates bundles the numeric rates that govern tom update rules.
// These must be read from content/balance.yaml by the caller and injected.
type Rates struct {
	Alpha              float64 // gossip.alpha
	Beta               float64 // self_calibration.beta
	MinTrust           float64 // gossip.min_trust
	InitialBeliefNoise float64 // generation.initial_belief_noise
	// P2 trade protocol — content/balance.yaml trade.*
	TradeSuccessTrustDelta  float64 // Trust gain per completed trade
	TradeRejectAffinityDrop float64 // Affinity drop magnitude on rejection
	FraudHonestyDrop        float64 // Honesty mean drop per fraud detection
	FraudThreshold          float64 // claimedValue−actualEffect above which fraud fires
	// P6 influence — content/balance.yaml politics.influence_weight
	InfluenceWeight float64 // reflection ratio for influence-amplified gossip; [0, 1]
}

// DefaultRates returns the canonical rate constants from the SPEC's
// content/balance.yaml excerpt.
func DefaultRates() Rates {
	return Rates{
		Alpha:              0.12,
		Beta:               0.08,
		MinTrust:           0.05,
		InitialBeliefNoise: 0.15,
		// P2 trade protocol
		TradeSuccessTrustDelta:  0.05,
		TradeRejectAffinityDrop: 0.02,
		FraudHonestyDrop:        0.10,
		FraudThreshold:          0.20,
		// P6 influence
		InfluenceWeight: 0.5,
	}
}

// ── Construction ─────────────────────────────────────────────────────────────

// NewToM constructs an agent's ToM, seeded with its self-belief (ToM[self], D8).
// self is the owning agent's id; realStats are the agent's Real Stats (god view)
// used ONLY to seed the self-estimate with calibrated noise; selfPerception is
// the perception capability (typically Intelligence from ToM[self]) governing
// initial uncertainty; rng is the injected seeded generator (D12).
// The self-belief's EstStats are NOT exact: they are realStats perturbed per
// rates.InitialBeliefNoise, producing the over/under-estimation asymmetry of D8.
func NewToM(self core.AgentID, realStats stats.Stats, selfPerception float64, rng *rng.RNG, reg *stats.Registry, rates Rates) ToM {
	t := ToM{
		self:            self,
		belief:          make(map[SubjectID]Belief),
		rates:           rates,
		reg:             reg,
		selfPerception:  clamp(selfPerception, 0, 1),
	}

	estStats := make(map[core.StatID]StatDist, reg.Len())
	for _, sid := range reg.IDs() {
		realVal := realStats.Get(sid)
		def, _ := reg.Def(sid)

		noise := rng.NormFloat64() * rates.InitialBeliefNoise
		noise = math.Round(noise*1e12) / 1e12
		estMean := def.Clamp(realVal + noise)

		estStats[sid] = StatDist{
			Mean:     estMean,
			Variance: 0, // self-belief has no uncertainty (the agent "lives" its own stats)
		}
	}

	t.belief[self] = Belief{
		EstStats: estStats,
		Trust:    1.0,
		Affinity: 0,
		LastSeen: 0,
	}
	return t
}

// ── Observe ───────────────────────────────────────────────────────────────────

// Observe folds DIRECT evidence about subject into the observer's belief, per stat.
// Direct observation is the ONLY mechanism that updates ToM[self] (D8).
// Each observed stat's StatDist is updated by the precision-weighted rule:
//
//	mean += beta * Weight * (Observed - mean)
//	Variance *= (1 - Weight)
//
// Creates the belief (via the initial-estimate seed) if subject is unknown.
// Sets LastSeen = ev.Tick.
func (t ToM) Observe(subject core.AgentID, ev StatEvidence) {
	if ev.Weight <= 0 {
		return
	}

	b := t.getOrCreateBelief(subject, ev)

	sd, ok := b.EstStats[ev.Stat]
	if !ok {
		return
	}

	def, _ := t.reg.Def(ev.Stat)
	delta := (ev.Observed - sd.Mean) * t.rates.Beta * ev.Weight
	sd.Mean += delta
	sd.Mean = def.Clamp(sd.Mean)

	sd.Variance *= (1 - ev.Weight)
	if sd.Variance < 0 {
		sd.Variance = 0
	}
	if sd.Variance < 1e-15 {
		sd.Variance = 0
	}

	b.EstStats[ev.Stat] = sd
	b.LastSeen = ev.Tick
	t.belief[subject] = b
}

// GossipUpdate folds HEARSAY about subject, told by a source whose model of the
// subject is source and whose credibility to this agent is trustWeight.
//
//	ToM[subject].EstStats[s].Mean += alpha * trustWeight * (source.EstStats[s].Mean - ToM[subject].EstStats[s].Mean)
//
// Hearsay is INFLATIONARY on uncertainty (variance does not shrink to zero from
// gossip). If trustWeight < rates.MinTrust the claim is ignored (no-op).
// Creates the belief from the initial seed if subject is unknown.
// GossipUpdate NEVER touches ToM[self] (self is only calibrated by Observe, D8).
func (t ToM) GossipUpdate(subject core.AgentID, source Belief, trustWeight float64) map[core.StatID]float64 {
	if trustWeight < t.rates.MinTrust {
		return nil
	}
	if subject == t.self {
		return nil // D8: gossip never touches self-belief
	}

	b := t.getOrCreateBeliefForGossip(subject, trustWeight)
	deltas := make(map[core.StatID]float64)

	for _, sid := range t.reg.IDs() {
		sd, sdOk := b.EstStats[sid]
		claimSD, claimOk := source.EstStats[sid]
		if !sdOk || !claimOk {
			continue
		}

		def, _ := t.reg.Def(sid)
		oldMean := sd.Mean
		delta := t.rates.Alpha * trustWeight * (claimSD.Mean - sd.Mean)
		sd.Mean += delta
		sd.Mean = def.Clamp(sd.Mean)

		// Gossip does NOT shrink variance. Mildly inflate to reflect hearsay uncertainty.
		if sd.Variance < 1e-15 {
			sd.Variance = 1e-15
		}
		sd.Variance *= (1 + t.rates.Alpha*trustWeight*0.1)

		b.EstStats[sid] = sd
		actualDelta := sd.Mean - oldMean
		if actualDelta != 0 {
			deltas[sid] = actualDelta
		}
	}

	b.LastSeen = 0 // gossip is not a direct observation
	t.belief[subject] = b
	return deltas
}

// ReputationDist DERIVES the reputation distribution of subject across the
// supplied observer models (D6: reputation is NEVER a stored single value).
// It returns, per stat, the StatDist whose Mean is the across-observer mean of
// each observer's estimated mean, and whose Variance is the across-observer
// variance of those means (the spread of opinion).
func (t ToM) ReputationDist(subject core.AgentID, observers []Belief) map[core.StatID]StatDist {
	if len(observers) == 0 {
		return nil
	}

	// Collect all stat IDs present across observers, sorted (D12).
	statSet := make(map[core.StatID]bool)
	for _, obs := range observers {
		for sid := range obs.EstStats {
			statSet[sid] = true
		}
	}

	statIDs := make([]core.StatID, 0, len(statSet))
	for sid := range statSet {
		statIDs = append(statIDs, sid)
	}
	sort.Slice(statIDs, func(i, j int) bool {
		return string(statIDs[i]) < string(statIDs[j])
	})

	result := make(map[core.StatID]StatDist, len(statIDs))
	for _, sid := range statIDs {
		var sum, sumSq float64
		var count int
		for _, obs := range observers {
			sd, ok := obs.EstStats[sid]
			if !ok {
				continue
			}
			sum += sd.Mean
			sumSq += sd.Mean * sd.Mean
			count++
		}

		if count == 0 {
			continue
		}

		mean := sum / float64(count)
		variance := (sumSq / float64(count)) - (mean*mean)
		if variance < 0 {
			variance = 0
		}

		result[sid] = StatDist{
			Mean:     mean,
			Variance: variance,
		}
	}

	return result
}

// ── Accessors ─────────────────────────────────────────────────────────────────

// Subjects returns all subject ids known to this ToM in canonical fixed order
// (sorted lexicographically by AgentID). The ONE ordering for iteration (D12).
// Returned slice is a copy.
func (t ToM) Subjects() []SubjectID {
	ids := make([]SubjectID, 0, len(t.belief))
	for id := range t.belief {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return string(ids[i]) < string(ids[j])
	})
	return ids
}

// Self returns the self-belief (ToM[self]) for owner id self, and whether it exists.
func (t ToM) Self(self core.AgentID) (Belief, bool) {
	b, ok := t.belief[self]
	return b, ok
}

// SelfID returns the owning agent's ID.
func (t ToM) SelfID() core.AgentID {
	return t.self
}

// SetSelfStats replaces the self-belief's EstStats with the given values.
// Used for state restoration (resume invariant, testing.md §1). Only touches
// ToM[self]; other subjects are unaffected. No-op if self doesn't exist.
func (t ToM) SetSelfStats(estStats map[core.StatID]StatDist) {
	b, ok := t.belief[t.self]
	if !ok {
		return
	}
	b.EstStats = estStats
	t.belief[t.self] = b
}

// ── Trade outcome methods (P2) ────────────────────────────────────────────────

// RecordTradeSuccess increments Trust for `other` by rates.TradeSuccessTrustDelta,
// clamped to [0,1]. Call on EACH agent's own ToM independently (양측 모두).
func (t ToM) RecordTradeSuccess(other core.AgentID, tick core.Tick) {
	b := t.getOrSeedBelief(other)
	b.Trust = clamp(b.Trust+t.rates.TradeSuccessTrustDelta, 0, 1)
	b.LastSeen = tick
	t.belief[other] = b
}

// RecordTradeRejection decrements Affinity for `other` by rates.TradeRejectAffinityDrop.
// Called by the REJECTED agent on its own ToM.
func (t ToM) RecordTradeRejection(other core.AgentID, tick core.Tick) {
	b := t.getOrSeedBelief(other)
	b.Affinity -= t.rates.TradeRejectAffinityDrop
	b.LastSeen = tick
	t.belief[other] = b
}

// AdjustAffinity persists an Affinity delta on the belief toward `subject`.
// Used by engine/agent to apply resentment-driven Affinity drops (P3).
// Seeds the belief if not yet known.
func (t ToM) AdjustAffinity(subject core.AgentID, delta float64) {
	b := t.getOrSeedBelief(subject)
	b.Affinity += delta
	t.belief[subject] = b
}

// RecordFraud lowers EstStats[honestyStatID].Mean for `other` by rates.FraudHonestyDrop
// when claimedValue−actualEffect > rates.FraudThreshold. A pleasant surprise (actual > claimed)
// is not fraud and is a no-op. Also a no-op when honestyStatID is not registered.
func (t ToM) RecordFraud(other core.AgentID, claimedValue, actualEffect float64, honestyStatID core.StatID, tick core.Tick) {
	if claimedValue-actualEffect <= t.rates.FraudThreshold {
		return
	}
	b := t.getOrSeedBelief(other)
	sd, ok := b.EstStats[honestyStatID]
	if !ok {
		return
	}
	def, ok := t.reg.Def(honestyStatID)
	if !ok {
		return
	}
	sd.Mean = def.Clamp(sd.Mean - t.rates.FraudHonestyDrop)
	b.EstStats[honestyStatID] = sd
	b.LastSeen = tick
	t.belief[other] = b
}

// ── P6 Reliance & Influence ──────────────────────────────────────────────────

// AdjustRelyOn folds a reliance increment toward `target` for `function` into
// this observer's belief about target: RelyOn[function] += delta, clamped to [0,1].
// Seeds the belief (initial estimate) if target is unknown. delta may be negative
// (reliance decays / shifts away). This is the ONLY mutator of RelyOn; the
// WHEN/WHOM decision is engine/agent's.
func (t ToM) AdjustRelyOn(target core.AgentID, function Function, delta float64) {
	b := t.getOrSeedBelief(target)
	if b.RelyOn == nil {
		b.RelyOn = make(map[Function]float64)
	}
	current := b.RelyOn[function]
	b.RelyOn[function] = clamp(current+delta, 0, 1)
	t.belief[target] = b
}

// BestProviderFor ranks candidates as reliance targets for `function` and returns
// the best (trusted AND competent) plus its score, or ("", 0) if none qualifies.
// Score = Trust[candidate] * EstStats[candidate][stat].Mean / maxPossibleStat,
// where maxPossibleStat is the stat's Max from the registry.
// Deterministic: candidates are evaluated in sorted AgentID order; ties break by
// lower id (D12).
func (t ToM) BestProviderFor(function Function, statSet []core.StatID, candidates []core.AgentID) (core.AgentID, float64) {
	if len(candidates) == 0 {
		return "", 0
	}

	// Sort candidates for deterministic evaluation (D12).
	sorted := make([]core.AgentID, len(candidates))
	copy(sorted, candidates)
	sort.Slice(sorted, func(i, j int) bool {
		return string(sorted[i]) < string(sorted[j])
	})

	// Use the first stat in statSet as the relevant stat for competence.
	var relevantStat core.StatID
	var maxPossible float64
	if len(statSet) > 0 {
		relevantStat = statSet[0]
		if def, ok := t.reg.Def(relevantStat); ok {
			maxPossible = def.Max
		}
	}

	bestID := core.AgentID("")
	bestScore := float64(-1)

	for _, id := range sorted {
		b := t.getOrSeedBelief(id)

		// Trust component.
		score := b.Trust

		// Competence component: EstStats[relevantStat].Mean / maxPossible.
		if relevantStat != "" && maxPossible > 0 {
			if sd, ok := b.EstStats[relevantStat]; ok {
				competence := sd.Mean / maxPossible
				score *= competence
			} else {
				score = 0
			}
		}

		if score > bestScore {
			bestScore = score
			bestID = id
		}
		// Ties break by lexicographic AgentID (already in sorted order, so first
		// encountered with equal score wins — that's the lower id).
	}

	if bestID == "" {
		return "", 0
	}
	return bestID, bestScore
}

// Influence DERIVES the social weight of `subject` for `function`: the mean of
// RelyOn[function] across the supplied observer beliefs about subject (D6-style:
// Influence is never a stored scalar — it is this aggregate over the RelyOn
// distribution, exactly as reputation is an aggregate over EstStats).
//
// If observers is empty, returns 0.
func (t ToM) Influence(subject core.AgentID, fn Function, observers []Belief) float64 {
	if len(observers) == 0 {
		return 0
	}
	var sum float64
	for _, obs := range observers {
		if obs.RelyOn != nil {
			sum += obs.RelyOn[fn]
		}
	}
	return sum / float64(len(observers))
}

// ── ToM Prune (memory bounding) ───────────────────────────────────────────────

// PruneBeliefs removes stale beliefs (LastSeen < currentTick - maxAge) to bound
// memory in long runs. It NEVER prunes the self-belief (ToM[self], D8) and keeps
// at least minKeep beliefs regardless of age (to retain recent social context).
// Iterates subjects in sorted order (D12).
func (t ToM) PruneBeliefs(currentTick core.Tick, maxAge core.Tick, minKeep int) {
	if maxAge <= 0 {
		return // 0 = never prune (current behaviour)
	}
	threshold := currentTick - maxAge
	subjects := t.Subjects()

	var toRemove []SubjectID
	for i, id := range subjects {
		if id == t.self {
			continue // D8: NEVER prune self-belief
		}
		// Keep at least minKeep beliefs (the most recent ones, which come
		// last in sorted order — last elements of the slice; we count from
		// the end).
		if len(subjects)-i <= minKeep {
			continue
		}
		b := t.belief[id]
		if b.LastSeen < threshold {
			toRemove = append(toRemove, id)
		}
	}
	for _, id := range toRemove {
		delete(t.belief, id)
	}
}

// ── Internal helpers ─────────────────────────────────────────────────────────

// getOrSeedBelief returns the belief for subject, seeding from initial estimate if unknown.
func (t ToM) getOrSeedBelief(subject core.AgentID) Belief {
	if b, ok := t.belief[subject]; ok {
		return b
	}
	return t.seedInitialBelief(subject)
}

// getOrCreateBelief returns the belief for subject, creating one from an initial
// seed if it does not yet exist. The initial estimate uses the stat range
// midpoint as priorMean, and variance = baseVar * (1 - selfPerception) where
// selfPerception = clamp(ToM[self].Intelligence, 0, 1).
func (t ToM) getOrCreateBelief(subject core.AgentID, ev StatEvidence) Belief {
	if b, ok := t.belief[subject]; ok {
		return b
	}
	return t.seedInitialBelief(subject)
}

// getOrCreateBeliefForGossip creates a belief with initial seed for gossip path.
func (t ToM) getOrCreateBeliefForGossip(subject core.AgentID, trustWeight float64) Belief {
	if b, ok := t.belief[subject]; ok {
		return b
	}
	return t.seedInitialBelief(subject)
}

// seedInitialBelief creates a fresh belief with initial estimates for all
// registered stats, using the agent's self-perception to set variance.
func (t ToM) seedInitialBelief(subject core.AgentID) Belief {
	baseVar := t.rates.InitialBeliefNoise * t.rates.InitialBeliefNoise
	initialVariance := baseVar * (1 - t.selfPerception)

	estStats := make(map[core.StatID]StatDist, t.reg.Len())
	for _, sid := range t.reg.IDs() {
		def, _ := t.reg.Def(sid)
		priorMean := (def.Min + def.Max) / 2.0
		estStats[sid] = StatDist{
			Mean:     priorMean,
			Variance: initialVariance,
		}
	}

	b := Belief{
		EstStats: estStats,
		Trust:    0.5,
		Affinity: 0,
		LastSeen: 0,
	}
	t.belief[subject] = b
	return b
}

// clamp returns v constrained to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
