package agent

import (
	"sort"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/perception"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/tom"
	"github.com/dogring/bdg/engine/values"
)

// The agent

// Agent is one simulated villager: its dynamic Body state, its ToM (incl. ToM[self],
// D8), its current Goal and durative Plan, and its coping state. Owned and mutated
// ONLY through this package's methods (Tick reads + assembles intents; ApplyOutcome
// folds back the resolved outcome). The world holds the Agent and calls these in the
// fixed apply order (D12).
type Agent struct {
	ID  core.AgentID
	Pos core.Vec2

	// Body — the dynamic state (glossary State layers).
	Inventory       map[core.Tag]int           // item-kind id count (sorted-key iteration only, D12)
	Stamina         float64                    // consumable effort budget, in [0, StaminaMax]
	Mood            float64                    // signed; += lambda*(actual-expected), decays to baseline
	Adrenaline      float64                    // urgency-driven surge in [0, AdrMax]
	NeedIntensities map[core.Dimension]float64 // grown need intensity per Dimension (higher = worse)

	// Real Stats — the god view; decides OUTCOMES only (engine/world), NEVER read by decisions (D8).
	RealStats stats.Stats

	// ToM — the agent's whole theory-of-mind, INCLUDING ToM[self]; decisions read ToM[self] (D8).
	ToM tom.ToM

	// Values — the agent's held value directions (Dimension + Referent + Posture + Setpoint).
	// What the agent ultimately cares about. Grows as the agent discovers new referents.
	// Populated at construction from the needs.yaml defaults and per-agent disposition modulation.
	Values []core.Value

	// Cfg — immutable per-agent config (injected at construction from balance.yaml, D10).
	// Stored on Agent so Tick has access to all tunables without bloating the method signature.
	Cfg Config

	// Deliberation state across ticks.
	Goal    core.Dimension  // the currently-pursued goal Dimension (empty = none raised)
	Plan    planner.Plan    // the active durative plan (ordered ActionIDs being executed)
	PlanIdx int             // index of the action currently executing within Plan.Actions
	Elapsed core.GameMinutes // game-minutes the current action has been running (durative progress)

	Coping     CopingState   // where the agent sits in the coping cascade (design 3)
	FailStreak int           // P3: consecutive failed re-plans; resets on any plan/action success
	Latent     []LatentGoal  // unmet goals stored below the surface (Longing/Latent), feed Resentment
	Resentment float64       // P3: accrues while Latent on trigger events; drives Aggression/Affinity drift (D2)
}

// Config (every rate from content/balance.yaml; no hardcoded constant, D10)

