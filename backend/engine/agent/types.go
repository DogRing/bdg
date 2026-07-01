// Package agent drives one agent through one tick of the decision loop:
// perception → value appraisal → goal mediation → planning → durative execution →
// signal → belief/reputation/reliance update. It is the orchestrator (D5): it owns
// the agent's dynamic Body state and ToM, sequences the upstream pure modules, and
// emits intents — it never mutates the world directly.
package agent

import (
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/tom"
)

// ── Coping state (design §3: dead goals = the drama engine) ─────────────────────

// CopingState is where the agent sits in the coping cascade. Idle is the normal
// state (a goal is being pursued with a valid plan). The other four are the cascade
// design §3 / glossary §Dynamics.
type CopingState uint8

const (
	Idle      CopingState = iota // pursuing a goal with a valid plan (or no goal raised)
	Rebinding                    // no plan found: expand budget / substitute goal
	Longing                      // unmet goal stored in latent memory
	Latent                       // goal persists below the surface; feeds Resentment drift
	Apathy                       // gave up: goal suppressed, Mood decays toward baseline
)

// ── Intent (the only thing a Tick returns; D12 read→plan→collect→apply) ─────────

// IntentKind classifies what the agent wants the world to do this tick.
type IntentKind uint8

const (
	IntentNone     IntentKind = iota // no action this tick (e.g. deep Apathy / blocked)
	IntentContinue                   // keep progressing the currently-executing durative action
	IntentStart                      // begin a new action (first tick of a planned step)
	IntentSignal                     // emit a Signal toward a target agent (interaction)
)

// Intent is what the agent wants to happen this tick. The world COLLECTS these from
// all agents and APPLIES them serially in fixed AgentID order (D12); the agent never
// mutates shared state itself.
type Intent struct {
	Kind   IntentKind
	Agent  core.AgentID     // the acting agent (world sorts/applies by this, D12)
	Action actions.ActionID // the atomic action to start/continue (empty for IntentSignal/None)
	Target core.ObjectID    // object/agent the action acts on (empty if none)
	Move   core.Vec2        // destination for a movement action (zero-value unless a move)
	Signal *Signal          // non-nil only for IntentSignal (interaction payload)
	Tick   core.Tick        // the tick this intent was produced on (for the why-trace / ordering)
}

// Signal is one interaction the agent emits toward another (glossary §Social: Signal{
// Kind, Intent, Valence, ClaimedValue, Truth, Intensity}). Truth is HIDDEN from the
// receiver — only the emitter and god know it; the receiver weighs the claim by its
// own ToM[emitter].Trust.
// P6: Function field added for SignalVote delegation signals.
// P6: Target field added — the voted holder for Vote signals (empty otherwise).
type Signal struct {
	Kind         SignalKind // assertion | request | threat | offer | vote
	Toward       core.AgentID
	Valence      float64       // signed stance in [-1,1]
	ClaimedValue float64       // the value the emitter CLAIMS
	Truth        float64       // the value the emitter actually believes (HIDDEN; deception = |Claimed-Truth|)
	Intensity    float64       // how forcefully it is pushed, in [0,1]
	Function     core.Function // P6: for Vote signals — the delegated Function (empty otherwise)
	Target       core.AgentID  // P6: for Vote signals — the voted holder agent (empty otherwise)
}

// SignalKind names a signal's pragmatic kind (canonical strings, not hardcoded logic).
type SignalKind string

// ── Outcome feedback ────────────────────────────────────────────────────────────

// OutcomeStatus classifies a resolved action.
type OutcomeStatus uint8

const (
	Succeeded   OutcomeStatus = iota // attempted, outcome at/above expectation
	Failed                           // attempted, outcome BELOW expectation → β overclaim correction (D8)
	Invisible                        // never attempted: a gate blocked it → belief unchanged (self-sealing, D8)
	Interrupted                      // durative action cut short (conflict / higher-priority interrupt)
)

