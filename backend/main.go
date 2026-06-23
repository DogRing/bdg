// Command sim is the entrypoint for the medieval village simulation.
//
// It runs in two overlapping modes, selected by environment:
//
//   - Batch (default): load content, spawn, run -ticks, print a JSON snapshot.
//     `go run ./backend -seed=1 -ticks=1440`
//   - Service (P7 wiring): when REDIS_ADDR / POSTGRES_DSN / HTTP_ADDR are set, the
//     run also streams events to a Redis STREAM, mirrors live state to the Redis
//     keyspace, periodically backs up the full snapshot + why-trace to Postgres, and
//     serves the read-only HTTP/SSE API. SIGTERM stops the tick loop, flushes a final
//     snapshot, then closes the SSE/HTTP server (in that order).
//
// Deployment knobs (data-contracts §2/§3) read from the environment:
//
//	REDIS_ADDR          live store + event stream + SSE source (empty = disabled)
//	REDIS_USERNAME      valkey/redis ACL user for the read+write path (empty = no auth)
//	REDIS_PASSWORD      password for REDIS_USERNAME
//	REDIS_DB            valkey/redis logical DB number (default 0)
//	REDIS_RO_USERNAME   read-only valkey user for the api/SSE read path (default: REDIS_USERNAME)
//	REDIS_RO_PASSWORD   password for REDIS_RO_USERNAME (default: REDIS_PASSWORD)
//	POSTGRES_DSN        periodic backup + why-trace (empty = disabled); carries its own user:pass
//	HTTP_ADDR           api listen address (default :8080; api needs REDIS_ADDR)
//	BACKUP_EVERY_TICKS  snapshot/backup cadence (default from content/balance.yaml)
//	GOD_MODE            "true" enables the gated /api/god/* + real_stats merge
//	SEED / RUN_ID / CONTENT_DIR  override the matching flag defaults
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/perception"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/spatial"
	"github.com/dogring/bdg/engine/tom"
	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/engine/worldtime"
	"github.com/dogring/bdg/platform/api"
	"github.com/dogring/bdg/platform/config"
	"github.com/dogring/bdg/platform/events"
	"github.com/dogring/bdg/platform/persist"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"
)