// Config bundles every tunable the agent loop reads, injected by the caller (read
// from content/balance.yaml via platform/config). The agent hardcodes NO numeric
// constant (D10). Immutable for the agent's lifetime; shared across agents (read-only).
type Config struct {
	// mood
	Lambda       float64 // balance.yaml mood.lambda
	MoodDecay    float64 // balance.yaml mood.decay
	MoodBaseline float64 // balance.yaml mood.baseline

	// adrenaline
	AdrTriggerUrgency float64 // balance.yaml adrenaline.trigger_urgency
	AdrSurge          float64 // balance.yaml adrenaline.surge
	AdrDecay          float64 // balance.yaml adrenaline.decay (the crash)
	AdrMax            float64 // balance.yaml adrenaline.max

	// stamina dynamics
	StaminaMax     float64 // balance.yaml stamina.max
	DrainPerEffort float64 // balance.yaml stamina.drain_per_effort (effort tag level)
	RegenRest      float64 // balance.yaml stamina.regen_rest
	RegenSleep     float64 // balance.yaml stamina.regen_sleep

	// urgency mapping
	UrgencyFromDeficit float64 // balance.yaml urgency.from_deficit
	BudgetPenalty      float64 // balance.yaml urgency.budget_penalty

	// beta self-calibration (D8): applied ONLY on a resolved attempt, per used stat.
	Beta float64 // balance.yaml self_calibration.beta

	// resentment drift fed by latent goals
	AffinityDrop    float64 // balance.yaml resentment.affinity_drop
	AggressionDrift float64 // balance.yaml resentment.aggression_drift

	// cross-tick goal-switching anti-thrash
	Stickiness   float64 // bonus to the current goal's Priority (anti-thrash)
	GoalDeadband float64 // don't switch goals until a rival beats current by this margin

	// budget scaling — shaped into a planner.Budget per Tick
	BudgetBase            int // base GOAP/HTN search nodes
	BudgetPerIntelligence int // + perceived Intelligence * this

	// gossip credibility floor used when folding interaction signals
	Rates tom.Rates // alpha, beta, min_trust, initial_belief_noise — threaded to ToM updates

	// P3: stamina effort-level resolution
	EffortLevels map[core.Tag]float64 // balance.yaml tag_levels.effort

	// adrenaline stamina coupling
	CrashStaminaPenalty float64 // balance.yaml adrenaline.crash_stamina_penalty

	// coping cascade
	RebindMinIntelligence float64 // balance.yaml intelligence.rebind_threshold
	ApathyFailStreak      int     // balance.yaml coping.apathy_fail_streak
	ApathyRecoverMood     float64 // balance.yaml coping.apathy_recover_mood
	ApathyBudgetPenalty   float64 // balance.yaml coping.apathy_budget_penalty

	// resentment accrual
	ResentmentPerTrigger float64 // balance.yaml resentment.per_trigger
	ResentmentThreshold  float64 // balance.yaml resentment.threshold

	// P5: trade-signal deceptive-claim band
	ClaimInflateMin float64 // balance.yaml trade.claim_inflate_min
	ClaimInflateMax float64 // balance.yaml trade.claim_inflate_max

	// P5: Other-referent bond multiplier and care threshold
	BondAffinityGain    float64 // balance.yaml social.bond_affinity_gain
	MinCareThreshold    float64 // balance.yaml social.min_care_threshold
	MaxPossiblePriority float64 // ceiling to normalize Other-referent priority into urgency proxy; default 2.5

	// P5: collective-safety defensive trigger (BLOCKER-2). When the mean collective
	// satisfaction (1 − mean member need-intensity) for a Collective value's dimension
	// drops below this, the holder adopts that dimension as a defensive goal (→ Patrol).
	SafetyThreatThreshold float64 // balance.yaml threats.safety_threat_threshold

	// P6 BLOCKER-1: threat perception tags and Safety-dimension config
	ThreatTags          []core.Tag      // balance.yaml threats.hostile_tags — tags that trigger defensive Safety goal insertion
	SafetyDim           core.Dimension  // content/needs.yaml Safety dimension id, resolved by platform/config
	RestDim             core.Dimension // content/needs.yaml Rest dimension id, resolved by platform/config; replaces hardcoded "Rest" literal (D10)
	ThreatPerThreatGain float64        // balance.yaml threats.per_threat_intensity — Safety intensity added per perceived threat
	ThreatSafetyDecay   float64        // balance.yaml threats.safety_decay — Safety intensity removed per tick with no threat

	// D10: stat IDs resolved from the stats registry (no hardcoded glossary literals in engine code).
	IntelligenceStatID    core.StatID // capability stat for Intelligence lookups
	VindictivenessStatID  core.StatID // disposition stat for Vindictiveness lookups
	AggressionStatID      core.StatID // disposition stat for Aggression lookups

	// P6: function→dimension→stat-set table (injected, D7/D10).
	// Replaces the hardcoded goalToFunction mapping.
	Functions []FunctionSpec // content: function id → (goal Dimension, capability stat-set)

	// P6: reliance + vote policy thresholds
	RelyCostThreshold    float64 // balance.yaml politics.rely_cost_threshold — plan cost above which a Function counts self-unsolvable
	RelyOnDelta          float64 // balance.yaml politics.relyon_delta — δ added to RelyOn on self-failure
	VoteRelyThreshold    float64 // balance.yaml politics.vote_rely_threshold — private reliance strength that licenses a Vote
	UrgencyThreshold     float64 // balance.yaml politics.urgency_threshold — combined-urgency proxy above which a Vote is cast (§H)
	VoteRelyOnDelta      float64 // balance.yaml politics.vote_relyon_delta — δ a heard Vote folds into RelyOn

	// P6: influence weighting for incoming signals
	InfluenceWeight float64 // balance.yaml politics.influence_weight — signalWeight = trust·(1 + influence_weight·Influence)
}

