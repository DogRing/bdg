package api

import (
	"context"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ── SSE tests ─────────────────────────────────────────────────────────────────

func TestSSE_HeadersAndFlush(t *testing.T) {
	rds := newFakeRedisReader()
	s := testServer(false, rds)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/sse", nil).WithContext(ctx)
	rec := newFlushRecorder()

	done := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	// WorldFrame is forwarded verbatim: additive ambient fields such as snow_cover
	// must survive the events stream and SSE transport without a whitelist.
	payload := `{"schema_version":1,"tick":42,"seq":0,"agent_id":null,"type":"WorldFrame","payload":{"temperature":-2.5,"raining":true,"snow_cover":0.375}}`
	rds.eventsCh <- StreamEntry{
		ID:     "123456789-0",
		Fields: map[string]string{"payload": payload},
	}

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	// Check all four SSE response headers.
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	if xa := rec.Header().Get("X-Accel-Buffering"); xa != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", xa)
	}
	if conn := rec.Header().Get("Connection"); conn != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", conn)
	}

	body := rec.Body.String()
	wantFrame := "data: " + payload + "\n\n"
	if !strings.Contains(body, wantFrame) {
		t.Errorf("body does not contain expected SSE frame.\nhave: %q\nwant: %q", body, wantFrame)
	}
	if rec.flushes < 1 {
		t.Error("Flush was not called")
	}
}

// serveSSE serves one /sse request (optional Last-Event-ID header) against s,
// optionally feeding live entries, waits settle, cancels, and returns the body.
func serveSSE(t *testing.T, s *Server, path string, lastEventID string, feed func(rds *fakeRedisReader), rds *fakeRedisReader, settle time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", path, nil).WithContext(ctx)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	rec := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	if feed != nil {
		feed(rds)
	}
	time.Sleep(settle)
	cancel()
	<-done
	return rec.Body.String()
}

// Replay strictly after the supplied cursor: retained entries at or before it
// are skipped, later ones are framed with their id: line, and a live entry
// pushed afterwards continues the SAME loop — replay → live with no gap and no
// duplicate of the cursor entry.
func TestSSE_CursorReplayThenLive(t *testing.T) {
	rds := newFakeRedisReader()
	rds.setRetained("0-0",
		StreamEntry{ID: "100-0", Fields: map[string]string{"payload": `{"type":"A"}`}},
		StreamEntry{ID: "101-0", Fields: map[string]string{"payload": `{"type":"B"}`}},
		StreamEntry{ID: "102-0", Fields: map[string]string{"payload": `{"type":"C"}`}},
	)
	s := testServer(false, rds)

	body := serveSSE(t, s, "/sse?cursor=100-0", "", func(rds *fakeRedisReader) {
		time.Sleep(30 * time.Millisecond)
		rds.eventsCh <- StreamEntry{ID: "103-0", Fields: map[string]string{"payload": `{"type":"LIVE"}`}}
	}, rds, 80*time.Millisecond)

	if strings.Contains(body, `{"type":"A"}`) {
		t.Errorf("cursor entry (100-0) replayed — replay must be STRICTLY after the cursor:\n%s", body)
	}
	for _, want := range []string{
		"id: 101-0\ndata: {\"type\":\"B\"}\n\n",
		"id: 102-0\ndata: {\"type\":\"C\"}\n\n",
		"id: 103-0\ndata: {\"type\":\"LIVE\"}\n\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing frame %q in body:\n%s", want, body)
		}
	}
	if strings.Count(body, "id: 101-0") != 1 {
		t.Errorf("duplicate replay of 101-0:\n%s", body)
	}
	if bIdx, lIdx := strings.Index(body, "id: 101-0"), strings.Index(body, "id: 103-0"); bIdx > lIdx {
		t.Errorf("replay did not precede live tail:\n%s", body)
	}
}

