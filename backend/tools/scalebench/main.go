// Command scalebench drives the ecosystem (flora+fauna+climate+scent, NO agents) from a
// worldgen fixture and reports where wall-clock time and memory go as the world scales
// (docs/plans/scaling.md P0). It is a dev instrument, not part of the engine: it imports
// worldgen (Load) + world (Tick).
//
// Single run (verbose, live per-phase timing to stderr):
//
//	go run ./tools/scalebench -fixture <f> -content <dir> -ticks 200 [-cpuprofile p]
//
// Parametric sweep (SC2): re-run the SAME fixture (densities/templates kept) over a list of
// square world sizes, one CSV row per size to stdout — the tick-time/memory-vs-area curve:
//
//	go run ./tools/scalebench -fixture <f> -content <dir> -sweep 500,1000,2000,3000,4000,6000
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/tools/worldgen"
	"github.com/dogring/bdg/platform/config"
)

func main() {
	fixture := flag.String("fixture", "./tools/worldgen/testdata/village_ecosystem.fixture.yaml", "world fixture to load")
	contentDir := flag.String("content", "./content", "content directory")
	ticks := flag.Int("ticks", 200, "ticks to run per world")
	warmup := flag.Int("warmup", 5, "leading ticks excluded from the tick average (cover-index build + GC settle)")
	sweep := flag.String("sweep", "", "comma-separated square world sizes (e.g. 500,1000,2000); empty ⇒ single verbose run")
	bounds := flag.Float64("bounds", 0, "override the fixture to a square world of this side (single-run mode); 0 ⇒ keep fixture bounds")
	cpuprofile := flag.String("cpuprofile", "", "write CPU profile of the tick loop (single-run mode only)")
	flag.Parse()

	logf := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "[%s] "+format+"\n", append([]any{time.Now().Format("15:04:05")}, a...)...)
	}

	cfg, err := config.Load(*contentDir)
	fatal(err, "config.Load")
	schemaPath := filepath.Join(*contentDir, "schema", "fixture.schema.json")
	fx, err := worldgen.ParseFile(*fixture, schemaPath)
	fatal(err, "ParseFile")

	if *sweep == "" {
		if *bounds > 0 {
			fx.Bounds = &worldgen.Bounds{Min: worldgen.Vec2{0, 0}, Max: worldgen.Vec2{*bounds, *bounds}}
		}
		singleRun(cfg, fx, *ticks, *warmup, *cpuprofile, logf)
		return
	}
	runSweep(cfg, fx, parseSizes(*sweep), *ticks, *warmup, logf)
}

// metrics is one (world-size) sample of the scaling curve.
type metrics struct {
	size          float64
	flora         int
	animals       int
	loadMs        float64
	tickAvgMs     float64
	tickMaxMs     float64
	heapMB, sysMB uint64
}

// runWorld loads fx (bounds already set) and ticks it, timing Load + each tick. The first
// `warmup` ticks are excluded from the average (tick 0 builds the cover index and GC settles);
// tickMax still reflects the worst tick (e.g. a flora-Step spike).
func runWorld(cfg *config.LoadOutput, fx worldgen.Fixture, ticks, warmup int) metrics {
	loadStart := time.Now()
	w, err := worldgen.Load(fx, cfg)
	fatal(err, "Load")
	loadMs := float64(time.Since(loadStart).Microseconds()) / 1000.0

	var total, maxTick time.Duration
	measured := 0
	for i := 0; i < ticks; i++ {
		ts := time.Now()
		w.Tick()
		d := time.Since(ts)
		if d > maxTick {
			maxTick = d
		}
		if i >= warmup {
			total += d
			measured++
		}
	}
	if measured == 0 {
		measured = 1
	}
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	sz := fx.Bounds.Max[0] - fx.Bounds.Min[0]
	return metrics{
		size:      sz,
		flora:     w.FloraCount(),
		animals:   len(w.Animals()),
		loadMs:    loadMs,
		tickAvgMs: float64(total.Microseconds()) / float64(measured) / 1000.0,
		tickMaxMs: float64(maxTick.Microseconds()) / 1000.0,
		heapMB:    ms.HeapAlloc / 1024 / 1024,
		sysMB:     ms.Sys / 1024 / 1024,
	}
}

