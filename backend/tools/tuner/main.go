package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/perception"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/spatial"
	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/engine/worldtime"
	"github.com/dogring/bdg/platform/config"
)

// ── Types for output JSON ───────────────────────────────────────────────────

// GridPoint holds one parameter combination.
type GridPoint struct {
	GossipAlpha                float64 `json:"gossip_alpha"`
	ConscienceUrgencyThreshold float64 `json:"conscience_urgency_threshold"`
}

// GridResult holds all seed results for one grid point plus the average.
type GridResult struct {
	Params GridPoint    `json:"params"`
	Seeds  []SeedResult `json:"seeds"`
	Avg    SeedResult   `json:"avg"`
}

// aggregatedSeedResult builds the averaged SeedResult from a slice.
func aggregatedSeedResult(seeds []SeedResult) SeedResult {
	if len(seeds) == 0 {
		return SeedResult{}
	}
	var sumCrime, sumRepVar, sumSafety, sumStarv float64
	var sumTotal, sumTransg int
	var roleCount int
	for _, s := range seeds {
		sumCrime += s.CrimeRate
		sumRepVar += s.ReputationVariance
		sumSafety += s.SafetyMean
		sumStarv += s.StarvationConcentration
		sumTotal += s.TotalActions
		sumTransg += s.TransgressiveActions
		if s.RoleConvergence {
			roleCount++
		}
	}
	n := float64(len(seeds))
	return SeedResult{
		CrimeRate:               round4(sumCrime / n),
		ReputationVariance:      round4(sumRepVar / n),
		RoleConvergence:         roleCount > len(seeds)/2, // majority vote
		SafetyMean:              round4(sumSafety / n),
		StarvationConcentration: round4(sumStarv / n),
		TotalActions:            sumTotal / len(seeds),
		TransgressiveActions:    sumTransg / len(seeds),
	}
}

// ── Grid definition ─────────────────────────────────────────────────────────

// defaultAlphas are the gossip_alpha values to sweep.
var defaultAlphas = []float64{0.05, 0.1, 0.2}

// defaultThresholds are the conscience_urgency_threshold values to sweep.
var defaultThresholds = []float64{0.5, 0.6, 0.7, 0.8}

// defaultSeeds are the seeds to run for each grid point.
var defaultSeeds = []int64{1, 42, 123}

// ── CLI flags ───────────────────────────────────────────────────────────────

type flags struct {
	contentDir string
	ticks      int
	seeds      string
	alphas     string
	thresholds string
	output     string
}

func parseFlags() flags {
	var f flags
	flag.StringVar(&f.contentDir, "content", "./content", "path to content directory")
	flag.IntVar(&f.ticks, "ticks", 720, "ticks to run per seed")
	flag.StringVar(&f.seeds, "seeds", "1,42,123", "comma-separated seed list")
	flag.StringVar(&f.alphas, "alphas", "0.05,0.1,0.2", "comma-separated gossip_alpha values")
	flag.StringVar(&f.thresholds, "thresholds", "0.5,0.6,0.7,0.8", "comma-separated conscience_urgency_threshold values")
	flag.StringVar(&f.output, "output", "tools/tuner/results.json", "output path")
	flag.Parse()
	return f
}

func parseFloats(s string) []float64 {
	parts := strings.Split(s, ",")
	out := make([]float64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping unparseable float %q: %v\n", p, err)
			continue
		}
		out = append(out, v)
	}
	return out
}

func parseInts(s string) []int64 {
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping unparseable seed %q: %v\n", p, err)
			continue
		}
		out = append(out, v)
	}
	return out
}

// ── Main ────────────────────────────────────────────────────────────────────

func main() {
	f := parseFlags()

	// Parse parameter lists.
	seeds := parseInts(f.seeds)
	if len(seeds) == 0 {
		seeds = defaultSeeds
	}
	alphas := parseFloats(f.alphas)
	if len(alphas) == 0 {
		alphas = defaultAlphas
	}
	thresholds := parseFloats(f.thresholds)
	if len(thresholds) == 0 {
		thresholds = defaultThresholds
	}

	fmt.Fprintf(os.Stderr, "tuner: grid %d alphas × %d thresholds × %d seeds = %d runs\n",
		len(alphas), len(thresholds), len(seeds), len(alphas)*len(thresholds)*len(seeds))
	fmt.Fprintf(os.Stderr, "tuner: ticks=%d  content=%s\n", f.ticks, f.contentDir)

	// Resolve absolute output path.
	outputPath := f.output

	// ── 1. Load base content ───────────────────────────────────────────────
	cfg, err := config.Load(f.contentDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: config.Load: %v\n", err)
		os.Exit(1)
	}

	// Build once-shared services (registries don't depend on runtime params).
	thePlanner := planner.New(
		cfg.ActionsRegistry, cfg.GatesRegistry, cfg.NeedsRegistry,
		cfg.StatsRegistry, cfg.Balance.PlannerConfig(),
	)
	dummyHash := spatial.New(cfg.Balance.World.SpatialHashCell)
	sensor := perception.NewSensor(dummyHash, cfg.PerceptionConfig)

	// ── 2. Grid search loop ───────────────────────────────────────────────
	var results []GridResult

	for _, alpha := range alphas {
		for _, threshold := range thresholds {
			fmt.Fprintf(os.Stderr, "tuner: grid point gossip_alpha=%.3f conscience_urgency=%.3f\n",
				alpha, threshold)

			var seedResults []SeedResult
			for _, seed := range seeds {
				sr := runOne(cfg, thePlanner, sensor, alpha, threshold, seed, f.ticks)
				seedResults = append(seedResults, sr)
				fmt.Fprintf(os.Stderr, "tuner:   seed %d done (crime=%.4f, rep_var=%.4f, safety=%.4f, total_actions=%d)\n",
					seed, sr.CrimeRate, sr.ReputationVariance, sr.SafetyMean, sr.TotalActions)
			}

			results = append(results, GridResult{
				Params: GridPoint{
					GossipAlpha:                alpha,
					ConscienceUrgencyThreshold: threshold,
				},
				Seeds: seedResults,
				Avg:   aggregatedSeedResult(seedResults),
			})
		}
	}

	// ── 3. Write output ───────────────────────────────────────────────────
	out, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: json marshal: %v\n", err)
		os.Exit(1)
	}

	// Ensure output directory exists.
	outDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: mkdir %s: %v\n", outDir, err)
		os.Exit(1)
	}

	if err := os.WriteFile(outputPath, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: write %s: %v\n", outputPath, err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "tuner: wrote %s (%d grid points)\n", outputPath, len(results))
}

