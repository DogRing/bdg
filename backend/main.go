// Command sim is the entrypoint for the medieval village simulation.
//
// Usage:
//
//	go run ./backend -seed=1 -ticks=1440
//	go run ./backend -seed=42 -ticks=1440 -agents=5 -content=./content
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/gates"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/perception"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/spatial"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/values"
	"github.com/dogring/bdg/engine/tom"
	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/engine/worldtime"
	"gopkg.in/yaml.v3"
)

func main() {
	var (
		seed        = flag.Int64("seed", 1, "deterministic RNG seed")
		ticks       = flag.Int64("ticks", 1440, "ticks to run (default = 1 game-day)")
		runID       = flag.String("run", "dev", "run id (keyspace prefix)")
		agentCount  = flag.Int("agents", 3, "number of agents to spawn")
		contentDir  = flag.String("content", "./content", "path to content directory")
	)
	flag.Parse()

	fmt.Fprintf(os.Stderr, "medieval-sim  seed=%d ticks=%d run=%s agents=%d\n",
		*seed, *ticks, *runID, *agentCount)

	// ── 1. Load content ──────────────────────────────────────────────────────
	cd := *contentDir

	statsReg, err := loadStats(filepath.Join(cd, "stats.yaml"))
	fatal(err, "stats")

	needsReg, err := loadNeeds(filepath.Join(cd, "needs.yaml"), filepath.Join(cd, "balance.yaml"))
	fatal(err, "needs")

	actReg, err := loadActions(filepath.Join(cd, "actions.yaml"))
	fatal(err, "actions")

	gatesReg, err := loadGates(filepath.Join(cd, "gates.yaml"), statsReg)
	fatal(err, "gates")

	balanceBytes, err := os.ReadFile(filepath.Join(cd, "balance.yaml"))
	fatal(err, "balance.yaml read")

	valCfg, err := values.Load(openBytes(balanceBytes))
	fatal(err, "values")

	perceptCfg, err := perception.LoadConfig(openBytes(balanceBytes))
	fatal(err, "perception config")

	bal, err := parseBalance(balanceBytes)
	fatal(err, "balance parse")

	// ── 2. Build planner ─────────────────────────────────────────────────────
	plannerCfg := planner.PlannerConfig{
		Budget: planner.Budget{
			MaxDepth:   bal.Planner.Budget.MaxDepth,
			MaxActions: bal.Planner.Budget.MaxActions,
			MaxNodes:   bal.Planner.Budget.MaxNodes,
		},
		BaseHorizonTicks: bal.Planner.BaseHorizonTicks,
		UrgencyThreshold: bal.Planner.UrgencyThreshold,
		TagCosts:         tagCostMap(bal.Planner.TagCosts),
	}
	thePlanner := planner.New(actReg, gatesReg, needsReg, statsReg, plannerCfg)

	// ── 3. Build sensor ──────────────────────────────────────────────────────
	// The sensor needs the spatial hash; we give it the world's hash via the
	// WorldView interface — Sensor.Sight reads through WorldSnapshot, which
	// delegates to the hash. We create a dummy hash here for the Sensor; the
	// world owns the authoritative one that the WorldSnapshot wraps.
	dummyHash := spatial.New(bal.World.SpatialHashCell)
	sensor := perception.NewSensor(dummyHash, perceptCfg)

	// ── 4. Build agent config ────────────────────────────────────────────────
	agentCfg := agentConfigFromBalance(bal)

	// ── 5. Build world config & clock ────────────────────────────────────────
	worldCfg := world.Config{
		SpatialHashCell:       bal.World.SpatialHashCell,
		RelianceThreshold:     bal.World.RelianceThreshold,
		OutcomeDifficultyBase: bal.World.OutcomeDifficultyBase,
		BackupEveryTicks:      bal.World.BackupEveryTicks,
		MoveSpeedPerTick:      0.5,
	}

	wtCfg := worldtime.Config{
		TickMinutes:    int64(bal.World.TickMinutes),
		DayMinutes:     int64(bal.World.DayMinutes),
		DaysPerSeason:  30,
		SeasonsPerYear: 4,
	}
	clock, err := worldtime.NewClock(wtCfg)
	fatal(err, "clock")

	// ── 6. Assemble services ─────────────────────────────────────────────────
	svc := agent.Services{
		Sensor:  sensor,
		Planner: thePlanner,
		Values:  valCfg,
		Needs:   needsReg,
		Stats:   statsReg,
		Actions: actReg,
	}

	// ── 7. Create world with stderr JSON event logger ─────────────────────────
	rootRNG := rng.New(*seed)
	w := world.New(worldCfg, clock, rootRNG, svc, actReg, &stderrLogger{})

	// ── 8. Place objects ──────────────────────────────────────────────────────
	// Seed a handful of fixed resources near the village centre.
	placeObjects(w, rootRNG)

	// ── 9. Spawn agents ───────────────────────────────────────────────────────
	for i := range *agentCount {
		id := core.AgentID(fmt.Sprintf("agent_%02d", i))
		pos := core.Vec2{
			X: (rootRNG.Float64() - 0.5) * 2,
			Y: (rootRNG.Float64() - 0.5) * 2,
		}
		w.Spawn(id, pos, agentCfg, rng.New(*seed+int64(i)+1))
	}

	fmt.Fprintf(os.Stderr, "spawned %d agents, running %d ticks...\n", *agentCount, *ticks)

	// ── 10. Tick loop ─────────────────────────────────────────────────────────
	for range *ticks {
		w.Tick()
	}

	// ── 11. Snapshot output ───────────────────────────────────────────────────
	printSnapshot(w, *runID)
}

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