// Last-Event-ID (browser reconnect) is honoured like ?cursor and wins over it.
func TestSSE_LastEventIDReconnect(t *testing.T) {
	rds := newFakeRedisReader()
	rds.setRetained("0-0",
		StreamEntry{ID: "200-0", Fields: map[string]string{"payload": `{"type":"OLD"}`}},
		StreamEntry{ID: "201-0", Fields: map[string]string{"payload": `{"type":"NEW"}`}},
	)
	s := testServer(false, rds)

	body := serveSSE(t, s, "/sse?cursor=100-0", "200-0", nil, rds, 50*time.Millisecond)
	if strings.Contains(body, `{"type":"OLD"}`) {
		t.Errorf("Last-Event-ID did not win over ?cursor (200-0 replayed):\n%s", body)
	}
	if !strings.Contains(body, "id: 201-0\ndata: {\"type\":\"NEW\"}\n\n") {
		t.Errorf("entry after Last-Event-ID not replayed:\n%s", body)
	}
}

// A cursor older than the trimmed backlog gets ONE StreamGap control frame
// (no id: line — Last-Event-ID stays untouched) and the handler CLOSES: the
// client must resnapshot instead of receiving a silently-partial history.
func TestSSE_TrimmedCursorSignalsGapAndCloses(t *testing.T) {
	rds := newFakeRedisReader()
	rds.setRetained("150-0", // entries ≤ 150-0 were trimmed away
		StreamEntry{ID: "151-0", Fields: map[string]string{"payload": `{"type":"KEPT"}`}},
	)
	s := testServer(false, rds)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", "/sse?cursor=100-0", nil).WithContext(ctx)
	rec := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done: // handler must close ON ITS OWN after the gap frame
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not close after StreamGap")
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"type":"StreamGap"`) {
		t.Fatalf("missing StreamGap control frame:\n%s", body)
	}
	if strings.Contains(body, "id: ") {
		t.Errorf("StreamGap frame must not carry an id: line:\n%s", body)
	}
	if strings.Contains(body, `{"type":"KEPT"}`) {
		t.Errorf("partial replay served despite the gap:\n%s", body)
	}
}

// A cursor equal to (or newer than) max-deleted-entry-id is NOT a gap — this is
// exactly the post-regen shape: the recreated stream reset its deletion
// metadata, the snapshot cursor references deleted-but-reflected construction
// entries, and only genuinely-new entries flow.
func TestSSE_PostRegenDanglingCursorIsNotAGap(t *testing.T) {
	rds := newFakeRedisReader()
	rds.setRetained("0-0", // recreated stream: nothing ever deleted from it
		StreamEntry{ID: "500-0", Fields: map[string]string{"payload": `{"type":"NEWWORLD"}`}},
	)
	s := testServer(false, rds)

	body := serveSSE(t, s, "/sse?cursor=300-0", "", nil, rds, 50*time.Millisecond)
	if strings.Contains(body, "StreamGap") {
		t.Errorf("false gap on a regen-recreated stream:\n%s", body)
	}
	if !strings.Contains(body, "id: 500-0") {
		t.Errorf("new-world entry not delivered:\n%s", body)
	}
}

// Malformed cursors are ignored (live tail), never an error.
func TestSSE_InvalidCursorFallsBackToLiveTail(t *testing.T) {
	rds := newFakeRedisReader()
	rds.setRetained("0-0",
		StreamEntry{ID: "700-0", Fields: map[string]string{"payload": `{"type":"BACKLOG"}`}},
	)
	s := testServer(false, rds)

	body := serveSSE(t, s, "/sse?cursor=not-a-cursor", "", func(rds *fakeRedisReader) {
		rds.eventsCh <- StreamEntry{ID: "701-0", Fields: map[string]string{"payload": `{"type":"LIVE"}`}}
	}, rds, 50*time.Millisecond)
	if strings.Contains(body, `{"type":"BACKLOG"}`) {
		t.Errorf("invalid cursor triggered a backlog replay:\n%s", body)
	}
	if !strings.Contains(body, `{"type":"LIVE"}`) {
		t.Errorf("live tail not served:\n%s", body)
	}
}

func TestSSE_CleanDisconnect(t *testing.T) {
	rds := newFakeRedisReader()
	s := testServer(false, rds)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/sse", nil).WithContext(ctx)
	rec := newFlushRecorder()

	initialG := runtime.NumGoroutine()

	done := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Handler returned.
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancellation")
	}

	time.Sleep(50 * time.Millisecond)
	finalG := runtime.NumGoroutine()

	// Allow a small delta for test framework goroutines.
	if finalG > initialG+5 {
		t.Errorf("possible goroutine leak: initial=%d, final=%d", initialG, finalG)
	}
}
