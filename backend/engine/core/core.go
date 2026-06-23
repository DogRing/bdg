package core

import "math"

// ── Identity ─────────────────────────────────────────────────────────────────

type AgentID string
type ObjectID string
type RunID string

// Tick is the engine's time unit. 1 Tick = 1 game-minute (default scale).
// Real-time ↔ game-time conversion (12×) is in engine/worldtime.
type Tick int64

// GameMinutes is the human-facing time unit: an absolute count of in-world minutes.
// It is DERIVED from Tick by engine/worldtime, not stored alongside Tick. The Tick↔
// GameMinutes ratio is a worldtime-owned, content-driven constant
// (content/balance.yaml world.tick_minutes; default 1 Tick = 1 GameMinute). core fixes
// only the TYPES, not the ratio, so the scale can change in content without a core
// edit (D10). All authored durations in content/balance.yaml are in GameMinutes.
type GameMinutes int64

// ── Domain primitives ────────────────────────────────────────────────────────

// StatID names a stat dimension. Canonical values are defined in content/stats.yaml.
type StatID string

// Tag annotates an Action (e.g. "uses:Strength", "violent", "noise:high").
// Cost and gate evaluation reads tags; no bespoke per-action functions (D4).
type Tag string

// Dimension names a need / value axis (glossary §"Values & goals" Dimension:
// Satiety, Hydration, Rest, Safety, Standing, Openness, …). Canonical values are defined
// in content/needs.yaml. A Value{Dimension,Ref,Posture,Setpoint} targets a Dimension, and
// engine/needs aliases it as `type NeedID = core.Dimension`. Defined here (not in needs)
// because both engine/needs and engine/values reference it; like StatID/Tag/Pred it is an
// uninterpreted string at this layer — the engine/needs Registry gives it semantics (D10).
type Dimension string

// Pred is a world-state predicate key used by the GOAP planner (e.g. "hasFood").
type Pred string

// ── Spatial ──────────────────────────────────────────────────────────────────

// Vec2 is a free 2-D coordinate (D11: not tiled, unbounded float64).
type Vec2 struct{ X, Y float64 }

func (v Vec2) Add(o Vec2) Vec2          { return Vec2{v.X + o.X, v.Y + o.Y} }
func (v Vec2) Sub(o Vec2) Vec2          { return Vec2{v.X - o.X, v.Y - o.Y} }
func (v Vec2) Scale(f float64) Vec2     { return Vec2{v.X * f, v.Y * f} }
func (v Vec2) DistSq(o Vec2) float64   { dx, dy := v.X-o.X, v.Y-o.Y; return dx*dx + dy*dy }
func (v Vec2) Distance(o Vec2) float64 { return math.Sqrt(v.DistSq(o)) }

// ── Referent ─────────────────────────────────────────────────────────────────

// ReferentKind classifies the target of a Value evaluation.
type ReferentKind uint8

const (
	Self       ReferentKind = iota // the agent itself
	Other                          // another agent or entity (by ID)
	Place                          // a spatial location
	Collective                     // an abstract group
)

// Referent is the pointer a Value reads during evaluation (glossary: Referent{Kind,ID}).
type Referent struct {
	Kind ReferentKind
	ID   ObjectID // empty when Kind == Self
}

// Posture names the direction a Value evaluates: whether the agent wants to
// Maximize, MaintainAbove, or PreventBelow the setpoint (glossary §Values & goals).
type Posture uint8

const (
	Maximize       Posture = iota // as much as possible
	MaintainAbove                 // keep at or above the setpoint
	PreventBelow                  // keep from falling below the setpoint
)

// Value is a root value: a goal direction expressed as (Dimension, Referent, Posture, Setpoint).
// An agent may carry many Values — one per referent-and-dimension pair it cares about.
// The planner selects the highest-Salience value to pursue; how to pursue it is the planner's job (D5).
type Value struct {
	Dimension Dimension
	Ref       Referent
	Posture   Posture
	Setpoint  float64 // the threshold/aspiration level for this dimension-and-referent (∈ [0,1])
}

// ── Events interface (dependency inversion) ───────────────────────────────────
//
// The engine emits observability events through EventEmitter.
// engine/* modules only import this interface.
// platform/events provides the concrete implementation (SSE + why-trace).

// Event carries one engine observation (data-contracts §4).
// JSON field names are snake_case to match the frontend SimEvent contract.
type Event struct {
	SchemaVersion int     `json:"schema_version"`
	Tick          Tick    `json:"tick"`
	Seq           int64   `json:"seq"`
	AgentID       AgentID `json:"agent_id"`
	Type          string  `json:"type"`
	Payload       any     `json:"payload"`
}

// EventEmitter is accepted by engine/world (and any future module that emits).
// Pass NoopEmitter in unit tests or headless runs.
type EventEmitter interface {
	Emit(e Event)
}

// NoopEmitter satisfies EventEmitter without side effects.
type NoopEmitter struct{}

func (NoopEmitter) Emit(Event) {}

// Compile-time interface satisfaction check.
var _ EventEmitter = NoopEmitter{}

// ── Signal (P2: trade / social protocol) ─────────────────────────────────────

// SignalKind classifies the social act conveyed by a Signal.
type SignalKind uint8

const (
	SignalOffer    SignalKind = iota // propose a trade or alliance
	SignalAccept                     // accept a previously received offer
	SignalReject                     // decline a previously received offer
	SignalGreet                      // social acknowledgement, no commitment
	SignalThreaten                   // assert intent to harm unless compliance
	SignalVote                       // collective judgment vote (P6: delegate a Function to the target)
)

// Signal is a structured social communication passed between agents.
// No natural language — content is structured for ToM-based parsing (design rule).
//
// Truth is the sender's actual veracity fraction. It is NEVER directly observable
// to the receiver; the receiver infers it via ToM[sender].Trust. The engine
// attaches Truth to Signal only for the event stream / why-trace.
//
// P6: Source and Target fields added for SignalVote — Source is the voter,
// Target is the voted holder (the agent being delegated to). Function is the
// delegated Function.
type Signal struct {
	Kind         SignalKind // classification of the social act
	Intent       Pred       // predicate being proposed/demanded (e.g. "has_food")
	Valence      float64    // affective stance: -1 (hostile) … +1 (friendly)
	ClaimedValue float64    // asserted deal value in Dimension units
	Truth        float64    // sender's actual veracity [0, 1]; NOT visible to receiver
	Intensity    float64    // urgency / emotional force [0, 1]
	Function     Function   // P6: for SignalVote — the delegated Function (empty otherwise)
	Source       AgentID    // P6: for SignalVote — the voting agent (empty otherwise)
	Target       AgentID    // P6: for SignalVote — the voted holder (empty otherwise)
}

// Function names a service an agent relies on another to provide (glossary: Reliance edge).
// D7/D2 — content/glossary id, never a hardcoded enum or role type; an emergent role is a
// CLUSTER of RelyOn edges over a Function, not a Function itself (the reliance-cluster signal,
// never the name stored anywhere).
type Function string

const (
	FuncSafety    Function = "Safety"    // protection from harm
	FuncJudgment  Function = "Judgment"  // arbitration / dispute resolution
	FuncKnowledge Function = "Knowledge" // information / expertise
)
