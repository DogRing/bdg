// Command sim is the entrypoint for the medieval village simulation.
//
// Scaffold only: it compiles with no engine dependencies. Wiring
// (config -> registries -> world -> deterministic tick loop) is added as
// modules land. See docs/architecture.md (build order) and
// docs/data-contracts.md (persistence).
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	var (
		seed  = flag.Int64("seed", 1, "deterministic RNG seed")
		ticks = flag.Int64("ticks", 0, "ticks to run (0 = scaffold no-op)")
		runID = flag.String("run", "dev", "run id (Redis/Postgres keyspace)")
	)
	flag.Parse()

	fmt.Printf("medieval-sim  seed=%d ticks=%d run=%s\n", *seed, *ticks, *runID)

	// TODO(world, config): load content registries, build the world, and run a
	// deterministic tick loop (read -> plan -> collect intents -> apply in ID order).
	if *ticks == 0 {
		fmt.Println("no engine wired yet — scaffold only. exiting.")
		os.Exit(0)
	}
	fmt.Println("engine not implemented; see backend/engine/*/SPEC.md")
}