// runOne runs the simulation for one parameter combination and one seed.
func runOne(
	cfg *config.LoadOutput,
	planner *planner.Planner,
	sensor *perception.Sensor,
	gossipAlpha float64,
	conscienceUrgencyThreshold float64,
	seed int64,
	ticks int,
) SeedResult {
	// Clone the balance config (it's a value type) and override runtime params.
	bal := cfg.Balance
	bal.Gossip.Alpha = gossipAlpha
	bal.Gates.ConscienceUrgencyThreshold = conscienceUrgencyThreshold

	// Build agent config from the modified balance.
	agentCfg := bal.AgentConfig(cfg.NeedsRegistry)

	// Build world config and clock.
	worldCfg := bal.WorldConfig()
	clock, err := worldtime.NewClock(bal.ClockConfig())
	if err != nil {
		// Fall back to default clock config if the balance one is invalid.
		clock, _ = worldtime.NewClock(worldtime.Config{
			TickMinutes:    1,
			DayMinutes:     1440,
			DaysPerSeason:  30,
			SeasonsPerYear: 4,
		})
	}

	// Assemble services.
	svc := agent.Services{
		Sensor:  sensor,
		Planner: planner,
		Values:  cfg.ValuesConfig,
		Needs:   cfg.NeedsRegistry,
		Stats:   cfg.StatsRegistry,
		Actions: cfg.ActionsRegistry,
	}

	// Create world with a counting emitter (headless).
	rootRNG := rng.New(seed)
	emitter := newCountingEmitter()
	w := world.New(worldCfg, clock, rootRNG, svc, cfg.ActionsRegistry, emitter)

	// Place deterministic objects (fixed positions, not random).
	placeTunerObjects(w)

	// Spawn agents with deterministic seeds.
	agentCount := 12
	for i := range agentCount {
		id := core.AgentID(fmt.Sprintf("agent_%02d", i))
		// Spread agents in a rough circle around the origin.
		angle := float64(i) * 3.14159 * 2 / float64(agentCount)
		pos := core.Vec2{
			X: 5.0 + 3.0*float64Cos(angle),
			Y: 5.0 + 3.0*float64Sin(angle),
		}
		agentSeed := seed + int64(i) + 1
		w.Spawn(id, pos, agentCfg, rng.New(agentSeed))
	}

	// Run N ticks.
	for range ticks {
		w.Tick()
	}

	// Collect metrics.
	result := collectMetrics(w, emitter)
	result.Seed = seed
	return result
}

// ── Object placement (deterministic, no RNG) ────────────────────────────────

func placeTunerObjects(w *world.World) {
	// Berry bushes (supply Satiety) in fixed positions.
	bushPositions := []core.Vec2{
		{X: 0, Y: 0},
		{X: 8, Y: 2},
		{X: 2, Y: 8},
		{X: 10, Y: 10},
		{X: -2, Y: -2},
	}
	for i, pos := range bushPositions {
		id := core.ObjectID(fmt.Sprintf("berry_bush_%d", i))
		w.PlaceObject(id, "berry_bush", pos, map[core.Dimension]float64{"Satiety": 0.4})
	}

	// Water sources (supply Hydration) in fixed positions.
	waterPositions := []core.Vec2{
		{X: 4, Y: 4},
		{X: 7, Y: 7},
		{X: 1, Y: 5},
	}
	for i, pos := range waterPositions {
		id := core.ObjectID(fmt.Sprintf("water_source_%d", i))
		w.PlaceObject(id, "water_source", pos, map[core.Dimension]float64{"Hydration": 0.5})
	}

	// Shelters (supply Rest) in fixed positions.
	shelterPositions := []core.Vec2{
		{X: 3, Y: 3},
		{X: 6, Y: 6},
	}
	for i, pos := range shelterPositions {
		id := core.ObjectID(fmt.Sprintf("shelter_%d", i))
		w.PlaceObject(id, "shelter", pos, map[core.Dimension]float64{"Rest": 0.6})
	}
}

// ── Math helpers (avoid importing math for just Cos/Sin) ─────────────────────

func float64Cos(angle float64) float64 {
	// Basic cosine via Taylor series (4 terms, good enough for positioning).
	a2 := angle * angle
	a4 := a2 * a2
	a6 := a4 * a2
	a8 := a6 * a2
	return 1 - a2/2 + a4/24 - a6/720 + a8/40320
}

func float64Sin(angle float64) float64 {
	// Basic sine via Taylor series.
	a2 := angle * angle
	a3 := angle * a2
	a5 := a3 * a2
	a7 := a5 * a2
	a9 := a7 * a2
	return angle - a3/6 + a5/120 - a7/5040 + a9/362880
}
