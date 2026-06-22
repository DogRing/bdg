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

	payload := `{"schema_version":1,"tick":42,"seq":0,"agent_id":"farmer_1","type":"ActionDone","payload":{"action":"idle"}}`
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
