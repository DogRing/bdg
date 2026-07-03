package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/platform/persist"
)

// ── Fake RedisReader ──────────────────────────────────────────────────────────

type fakeRedisReader struct {
	mu           sync.Mutex
	pingErr      error
	snapshotBlob []byte
	blobs        map[string][]byte // key-addressed GET blobs (e.g. sim:{run}:terrain)
	agentHashes  map[string]map[string]string
	eventsCh     chan StreamEntry
}

func newFakeRedisReader() *fakeRedisReader {
	return &fakeRedisReader{
		blobs:       make(map[string][]byte),
		agentHashes: make(map[string]map[string]string),
		eventsCh:    make(chan StreamEntry, 100),
	}
}

func (f *fakeRedisReader) Ping(_ context.Context) error { return f.pingErr }
func (f *fakeRedisReader) Get(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.blobs[key]; ok {
		return b, nil
	}
	return f.snapshotBlob, nil // legacy fallback (snapshot-key reads)
}

func (f *fakeRedisReader) HGetAll(_ context.Context, key string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.agentHashes[key]
	if !ok {
		return nil, nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out, nil
}

func (f *fakeRedisReader) XRead(ctx context.Context, _, _ string, _ time.Duration) ([]StreamEntry, string, error) {
	select {
	case entry, ok := <-f.eventsCh:
		if !ok {
			return nil, "", ctx.Err()
		}
		return []StreamEntry{entry}, entry.ID, nil
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}

func (f *fakeRedisReader) setAgentHash(key string, fields map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentHashes[key] = fields
}

func (f *fakeRedisReader) setSnapshot(blob []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshotBlob = blob
}

// ── flushRecorder: httptest.ResponseRecorder + Flush counter ──────────────────

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (f *flushRecorder) Flush() { f.flushes++ }

// ── snapshotLiveStore: test LiveStore with controlled ReadSnapshot ────────────

type snapshotLiveStore struct {
	persist.LiveStore
	blob    []byte
	errRead error
}

func (s *snapshotLiveStore) ReadSnapshot(_ context.Context, _ core.RunID) ([]byte, error) {
	if s.errRead != nil {
		return nil, s.errRead
	}
	if s.blob == nil {
		return nil, fmt.Errorf("snapshot not found")
	}
	return s.blob, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func testServer(godMode bool, rds *fakeRedisReader) *Server {
	cfg := Config{
		Addr:    ":0",
		RunID:   core.RunID("test-run"),
		GodMode: godMode,
	}
	live := &persist.FakeRedis{}
	return New(cfg, live, rds, nil)
}

func setupAgentHash(rds *fakeRedisReader, id string, fields map[string]string) {
	if fields == nil {
		fields = map[string]string{
			"id":     id,
			"pos":    `{"x":10,"y":20}`,
			"goal":   "explore",
			"action": "idle",
			"mood":   "0.75",
		}
	}
	keyer := persist.Keyer{Run: "test-run"}
	rds.setAgentHash(keyer.Agent(core.AgentID(id)), fields)
}

func buildSnapshotBlob(_ *testing.T, agentID string, realStats map[string]any) []byte {
	doc := map[string]any{
		"schema_version": 1,
		"run_id":         "test-run",
		"tick":           42,
		"world": map[string]any{
			"Tick":     42,
			"RNGState": map[string]any{"Data": "dGVzdA=="},
			"Agents": []any{
				map[string]any{
					"id":         agentID,
					"pos":        map[string]any{"x": 10.0, "y": 20.0},
					"real_stats": realStats,
				},
			},
		},
	}
	blob, _ := json.Marshal(doc)
	return blob
}