func main() {
	var (
		seed         = flag.Int64("seed", envInt64("SEED", 1), "deterministic RNG seed")
		ticks        = flag.Int64("ticks", 1440, "ticks to run (<=0 = run until SIGTERM)")
		runID        = flag.String("run", envStr("RUN_ID", "dev"), "run id (keyspace prefix)")
		agentCount   = flag.Int("agents", 3, "number of agents to spawn (ignored when -scenario is set)")
		contentDir   = flag.String("content", envStr("CONTENT_DIR", "./content"), "path to content directory")
		scenarioFile = flag.String("scenario", "", "optional scenario YAML: explicit agents/objects (overrides random spawn)")
	)
	flag.Parse()

	// Deployment knobs. An empty REDIS_ADDR/POSTGRES_DSN disables that tier; the
	// process then behaves as the original batch runner (no IO beyond stderr/stdout).
	redisAddr := envStr("REDIS_ADDR", "")
	redisUser := envStr("REDIS_USERNAME", "")
	redisPass := envStr("REDIS_PASSWORD", "")
	redisDB := envInt("REDIS_DB", 0)
	redisROUser := envStr("REDIS_RO_USERNAME", redisUser)
	redisROPass := envStr("REDIS_RO_PASSWORD", redisPass)
	pgDSN := envStr("POSTGRES_DSN", "")
	httpAddr := envStr("HTTP_ADDR", ":8080")
	godMode := envStr("GOD_MODE", "") == "true"
	tickSleepMs := envInt("TICK_SLEEP_MS", 0) // 0 = batch speed; set e.g. 5000 for 12× real-time
	runIDc := core.RunID(*runID)

	fmt.Fprintf(os.Stderr, "medieval-sim seed=%d ticks=%d run=%s agents=%d redis=%q pg=%v http=%q god=%v tick_sleep_ms=%d\n",
		*seed, *ticks, *runID, *agentCount, redisAddr, pgDSN != "", httpAddr, godMode, tickSleepMs)

	// ── 1. Load content (single call — schema-validated, registries + balance) ──
	cfg, err := config.Load(*contentDir)
	fatal(err, "config")
	fmt.Fprintf(os.Stderr, "config hash: %s\n", cfg.ConfigHash())

	// ── 2. Build planner ─────────────────────────────────────────────────────
	thePlanner := planner.New(
		cfg.ActionsRegistry, cfg.GatesRegistry, cfg.NeedsRegistry,
		cfg.StatsRegistry, cfg.Balance.PlannerConfig(),
	)

	// ── 3. Build sensor ──────────────────────────────────────────────────────
	dummyHash := spatial.New(cfg.Balance.World.SpatialHashCell)
	sensor := perception.NewSensor(dummyHash, cfg.PerceptionConfig)

	// ── 4. Build agent config ────────────────────────────────────────────────
	agentCfg := cfg.Balance.AgentConfig(cfg.NeedsRegistry, cfg.StatsRegistry)

	// ── 5. Build world config & clock ────────────────────────────────────────
	clock, err := worldtime.NewClock(cfg.Balance.ClockConfig())
	fatal(err, "clock")

	// ── 6. Assemble services ─────────────────────────────────────────────────
	svc := agent.Services{
		Sensor:  sensor,
		Planner: thePlanner,
		Values:  cfg.ValuesConfig,
		Needs:   cfg.NeedsRegistry,
		Stats:   cfg.StatsRegistry,
		Actions: cfg.ActionsRegistry,
	}

	// ── 7. Signal-scoped context (SIGTERM/SIGINT stops the tick loop) ─────────
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	// ── 8. Wire infra: Redis (events stream + live store + api reader) & Postgres ─
	// The event sink always includes the stderr JSON logger; the Redis STREAM emitter
	// (SSE source) and the Postgres why-trace buffer are appended when configured.
	sinks := []core.EventEmitter{&stderrLogger{}}
	var (
		eventsEmitter *events.Emitter
		liveStore     persist.LiveStore
		backupStore   persist.BackupStore
		redisReader   api.RedisReader
		pgEventBuf    *eventBuffer
	)

	if redisAddr != "" {
		rc := redis.NewClient(&redis.Options{
			Addr:     redisAddr,
			Username: redisUser,
			Password: redisPass,
			DB:       redisDB,
		})
		defer func() { _ = rc.Close() }()
		if err := rc.Ping(sigCtx).Err(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: redis ping %s failed: %v (ops will retry per call)\n", redisAddr, err)
		}
		wa := redisWriteAdapter{c: rc}
		em, err := events.New(sigCtx, wa, runIDc)
		fatal(err, "events")
		eventsEmitter = em
		sinks = append(sinks, em)
		liveStore = persist.NewRedisLiveStore(wa, 0) // ttl 0: keep live keys for the active run

		// Read path (api + SSE) uses a separate, optionally read-only, valkey user
		// (REDIS_RO_*). When those are unset they fall back to the primary credentials,
		// in which case we reuse the single write client instead of opening a second
		// connection. The public SSE/api boundary thus needs only read-only grants.
		rcRead := rc
		if redisROUser != redisUser || redisROPass != redisPass {
			rcRead = redis.NewClient(&redis.Options{
				Addr:     redisAddr,
				Username: redisROUser,
				Password: redisROPass,
				DB:       redisDB,
			})
			defer func() { _ = rcRead.Close() }()
		}
		redisReader = redisReadAdapter{c: rcRead}
	}

	if pgDSN != "" {
		switch pool, perr := pgxpool.New(sigCtx, pgDSN); {
		case perr != nil:
			fmt.Fprintf(os.Stderr, "warning: postgres connect failed: %v (backup disabled)\n", perr)
		default:
			if err := pool.Ping(sigCtx); err != nil {
				fmt.Fprintf(os.Stderr, "warning: postgres ping failed: %v (backup disabled)\n", err)
				pool.Close()
			} else if err := persist.EnsureSchema(sigCtx, pool); err != nil {
				fmt.Fprintf(os.Stderr, "warning: postgres schema init failed: %v (backup disabled)\n", err)
				pool.Close()
			} else {
				defer pool.Close()
				backupStore = persist.NewPgBackupStorePool(pool)
				pgEventBuf = &eventBuffer{}
				sinks = append(sinks, pgEventBuf)
			}
		}
	}

	emitter := core.EventEmitter(&multiEmitter{sinks: sinks})

	// ── 9. Create world (events.Emitter injected; D-inversion: engine→core←platform) ─
	rootRNG := rng.New(*seed)
	w := world.New(cfg.Balance.WorldConfig(), clock, rootRNG, svc, cfg.ActionsRegistry, emitter)

	// ── 10. Place objects & spawn agents ─────────────────────────────────────
	if *scenarioFile != "" {
		doc, err := loadScenario(*scenarioFile)
		fatal(err, "scenario")
		fatal(spawnScenario(w, doc, agentCfg, *seed), "scenario spawn")
		fmt.Fprintf(os.Stderr, "loaded scenario %s: %d agents, %d objects\n",
			*scenarioFile, len(doc.Agents), len(doc.Objects))
	} else {
		placeObjects(w, rootRNG)
		for i := range *agentCount {
			id := core.AgentID(fmt.Sprintf("agent_%02d", i))
			pos := core.Vec2{
				X: (rootRNG.Float64() - 0.5) * 2,
				Y: (rootRNG.Float64() - 0.5) * 2,
			}
			w.Spawn(id, pos, agentCfg, rng.New(*seed+int64(i)+1))
		}
		fmt.Fprintf(os.Stderr, "spawned %d agents\n", *agentCount)
	}

	// ── 11. Initialise run metadata (Redis meta hash + Postgres runs row) ─────
	startedAt := time.Now().UTC().Format(time.RFC3339)
	backupEvery := cfg.Balance.WorldConfig().BackupEveryTicks
	if v := envInt(persist.BackupEveryTicksEnv, 0); v > 0 {
		backupEvery = v
	}
	if liveStore != nil {
		if err := liveStore.InitMeta(sigCtx, runIDc, persist.RunMeta{
			Tick: 0, SchemaVersion: persist.SchemaVersion, StartedAt: startedAt, Status: "running",
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: redis InitMeta: %v\n", err)
		}
	}
	if backupStore != nil {
		if err := backupStore.UpsertRun(sigCtx, persist.RunRecord{
			RunID: runIDc, Seed: *seed, SchemaVersion: persist.SchemaVersion,
			StartedAt: startedAt, Status: "running", ConfigHash: cfg.ConfigHash(),
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pg UpsertRun: %v\n", err)
		}
	}

	// ── 12. Start the read-only HTTP/SSE API (own context, cancelled AFTER the
	//        final flush so the SIGTERM order is: stop ticks → flush → close SSE) ──
	apiCtx, apiCancel := context.WithCancel(context.Background())
	apiDone := make(chan struct{})
	apiStarted := httpAddr != "" && redisReader != nil
	if apiStarted {
		var gv api.GodViewStore // nil for P1: BackupStore has no QueryEvents yet (/why → 503)
		srv := api.New(api.Config{Addr: httpAddr, RunID: runIDc, GodMode: godMode}, liveStore, redisReader, gv)
		go func() {
			defer close(apiDone)
			if err := srv.ListenAndServe(apiCtx); err != nil {
				fmt.Fprintf(os.Stderr, "api server: %v\n", err)
			}
		}()
		fmt.Fprintf(os.Stderr, "api listening on %s\n", httpAddr)
	} else {
		close(apiDone)
		if httpAddr != "" && redisReader == nil {
			fmt.Fprintf(os.Stderr, "note: HTTP_ADDR set but REDIS_ADDR empty — api not started (it tails Redis)\n")
		}
	}

	// ── 13. Tick loop (stops on -ticks exhaustion or SIGTERM) ─────────────────
	fmt.Fprintf(os.Stderr, "running (ticks=%d backup_every=%d)...\n", *ticks, backupEvery)
	runLoop(sigCtx, w, *ticks, runIDc, backupEvery, liveStore, backupStore, pgEventBuf,
		time.Duration(tickSleepMs)*time.Millisecond)

	// ── 14. Final snapshot flush (fresh ctx — sigCtx is already cancelled on SIGTERM) ─
	fmt.Fprintln(os.Stderr, "tick loop stopped; flushing final snapshot...")
	flushCtx, flushCancel := context.WithTimeout(context.Background(), 10*time.Second)
	flushSnapshot(flushCtx, w, runIDc, liveStore, backupStore, pgEventBuf)
	finalizeRun(flushCtx, w, runIDc, *seed, startedAt, cfg.ConfigHash(), liveStore, backupStore)
	flushCancel()
	if eventsEmitter != nil {
		if e := eventsEmitter.Err(); e != nil {
			fmt.Fprintf(os.Stderr, "warning: event stream had transport errors (first: %v)\n", e)
		}
	}

	// ── 15. Close SSE/HTTP (after the flush, per the required SIGTERM ordering) ──
	apiCancel()
	<-apiDone
	if apiStarted {
		fmt.Fprintln(os.Stderr, "api stopped; sse connections closed")
	}

	// ── 16. Snapshot to stdout (batch view; unchanged) ───────────────────────
	printSnapshot(w, *runID)
}

// ── Tick loop & persistence flush ───────────────────────────────────────────────

// runLoop advances the world until the tick limit is reached (limit <= 0 = until
// SIGTERM) or sigCtx is cancelled. Each tick mirrors the live tick counter to Redis;
// every backupEvery ticks it flushes a full snapshot to Redis + Postgres.
func runLoop(sigCtx context.Context, w *world.World, limit int64, runID core.RunID,
	backupEvery int, live persist.LiveStore, backup persist.BackupStore, buf *eventBuffer,
	tickSleep time.Duration) {
	infinite := limit <= 0
	for i := int64(0); infinite || i < limit; i++ {
		select {
		case <-sigCtx.Done():
			return
		default:
		}
		w.Tick()
		tick := w.CurrentTick()
		if live != nil {
			if err := live.WriteTick(sigCtx, runID, tick); err != nil {
				fmt.Fprintf(os.Stderr, "warning: WriteTick(%d): %v\n", tick, err)
			}
		}
		if backupEvery > 0 && int64(tick)%int64(backupEvery) == 0 {
			flushSnapshot(sigCtx, w, runID, live, backup, buf)
		}
		if tickSleep > 0 {
			select {
			case <-sigCtx.Done():
				return
			case <-time.After(tickSleep):
			}
		}
	}
}

// flushSnapshot captures the world's deterministic state and writes it to the live
// Redis keyspace (snapshot + per-agent render views) and, when configured, the Postgres
// backup (snapshot row + drained why-trace events). All errors are logged, not fatal —
// a transient store outage must not abort the simulation.
func flushSnapshot(ctx context.Context, w *world.World, runID core.RunID,
	live persist.LiveStore, backup persist.BackupStore, buf *eventBuffer) {
	if live == nil && backup == nil {
		return
	}
	blob, err := persist.Encode(persist.CaptureSnapshot(runID, w))
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: encode snapshot: %v\n", err)
		return
	}
	tick := w.CurrentTick()

	if live != nil {
		if err := live.WriteSnapshot(ctx, runID, blob); err != nil {
			fmt.Fprintf(os.Stderr, "warning: live WriteSnapshot: %v\n", err)
		}
		for _, id := range w.AgentIDs() {
			a, ok := w.AgentOf(id)
			if !ok {
				continue
			}
			if err := live.WriteAgent(ctx, runID, persist.AgentView{
				ID: a.ID, Pos: a.Pos, Goal: string(a.Goal), Action: currentAction(a), Mood: a.Mood,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "warning: live WriteAgent(%s): %v\n", id, err)
			}
		}
	}

	if backup != nil {
		if err := backup.WriteSnapshot(ctx, runID, tick, blob); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pg WriteSnapshot: %v\n", err)
		}
		if buf != nil {
			if evs := buf.drain(); len(evs) > 0 {
				if err := backup.WriteEvents(ctx, runID, evs); err != nil {
					fmt.Fprintf(os.Stderr, "warning: pg WriteEvents: %v\n", err)
				}
			}
		}
	}
}

