// Command scalebench drives the ecosystem (flora+fauna+climate+scent, NO agents) from a
// worldgen fixture and reports where wall-clock time and memory go as the world scales
// (docs/plans/scaling.md P0). It is a dev instrument, not part of the engine: it imports
// worldgen (Load) + world (Tick) and prints per-phase timing live to stderr so a long run is
// observable while it runs.
//
//	go run ./tools/scalebench -fixture ./tools/worldgen/testdata/village_ecosystem.fixture.yaml -ticks 200
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"time"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/tools/worldgen"
	"github.com/dogring/bdg/platform/config"
)

func main() {
	fixture := flag.String("fixture", "./tools/worldgen/testdata/village_ecosystem.fixture.yaml", "world fixture to load")
	contentDir := flag.String("content", "./content", "content directory")
	ticks := flag.Int("ticks", 200, "ticks to run")
	cpuprofile := flag.String("cpuprofile", "", "write CPU profile to file (covers the tick loop only)")
	flag.Parse()

	logf := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "[%s] "+format+"\n", append([]any{time.Now().Format("15:04:05")}, a...)...)
	}

	t0 := time.Now()
	cfg, err := config.Load(*contentDir)
	fatal(err, "config.Load")
	logf("config.Load %v", time.Since(t0))

	schemaPath := filepath.Join(*contentDir, "schema", "fixture.schema.json")
	t0 = time.Now()
	fx, err := worldgen.ParseFile(*fixture, schemaPath)
	fatal(err, "ParseFile")
	logf("ParseFile %v", time.Since(t0))

	t0 = time.Now()
	w, err := worldgen.Load(fx, cfg)
	fatal(err, "Load")
	logf("Load (terrain gen + placement + world build) %v", time.Since(t0))

	animalsBy := map[core.Tag]int{}
	for _, a := range w.Animals() {
		animalsBy[a.Species]++
	}
	logf("animals loaded: %d total %v", len(w.Animals()), sortedCounts(animalsBy))
	memReport(logf, "post-Load")

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		fatal(err, "create cpuprofile")
		fatal(pprof.StartCPUProfile(f), "start cpuprofile")
		defer pprof.StopCPUProfile()
		logf("CPU profiling → %s", *cpuprofile)
	}

	var (
		total   time.Duration
		maxTick time.Duration
	)
	for i := 0; i < *ticks; i++ {
		ts := time.Now()
		w.Tick()
		d := time.Since(ts)
		total += d
		if d > maxTick {
			maxTick = d
		}
		if i < 10 || (i+1)%50 == 0 {
			logf("tick %d %v", i, d)
		}
	}
	logf("ticks done: %d in %v (avg %v, max %v)", *ticks, total, total/time.Duration(max(*ticks, 1)), maxTick)
	memReport(logf, "post-ticks")
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