// ActionOutcome is the world's verdict on one applied action. The agent NEVER sees
// Real Stats here — only the resolved result and the per-stat evidence the world
// attributes (D8: outcomes decided by Real Stats; the agent only learns the result,
// then calibrates its SELF-belief from it).
type ActionOutcome struct {
	Action    actions.ActionID
	Status    OutcomeStatus
	Completed bool                       // true iff this was the final tick of the durative action
	StatsUsed []core.StatID              // the stat(s) the action exercised
	Expected  float64                    // the agent's pre-action expected progress, in [0,1]
	Actual    float64                    // the realized progress, in [0,1]
	Effect    map[core.Dimension]float64 // realized need deltas applied
	Evidence  []tom.StatEvidence         // direct-observation evidence to fold into ToM[self] (D8)
}

// ── FunctionSpec (P6 — injected Function→Dimension→Stats mapping, D7/D10) ────────

// FunctionSpec maps a Function id to the goal Dimension it serves and the
// capability stat-set required to provide it. Injected via Config.Functions (D7/D10):
// it replaces the hardcoded goalToFunction mapping — the agent resolves a goal
// Dimension to its Function + capability Stats by scanning this table, never a literal.
type FunctionSpec struct {
	ID    core.Function  // e.g. core.FuncSafety
	Dim   core.Dimension // goal dimension this function covers (e.g. "Safety")
	Stats []core.StatID  // capability stats required to provide this function (passed to BestProviderFor)
}

// ── Latent goal ─────────────────────────────────────────────────────────────────

// LatentGoal is an unmet goal pushed below the conscious threshold (Longing/Latent).
// It carries the dimension that could not be satisfied and how long it has been
// unmet, which drives the Resentment drift.
type LatentGoal struct {
	Dim       core.Dimension
	Since     core.Tick
	Intensity float64 // residual urgency carried below the surface
}

// ── World view (dependency inversion — agent defines, world implements) ──────────

// WorldView is the READ-ONLY per-tick snapshot the agent perceives and plans against.
// It embeds perception.WorldSnapshot for the three senses and adds agent-specific
// queries for the value-map and gossip folding.
type WorldView interface {
	perception.WorldSnapshot // EntitiesInRadius / Tags / IsOpaque

	SoundEvents() []perception.SoundEvent                               // tick-scoped sound events for Hearing
	KnownObjects(self core.AgentID) []KnownObject                       // objects this agent knows, AgentID-stable order
	BeliefOf(self, subject core.AgentID) (tom.Belief, bool)             // another agent's belief, for gossip folding
	HasPendingOffer(receiver core.AgentID) bool                         // true if another agent has sent an unresolved Offer to receiver
	ResentmentTriggers(self core.AgentID) []core.AgentID                // NEW P3: agents who rejected/beat self this tick, AgentID-stable order
	PlaceQuality(placeID core.ObjectID) float64                         // quality of a place ∈ [0,1]; 1 = pristine (no obstruction), 0 = fully blocked
	MemberNeedIntensities() map[core.AgentID]map[core.Dimension]float64 // NEW P5: need intensities for all other agents in the village; caller MUST NOT mutate the returned map; returns nil if world doesn't track this
	AgentIDs() []core.AgentID                                           // NEW P6: all agent IDs in the village (excluding self), sorted (D12)
	IncomingSignals(self core.AgentID) []core.Signal                    // NEW P6: signals addressed to self this tick (for vote/hearsay processing)
}

// KnownObject is one object in the agent's value map (glossary: Known map[ObjectID]Valuation).
// It carries the object's id, position, and per-Dimension supply Effect (D9: supply only —
// NO "future need" field).
type KnownObject struct {
	ID     core.ObjectID
	Pos    core.Vec2
	Kind   core.Tag                   // object-kind id (for action targeting)
	Supply map[core.Dimension]float64 // satisfaction Effect the object provides (D9 supply)
}