// finalizeRun marks the run completed: it refreshes the Redis meta hash and the Postgres
// runs row (status + ended_at). Called once after the tick loop ends.
func finalizeRun(ctx context.Context, w *world.World, runID core.RunID, seed int64,
	startedAt, configHash string, live persist.LiveStore, backup persist.BackupStore) {
	if live != nil {
		if err := live.InitMeta(ctx, runID, persist.RunMeta{
			Tick: w.CurrentTick(), SchemaVersion: persist.SchemaVersion,
			StartedAt: startedAt, Status: "completed",
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: redis finalize meta: %v\n", err)
		}
	}
	if backup != nil {
		if err := backup.UpsertRun(ctx, persist.RunRecord{
			RunID: runID, Seed: seed, SchemaVersion: persist.SchemaVersion,
			StartedAt: startedAt, EndedAt: time.Now().UTC().Format(time.RFC3339),
			Status: "completed", ConfigHash: configHash,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pg finalize run: %v\n", err)
		}
	}
}

// currentAction returns the ActionID the agent is currently executing within its plan
// (the AgentView.Action render field), or "" when the agent has no active plan step.
func currentAction(a *agent.Agent) string {
	if a != nil && a.PlanIdx >= 0 && a.PlanIdx < len(a.Plan.Actions) {
		return string(a.Plan.Actions[a.PlanIdx])
	}
	return ""
}

// ── Event sinks & Redis/Postgres client adapters ────────────────────────────────

// multiEmitter fans one engine event out to every sink (stderr log, Redis STREAM, and
// the Postgres why-trace buffer) in order. It implements core.EventEmitter.
type multiEmitter struct{ sinks []core.EventEmitter }

func (m *multiEmitter) Emit(e core.Event) {
	for _, s := range m.sinks {
		s.Emit(e)
	}
}

// eventBuffer accumulates why-trace events for periodic batch insert into Postgres.
// High-frequency housekeeping (TickDone/SnapshotReady) is dropped — those are operational
// signals, not part of the why-trace (data-contracts §3 events table). It is filled by the
// tick goroutine and drained on the backup cadence; the mutex guards that hand-off.
type eventBuffer struct {
	mu  sync.Mutex
	evs []core.Event
}

func (b *eventBuffer) Emit(e core.Event) {
	switch e.Type {
	case events.TypeTickDone, events.TypeSnapshotReady:
		return
	}
	b.mu.Lock()
	b.evs = append(b.evs, e)
	b.mu.Unlock()
}

func (b *eventBuffer) drain() []core.Event {
	b.mu.Lock()
	out := b.evs
	b.evs = nil
	b.mu.Unlock()
	return out
}

// redisWriteAdapter wraps a *redis.Client for the write/append path. It satisfies both
// events.RedisClient (XAdd → events STREAM) and persist's goRedisClient (Set/Get/Del/
// HSet/Expire → live keyspace), so one client backs both the event stream and the live
// store. go-redis returns *Cmd values; the adapter unwraps them to the (value, error)
// shapes the interfaces expect, mapping redis.Nil (missing key) to a zero value + nil err.
type redisWriteAdapter struct{ c *redis.Client }

func (a redisWriteAdapter) XAdd(ctx context.Context, stream string, values map[string]string) error {
	m := make(map[string]any, len(values))
	for k, v := range values {
		m[k] = v
	}
	return a.c.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: m}).Err()
}

