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
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/kernel/worldtime"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/mind/tom"
	"github.com/dogring/bdg/engine/space/spatial"
	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/platform/api"
	"github.com/dogring/bdg/platform/config"
	"github.com/dogring/bdg/platform/events"
	"github.com/dogring/bdg/platform/persist"
	"github.com/dogring/bdg/tools/worldgen"
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
		fixtureFile  = flag.String("fixture", envStr("FIXTURE", "./tools/worldgen/testdata/starter_village.fixture.yaml"), "world fixture YAML to load when -scenario is unset")
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
		purgeEntities func(context.Context, *world.World)
		purgeRunKeys  func(context.Context)
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
		// The fan-out emitter (multiEmitter) stamps ONE shared seq per event, so the
		// Redis stream, the stderr log, and the Postgres why-trace buffer all carry
		// the same numbering — the emitter must not re-stamp (WithCallerSeq).
		em, err := events.New(sigCtx, wa, runIDc, events.WithCallerSeq())
		fatal(err, "events")
		eventsEmitter = em
		sinks = append(sinks, em)
		liveStore = persist.NewRedisLiveStore(wa, 0) // ttl 0: keep live keys for the active run

		// Restart/regen rebuilds swap the whole entity set, but the per-entity live
		// keys (sim:{run}:agent:{id} / :animal:{id}) are written with TTL 0 — without
		// an explicit purge the outgoing world's hashes would linger forever. The tick
		// loop calls this with the OLD world right before swapping in the rebuilt one.
		keyer := persist.Keyer{Run: runIDc}
		purgeEntities = func(ctx context.Context, old *world.World) {
			animals := old.Animals()
			keys := make([]string, 0, len(old.AgentIDs())+len(animals))
			for _, id := range old.AgentIDs() {
				keys = append(keys, keyer.Agent(id))
			}
			for _, a := range animals {
				keys = append(keys, keyer.Animal(a.ID))
			}
			if len(keys) == 0 {
				return
			}
			if err := wa.Del(ctx, keys...); err != nil {
				fmt.Fprintf(os.Stderr, "warning: purge stale entity keys: %v\n", err)
			}
		}

		// Regen ("new map") additionally deletes the fixed single-key-per-run live
		// keys so no old map state stays visible under the same run_id. Explicit,
		// deterministic key list (no SCAN); sim:{run}:meta is NOT deleted — the
		// regen path immediately refreshes it via InitMeta. Deleting the events
		// STREAM is safe for SSE: clients tail from "$" and XADD entry IDs are
		// wall-clock-based, so a recreated stream keeps IDs monotone for a
		// connection that outlives the regen.
		purgeRunKeys = func(ctx context.Context) {
			keys := []string{
				keyer.SnapshotKey(), keyer.Tick(), keyer.Events(),
				keyer.Flora(), keyer.Climate(), keyer.Terrain(),
			}
			if err := wa.Del(ctx, keys...); err != nil {
				fmt.Fprintf(os.Stderr, "warning: purge run keys: %v\n", err)
			}
		}

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
	var w *world.World
	runSeed := *seed

	// ── 10. Load fixture or explicit scenario ────────────────────────────────
	// buildWorld reconstructs the world from the same fixture/scenario — the
	// deterministic initial state (D12). Called once here and again by the tick
	// loop on a control signal: POST /api/restart passes seed 0 (the original
	// seed — identical world) and POST /api/regen a fresh seed (a fixture with
	// terrain{random:true} / pos-less placements re-rolls; regen is fixture-only).
	var buildWorld func(seed int64) (*world.World, error)
	fixtureMode := *scenarioFile == ""
	if !fixtureMode {
		doc, err := loadScenario(*scenarioFile)
		fatal(err, "scenario")
		buildWorld = func(int64) (*world.World, error) { // scenario: no regen, seed arg unused
			nw := world.New(cfg.Balance.WorldConfig(), clock, rng.New(*seed), svc, cfg.ActionsRegistry, emitter)
			if err := spawnScenario(nw, doc, agentCfg, *seed); err != nil {
				return nil, err
			}
			return nw, nil
		}
		w, err = buildWorld(0)
		fatal(err, "scenario spawn")
		fmt.Fprintf(os.Stderr, "loaded scenario %s: %d agents, %d objects\n",
			*scenarioFile, len(doc.Agents), len(doc.Objects))
	} else {
		schemaPath := filepath.Join(*contentDir, "schema", "fixture.schema.json")
		fx, err := worldgen.ParseFile(*fixtureFile, schemaPath)
		fatal(err, "fixture")
		buildWorld = func(seed int64) (*world.World, error) {
			f := fx
			if seed != 0 {
				f.Seed = seed
			}
			return worldgen.Load(f, cfg, worldgen.WithEmitter(emitter))
		}
		w, err = buildWorld(0)
		fatal(err, "worldgen")
		runSeed = fx.Seed
		fmt.Fprintf(os.Stderr, "loaded fixture %s: %d agents, %d objects, %d flora, %d animals\n",
			*fixtureFile, len(fx.Agents), len(fx.Objects), len(fx.Flora), len(fx.Animals))
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
			RunID: runIDc, Seed: runSeed, SchemaVersion: persist.SchemaVersion,
			StartedAt: startedAt, Status: "running", ConfigHash: cfg.ConfigHash(),
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pg UpsertRun: %v\n", err)
		}
	}

	// ── 11b. Single-world publication marker (world_revision, data-contracts §2) ─
	// Boot rule (persist SPEC step 5): read the stored revision and claim
	// stored+1 — never backwards, never reused. A restarted process rebuilds
	// from the fixture and may therefore publish a DIFFERENT map than the last
	// published revision, so the stored value must not be re-claimed. The boot
	// revision is published with the FIRST successful baseline flush; until
	// then meta/baselines still describe the previous process's world
	// self-consistently.
	var pub *worldPub
	if liveStore != nil {
		stored := int64(0)
		if redisReader != nil {
			if h, err := redisReader.HGetAll(sigCtx, persist.Keyer{Run: runIDc}.Meta()); err == nil {
				if v, perr := strconv.ParseInt(h["world_revision"], 10, 64); perr == nil && v > 0 {
					stored = v
				}
			}
		}
		pub = &worldPub{live: liveStore, runID: runIDc, revision: stored + 1}
		if eventsEmitter != nil {
			pub.cursorFn = eventsEmitter.LastStreamID
		}
		fmt.Fprintf(os.Stderr, "world_revision: boot epoch %d (stored %d; publishes with the first flush)\n",
			pub.revision, stored)
	}

	// resetRunData is the POST /api/regen ("new map") cleanup — the current
	// single-world development mode: the run keeps its run_id and the OLD map's
	// data is deleted (a multi-world redesign that would instead preserve old
	// runs is DEFERRED — docs/plans/run-generation.md, design notes only).
	// Ordering is the consistency mechanism
	// (persist SPEC "restart vs regen"; Redis and Postgres cannot share one
	// transaction):
	//   1. Postgres reset, MANDATORY: ResetRunData — ONE transaction deleting
	//      the run's events + snapshots AND upserting the runs row to the new
	//      seed. A failure at any step (even after the deletes) rolls everything
	//      back, returns an error, and the tick loop ABORTS the regen — the
	//      current world keeps running, nothing in Redis was touched yet, and a
	//      half-cleaned run is never presented as new.
	//   2. Redis cleanup, best-effort (ACCEPTED interim limitation): per-entity +
	//      fixed live keys deleted, meta refreshed via InitMeta. Failures are
	//      logged only — the immediate fresh flush plus per-tick writes overwrite
	//      every fixed key, so stale data self-heals (worst case: a stale
	//      per-entity hash if that DEL failed).
	// POST /api/restart never runs this: a restart is a debugging rewind that
	// keeps the Postgres history (append-only) and only purges the swapped-out
	// entity live keys.
	resetRunData := func(ctx context.Context, old *world.World, seed int64) error {
		newStartedAt := time.Now().UTC().Format(time.RFC3339)
		if backupStore != nil {
			if err := backupStore.ResetRunData(ctx, persist.RunRecord{
				RunID: runIDc, Seed: seed, SchemaVersion: persist.SchemaVersion,
				StartedAt: newStartedAt, Status: "running", ConfigHash: cfg.ConfigHash(),
			}); err != nil {
				return fmt.Errorf("pg ResetRunData: %w", err)
			}
		}
		if purgeEntities != nil {
			purgeEntities(ctx, old)
		}
		if purgeRunKeys != nil {
			purgeRunKeys(ctx)
		}
		runSeed = seed
		startedAt = newStartedAt
		if liveStore != nil {
			if err := liveStore.InitMeta(ctx, runIDc, persist.RunMeta{
				Tick: 0, SchemaVersion: persist.SchemaVersion, StartedAt: startedAt, Status: "running",
			}); err != nil {
				fmt.Fprintf(os.Stderr, "warning: regen redis InitMeta: %v\n", err)
			}
		}
		return nil
	}

	// ── 12. Start the read-only HTTP/SSE API (own context, cancelled AFTER the
	//        final flush so the SIGTERM order is: stop ticks → flush → close SSE) ──
	apiCtx, apiCancel := context.WithCancel(context.Background())
	apiDone := make(chan struct{})
	apiStarted := httpAddr != "" && redisReader != nil
	// POST /api/restart → non-blocking signal; the tick loop (the single writer,
	// D12) performs the actual world rebuild. Buffered(1): concurrent requests
	// while a restart is already pending coalesce into one.
	restartCh := make(chan struct{}, 1)
	requestRestart := func() {
		select {
		case restartCh <- struct{}{}:
		default:
		}
	}
	// POST /api/regen → same pattern, carrying the requested seed (0 ⇒ the loop
	// draws a random one). Fixture mode only: a scenario world has nothing seeded
	// to re-roll, so the callback stays nil and the route responds 503.
	regenCh := make(chan int64, 1)
	var requestRegen func(int64)
	if fixtureMode {
		requestRegen = func(seed int64) {
			select {
			case regenCh <- seed:
			default:
			}
		}
	}
	if apiStarted {
		var gv api.GodViewStore // nil for P1: BackupStore has no QueryEvents yet (/why → 503)
		srv := api.New(api.Config{Addr: httpAddr, RunID: runIDc, GodMode: godMode, Restart: requestRestart, Regen: requestRegen},
			liveStore, redisReader, gv)
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
	w = runLoop(sigCtx, w, *ticks, runIDc, backupEvery, liveStore, backupStore, pgEventBuf,
		time.Duration(tickSleepMs)*time.Millisecond,
		loopControl{restart: restartCh, regen: regenCh, rebuild: buildWorld,
			purge: purgeEntities, reset: resetRunData, pub: pub})

	// ── 14. Final snapshot flush (fresh ctx — sigCtx is already cancelled on SIGTERM) ─
	fmt.Fprintln(os.Stderr, "tick loop stopped; flushing final snapshot...")
	flushCtx, flushCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if ok, terrainOn := flushSnapshot(flushCtx, w, runIDc, pub, liveStore, backupStore, pgEventBuf); ok {
		pub.publishIfReady(flushCtx, ok, terrainOn)
	}
	finalizeRun(flushCtx, w, runIDc, runSeed, startedAt, cfg.ConfigHash(), liveStore, backupStore)
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

// loopControl carries the tick loop's runtime control surface: the restart/regen
// signal channels (POST /api/restart / /api/regen) and the world (re)build +
// cleanup callbacks. A nil channel case never fires; a nil callback is skipped.
//   - purge: restart's cleanup — the outgoing world's per-entity live keys only
//     (a restart is a debugging rewind; the Postgres history is preserved).
//   - reset: regen's cleanup — the full "new map, same run_id" wipe (old live
//     keys + Postgres snapshots/events history + runs/meta refresh). An error
//     means the MANDATORY part of the cleanup failed: the loop ABORTS the regen
//     and keeps the current world (a half-cleaned run must not be presented as
//     a new map). When nil, regen falls back to purge.
type loopControl struct {
	restart <-chan struct{}
	regen   <-chan int64
	rebuild func(seed int64) (*world.World, error) // seed 0 ⇒ the original fixture/scenario seed
	purge   func(ctx context.Context, old *world.World)
	reset   func(ctx context.Context, old *world.World, seed int64) error
	// pub is the single-world publication marker (world_revision). nil ⇒ no
	// publication (no Redis). A successful regen bumps it BEFORE the fresh
	// flush; every successful baseline flush publishes a pending revision
	// (publish-last, persist SPEC steps 4–5). Restarts and failed/aborted
	// regens never touch it.
	pub *worldPub
}

// runLoop advances the world until the tick limit is reached (limit <= 0 = until
// SIGTERM) or sigCtx is cancelled. Each tick mirrors the live tick counter to Redis;
// every backupEvery ticks it flushes a full snapshot to Redis + Postgres.
// A control signal rebuilds the world ON THIS GOROUTINE (D12 single-writer):
// restart (POST /api/restart) re-runs the original fixture/scenario seed — the
// deterministic initial state; regen (POST /api/regen) re-runs with a NEW seed
// (0 in the signal ⇒ drawn here and logged, so the world stays reproducible via
// /api/regen?seed=). Both purge the outgoing world's per-entity live keys, reset
// the tick-limit counter, and flush the fresh state immediately so REST reads
// (snapshot/terrain) are current before clients reload. Returns the world that
// was live when the loop stopped (the caller flushes/finalizes it).
func runLoop(sigCtx context.Context, w *world.World, limit int64, runID core.RunID,
	backupEvery int, live persist.LiveStore, backup persist.BackupStore, buf *eventBuffer,
	tickSleep time.Duration, ctl loopControl) *world.World {
	infinite := limit <= 0
	doRebuild := func(i *int64, seed int64, kind string) {
		regen := kind == "regen"
		// Separate the OLD world's buffered why-trace from CANDIDATE-world
		// construction events (persist SPEC "restart vs regen" step 1): the
		// buffer is drained before ctl.rebuild, so everything buffered during
		// the rebuild belongs to the candidate. On ANY failure the candidate
		// batch is discarded and the old batch restored exactly once (original
		// seq order) — candidate events never leak into the continuing world's
		// why-trace. On success, regen DROPS the old batch (its Postgres rows
		// were just wiped) while restart re-queues it AHEAD of the candidate's
		// (append-only history, seq order preserved).
		var old []core.Event
		if buf != nil {
			old = buf.drain()
		}
		abort := func(step string, err error) {
			if buf != nil {
				_ = buf.drain() // discard the candidate's construction events
				if len(old) > 0 {
					buf.restore(old)
				}
			}
			fmt.Fprintf(os.Stderr, "warning: %s %s: %v (keeping current world)\n", kind, step, err)
		}
		nw, err := ctl.rebuild(seed)
		if err != nil {
			abort("rebuild failed", err)
			return
		}
		if regen && ctl.reset != nil {
			if err := ctl.reset(sigCtx, w, seed); err != nil {
				// The mandatory Postgres cleanup failed: do NOT present a
				// half-cleaned run as a new map.
				abort("cleanup failed", err)
				return
			}
		} else if ctl.purge != nil {
			ctl.purge(sigCtx, w)
		}
		if !regen && buf != nil && len(old) > 0 {
			buf.restore(old)
		}
		if regen {
			// Claim the next world_revision for the regenerated world BEFORE its
			// baseline flush (the fresh snapshot/terrain are tagged with it); it
			// becomes externally visible only via publishIfReady below.
			ctl.pub.bump()
		}
		w = nw
		*i = 0
		ok, terrainOn := flushSnapshot(sigCtx, w, runID, ctl.pub, live, backup, buf)
		ctl.pub.publishIfReady(sigCtx, ok, terrainOn)
	}
	doRestart := func(i *int64) {
		fmt.Fprintln(os.Stderr, "restart signal: rebuilding world from initial state (tick 0)")
		doRebuild(i, 0, "restart")
	}
	doRegen := func(i *int64, seed int64) {
		if seed == 0 {
			seed = randomSeed()
		}
		fmt.Fprintf(os.Stderr, "regen signal: rebuilding world with seed=%d (tick 0)\n", seed)
		doRebuild(i, seed, "regen")
	}
	for i := int64(0); infinite || i < limit; i++ {
		select {
		case <-sigCtx.Done():
			return w
		case <-ctl.restart:
			doRestart(&i)
		case seed := <-ctl.regen:
			doRegen(&i, seed)
		default:
		}
		w.Tick()
		tick := w.CurrentTick()
		if live != nil {
			if err := live.WriteTick(sigCtx, runID, tick); err != nil {
				fmt.Fprintf(os.Stderr, "warning: WriteTick(%d): %v\n", tick, err)
			}
		}
		// Live movement streams to the god-view via SSE AgentFrame/WorldFrame events,
		// so the full snapshot + Postgres backup only need to run on the backup cadence.
		if backupEvery > 0 && int64(tick)%int64(backupEvery) == 0 {
			ok, terrainOn := flushSnapshot(sigCtx, w, runID, ctl.pub, live, backup, buf)
			ctl.pub.publishIfReady(sigCtx, ok, terrainOn)
		}
		if tickSleep > 0 {
			select {
			case <-sigCtx.Done():
				return w
			case <-ctl.restart:
				doRestart(&i)
			case seed := <-ctl.regen:
				doRegen(&i, seed)
			case <-time.After(tickSleep):
			}
		}
	}
	return w
}

// randomSeed draws a non-zero seed from the OS entropy source for POST /api/regen.
// Platform layer only — the engine still sees nothing but the injected seeded RNG
// (D12); the drawn seed is logged by the caller so any regenerated world can be
// reproduced with /api/regen?seed=<value>.
func randomSeed() int64 {
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return time.Now().UnixNano() // entropy source unavailable; still a valid one-off seed
	}
	s := int64(binary.LittleEndian.Uint64(b[:]) & (1<<63 - 1))
	if s == 0 {
		s = 1
	}
	return s
}

