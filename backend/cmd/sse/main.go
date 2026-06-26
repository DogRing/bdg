// Command sse is the standalone, READ-ONLY SSE server (sse.dogring.kr).
//
// It is the public read boundary, split out from the simulation writer so it can run
// with a read-only valkey user, no Postgres, and no content/ — it never advances a
// tick or writes a key. It tails the valkey event STREAM that the main backend
// (the writer) appends to and forwards each entry to connected clients over /sse.
//
// Routes: GET /healthz, GET /readyz (pings valkey), GET /sse. Nothing else.
//
// Environment (data-contracts §2; same names the writer uses, so one Secret feeds both):
//
//	REDIS_ADDR          valkey host:port (required)
//	REDIS_DB            valkey logical DB (default 0)
//	REDIS_RO_USERNAME   read-only valkey user (falls back to REDIS_USERNAME)
//	REDIS_RO_PASSWORD   password for it      (falls back to REDIS_PASSWORD)
//	RUN_ID              keyspace prefix — MUST match the writer's RUN_ID (default "dev")
//	HTTP_ADDR           listen address (default ":8080")
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/platform/api"
	"github.com/redis/go-redis/v9"
)

func main() {
	redisAddr := envStr("REDIS_ADDR", "")
	redisDB := envInt("REDIS_DB", 0)
	// The SSE server authenticates as the read-only user; fall back to the primary
	// credentials so a single .env/Secret works for both binaries.
	redisUser := envStr("REDIS_RO_USERNAME", envStr("REDIS_USERNAME", ""))
	redisPass := envStr("REDIS_RO_PASSWORD", envStr("REDIS_PASSWORD", ""))
	runID := core.RunID(envStr("RUN_ID", "dev"))
	httpAddr := envStr("HTTP_ADDR", ":8080")

	if redisAddr == "" {
		log.Fatal("REDIS_ADDR is required (the SSE server tails the valkey event stream)")
	}
	fmt.Fprintf(os.Stderr, "sse-server run=%s redis=%q db=%d user=%q http=%q\n",
		runID, redisAddr, redisDB, redisUser, httpAddr)

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rc := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Username: redisUser,
		Password: redisPass,
		DB:       redisDB,
	})
	defer func() { _ = rc.Close() }()
	if err := rc.Ping(sigCtx).Err(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: valkey ping %s failed: %v (will retry per request)\n", redisAddr, err)
	}

	srv := api.NewSSE(api.Config{Addr: httpAddr, RunID: runID}, redisReadAdapter{c: rc})
	fmt.Fprintf(os.Stderr, "sse-server listening on %s\n", httpAddr)
	if err := srv.ListenAndServe(sigCtx); err != nil {
		log.Fatalf("sse-server: %v", err)
	}
	fmt.Fprintln(os.Stderr, "sse-server stopped")
}

// redisReadAdapter wraps *redis.Client for api's read path (api.RedisReader). It mirrors
// the read adapter in the main backend; the SSE handler uses only Ping + XRead, but the
// interface requires Get/HGetAll too. redis.Nil (missing key / BLOCK timeout) maps to a
// zero value + nil error so a quiet stream is not treated as failure.
type redisReadAdapter struct{ c *redis.Client }

func (a redisReadAdapter) Ping(ctx context.Context) error { return a.c.Ping(ctx).Err() }

func (a redisReadAdapter) Get(ctx context.Context, key string) ([]byte, error) {
	b, err := a.c.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return b, err
}

func (a redisReadAdapter) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return a.c.HGetAll(ctx, key).Result()
}

func (a redisReadAdapter) XRead(ctx context.Context, key, lastID string, block time.Duration) ([]api.StreamEntry, string, error) {
	if lastID == "" {
		lastID = "$" // only entries appended after this connection (SSE SPEC: start from "$")
	}
	res, err := a.c.XRead(ctx, &redis.XReadArgs{Streams: []string{key, lastID}, Block: block}).Result()
	if err == redis.Nil {
		return nil, lastID, nil // BLOCK timeout, no new entries — not an error
	}
	if err != nil {
		return nil, lastID, err // includes ctx cancellation; the SSE loop checks ctx.Err()
	}
	var entries []api.StreamEntry
	newLast := lastID
	for _, st := range res {
		for _, msg := range st.Messages {
			fields := make(map[string]string, len(msg.Values))
			for k, v := range msg.Values {
				fields[k] = fmt.Sprint(v)
			}
			entries = append(entries, api.StreamEntry{ID: msg.ID, Fields: fields})
			newLast = msg.ID
		}
	}
	return entries, newLast, nil
}

var _ api.RedisReader = redisReadAdapter{}

// ── env helpers (set-but-empty = unset; malformed int = fatal) ──────────────────

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			log.Fatalf("env %s=%q: %v", key, v, err)
		}
		return n
	}
	return def
}