func (a redisWriteAdapter) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	return a.c.Set(ctx, key, value, expiration).Err()
}

func (a redisWriteAdapter) Get(ctx context.Context, key string) (string, error) {
	v, err := a.c.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	return v, err
}

func (a redisWriteAdapter) Del(ctx context.Context, keys ...string) error {
	return a.c.Del(ctx, keys...).Err()
}

func (a redisWriteAdapter) HSet(ctx context.Context, key string, fields ...string) error {
	args := make([]any, len(fields))
	for i, f := range fields {
		args[i] = f
	}
	return a.c.HSet(ctx, key, args...).Err()
}

func (a redisWriteAdapter) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return a.c.Expire(ctx, key, expiration).Err()
}

// redisReadAdapter wraps the same *redis.Client for api's read path. It is a separate
// type from redisWriteAdapter because api.RedisReader.Get returns ([]byte, error) whereas
// persist's Get returns (string, error) — a single Go type cannot carry both Get methods.
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

// Compile-time interface satisfaction (persist's goRedisClient is unexported and is
// checked structurally at the NewRedisLiveStore call site).
var (
	_ events.RedisClient = redisWriteAdapter{}
	_ api.RedisReader    = redisReadAdapter{}
)

// ── Object seeding ────────────────────────────────────────────────────────────