// worldPub tracks the single-world publication marker (world_revision,
// data-contracts §2; persist SPEC "restart vs regen" steps 4–5). NOT a run
// generation: one run_id, one active world — the marker only identifies which
// published map revision the live baselines belong to. Single-writer: every
// touch happens on the run's main goroutine (same contract as multiEmitter).
type worldPub struct {
	live     persist.LiveStore
	runID    core.RunID
	revision int64
	// published: the current revision is visible in sim:{run}:meta. false ⇒
	// pending — the next successful baseline flush publishes it (publish-last;
	// self-healing across transient Redis failures).
	published bool
	// cursorFn returns the events emitter's last successfully appended stream
	// entry ID — the snapshot wrapper's stream_cursor. nil ⇒ "" (no stream).
	cursorFn func() string
}

func (p *worldPub) rev() int64 {
	if p == nil {
		return 0
	}
	return p.revision
}

func (p *worldPub) cursor() string {
	if p == nil || p.cursorFn == nil {
		return ""
	}
	return p.cursorFn()
}

// bump claims the NEXT revision for a successful regen. It stays unpublished
// until the regenerated baseline flush succeeds — a reader observing the new
// revision must find matching baselines servable. Serial (tick goroutine), so
// concurrent regen signals can never reuse a revision.
func (p *worldPub) bump() {
	if p == nil {
		return
	}
	p.revision++
	p.published = false
}

