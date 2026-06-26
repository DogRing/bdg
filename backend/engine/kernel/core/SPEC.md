# SPEC — `engine/kernel/core`

> Status: `READY`
> Leaf level: `L0`  ·  Owner agent: `implementer`

## Purpose

Declares every primitive type and cross-cutting interface used by the rest of the engine.
Contains **no logic and no mutable state** — only type definitions, interfaces, and
trivial value-type methods (Vec2 arithmetic).
The `EventEmitter` interface lives here so that engine modules can emit events
without importing any platform package (dependency-inversion; `platform/events` supplies
the concrete implementation).

## Public Interface

```go
package core

// ── Identity ────────────────────────────────────────────────────────────────

type AgentID  string
type ObjectID string
type RunID    string

// Tick is the simulation loop counter — the engine's authoritative time unit.
// It is a plain monotone integer advanced by engine/world each tick; it is derived
// from the loop, never from a wall-clock (no time.Now(), D12). Tick is defined here
// (not in worldtime) because nearly every module references it (snapshot tick,
// LastSeen, durative progress); worldtime only *interprets* it.
type Tick int64

// GameMinutes is the human-facing time unit: an absolute count of in-world minutes.
// It is DERIVED from Tick by engine/kernel/worldtime, not stored alongside Tick. The Tick↔
// GameMinutes ratio is a worldtime-owned, content-driven constant
// (content/balance.yaml world.tick_minutes; default 1 Tick = 1 GameMinute — the 12×
// day scale of glossary §"World & time"). core fixes only the TYPES, not the ratio,
// so the scale can change in content without a core edit (D10). All authored durations
// in content/balance.yaml are in GameMinutes.
type GameMinutes int64

// ── Domain primitives ───────────────────────────────────────────────────────

// StatID names a stat dimension. Canonical values are defined in content/stats.yaml.
type StatID string

// Dimension names a need / value axis (glossary §"Values & goals" Dimension:
// Satiety, Hydration, Rest, Safety, Standing, Openness, …). Canonical values are defined
// in content/needs.yaml. A Value{Dimension,Ref,Posture,Setpoint} targets a Dimension, and
// engine/mind/needs aliases it as `type NeedID = core.Dimension`. Defined here (not in needs)
// because both engine/mind/needs and engine/mind/values reference it; like StatID/Tag/Pred it is an
// uninterpreted string at this layer — the engine/mind/needs Registry gives it semantics (D10).
type Dimension string

// Tag annotates an Action (e.g. "uses:Strength", "violent", "noise:high").
// Cost and gate evaluation reads tags; no bespoke per-action functions (D4).
type Tag string

// Pred is a world-state predicate key used by the GOAP planner (e.g. "hasFood").
type Pred string

// ── Spatial ─────────────────────────────────────────────────────────────────

// Vec2 is a free 2-D coordinate (D11: not tiled, unbounded float64).
type Vec2 struct{ X, Y float64 }

func (v Vec2) Add(o Vec2) Vec2            { return Vec2{v.X + o.X, v.Y + o.Y} }
func (v Vec2) Sub(o Vec2) Vec2            { return Vec2{v.X - o.X, v.Y - o.Y} }
func (v Vec2) Scale(f float64) Vec2       { return Vec2{v.X * f, v.Y * f} }
func (v Vec2) DistSq(o Vec2) float64     { dx, dy := v.X-o.X, v.Y-o.Y; return dx*dx + dy*dy }
func (v Vec2) Distance(o Vec2) float64   // math.Sqrt(v.DistSq(o))

// ── Referent & Value ─────────────────────────────────────────────────────────

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
    Setpoint  float64 // the threshold/aspiration level for this dimension-and-referent (in [0,1])
}

// ── Events interface (dependency inversion) ──────────────────────────────────
//
// The engine emits observability events through EventEmitter.
// engine/* modules only import this interface.
// platform/events provides the concrete implementation (SSE + why-trace).

// Event carries one engine observation (data-contracts §4).
type Event struct {
    SchemaVersion int
    Tick          Tick
    Seq           int64   // monotonically increasing within a run
    AgentID       AgentID // empty when not agent-scoped
    Type          string  // e.g. "GoalSelected", "ActionStarted"
    Payload       any     // type-specific struct; serialized by platform/events
}

// EventEmitter is accepted by engine/world (and any future module that emits).
// Pass NoopEmitter in unit tests or headless runs.
type EventEmitter interface {
    Emit(e Event)
}

// NoopEmitter satisfies EventEmitter without side effects.
type NoopEmitter struct{}

func (NoopEmitter) Emit(Event) {}

// ── Signal (P2: trade / social protocol) ────────────────────────────────────

// SignalKind classifies the social act conveyed by a Signal.
// Constant names are prefixed Signal to avoid collision with common English words.
type SignalKind uint8

const (
    SignalOffer    SignalKind = iota // propose a trade or alliance
    SignalAccept                     // accept a previously received offer
    SignalReject                     // decline a previously received offer
    SignalGreet                      // social acknowledgement, no commitment
    SignalThreaten                   // assert intent to harm unless compliance
    SignalVote                       // P6: publicly delegate a Function to `Toward` (emergent politics)
)

// Signal is a structured social communication passed between agents.
// It carries NO natural language — content is structured so observer agents
// parse it via ToM without an LLM (design rule: no LLM at runtime).
//
// Truth is the sender's ACTUAL veracity fraction. It is NEVER directly
// observable to the receiver; the receiver must infer it via ToM[sender].Trust
// (analogous to D6: truth is a belief distribution, not a stored scalar).
// The engine attaches Truth to the Signal only for the event stream / why-trace.
type Signal struct {
    Kind         SignalKind // classification of the social act
    Intent       Pred       // predicate being proposed/demanded (e.g. "has_food")
    Valence      float64    // affective stance: -1 (hostile) … +1 (friendly)
    ClaimedValue float64    // asserted deal value in Dimension units
    Truth        float64    // sender's actual veracity [0, 1]; NOT visible to receiver
    Intensity    float64    // urgency / emotional force [0, 1]
    Function     Function   // P6: for SignalVote — the delegated Function (empty otherwise)
}

// Function names a service an agent relies on another to provide (glossary: Reliance edge):
// e.g. "safety", "judgment", "knowledge". D2/D7 — a content/glossary id, never a hardcoded enum
// or role type; an emergent role is a CLUSTER of RelyOn edges over a Function, not a Function.
// Mirrors tom.Function (this is the canonical L0 declaration the other packages alias).
type Function string
```