func placeObjects(w *world.World, r *rng.RNG) {
	// Berry bushes (supply Satiety)
	for i := range 5 {
		id := core.ObjectID(fmt.Sprintf("berry_bush_%d", i))
		pos := core.Vec2{X: (r.Float64() - 0.5) * 40, Y: (r.Float64() - 0.5) * 40}
		w.PlaceObject(id, "berry_bush", pos, map[core.Dimension]float64{"Satiety": 0.4})
	}
	// Water sources (supply Hydration)
	for i := range 3 {
		id := core.ObjectID(fmt.Sprintf("water_source_%d", i))
		pos := core.Vec2{X: (r.Float64() - 0.5) * 40, Y: (r.Float64() - 0.5) * 40}
		w.PlaceObject(id, "water_source", pos, map[core.Dimension]float64{"Hydration": 0.5})
	}
	// Shelters (supply Rest)
	for i := range 2 {
		id := core.ObjectID(fmt.Sprintf("shelter_%d", i))
		pos := core.Vec2{X: (r.Float64() - 0.5) * 30, Y: (r.Float64() - 0.5) * 30}
		w.PlaceObject(id, "shelter", pos, map[core.Dimension]float64{"Rest": 0.6})
	}
}

// ── Scenario spawn (BLOCKER-1) ──────────────────────────────────────────────────
//
// A scenario YAML pins an explicit, ordered population — heterogeneous stats, held
// Values, inventory, and objects — so the integration scenarios (high/low-Intel
// divergence, place dispute, collective safety) are reproducible. Stat values are
// the canonical NORMALIZED [0,1] range (content/stats.yaml), set on both RealStats
// (outcome view) and ToM[self] (decision view, D8) with zero noise. This loader is
// deliberately local to main.go and independent of the platform/config content path.