// publishIfReady publishes the pending revision AFTER a successful baseline
// flush (persist SPEC step 5). baselineOK=false or an HSET failure keeps the
// revision pending; the next backup-cadence flush retries flush+publication.
func (p *worldPub) publishIfReady(ctx context.Context, baselineOK, terrainOn bool) {
	if p == nil || p.published || !baselineOK {
		return
	}
	if p.live == nil {
		p.published = true // nothing externally visible to publish to
		return
	}
	if err := p.live.PublishWorldRevision(ctx, p.runID, p.revision, terrainOn); err != nil {
		fmt.Fprintf(os.Stderr, "warning: publish world_revision %d: %v (retrying next flush)\n", p.revision, err)
		return
	}
	p.published = true
	fmt.Fprintf(os.Stderr, "world_revision %d published (terrain_on=%t)\n", p.revision, terrainOn)
}

// flushSnapshot captures the world's deterministic state once, encodes the untouched
// base for Postgres, then stamps and separately encodes a copy for the live Redis
// keyspace. This keeps transport/publication metadata out of deterministic backup bytes.
// Postgres also receives the drained why-trace events and applies retention pruning.
// All errors are logged, not fatal — a transient store outage must not abort the simulation.
//
// The snapshot wrapper is stamped with the publication metadata (data-contracts
// §1): pub's world_revision, the emitter's stream_cursor (captured HERE, on the
// tick goroutine, after the tick's emissions completed — so the state reflects
// every entry at or before it) and the explicit terrain availability flag.
// Returns baselineOK (the live snapshot — and terrain, when env is on — writes
// succeeded: the gate for pub.publishIfReady) and terrainOn.
func flushSnapshot(ctx context.Context, w *world.World, runID core.RunID, pub *worldPub,
	live persist.LiveStore, backup persist.BackupStore, buf *eventBuffer) (baselineOK, terrainOn bool) {
	if live == nil && backup == nil {
		return true, false
	}
	rv := w.RenderView()
	terrainOn = rv.Terrain != nil

	baseSnapshot := persist.CaptureSnapshot(runID, w)
	backupBlob, err := persist.Encode(baseSnapshot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: encode backup snapshot: %v\n", err)
		return false, terrainOn
	}

	liveSnapshot := baseSnapshot
	liveSnapshot.WorldRevision = pub.rev()
	liveSnapshot.StreamCursor = pub.cursor()
	if terrainOn {
		liveSnapshot.TerrainStatus = "on"
	} else {
		liveSnapshot.TerrainStatus = "off"
	}
	liveBlob, err := persist.Encode(liveSnapshot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: encode live snapshot: %v\n", err)
		return false, terrainOn
	}
	tick := w.CurrentTick()

	baselineOK = writeLive(ctx, w, rv, runID, pub.rev(), live, liveBlob)

	if backup != nil {
		// One transaction: the drained why-trace batch + the snapshot row stamped
		// with the batch's max seq (persist SPEC WriteBackup) — a partial flush
		// cannot exist. On failure NOTHING was stored, so the batch goes back
		// into the buffer and the next flush retries it (a transient Postgres
		// outage loses no why-trace unless the bounded diagnostic buffer reaches
		// its 5,000-event safety limit).
		var evs []core.Event
		if buf != nil {
			if dropped := buf.takeDropped(); dropped > 0 {
				fmt.Fprintf(os.Stderr, "warning: pg event buffer dropped %d oldest why-trace events (limit %d)\n",
					dropped, eventBufferMaxEvents)
			}
			evs = buf.drain()
		}
		if err := backup.WriteBackup(ctx, runID, tick, backupBlob, evs); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pg WriteBackup: %v\n", err)
			if buf != nil && len(evs) > 0 {
				buf.restore(evs)
			}
		} else if err := backup.PruneSnapshots(ctx, runID, time.Now().UTC()); err != nil {
			// Prune runs ONLY after a committed backup (never alongside a failed
			// one); its own failure just defers downsampling to the next flush —
			// the committed backup stands.
			fmt.Fprintf(os.Stderr, "warning: pg PruneSnapshots: %v\n", err)
		}
	}
	return baselineOK, terrainOn
}