// ── Content loaders ───────────────────────────────────────────────────────────

func loadStats(path string) (*stats.Registry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return stats.Load(f)
}

func loadNeeds(needsPath, balancePath string) (*needs.Registry, error) {
	nf, err := os.Open(needsPath)
	if err != nil {
		return nil, err
	}
	defer nf.Close()
	bf, err := os.Open(balancePath)
	if err != nil {
		return nil, err
	}
	defer bf.Close()
	return needs.Load(nf, bf)
}

func loadActions(path string) (*actions.Registry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return actions.Load(f)
}

func loadGates(path string, statReg *stats.Registry) (*gates.Registry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return gates.Load(f, statReg)
}

// ── Balance YAML parsing ──────────────────────────────────────────────────────

type balanceDoc struct {
	World struct {
		TickMinutes           int     `yaml:"tick_minutes"`
		DayMinutes            int     `yaml:"day_minutes"`
		SpatialHashCell       float64 `yaml:"spatial_hash_cell"`
		RelianceThreshold     float64 `yaml:"reliance_threshold"`
		OutcomeDifficultyBase float64 `yaml:"outcome_difficulty_base"`
		BackupEveryTicks      int     `yaml:"backup_every_ticks"`
	} `yaml:"world"`
	Mood struct {
		Lambda   float64 `yaml:"lambda"`
		Decay    float64 `yaml:"decay"`
		Baseline float64 `yaml:"baseline"`
	} `yaml:"mood"`
	Adrenaline struct {
		TriggerUrgency      float64 `yaml:"trigger_urgency"`
		Surge               float64 `yaml:"surge"`
		Decay               float64 `yaml:"decay"`
		Max                 float64 `yaml:"max"`
		CrashStaminaPenalty float64 `yaml:"crash_stamina_penalty"`
	} `yaml:"adrenaline"`
	Stamina struct {
		Max           float64 `yaml:"max"`
		DrainPerEffort float64 `yaml:"drain_per_effort"`
		RegenRest     float64 `yaml:"regen_rest"`
		RegenSleep    float64 `yaml:"regen_sleep"`
	} `yaml:"stamina"`
	Urgency struct {
		FromDeficit   float64 `yaml:"from_deficit"`
		BudgetPenalty float64 `yaml:"budget_penalty"`
	} `yaml:"urgency"`
	SelfCalibration struct {
		Beta float64 `yaml:"beta"`
	} `yaml:"self_calibration"`
	Gossip struct {
		Alpha    float64 `yaml:"alpha"`
		MinTrust float64 `yaml:"min_trust"`
	} `yaml:"gossip"`
	Resentment struct {
		AffinityDrop    float64 `yaml:"affinity_drop"`
		AggressionDrift float64 `yaml:"aggression_drift"`
		PerTrigger      float64 `yaml:"per_trigger"`
		Threshold       float64 `yaml:"threshold"`
	} `yaml:"resentment"`
	Planning struct {
		BudgetBase            int     `yaml:"budget_base"`
		BudgetPerIntelligence int     `yaml:"budget_per_intelligence"`
		Stickiness            float64 `yaml:"stickiness"`
		GoalDeadband          float64 `yaml:"goal_deadband"`
	} `yaml:"planning"`
	Generation struct {
		InitialBeliefNoise float64 `yaml:"initial_belief_noise"`
	} `yaml:"generation"`
	Planner struct {
		Budget struct {
			MaxDepth   int `yaml:"max_depth"`
			MaxActions int `yaml:"max_actions"`
			MaxNodes   int `yaml:"max_nodes"`
		} `yaml:"budget"`
		BaseHorizonTicks int                `yaml:"base_horizon_ticks"`
		UrgencyThreshold float64            `yaml:"urgency_threshold"`
		TagCosts         map[string]float64 `yaml:"tag_costs"`
	} `yaml:"planner"`
	Coping struct {
		RebindMinIntelligence float64 `yaml:"rebind_min_intelligence"`
		ApathyFailStreak      int     `yaml:"apathy_fail_streak"`
		ApathyRecoverMood     float64 `yaml:"apathy_recover_mood"`
		ApathyBudgetPenalty   float64 `yaml:"apathy_budget_penalty"`
	} `yaml:"coping"`
	TagLevels struct {
		Effort map[string]float64 `yaml:"effort"`
	} `yaml:"tag_levels"`
}