type scenarioDoc struct {
	Agents  []scenarioAgent  `yaml:"agents"`
	Objects []scenarioObject `yaml:"objects"`
}

type scenarioAgent struct {
	ID        string             `yaml:"id"`
	Stats     map[string]float64 `yaml:"stats"`
	Values    []scenarioValue    `yaml:"values"`
	Inventory map[string]int     `yaml:"inventory"`
	Needs     map[string]float64 `yaml:"needs"` // optional: pin initial need intensities (higher = worse)
	Pos       scenarioVec        `yaml:"pos"`
}

type scenarioValue struct {
	Dimension string      `yaml:"dimension"`
	Ref       scenarioRef `yaml:"ref"`
	Weight    float64     `yaml:"weight"`
	Posture   string      `yaml:"posture"`
	Setpoint  float64     `yaml:"setpoint"`
}

type scenarioRef struct {
	Kind string `yaml:"kind"`
	ID   string `yaml:"id"`
}

type scenarioObject struct {
	ID     string             `yaml:"id"`
	Kind   string             `yaml:"kind"`
	Pos    scenarioVec        `yaml:"pos"`
	Supply map[string]float64 `yaml:"supply"`
}

type scenarioVec struct {
	X float64 `yaml:"x"`
	Y float64 `yaml:"y"`
}