## Dependencies

None. This is an L0 leaf with no engine or platform imports.

## Owned Data

Only value types and interfaces. No heap-allocated state owned by this package.

## Invariants

- No imports outside the Go standard library (`math` for `Distance` is the only stdlib use).
- All exported types are safe to copy by value (no hidden pointers or mutexes).
- `Vec2` coordinates are unbounded `float64` — no tiling, no grid clamping (D11).
- `Tick` and `GameMinutes` are plain `int64` time types. `core` fixes only the types; the
  conversion ratio between them lives in `engine/kernel/worldtime` (content-driven, D10). No
  wall-clock anywhere — both are loop-derived integers (D12).
- `StatID`, `Dimension`, `Tag`, and `Pred` are uninterpreted strings at this layer; registries
  in `engine/mind/stats`, `engine/mind/needs`, `engine/mind/actions`, and `engine/mind/gates` give them semantics.
- `NoopEmitter` must compile as a valid `EventEmitter` at all times (enforced by
  `var _ EventEmitter = NoopEmitter{}`).

## Acceptance Criteria (testable)

- [ ] `var _ EventEmitter = NoopEmitter{}` compiles (interface satisfaction, compile-time).
- [ ] `Vec2.Distance` table test: (0,0)→(3,4)=5, (1,1)→(1,1)=0, negative coordinates correct.
- [ ] `Vec2.Add/Sub/Scale` table tests cover identity, zero, and negative cases.
- [ ] `Vec2.DistSq` equals `Distance²` for at least 10 random pairs (property test).
- [ ] `Tick` and `GameMinutes` are distinct `int64` types (a value of one is not assignable to
  the other without a conversion — compile-time guard; conversion ownership is worldtime's).
- [ ] `StatID`, `Dimension`, `Tag`, `Pred` are distinct named string types (compile-time guard;
  one is not assignable to another without a conversion — prevents id-kind drift).
- [ ] All zero values are valid and predictable (no panics on zero `AgentID`, `Tick`, etc.).
- [ ] Package has no side-effects on import (no `init()` with global state).
- [ ] `SignalKind` constants have iota values 0–4 matching Offer/Accept/Reject/Greet/Threaten order.
- [ ] `Signal` zero value is valid (Kind=SignalOffer, all floats=0, Intent="").
- [ ] `Signal` fields `Valence`, `Truth`, `Intensity` accept the full float64 range without clamping at this layer (clamping is the caller's responsibility).

## Out of Scope

- Stat values and `StatRegistry` → `engine/mind/stats`
- Need / value dimension definitions and their rates → `engine/mind/needs` (which aliases
  `NeedID = core.Dimension`) + `content/needs.yaml`
- Spatial indexing and radius queries → `engine/space/spatial`
- Tick ↔ GameMinutes / hour / day / season conversion (and the ratio) → `engine/kernel/worldtime`
- Event stream storage, SSE, why-trace → `platform/events`
- Any GOAP or planning logic → `engine/mind/planner`

## Notes

- `Distance` calls `math.Sqrt`; callers that only need ordering should prefer `DistSq`
  to avoid the sqrt.
- The `Payload any` field on `Event` is intentionally untyped here to keep `core`
  free of higher-level types. `platform/events` type-switches on `Type` to serialize.
- `SchemaVersion` on `Event` mirrors the global contract (data-contracts §0).
  Bump when the Event struct shape changes.
- `Tick` vs `GameMinutes`: `Tick` is the loop counter and is what the snapshot serializes
  (data-contracts §1 `tick`). `GameMinutes` is always re-derivable from `Tick` via the
  worldtime ratio, so it is never serialized on its own.
- `Dimension` is the need/value axis; `StatID` is the agent-attribute axis. They are separate
  named types so a `StatID` is never silently used where a `Dimension` is expected (glossary
  keeps these vocabularies distinct).