func parseBalance(data []byte) (balanceDoc, error) {
	var doc balanceDoc
	// Use a plain decoder (not KnownFields) so unknown top-level keys (needs,
	// values, tag_levels, etc.) don't cause errors.
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return balanceDoc{}, fmt.Errorf("balance parse: %w", err)
	}
	return doc, nil
}

func tagCostMap(raw map[string]float64) map[core.Tag]float64 {
	out := make(map[core.Tag]float64, len(raw))
	for k, v := range raw {
		out[core.Tag(k)] = v
	}
	return out
}

func effortLevelMap(raw map[string]float64) map[core.Tag]float64 {
	out := make(map[core.Tag]float64, len(raw))
	for k, v := range raw {
		out[core.Tag(k)] = v
	}
	return out
}

func agentConfigFromBalance(bal balanceDoc) agent.Config {
	return agent.Config{
		Lambda:       bal.Mood.Lambda,
		MoodDecay:    bal.Mood.Decay,
		MoodBaseline: bal.Mood.Baseline,

		AdrTriggerUrgency: bal.Adrenaline.TriggerUrgency,
		AdrSurge:          bal.Adrenaline.Surge,
		AdrDecay:          bal.Adrenaline.Decay,
		AdrMax:            bal.Adrenaline.Max,

		StaminaMax:     bal.Stamina.Max,
		DrainPerEffort: bal.Stamina.DrainPerEffort,
		RegenRest:      bal.Stamina.RegenRest,
		RegenSleep:     bal.Stamina.RegenSleep,

		UrgencyFromDeficit: bal.Urgency.FromDeficit,
		BudgetPenalty:      bal.Urgency.BudgetPenalty,

		Beta: bal.SelfCalibration.Beta,

		AffinityDrop:    bal.Resentment.AffinityDrop,
		AggressionDrift: bal.Resentment.AggressionDrift,

		Stickiness:   bal.Planning.Stickiness,
		GoalDeadband: bal.Planning.GoalDeadband,

		BudgetBase:            bal.Planning.BudgetBase,
		BudgetPerIntelligence: bal.Planning.BudgetPerIntelligence,

		Rates: tom.Rates{
			Alpha:                   bal.Gossip.Alpha,
			Beta:                    bal.SelfCalibration.Beta,
			MinTrust:                bal.Gossip.MinTrust,
			InitialBeliefNoise:      bal.Generation.InitialBeliefNoise,
			TradeSuccessTrustDelta:  0.05,
			TradeRejectAffinityDrop: 0.02,
			FraudHonestyDrop:        0.10,
			FraudThreshold:          0.20,
		},

		// ── P3 additions ─────────────────────────────────────────────────────
		CrashStaminaPenalty: bal.Adrenaline.CrashStaminaPenalty,

		EffortLevels: effortLevelMap(bal.TagLevels.Effort),

		RebindMinIntelligence: bal.Coping.RebindMinIntelligence,
		ApathyFailStreak:      bal.Coping.ApathyFailStreak,
		ApathyRecoverMood:     bal.Coping.ApathyRecoverMood,
		ApathyBudgetPenalty:   bal.Coping.ApathyBudgetPenalty,

		ResentmentPerTrigger: bal.Resentment.PerTrigger,
		ResentmentThreshold:  bal.Resentment.Threshold,
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

func openBytes(data []byte) io.Reader { return bytes.NewReader(data) }

func fatal(err error, label string) {
	if err != nil {
		log.Fatalf("%s: %v", label, err)
	}
}