// writeLive writes the snapshot blob and each agent's render view to the live Redis
// keyspace. Called from flushSnapshot on the backup cadence. Returns whether the
// BASELINE writes succeeded — the snapshot key and, when env is on, the terrain
// key (what a bootstrap client requires; per-entity/flora/climate failures are
// logged but self-heal on the per-flush overwrite and do not gate publication).
func writeLive(ctx context.Context, w *world.World, rv world.RenderView, runID core.RunID,
	rev int64, live persist.LiveStore, blob []byte) bool {
	if live == nil {
		return true
	}
	ok := true
	if err := live.WriteSnapshot(ctx, runID, blob); err != nil {
		fmt.Fprintf(os.Stderr, "warning: live WriteSnapshot: %v\n", err)
		ok = false
	}
	for _, id := range w.AgentIDs() {
		a, aok := w.AgentOf(id)
		if !aok {
			continue
		}
		if err := live.WriteAgent(ctx, runID, persist.AgentView{
			ID: a.ID, Pos: a.Pos, Goal: string(a.Goal), Action: currentAction(a), Mood: a.Mood,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: live WriteAgent(%s): %v\n", id, err)
		}
	}

	if !writeEnvLive(ctx, rv, runID, rev, live) {
		ok = false
	}
	return ok
}

// writeEnvLive writes the WI-P4 env render keys (sim:{run}:animal:{id} / :flora /
// :climate / :terrain, data-contracts §2) from the world's god-view-filtered
// RenderView. Env-OFF ⇒ the view carries no env blocks ⇒ nothing is written and
// the keys stay ABSENT (§2 "absent ⇒ env-off"). Staleness is TTL-bounded: dead
// animals' hashes and a flora set that empties out age off via the store's TTL
// rather than an explicit delete (same policy as agent hashes).
// The terrain blob is tagged with the publishing world_revision; its write is
// the only env write that gates baseline publication (returned bool) — animal/
// flora/climate failures are logged and self-heal on the next flush.
func writeEnvLive(ctx context.Context, rv world.RenderView, runID core.RunID,
	rev int64, live persist.LiveStore) bool {
	for _, a := range rv.Animals {
		if err := live.WriteAnimal(ctx, runID, persist.AnimalView{
			ID: a.ID, Pos: a.Pos, Species: a.Species,
			Action: a.Action, Heading: a.Heading, Stamina: a.Stamina,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: live WriteAnimal(%s): %v\n", a.ID, err)
		}
	}

	if len(rv.Flora) > 0 {
		plants := make([]persist.FloraView, 0, len(rv.Flora))
		for _, p := range rv.Flora {
			plants = append(plants, persist.FloraView{
				ID: p.ID, Species: p.Species, Pos: p.Pos, Stage: p.Stage, Width: p.Width,
			})
		}
		if err := live.WriteFlora(ctx, runID, plants); err != nil {
			fmt.Fprintf(os.Stderr, "warning: live WriteFlora: %v\n", err)
		}
	}

	if rv.ClimateOn {
		if err := live.WriteClimate(ctx, runID, persist.ClimateView{
			Temperature: rv.Temperature, Moisture: rv.Moisture, Raining: rv.Raining,
			WindDir: rv.WindDir, WindMag: rv.WindMag,
			HourOfDay: rv.HourOfDay, DayNight: rv.DayNight, YearFraction: rv.YearFraction,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: live WriteClimate: %v\n", err)
		}
	}

	if rv.Terrain != nil {
		if err := live.WriteTerrain(ctx, runID, persist.TerrainView{
			CellSize:      rv.Terrain.CellSize,
			Orientation:   rv.Terrain.Orientation,
			Size:          persist.TerrainSize{Cols: rv.Terrain.Cols, Rows: rv.Terrain.Rows},
			Terrain:       rv.Terrain.Terrain,
			Wear:          rv.Terrain.Wear,
			Elevation:     rv.Terrain.Elevation,
			WorldRevision: rev,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: live WriteTerrain: %v\n", err)
			return false
		}
	}
	return true
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
// the Postgres why-trace buffer) in order. It stamps a monotone Event.Seq ONCE
// before the fan-out, so every sink carries the same numbering (the Redis
// emitter is constructed WithCallerSeq and serializes it verbatim) — the seq that
// backs snapshots.last_event_seq. It implements core.EventEmitter.
//
// SEQ SCOPE (persist SPEC Notes "Seq scope"): the counter starts at 0 with the
// PROCESS, not the run. /api/restart rebuilds in-process, so the sequence stays
// monotone across that rewind; a real process restart begins a new process-local
// epoch while the run_id and its Postgres history persist, so seq values repeat
// within one run across process lifetimes. Replay across process restarts is
// unsupported; durable cross-process identity is deferred (run-generation.md).
//
// SINGLE-WRITER CONTRACT: every production emission happens on the run's main
// goroutine — the world buffers plan-phase events and flushes them serially in
// sorted agent-ID order (engine/world SPEC-tick.md "PLAN-PHASE EVENTS ARE
// BUFFERED"), and all other emitters (apply phase, fauna, world construction)
// already run there. seq is therefore a plain counter, deterministic across
// identical-seed runs; a future concurrent emitter is a bug that `go test -race`
// should surface, not something to mask with synchronization here.
type multiEmitter struct {
	seq   int64 // next Event.Seq (single-writer, see contract above)
	sinks []core.EventEmitter
}

func (m *multiEmitter) Emit(e core.Event) {
	e.Seq = m.seq
	m.seq++
	for _, s := range m.sinks {
		s.Emit(e)
	}
}

// eventBuffer accumulates why-trace events for the periodic WriteBackup flush.
// High-frequency housekeeping (TickDone/AgentFrame/WorldFrame/SnapshotReady) is
// dropped — those are operational/render signals, not part of the why-trace
// (data-contracts §3 events table); their seqs therefore appear as gaps in the
// Postgres rows.
//
// SINGLE-WRITER CONTRACT (same as multiEmitter): Emit, drain and restore all run
// on the run's main goroutine, so there is no lock — a future concurrent emitter
// is race-detectable. Append order equals seq order (the fan-out stamps seq
// immediately before Emit), and restore prepends an older drained batch ahead of
// anything buffered since, so the buffer stays seq-ascending by construction.
type eventBuffer struct {
	evs                []core.Event
	droppedSinceReport uint64
}

const (
	// why-trace is diagnostic, not simulation state. Bound it tightly enough to
	// keep the backend alive through a prolonged Postgres outage. At roughly
	// 0.5–1 KiB/event this is normally a few MiB plus payload allocations.
	eventBufferMaxEvents = 5_000
	// Trim in chunks to avoid shifting the slice for every new event once full.
	eventBufferTrimEvents = 500
)

func (b *eventBuffer) Emit(e core.Event) {
	switch e.Type {
	case events.TypeTickDone, events.TypeAgentFrame, events.TypeWorldFrame, events.TypeSnapshotReady:
		return
	}
	b.evs = append(b.evs, e)
	b.enforceLimit()
}

func (b *eventBuffer) enforceLimit() {
	if len(b.evs) <= eventBufferMaxEvents {
		return
	}
	drop := len(b.evs) - (eventBufferMaxEvents - eventBufferTrimEvents)
	copy(b.evs, b.evs[drop:])
	clear(b.evs[len(b.evs)-drop:])
	b.evs = b.evs[:len(b.evs)-drop]
	b.droppedSinceReport += uint64(drop)
}

func (b *eventBuffer) takeDropped() uint64 {
	dropped := b.droppedSinceReport
	b.droppedSinceReport = 0
	return dropped
}

// drain empties the buffer and returns the events in emission (= seq) order.
func (b *eventBuffer) drain() []core.Event {
	out := b.evs
	b.evs = nil
	return out
}

// restore re-queues previously drained events at the FRONT of the buffer (their
// seqs predate anything buffered since). Used when a backup flush or a regen
// fails after draining, so the why-trace is retried instead of lost.
func (b *eventBuffer) restore(evs []core.Event) {
	b.evs = append(evs, b.evs...)
	b.enforceLimit()
}

// redisWriteAdapter wraps a *redis.Client for the write/append path. It satisfies both
// events.RedisClient (XAdd → events STREAM) and persist's goRedisClient (Set/Get/Del/
// HSet/Expire → live keyspace), so one client backs both the event stream and the live
// store. go-redis returns *Cmd values; the adapter unwraps them to the (value, error)
// shapes the interfaces expect, mapping redis.Nil (missing key) to a zero value + nil err.
type redisWriteAdapter struct{ c *redis.Client }

// eventStreamMaxLen caps the events STREAM length (approximate trim). AgentFrame carries
// per-agent render deltas, so the stream would otherwise grow
// unbounded; ~10k entries is ample backlog for SSE (which tails from "$").
const eventStreamMaxLen = 10_000

// XAdd returns the entry ID Redis assigned (XADD *) — the transport cursor
// the events.Emitter retains for the snapshot's stream_cursor.
func (a redisWriteAdapter) XAdd(ctx context.Context, stream string, values map[string]string) (string, error) {
	m := make(map[string]any, len(values))
	for k, v := range values {
		m[k] = v
	}
	return a.c.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		MaxLen: eventStreamMaxLen,
		Approx: true, // MAXLEN ~ N: cheap radix-tree-node-boundary trim
		Values: m,
	}).Result()
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

// StreamMaxDeletedID exposes XINFO STREAM's max-deleted-entry-id — the SSE
// handler's trim/gap check (api SPEC GET /sse). A missing key (fresh or
// regen-recreated stream) maps to "0-0": nothing after any cursor was lost.
func (a redisReadAdapter) StreamMaxDeletedID(ctx context.Context, key string) (string, error) {
	info, err := a.c.XInfoStream(ctx, key).Result()
	if err != nil {
		if err == redis.Nil || strings.Contains(err.Error(), "no such key") {
			return "0-0", nil
		}
		return "", err
	}
	if info.MaxDeletedEntryID == "" {
		return "0-0", nil
	}
	return info.MaxDeletedEntryID, nil
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
	// Resources are placed in the same COMPACT span as the agents (±6) so the
	// village is dense: agents reach food/water/shelter quickly AND stay within
	// each other's interaction radius, keeping social needs (Comfort) satisfiable.
	// Berry bushes (supply Satiety)
	for i := range 5 {
		id := core.ObjectID(fmt.Sprintf("berry_bush_%d", i))
		pos := core.Vec2{X: (r.Float64() - 0.5) * 12, Y: (r.Float64() - 0.5) * 12}
		w.PlaceObject(id, "berry_bush", pos, map[core.Dimension]float64{"Satiety": 0.4})
	}
	// Water sources (supply Hydration)
	for i := range 3 {
		id := core.ObjectID(fmt.Sprintf("water_source_%d", i))
		pos := core.Vec2{X: (r.Float64() - 0.5) * 12, Y: (r.Float64() - 0.5) * 12}
		w.PlaceObject(id, "water_source", pos, map[core.Dimension]float64{"Hydration": 0.5})
	}
	// Shelters (supply Rest)
	for i := range 2 {
		id := core.ObjectID(fmt.Sprintf("shelter_%d", i))
		pos := core.Vec2{X: (r.Float64() - 0.5) * 10, Y: (r.Float64() - 0.5) * 10}
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
// (AgentFrame, WorldFrame, SnapshotReady) is suppressed to keep the log readable.
type stderrLogger struct{}

func (l *stderrLogger) Emit(e core.Event) {
	switch e.Type {
	case events.TypeTickDone, events.TypeAgentFrame, events.TypeWorldFrame, events.TypeSnapshotReady:
		return // skip high-frequency housekeeping/render events
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
