package api

import "github.com/dogring/bdg/engine/kernel/core"

// ── GodViewResponse group (the four /api/god/* response shapes) ───────────────────

// StatTriple is the per-stat 3-way divergence for /api/god/agent/{id}/divergence.
// D8: Real != SelfEstimate != OthersEstimateMean are SEPARATE channels — the handler MUST NOT blend
// them. Real reads RealStats (god view); SelfEstimate reads SelfEstStats (ToM[self]); OthersEstimateMean
// is the mean across all OTHER agents' Belief.EstStats about this subject (cross-agent ToM).
type StatTriple struct {
	Real               float64 `json:"real"`
	SelfEstimate       float64 `json:"self_estimate"`
	OthersEstimateMean float64 `json:"others_estimate_mean"`
}

// DivergenceResponse is GET /api/god/agent/{id}/divergence.
type DivergenceResponse struct {
	AgentID core.AgentID                `json:"agent_id"`
	Tick    core.Tick                   `json:"tick"`
	PerStat map[core.StatID]StatTriple  `json:"per_stat"` // keyed by StatID (glossary); JSON sorts keys (D12)
}

// FactionRep is one observer-cluster's mean opinion of a subject for one stat (reputation breakdown).
// FactionID is a DERIVED cluster label "cluster_<holder_agent_id>" (D2: no Faction type) inferred
// from the snapshot's reliance clustering (EmergedRoles + per-agent RelyOn). Agents is the cluster's
// member ids in sorted order (D12).
type FactionRep struct {
	FactionID string         `json:"faction_id"` // "cluster_<holder>" — derived, never a stored type (D2)
	Agents    []core.AgentID `json:"agents"`     // cluster members, sorted (D12)
	Mean      float64        `json:"mean"`       // mean of this cluster's EstStats[stat].Mean about subject
}

// StatReputation is the D6 reputation distribution for one stat: an aggregate over the DISTRIBUTION of
// observer beliefs, NEVER a stored scalar. Mean = across-observer mean of means; Variance = across-observer
// spread (high = contested). PerFaction breaks the spread down by reliance cluster.
type StatReputation struct {
	Mean       float64      `json:"mean"`        // across-observer mean of EstStats[stat].Mean (derived)
	Variance   float64      `json:"variance"`    // across-observer variance (contested-reputation signal)
	PerFaction []FactionRep `json:"per_faction"` // per reliance cluster, sorted by FactionID (D12)
}

// ReputationResponse is GET /api/god/reputation/{id}. D6: there is NO top-level reputation scalar —
// only the per-stat {mean, variance, per_faction} aggregate, derived on demand from the TomDigest.
type ReputationResponse struct {
	SubjectID core.AgentID                     `json:"subject_id"`
	Tick      core.Tick                        `json:"tick"`
	PerStat   map[core.StatID]StatReputation   `json:"per_stat"` // keyed by StatID; JSON sorts keys (D12)
}

// RelationEdge is one directed (from->to) social-graph edge for /api/god/relations.
type RelationEdge struct {
	From     core.AgentID       `json:"from"`
	To       core.AgentID       `json:"to"`
	Affinity float64            `json:"affinity"`
	Trust    float64            `json:"trust"`
	RelyOn   map[string]float64 `json:"rely_on"` // Function id -> share; sorted keys
}

// RelationsResponse is GET /api/god/relations. Edges are emitted only when at least one of
// {Affinity, Trust, any RelyOn value} is non-zero, and are sorted by (From, To) ascending (D12).
type RelationsResponse struct {
	Tick  core.Tick      `json:"tick"`
	Edges []RelationEdge `json:"edges"` // sorted by (from, to) lexicographic (D12)
}

// GoalCandidate is one (competing or chosen) goal candidate in a why-trace.
type GoalCandidate struct {
	Dimension core.Dimension `json:"dimension"`
	Target    *core.ObjectID `json:"target"`     // nullable — an abstract value goal may have no object (D1)
	EffValue  float64        `json:"eff_value"`
}

// GoalSelectedView mirrors the GoalSelected event payload (data-contracts §4) plus the new
// competing_candidates[] (the prerequisite payload extension flagged in Open Questions).
type GoalSelectedView struct {
	Dimension           core.Dimension  `json:"dimension"`
	Target              *core.ObjectID  `json:"target"`
	EffValue            float64         `json:"eff_value"`
	CompetingCandidates []GoalCandidate `json:"competing_candidates"` // present iff the event payload carries it
}

// PlanBuiltView mirrors the PlanBuilt event payload (data-contracts §4).
type PlanBuiltView struct {
	Steps       []string `json:"steps"`        // e.g. ["MoveTo(forest)","Gather(berry_bush_1)"]
	TotalCost   float64  `json:"total_cost"`
	Provisioned []string `json:"provisioned"`  // forward-sim provisioning summary (D9)
}

// WhyResponse is GET /api/god/why/{agent_id}/{tick}, reconstructed from the Postgres events rows.
type WhyResponse struct {
	AgentID      core.AgentID      `json:"agent_id"`
	Tick         core.Tick         `json:"tick"`
	GoalSelected *GoalSelectedView `json:"goal_selected"` // nil if no GoalSelected event for (agent,tick)
	PlanBuilt    *PlanBuiltView    `json:"plan_built"`    // nil if no PlanBuilt event for (agent,tick)
}

// PartialResponse is returned with 206 Partial Content by divergence/reputation/relations when the
// snapshot blob has no TomDigest (the cross-agent ToM is not available).
type PartialResponse struct {
	Partial bool   `json:"partial"` // always true
	Reason  string `json:"reason"`  // e.g. "tom_digest not available in snapshot"
}