// DefaultConfig returns the canonical Config from content/balance.yaml (for tests / headless).
func DefaultConfig() Config {
	return Config{
		// mood
		Lambda:       0.25,
		MoodDecay:    0.02,
		MoodBaseline: 0.0,

		// adrenaline
		AdrTriggerUrgency: 0.65,
		AdrSurge:          0.6,
		AdrDecay:          0.03,
		AdrMax:            1.0,

		// stamina
		StaminaMax:     1.0,
		DrainPerEffort: 0.015,
		RegenRest:      0.010,
		RegenSleep:     0.030,

		// urgency
		UrgencyFromDeficit: 1.4,
		BudgetPenalty:      0.6,

		// self-calibration
		Beta: 0.08,

		// resentment
		AffinityDrop:    0.15,
		AggressionDrift: 0.02,

		// goal-switching
		Stickiness:   0.15,
		GoalDeadband: 0.08,

		// budget
		BudgetBase:            24,
		BudgetPerIntelligence: 60,

		// gossip
		Rates: tom.DefaultRates(),

		// P3: effort levels from balance.yaml tag_levels.effort
		EffortLevels: map[core.Tag]float64{
			"effort:none": 0.0,
			"effort:low":  0.20,
			"effort:med":  0.50,
			"effort:high": 0.90,
		},

		// P3: adrenaline-stamina coupling
		CrashStaminaPenalty: 0.50,

		// P3: coping cascade
		RebindMinIntelligence: 0.4,
		ApathyFailStreak:      3,
		ApathyRecoverMood:     0.15,
		ApathyBudgetPenalty:   0.5,

		// P3: resentment
		ResentmentPerTrigger: 0.05,
		ResentmentThreshold:  0.30,

		// P5: trade-signal deceptive-claim band
		ClaimInflateMin: 0.50,
		ClaimInflateMax: 0.90,

		// P5: Other-referent social tuning
		BondAffinityGain:    0.20,
		MinCareThreshold:    0.30,
		MaxPossiblePriority: 2.5,

		// P5: collective-safety defensive trigger (BLOCKER-2)
		SafetyThreatThreshold: 0.30,

		// P6: function→dimension→stat-set table (injected defaults for tests; D7/D10).
		Functions: []FunctionSpec{
			{ID: core.FuncSafety, Dim: "Safety", Stats: []core.StatID{"Strength"}},
			{ID: core.FuncKnowledge, Dim: "Knowledge", Stats: []core.StatID{"Intelligence"}},
		},

		// P6: reliance + vote policy thresholds
		RelyCostThreshold: 1.2,
		RelyOnDelta:       0.15,
		VoteRelyThreshold: 0.4,
		UrgencyThreshold:  0.65,
		VoteRelyOnDelta:   0.10,

		// P6: influence weighting for incoming signals
		InfluenceWeight: 0.5,

		// D10: glossary-canonical IDs — defaults used when config is not loaded from
		// content (tests). When content IS loaded, platform/config resolves these from
		// the registries (engine/agent code never hardcodes ID literals).
		SafetyDim:            "Safety",
		RestDim:              "Rest",
		IntelligenceStatID:   "Intelligence",
		VindictivenessStatID: "Vindictiveness",
		AggressionStatID:     "Aggression",
	}
}

// Services (borrowed read-only, passed per-Tick)

// Services bundles the shared, immutable upstream services the loop needs each tick.
// They are borrowed read-only (constructed once by the world, shared across all
// agents) — the agent never mutates them. Passing them per-Tick (rather than storing
// on Agent) keeps the Agent serializable and avoids each agent owning a copy of the
// registries.
type Services struct {
	Sensor  *perception.Sensor // the three senses (engine/perception)
	Planner *planner.Planner   // HTN+GOAP deliberation (engine/planner)
	Values  *values.Config     // per-Dimension arbitration weights (engine/values)
	Needs   *needs.Registry    // need catalog + forward-roll helpers (engine/needs)
	Stats   *stats.Registry    // stat metadata (capability set, ranges) - no hardcoded ids (D7)
	Actions *actions.Registry  // action catalog (tags, duration, producers) - for execution
}

// Construction

// New constructs an Agent with its generated Real Stats, seeded ToM (incl. ToM[self],
// built by engine/tom with the injected rng), starting Body state from cfg, and an
// empty plan/coping Idle.
func New(id core.AgentID, pos core.Vec2, realStats stats.Stats, selfToM tom.ToM, cfg Config) *Agent {
	return &Agent{
		ID:              id,
		Pos:             pos,
		Inventory:       make(map[core.Tag]int),
		Stamina:         cfg.StaminaMax,
		Mood:            cfg.MoodBaseline,
		Adrenaline:      0,
		NeedIntensities: make(map[core.Dimension]float64),
		RealStats:       realStats,
		ToM:             selfToM,
		Values:          nil,
		Cfg:             cfg,
		Goal:            "",
		Plan:            planner.Plan{},
		PlanIdx:         0,
		Elapsed:         0,
		Coping:          Idle,
		FailStreak:      0,
		Latent:          nil,
		Resentment:      0,
	}
}

// Helpers

// clamp clamps v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clamp01 clamps v to [0, 1].
func clamp01(v float64) float64 {
	return clamp(v, 0, 1)
}

// sortedNeedIDs returns the need IDs from the registry in canonical sorted order.
func sortedNeedIDs(needsReg *needs.Registry) []core.Dimension {
	return needsReg.IDs()
}

// sortInventoryKeys returns sorted string keys of an inventory map for D12 iteration.
func sortInventoryKeys(inv map[core.Tag]int) []core.Tag {
	keys := make([]core.Tag, 0, len(inv))
	for k := range inv {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i]) < string(keys[j])
	})
	return keys
}

// sortNeedIntensityKeys returns sorted Dimension keys for D12 iteration.
func sortNeedIntensityKeys(ni map[core.Dimension]float64) []core.Dimension {
	keys := make([]core.Dimension, 0, len(ni))
	for k := range ni {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i]) < string(keys[j])
	})
	return keys
}

// sortStatIDs sorts a slice of StatIDs lexicographically (D12).
func sortStatIDs(ids []core.StatID) {
	sort.Slice(ids, func(i, j int) bool {
		return string(ids[i]) < string(ids[j])
	})
}