// loadScenario reads and strictly parses a scenario YAML; any parse error or an empty
// agent list is fatal (the caller exits).
func loadScenario(path string) (scenarioDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return scenarioDoc{}, err
	}
	var doc scenarioDoc
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // strict: catch authoring typos in the scenario file
	if err := dec.Decode(&doc); err != nil {
		return scenarioDoc{}, fmt.Errorf("scenario parse: %w", err)
	}
	if len(doc.Agents) == 0 {
		return scenarioDoc{}, fmt.Errorf("scenario %q has no agents", path)
	}
	return doc, nil
}

// spawnScenario places the scenario's objects, then spawns its agents in authored
// order (D12: fixed agent-ID order), pinning each agent's stats, Values, bonds, and
// inventory.
func spawnScenario(w *world.World, doc scenarioDoc, cfg agent.Config, seed int64) error {
	for _, obj := range doc.Objects {
		supply := make(map[core.Dimension]float64, len(obj.Supply))
		for k, v := range obj.Supply {
			supply[core.Dimension(k)] = v
		}
		if len(supply) == 0 {
			supply = defaultSupplyForKind(obj.Kind)
		}
		w.PlaceObject(core.ObjectID(obj.ID), core.Tag(obj.Kind),
			core.Vec2{X: obj.Pos.X, Y: obj.Pos.Y}, supply)
	}

	for i, ag := range doc.Agents {
		pos := core.Vec2{X: ag.Pos.X, Y: ag.Pos.Y}
		a := w.Spawn(core.AgentID(ag.ID), pos, cfg, rng.New(seed+int64(i)+1))

		// Pin RealStats, then mirror into ToM[self] (perceived, zero noise) so a
		// high/low-Intelligence agent actually deliberates as one (D8).
		for stat, v := range ag.Stats {
			a.RealStats[core.StatID(stat)] = v
		}
		est := make(map[core.StatID]tom.StatDist, len(a.RealStats))
		for sid, v := range a.RealStats {
			est[sid] = tom.StatDist{Mean: v, Variance: 0}
		}
		a.ToM.SetSelfStats(est)

		for _, sv := range ag.Values {
			kind, err := parseReferentKind(sv.Ref.Kind)
			if err != nil {
				return fmt.Errorf("agent %q: %w", ag.ID, err)
			}
			posture, err := parsePosture(sv.Posture)
			if err != nil {
				return fmt.Errorf("agent %q: %w", ag.ID, err)
			}
			a.Values = append(a.Values, core.Value{
				Dimension: core.Dimension(sv.Dimension),
				Ref:       core.Referent{Kind: kind, ID: core.ObjectID(sv.Ref.ID)},
				Posture:   posture,
				Setpoint:  sv.Setpoint,
			})
			// An Other-care value is a social bond: seed Affinity toward the target so
			// appraiseOthers picks it up (Affinity > MinCareThreshold). The authored
			// weight is the bond strength.
			if kind == core.Other && sv.Ref.ID != "" {
				a.ToM.AdjustAffinity(core.AgentID(sv.Ref.ID), sv.Weight)
			}
		}

		for k, n := range ag.Inventory {
			a.Inventory[core.Tag(k)] = n
		}
		for dim, v := range ag.Needs {
			a.NeedIntensities[core.Dimension(dim)] = v
		}
	}
	return nil
}

func parseReferentKind(s string) (core.ReferentKind, error) {
	switch s {
	case "Self":
		return core.Self, nil
	case "Other":
		return core.Other, nil
	case "Place":
		return core.Place, nil
	case "Collective":
		return core.Collective, nil
	default:
		return 0, fmt.Errorf("unknown referent kind %q", s)
	}
}

