// Package events implements core.EventEmitter — the engine's sole IO escape hatch
// for observability. It serializes each core.Event to JSON and appends it to the
// run's Redis STREAM via XADD. It contains no simulation logic.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── Event type constants ──────────────────────────────────────────────────────
// Plain string constants — no iota, no bespoke type — so content additions never
// require a code change (D10 spirit). Callers import this package for these
// constants and never type raw string literals.

const (
	TypePerceived        = "Perceived"
	TypeGoalSelected     = "GoalSelected"
	TypePlanBuilt        = "PlanBuilt"
	TypeActionStarted    = "ActionStarted"
	TypeActionDone       = "ActionDone"
	TypeInteracted       = "Interacted"
	TypeBeliefUpdated    = "BeliefUpdated"
	TypeReputationGossip = "ReputationGossip"
	TypeCopingEntered    = "CopingEntered"
	TypeRoleEmerged      = "RoleEmerged"
	TypeTickDone         = "TickDone"
	TypeWorldFrame       = "WorldFrame"
	TypeSnapshotReady    = "SnapshotReady"
)

// RedisClient is the minimal interface Emitter needs from a Redis client.
// Using an interface keeps the package testable without a live Redis connection.
// The concrete implementation is injected by the run-driver.
type RedisClient interface {
	XAdd(ctx context.Context, stream string, values map[string]string) error
}

// Emitter implements core.EventEmitter and writes to a Redis STREAM.
// It is created once per run and injected into the engine (dependency inversion;
// the engine imports core, not platform).
type Emitter struct {
	ctx    context.Context
	client RedisClient
	stream string // "sim:{runID}:events", composed once on construction

	seq     int64  // monotone counter; incremented atomically per Emit
	errOnce sync.Once
	firstErr error
}

// Compile-time interface satisfaction check.
var _ core.EventEmitter = (*Emitter)(nil)

// New creates an Emitter for the given run.
// client is a Redis client satisfying RedisClient (injected via interface).
// ctx scopes the run's XADD calls.
// seq starts at 0; the first Emit call stamps seq 0 and increments to 1.
func New(ctx context.Context, client RedisClient, runID core.RunID) (*Emitter, error) {
	if client == nil {
		return nil, fmt.Errorf("events.New: client must not be nil")
	}
	return &Emitter{
		ctx:    ctx,
		client: client,
		stream: "sim:" + string(runID) + ":events",
		seq:    0,
	}, nil
}

// Emit satisfies core.EventEmitter. It serializes the event to JSON, stamps the
// next monotone seq, strips any real_stats field from the payload, and calls
// XADD sim:{runID}:events * payload <json>.
//
// Emit is synchronous: it blocks until XADD returns. A transport error is recorded
// on the Emitter (retrievable via Err) and does NOT panic.
// Emit NEVER modifies the event payload beyond stamping seq and stripping real_stats.
func (e *Emitter) Emit(ev core.Event) {
	if err := e.EmitErr(ev); err != nil {
		e.errOnce.Do(func() {
			e.firstErr = err
		})
	}
}

// EmitErr is the error-returning form used by the run-driver and non-engine callers
// (platform/api, cmd/run) that want the failure inline.
// Emit routes through this and stores the result.
func (e *Emitter) EmitErr(ev core.Event) error {
	// Atomically claim the next seq value (starting at 0 for the first call).
	seq := atomic.AddInt64(&e.seq, 1) - 1

	// Stamp seq on the event. We do NOT modify the caller's ev.Seq — we stamp
	// the serialized form only. We set ev.Seq here before marshaling so that
	// the JSON carries the correct value.
	ev.Seq = seq

	// Marshal the full event to JSON.
	raw, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("events.EmitErr: marshal event: %w", err)
	}

	// Strip real_stats from the Payload field if present.
	// Strategy: unmarshal into a map, delete the key, re-marshal.
	// This only touches ev.Payload when it round-trips as a map with "real_stats".
	stripped, err := stripRealStats(raw)
	if err != nil {
		return fmt.Errorf("events.EmitErr: strip real_stats: %w", err)
	}

	values := map[string]string{
		"payload": string(stripped),
	}

	if err := e.client.XAdd(e.ctx, e.stream, values); err != nil {
		return fmt.Errorf("events.EmitErr: XAdd: %w", err)
	}
	return nil
}

// Err returns the first transport error observed by Emit since construction (nil if healthy).
// The run-driver checks this after a tick batch to decide whether to abort the run.
func (e *Emitter) Err() error {
	return e.firstErr
}

// stripRealStats removes the "real_stats" key from the top-level Payload object
// in the serialized event JSON, if present.
//
// The full serialized event is passed in (not just the payload) because the
// real_stats key may appear inside the Payload value. We operate on the full
// envelope by unmarshaling into a generic map, then operating on the Payload
// sub-object.
func stripRealStats(raw []byte) ([]byte, error) {
	// Unmarshal the outer event envelope.
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}

	// The payload field may be nil or may not be a map — handle gracefully.
	if payload, ok := envelope["payload"]; ok && payload != nil {
		if payloadMap, ok := payload.(map[string]any); ok {
			delete(payloadMap, "real_stats")
			envelope["payload"] = payloadMap
		}
	}

	return json.Marshal(envelope)
}