func runSweep(cfg *config.LoadOutput, fx worldgen.Fixture, sizes []float64, ticks, warmup int, logf func(string, ...any)) {
	fmt.Println("size,flora,animals,load_ms,tick_avg_ms,tick_max_ms,heap_mb,sys_mb")
	for _, s := range sizes {
		logf("sweep %g² … (Load + %d ticks)", s, ticks)
		run := fx // value copy; only Bounds is replaced (density maps/templates shared, read-only in Load)
		run.Bounds = &worldgen.Bounds{Min: worldgen.Vec2{0, 0}, Max: worldgen.Vec2{s, s}}
		m := runWorld(cfg, run, ticks, warmup)
		fmt.Printf("%g,%d,%d,%.0f,%.2f,%.2f,%d,%d\n",
			m.size, m.flora, m.animals, m.loadMs, m.tickAvgMs, m.tickMaxMs, m.heapMB, m.sysMB)
		logf("  → flora=%d animals=%d load=%.1fs tick avg=%.1fms max=%.1fms heap=%dMB",
			m.flora, m.animals, m.loadMs/1000, m.tickAvgMs, m.tickMaxMs, m.heapMB)
	}
}

func singleRun(cfg *config.LoadOutput, fx worldgen.Fixture, ticks, warmup int, cpuprofile string, logf func(string, ...any)) {
	t0 := time.Now()
	w, err := worldgen.Load(fx, cfg)
	fatal(err, "Load")
	logf("Load (terrain gen + placement + world build) %v", time.Since(t0))

	animalsBy := map[core.Tag]int{}
	for _, a := range w.Animals() {
		animalsBy[a.Species]++
	}
	logf("flora=%d | animals=%d %s", w.FloraCount(), len(w.Animals()), sortedCounts(animalsBy))
	memReport(logf, "post-Load")

	if cpuprofile != "" {
		f, err := os.Create(cpuprofile)
		fatal(err, "create cpuprofile")
		fatal(pprof.StartCPUProfile(f), "start cpuprofile")
		defer pprof.StopCPUProfile()
		logf("CPU profiling → %s", cpuprofile)
	}

	var total, maxTick time.Duration
	measured := 0
	for i := 0; i < ticks; i++ {
		ts := time.Now()
		w.Tick()
		d := time.Since(ts)
		if d > maxTick {
			maxTick = d
		}
		if i >= warmup {
			total += d
			measured++
		}
		if i < 10 || (i+1)%50 == 0 {
			logf("tick %d %v", i, d)
		}
	}
	if measured == 0 {
		measured = 1
	}
	logf("ticks done: %d (avg over %d post-warmup %v, max %v)", ticks, measured, total/time.Duration(measured), maxTick)
	memReport(logf, "post-ticks")
}

func parseSizes(s string) []float64 {
	var out []float64
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		v, err := strconv.ParseFloat(tok, 64)
		fatal(err, "parse -sweep size "+tok)
		out = append(out, v)
	}
	if len(out) == 0 {
		fatal(fmt.Errorf("no sizes"), "-sweep")
	}
	return out
}

func memReport(logf func(string, ...any), label string) {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	logf("mem %s: heap %d MB, sys %d MB, numGC %d", label, ms.HeapAlloc/1024/1024, ms.Sys/1024/1024, ms.NumGC)
}

func sortedCounts(m map[core.Tag]int) string {
	keys := make([]core.Tag, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	s := ""
	for _, k := range keys {
		s += fmt.Sprintf("%s=%d ", k, m[k])
	}
	return s
}

func fatal(err error, ctx string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", ctx, err)
		os.Exit(1)
	}
}