func parsePosture(s string) (core.Posture, error) {
	switch s {
	case "Maximize":
		return core.Maximize, nil
	case "MaintainAbove":
		return core.MaintainAbove, nil
	case "PreventBelow":
		return core.PreventBelow, nil
	default:
		return 0, fmt.Errorf("unknown posture %q", s)
	}
}

// defaultSupplyForKind mirrors placeObjects' per-kind supply so a scenario object may
// omit an explicit supply block (e.g. village_center, which supplies nothing).
func defaultSupplyForKind(kind string) map[core.Dimension]float64 {
	switch kind {
	case "berry_bush":
		return map[core.Dimension]float64{"Satiety": 0.4}
	case "water_source":
		return map[core.Dimension]float64{"Hydration": 0.5}
	case "shelter":
		return map[core.Dimension]float64{"Rest": 0.6}
	default:
		return map[core.Dimension]float64{}
	}
}

// ── Snapshot output ───────────────────────────────────────────────────────────

type agentSummary struct {
	ID              string             `json:"id"`
	Pos             [2]float64         `json:"pos"`
	Mood            float64            `json:"mood"`
	Stamina         float64            `json:"stamina"`
	Adrenaline      float64            `json:"adrenaline"`
	Goal            string             `json:"goal"`
	Coping          string             `json:"coping"`
	NeedIntensities map[string]float64 `json:"need_intensities"`
}

type snapshot struct {
	RunID  string         `json:"run_id"`
	Tick   int64          `json:"tick"`
	Agents []agentSummary `json:"agents"`
}

func printSnapshot(w *world.World, runID string) {
	snap := snapshot{
		RunID: runID,
		Tick:  int64(w.CurrentTick()),
	}
	for _, id := range w.AgentIDs() {
		a, _ := w.AgentOf(id)
		ni := make(map[string]float64, len(a.NeedIntensities))
		for dim, v := range a.NeedIntensities {
			ni[string(dim)] = v
		}
		snap.Agents = append(snap.Agents, agentSummary{
			ID:              string(a.ID),
			Pos:             [2]float64{a.Pos.X, a.Pos.Y},
			Mood:            a.Mood,
			Stamina:         a.Stamina,
			Adrenaline:      a.Adrenaline,
			Goal:            string(a.Goal),
			Coping:          copingName(a.Coping),
			NeedIntensities: ni,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		log.Fatalf("snapshot encode: %v", err)
	}
}

func copingName(c agent.CopingState) string {
	switch c {
	case agent.Idle:
		return "idle"
	case agent.Rebinding:
		return "rebinding"
	case agent.Longing:
		return "longing"
	case agent.Latent:
		return "latent"
	case agent.Apathy:
		return "apathy"
	default:
		return "unknown"
	}
}

// ── Event logger ───────────────────────────────────────────────────────────────

// stderrLogger emits events as JSON lines on stderr, filtering tick noise.
// Trade, plan, and coping events are always emitted; per-tick housekeeping
// (TickDone, SnapshotReady) is suppressed to keep the log readable.
type stderrLogger struct{}

func (l *stderrLogger) Emit(e core.Event) {
	switch e.Type {
	case "TickDone", "SnapshotReady":
		return // skip high-frequency housekeeping events
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s\n", b)
}

// ── Utilities ─────────────────────────────────────────────────────────────────

func fatal(err error, label string) {
	if err != nil {
		log.Fatalf("%s: %v", label, err)
	}
}

// ── Environment helpers ─────────────────────────────────────────────────────────
//
// A set-but-empty env var is treated as unset (falls back to the default). Malformed
// numeric values are fatal — never a silent zero (config SPEC ParseEnv contract).

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		n, err := strconv.Atoi(v)
		fatal(err, fmt.Sprintf("env %s=%q", key, v))
		return n
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		fatal(err, fmt.Sprintf("env %s=%q", key, v))
		return n
	}
	return def
}
