package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/platform/events"
)

// ── Stub RedisClient ─────────────────────────────────────────────────────────

type xAddCall struct {
	stream string
	values map[string]string
}

type stubRedis struct {
	mu    sync.Mutex
	calls []xAddCall
	err   error // returned for every XAdd call if non-nil
}

func (s *stubRedis) XAdd(_ context.Context, stream string, values map[string]string) error {
	s.mu.Lock()
	s.calls = append(s.calls, xAddCall{stream: stream, values: values})
	s.mu.Unlock()
	return s.err
}

func (s *stubRedis) recorded() []xAddCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]xAddCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// parsePayload unmarshals the "payload" string from an XAdd call into a generic map.
func parsePayload(t *testing.T, call xAddCall) map[string]any {
	t.Helper()
	raw, ok := call.values["payload"]
	if !ok {
		t.Fatal("XAdd values missing 'payload' key")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return m
}

// makeEmitter creates an Emitter with the given stub and runID "run1".
func makeEmitter(t *testing.T, stub *stubRedis) *events.Emitter {
	t.Helper()
	em, err := events.New(context.Background(), stub, core.RunID("run1"))
	if err != nil {
		t.Fatalf("events.New: %v", err)
	}
	return em
}

func simpleEvent(typ string) core.Event {
	return core.Event{
		SchemaVersion: 1,
		Tick:          core.Tick(5),
		AgentID:       core.AgentID("agent-1"),
		Type:          typ,
		Payload:       map[string]any{"hunger": 0.5},
	}
}

// ── Tests ────────────────────────────────────────────────────────────────────

// AC1: Emit writes exactly one XAdd call on the correct stream key.
func TestEmitWritesXAddOnce(t *testing.T) {
	stub := &stubRedis{}
	em := makeEmitter(t, stub)

	em.Emit(simpleEvent(events.TypeGoalSelected))

	calls := stub.recorded()
	if len(calls) != 1 {
		t.Fatalf("want 1 XAdd call, got %d", len(calls))
	}
	if calls[0].stream != "sim:run1:events" {
		t.Errorf("want stream 'sim:run1:events', got %q", calls[0].stream)
	}
}

// AC2: Three consecutive Emit calls produce seq 0, 1, 2.
func TestSeqIsMonotone(t *testing.T) {
	stub := &stubRedis{}
	em := makeEmitter(t, stub)

	for i := 0; i < 3; i++ {
		em.Emit(simpleEvent(events.TypeActionStarted))
	}

	calls := stub.recorded()
	if len(calls) != 3 {
		t.Fatalf("want 3 XAdd calls, got %d", len(calls))
	}

	for i, call := range calls {
		m := parsePayload(t, call)
		// JSON numbers unmarshal to float64 by default.
		seqRaw, ok := m["Seq"]
		if !ok {
			t.Fatalf("call %d: payload missing 'Seq' field", i)
		}
		seq, ok := seqRaw.(float64)
		if !ok {
			t.Fatalf("call %d: Seq field not a number, got %T", i, seqRaw)
		}
		if int(seq) != i {
			t.Errorf("call %d: want Seq=%d, got %v", i, i, seq)
		}
	}
}

// AC3: real_stats is stripped from the payload before XAdd.
func TestRealStatsStripped(t *testing.T) {
	stub := &stubRedis{}
	em := makeEmitter(t, stub)

	ev := core.Event{
		SchemaVersion: 1,
		Tick:          core.Tick(1),
		AgentID:       core.AgentID("agent-1"),
		Type:          events.TypePerceived,
		Payload: map[string]any{
			"hunger":     0.5,
			"real_stats": map[string]any{"STR": 90},
		},
	}
	em.Emit(ev)

	calls := stub.recorded()
	if len(calls) != 1 {
		t.Fatalf("want 1 XAdd call, got %d", len(calls))
	}

	m := parsePayload(t, calls[0])

	// Payload is nested in the envelope under "Payload".
	payloadRaw, ok := m["Payload"]
	if !ok {
		t.Fatal("envelope missing 'Payload' field")
	}
	payload, ok := payloadRaw.(map[string]any)
	if !ok {
		t.Fatalf("Payload not a map, got %T", payloadRaw)
	}

	if _, found := payload["real_stats"]; found {
		t.Error("real_stats should have been stripped but is present")
	}
	if _, found := payload["hunger"]; !found {
		t.Error("hunger should be present but is missing")
	}
}

// AC4: XAdd values["payload"] JSON round-trips back to the original event fields
// (minus real_stats, plus seq stamped).
func TestPayloadRoundTrips(t *testing.T) {
	stub := &stubRedis{}
	em := makeEmitter(t, stub)

	ev := core.Event{
		SchemaVersion: 1,
		Tick:          core.Tick(42),
		AgentID:       core.AgentID("agent-7"),
		Type:          events.TypeActionDone,
		Payload:       map[string]any{"action": "Gather", "success": true},
	}
	em.Emit(ev)

	calls := stub.recorded()
	if len(calls) != 1 {
		t.Fatalf("want 1 XAdd call, got %d", len(calls))
	}

	m := parsePayload(t, calls[0])

	// Check top-level event fields.
	if got := m["Type"]; got != events.TypeActionDone {
		t.Errorf("Type: want %q, got %v", events.TypeActionDone, got)
	}
	if got, ok := m["Tick"].(float64); !ok || int(got) != 42 {
		t.Errorf("Tick: want 42, got %v", m["Tick"])
	}
	if got := m["AgentID"]; got != "agent-7" {
		t.Errorf("AgentID: want 'agent-7', got %v", got)
	}
	// Seq should be 0 (first call).
	if got, ok := m["Seq"].(float64); !ok || int(got) != 0 {
		t.Errorf("Seq: want 0, got %v", m["Seq"])
	}
	// Payload sub-fields should be intact.
	payloadRaw, ok := m["Payload"].(map[string]any)
	if !ok {
		t.Fatalf("Payload not a map, got %T", m["Payload"])
	}
	if payloadRaw["action"] != "Gather" {
		t.Errorf("payload.action: want 'Gather', got %v", payloadRaw["action"])
	}
	if payloadRaw["success"] != true {
		t.Errorf("payload.success: want true, got %v", payloadRaw["success"])
	}
}

// AC5: EmitErr returns an error when the stub returns one.
func TestEmitErrReturnsError(t *testing.T) {
	stub := &stubRedis{err: errors.New("redis unavailable")}
	em := makeEmitter(t, stub)

	err := em.EmitErr(simpleEvent(events.TypeTickDone))
	if err == nil {
		t.Fatal("want error from EmitErr, got nil")
	}
}

// AC6: Err() records the first transport error observed by Emit.
func TestErrRecordsFirstError(t *testing.T) {
	stub := &stubRedis{err: errors.New("connection refused")}
	em := makeEmitter(t, stub)

	// Before any Emit, Err() should be nil.
	if em.Err() != nil {
		t.Fatal("want nil Err before any Emit, got non-nil")
	}

	em.Emit(simpleEvent(events.TypeSnapshotReady))

	if em.Err() == nil {
		t.Fatal("want non-nil Err after failed Emit, got nil")
	}
}

// AC6b: Err() returns nil when no errors occur.
func TestErrNilOnSuccess(t *testing.T) {
	stub := &stubRedis{}
	em := makeEmitter(t, stub)

	em.Emit(simpleEvent(events.TypeTickDone))

	if em.Err() != nil {
		t.Errorf("want nil Err on success, got %v", em.Err())
	}
}

// AC6c: Err() returns only the FIRST error — subsequent errors do not overwrite it.
func TestErrRecordsOnlyFirstError(t *testing.T) {
	first := errors.New("first error")
	stub := &stubRedis{err: first}
	em := makeEmitter(t, stub)

	em.Emit(simpleEvent(events.TypeGoalSelected))
	// Change the error to a different one.
	stub.mu.Lock()
	stub.err = errors.New("second error")
	stub.mu.Unlock()
	em.Emit(simpleEvent(events.TypePlanBuilt))

	if !errors.Is(em.Err(), first) {
		t.Errorf("want first error to be recorded (via errors.Is), got %v", em.Err())
	}
}

// New returns an error when client is nil.
func TestNewNilClientError(t *testing.T) {
	_, err := events.New(context.Background(), nil, core.RunID("run1"))
	if err == nil {
		t.Fatal("want error when client is nil, got nil")
	}
}

// All 12 type constants are defined and non-empty.
func TestTypeConstants(t *testing.T) {
	constants := []string{
		events.TypePerceived,
		events.TypeGoalSelected,
		events.TypePlanBuilt,
		events.TypeActionStarted,
		events.TypeActionDone,
		events.TypeInteracted,
		events.TypeBeliefUpdated,
		events.TypeReputationGossip,
		events.TypeCopingEntered,
		events.TypeRoleEmerged,
		events.TypeTickDone,
		events.TypeSnapshotReady,
	}
	for _, c := range constants {
		if c == "" {
			t.Errorf("event type constant is empty")
		}
	}
	if len(constants) != 12 {
		t.Errorf("want 12 type constants, got %d", len(constants))
	}
}

// Emitter satisfies core.EventEmitter (compile-time check via assignment).
var _ core.EventEmitter = (*events.Emitter)(nil)
