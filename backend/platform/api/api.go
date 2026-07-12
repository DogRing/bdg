// Package api provides the read-only HTTP boundary for the simulation:
// liveness/readiness probes, SSE event stream, snapshot/agent query endpoints.
// It holds no simulation state, never advances the tick, and enforces the god-view
// boundary (D8): real_stats appears in agent responses only when both startup GodMode
// and per-request ?god=true are set.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/platform/persist"
)

// Server wires all HTTP handlers onto one http.ServeMux and exposes ListenAndServe.
// It holds no simulation state; it reads Redis through the injected store and a minimal
// read client (RedisReader) and serialises the result. All routes are registered in New.
type Server struct {
	live    persist.LiveStore // ReadSnapshot + key/shape contracts
	rds     RedisReader       // PING, HGETALL agent hash, XREAD events, GET snapshot
	gv      GodViewStore      // QueryEvents — the /api/god/why event source (Postgres); may be nil
	keyer   persist.Keyer     // sim:{run}:* key strings (never hand-formatted)
	runID   core.RunID        // which sim run to tail/query
	godMode bool              // startup flag; gates real_stats on /api/agents/{id}?god=true AND all /api/god/*
	restart func()            // POST /api/restart signal to the sim writer; nil ⇒ route 503s
	regen   func(seed int64)  // POST /api/regen signal (new-seed rebuild); nil ⇒ route 503s
	mux     *http.ServeMux
	addr    string
}

// GodViewStore is the read-side of Postgres for /api/god/why queries.
// It MAY be nil when cfg.GodMode is false (all /api/god/* routes 403 before touching it).
type GodViewStore interface {
	QueryEvents(ctx context.Context, run core.RunID, agentID core.AgentID, tick core.Tick) ([]core.Event, error)
}

// Config holds the API-layer knobs injected at construction.
type Config struct {
	Addr    string     // e.g. ":8080" from HTTP_ADDR env
	RunID   core.RunID // which sim run to tail/query
	GodMode bool       // startup-only: enables real_stats on /api/agents/{id}?god=true
	Restart func()     // POST /api/restart signal sink (non-blocking; the sim writer rebuilds
	// the world on its own tick goroutine). nil ⇒ the route responds 503.
	Regen func(seed int64) // POST /api/regen signal sink (non-blocking; new-seed rebuild — seed 0
	// ⇒ the writer draws one). nil (e.g. scenario mode) ⇒ the route responds 503.
}

// RedisReader is the minimal Redis surface api needs for the read path.
// All key strings come from persist.Keyer; api formats none.
type RedisReader interface {
	Ping(ctx context.Context) error
	Get(ctx context.Context, key string) ([]byte, error)
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	XRead(ctx context.Context, key, lastID string, block time.Duration) (entries []StreamEntry, newLastID string, err error)
	// StreamMaxDeletedID returns the stream's max-deleted-entry-id (XINFO
	// STREAM) — the highest entry ID ever trimmed/deleted; "0-0" when nothing
	// was deleted OR the key does not exist (a regen-recreated stream restarts
	// this metadata). The SSE handler compares a client replay cursor against
	// it to detect a trimmed (gapped) backlog.
	StreamMaxDeletedID(ctx context.Context, key string) (string, error)
}

// StreamEntry is one Redis STREAM entry the SSE tail forwards.
type StreamEntry struct {
	ID     string
	Fields map[string]string
}

// New wires the mux (all routes registered here) and returns a ready Server.
// It does not bind a socket — ListenAndServe does.
func New(cfg Config, live persist.LiveStore, rds RedisReader, gv GodViewStore) *Server {
	s := &Server{
		live:    live,
		rds:     rds,
		gv:      gv,
		keyer:   persist.Keyer{Run: cfg.RunID},
		runID:   cfg.RunID,
		godMode: cfg.GodMode,
		restart: cfg.Restart,
		regen:   cfg.Regen,
		mux:     http.NewServeMux(),
		addr:    cfg.Addr,
	}
	// Register routes in New (no global state, D12).
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.mux.HandleFunc("GET /sse", s.handleSSE)
	s.mux.HandleFunc("GET /api/meta", s.handleMeta)
	s.mux.HandleFunc("GET /api/snapshot", s.handleSnapshot)
	s.mux.HandleFunc("GET /api/terrain", s.handleTerrain)
	s.mux.HandleFunc("GET /api/agents/{id}", s.handleAgent)
	s.mux.HandleFunc("POST /api/restart", s.handleRestart)
	s.mux.HandleFunc("POST /api/regen", s.handleRegen)
	s.mux.HandleFunc("GET /api/god/agent/{id}/divergence", s.handleGodDivergence)
	s.mux.HandleFunc("GET /api/god/reputation/{id}", s.handleGodReputation)
	s.mux.HandleFunc("GET /api/god/relations", s.handleGodRelations)
	s.mux.HandleFunc("GET /api/god/why/{agent_id}/{tick}", s.handleGodWhy)
	return s
}

// NewSSE wires a READ-ONLY server that exposes only the liveness/readiness probes
// and the SSE event stream — no snapshot/agent/god routes, no LiveStore, no Postgres.
// It backs the standalone SSE deployment (sse.dogring.kr), which connects to valkey
// with a read-only user and never touches the write path. The three registered
// handlers (handleHealthz/handleReadyz/handleSSE) read only s.rds + s.keyer, so the
// nil live/gv fields are never dereferenced.
func NewSSE(cfg Config, rds RedisReader) *Server {
	s := &Server{
		rds:   rds,
		keyer: persist.Keyer{Run: cfg.RunID},
		runID: cfg.RunID,
		mux:   http.NewServeMux(),
		addr:  cfg.Addr,
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.mux.HandleFunc("GET /sse", s.handleSSE)
	return s
}

// cors wraps h and adds Access-Control-Allow-Origin: * to every response so that
// browser clients (e.g., Cloudflare Pages) can reach gapi/sse cross-origin.
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// ListenAndServe binds s.addr and serves until ctx is cancelled (graceful shutdown)
// or a fatal listen error occurs.
func (s *Server) ListenAndServe(ctx context.Context) error {
	hs := &http.Server{
		Addr:    s.addr,
		Handler: cors(s.mux),
	}
	done := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done <- hs.Shutdown(shutdownCtx)
	}()
	err := hs.ListenAndServe()
	if err == http.ErrServerClosed {
		return <-done
	}
	return err
}

// Handler returns the underlying http.Handler (the wired mux) for test injection.
func (s *Server) Handler() http.Handler {
	return s.mux
}
