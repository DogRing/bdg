// Package config is the single content-loading + environment-parsing layer.
// It encapsulates the init logic currently scattered in main.go behind two
// entry points: Load (content dir) and ParseEnv (env vars). It adds JSON-Schema
// validation that main.go did not do, plus cross-file referential-integrity checks.
//
// Determinism:
//   - Load uses no wall-clock, no global rand, no map-iteration for logic (D12).
//   - All RNG-free: schema validation, YAML parsing, struct copying.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/gates"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/perception"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/values"
	"gopkg.in/yaml.v3"
)

// ── LoadOutput ────────────────────────────────────────────────────────────────

// LoadOutput is the immutable bundle returned by Load. The caller (main / platform)
// owns it; Load never retains or mutates it after returning. Load is deterministic:
// given the same directory contents, two calls produce identical output.
type LoadOutput struct {
	StatsRegistry   *stats.Registry
	NeedsRegistry   *needs.Registry
	ActionsRegistry *actions.Registry
	GatesRegistry   *gates.Registry
	ValuesConfig    *values.Config
	PerceptionConfig perception.PerceptionConfig
	Balance         Balance // parsed balance.yaml, exposed as typed fields

	// configHash is the SHA-256 of the canonical content fingerprint.
	configHash string
}

// ConfigHash returns the SHA-256 (hex) of the canonical content fingerprint:
// the raw bytes of the loaded YAML files concatenated in FIXED lexicographic
// filename order, each prefixed by its filename. Deterministic across processes
// given identical file contents (no path, no mtime, no map iteration).
func (o *LoadOutput) ConfigHash() string { return o.configHash }

// ── Balance ───────────────────────────────────────────────────────────────────

// Balance holds every field from content/balance.yaml, mirroring the private
// balanceDoc struct in main.go (lines 473–568). One flat struct with exported
// fields; callers read values directly rather than through accessor methods.
type Balance struct {
	World struct {
		TickMinutes           int     `yaml:"tick_minutes"`
		DayMinutes            int     `yaml:"day_minutes"`
		SpatialHashCell       float64 `yaml:"spatial_hash_cell"`
		RelianceThreshold     float64 `yaml:"reliance_threshold"`
		OutcomeDifficultyBase float64 `yaml:"outcome_difficulty_base"`
		BackupEveryTicks      int     `yaml:"backup_every_ticks"`
		PlanInterval          int     `yaml:"plan_interval"`
		PruneThreshold        int     `yaml:"prune_threshold"`
		ArrivalEpsilon        float64 `yaml:"arrival_epsilon"` // locomotion completes when within this distance of Intent.Move; 0 ⇒ default 1.0
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
		Max            float64 `yaml:"max"`
		DrainPerEffort float64 `yaml:"drain_per_effort"`
		RegenRest      float64 `yaml:"regen_rest"`
		RegenSleep     float64 `yaml:"regen_sleep"`
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

	Trade struct {
		ClaimInflateMin float64 `yaml:"claim_inflate_min"`
		ClaimInflateMax float64 `yaml:"claim_inflate_max"`
	} `yaml:"trade"`

	Social struct {
		BondAffinityGain float64 `yaml:"bond_affinity_gain"`
		MinCareThreshold float64 `yaml:"min_care_threshold"`
	} `yaml:"social"`

	Threats struct {
		SafetyThreatThreshold float64   `yaml:"safety_threat_threshold"`
		PerThreatIntensity    float64   `yaml:"per_threat_intensity"`
		SafetyDecay           float64   `yaml:"safety_decay"`
		HostileTags           []string  `yaml:"hostile_tags"`
	} `yaml:"threats"`

	Politics struct {
		RoleConvergenceThreshold float64 `yaml:"role_convergence_threshold"`
		InfluenceWeight          float64 `yaml:"influence_weight"`
		RelyCostThreshold        float64 `yaml:"rely_cost_threshold"`
		RelyOnDelta              float64 `yaml:"relyon_delta"`
		VoteRelyThreshold        float64 `yaml:"vote_rely_threshold"`
		VoteUrgencyThreshold     float64 `yaml:"vote_urgency_threshold"`
		VoteRelyOnDelta          float64 `yaml:"vote_relyon_delta"`
	} `yaml:"politics"`

	// Needs holds per-Dimension rate constants from balance.yaml's needs: block.
	// Keys are canonical Dimension IDs (Satiety, Hydration, Rest, Social, Comfort).
	Needs map[string]NeedRateConfig `yaml:"needs"`

	// Perception holds the perception radii from balance.yaml's perception: block.
	Perception struct {
		SightRadius   float64 `yaml:"sight_radius"`
		SmellRadius   float64 `yaml:"smell_radius"`
		HearingRadius float64 `yaml:"hearing_radius"`
	} `yaml:"perception"`

	// Intelligence thresholds from balance.yaml's intelligence: block.
	Intelligence struct {
		LookaheadThreshold  float64 `yaml:"lookahead_threshold"`
		RebindThreshold     float64 `yaml:"rebind_threshold"`
		OtherIntelThreshold float64 `yaml:"other_intel_threshold"`
	} `yaml:"intelligence"`

	// Values block from balance.yaml.
	Values struct {
		Weights                   map[string]float64 `yaml:"weights"`
		CollectiveAggregationMode string             `yaml:"collective_aggregation_mode"`
	} `yaml:"values"`

	// Gates thresholds from balance.yaml's gates: block.
	Gates struct {
		StaminaEffortHighThreshold float64 `yaml:"stamina_effort_high_threshold"`
		ApathyMoodThreshold        float64 `yaml:"apathy_mood_threshold"`
		ConscienceUrgencyThreshold float64 `yaml:"conscience_urgency_threshold"`
		AdrenalineCostMultiplier   float64 `yaml:"adrenaline_cost_multiplier"`
	} `yaml:"gates"`

	// ForwardSim from balance.yaml.
	ForwardSim struct {
		HorizonMinutes          int `yaml:"horizon_minutes"`
		HorizonPerIntelligence  int `yaml:"horizon_per_intelligence"`
		StepMinutes             int `yaml:"step_minutes"`
	} `yaml:"forward_sim"`

	// Salience from balance.yaml.
	Salience struct {
		ProximityGain    float64 `yaml:"proximity_gain"`
		ProximityFalloff float64 `yaml:"proximity_falloff"`
	} `yaml:"salience"`

	// Regen from balance.yaml.
	Regen struct {
		BerryBush   int `yaml:"berry_bush"`
		PreyRespawn int `yaml:"prey_respawn"`
	} `yaml:"regen"`
}

// NeedRateConfig holds per-need rate constants from balance.yaml.
type NeedRateConfig struct {
	DecayPerTick          float64 `yaml:"decay_per_tick"`
	SatisfactionThreshold float64 `yaml:"satisfaction_threshold"`
}

// ── Load ──────────────────────────────────────────────────────────────────────

// Load reads every content YAML from contentDir, validates each against its
// JSON Schema, builds all engine registries, and returns the populated LoadOutput.
// It performs no IO beyond reading the listed files — no network, no wall-clock, no rand.
//
// Pipeline (in order):
//  1. Read stats.yaml, needs.yaml, actions.yaml, gates.yaml, balance.yaml.
//  2. Schema-validate each against contentDir/schema/<file>.schema.json.
//  3. Build stats.Registry via stats.Load.
//  4. Build gates.Registry via gates.Load (needs stats.Registry).
//  5. Build actions.Registry via actions.Load.
//  6. Build needs.Registry via needs.Load (merges needs.yaml + balance.yaml needs: block).
//  7. Build values.Config via values.Load (reads balance.yaml's values: block).
//  8. Build perception.PerceptionConfig via perception.LoadConfig.
//  9. Parse balance.yaml into Balance struct.
//  10. Compute ConfigHash.
func Load(contentDir string) (*LoadOutput, error) {
	// ── 1. Read all content files ────────────────────────────────────────────────
	paths := map[string]string{
		"stats.yaml":   filepath.Join(contentDir, "stats.yaml"),
		"needs.yaml":   filepath.Join(contentDir, "needs.yaml"),
		"actions.yaml": filepath.Join(contentDir, "actions.yaml"),
		"gates.yaml":   filepath.Join(contentDir, "gates.yaml"),
		"balance.yaml": filepath.Join(contentDir, "balance.yaml"),
	}

	raw := make(map[string][]byte, len(paths))
	// Read files in deterministic order (D12): sorted filenames.
	filenames := make([]string, 0, len(paths))
	for fn := range paths {
		filenames = append(filenames, fn)
	}
	sort.Strings(filenames)

	for _, fn := range filenames {
		data, err := os.ReadFile(paths[fn])
		if err != nil {
			return nil, fmt.Errorf("config: read %s: %w", fn, err)
		}
		raw[fn] = data
	}

	// ── 2. Schema-validate each file ────────────────────────────────────────────
	schemaDir := filepath.Join(contentDir, "schema")
	schemaFiles := map[string]string{
		"stats.yaml":   filepath.Join(schemaDir, "stats.schema.json"),
		"needs.yaml":   filepath.Join(schemaDir, "needs.schema.json"),
		"actions.yaml": filepath.Join(schemaDir, "actions.schema.json"),
		"gates.yaml":   filepath.Join(schemaDir, "gates.schema.json"),
		"balance.yaml": filepath.Join(schemaDir, "balance.schema.json"),
	}

	for _, fn := range filenames {
		schemaPath, ok := schemaFiles[fn]
		if !ok {
			continue
		}
		schemaData, err := os.ReadFile(schemaPath)
		if err != nil {
			return nil, fmt.Errorf("config: read schema %s: %w", schemaPath, err)
		}
		if err := validateYAML(raw[fn], schemaData, fn); err != nil {
			return nil, fmt.Errorf("config: schema %s: %w", fn, err)
		}
	}

	// ── 3. Build engine registries (in dependency order) ─────────────────────────

	// stats.Registry — no dependencies.
	statReg, err := stats.Load(bytes.NewReader(raw["stats.yaml"]))
	if err != nil {
		return nil, fmt.Errorf("config: stats.Load: %w", err)
	}

	// gates.Registry — needs stats.Registry.
	gateReg, err := gates.Load(bytes.NewReader(raw["gates.yaml"]), statReg)
	if err != nil {
		return nil, fmt.Errorf("config: gates.Load: %w", err)
	}

	// actions.Registry — no dependencies.
	actReg, err := actions.Load(bytes.NewReader(raw["actions.yaml"]))
	if err != nil {
		return nil, fmt.Errorf("config: actions.Load: %w", err)
	}

	// needs.Registry — needs needs.yaml + balance.yaml needs: block.
	needReg, err := needs.Load(
		bytes.NewReader(raw["needs.yaml"]),
		bytes.NewReader(raw["balance.yaml"]),
	)
	if err != nil {
		return nil, fmt.Errorf("config: needs.Load: %w", err)
	}

	// ── 4. Build values config ───────────────────────────────────────────────────
	valCfg, err := values.Load(bytes.NewReader(raw["balance.yaml"]))
	if err != nil {
		return nil, fmt.Errorf("config: values.Load: %w", err)
	}

	// ── 5. Build perception config ───────────────────────────────────────────────
	perceptCfg, err := perception.LoadConfig(bytes.NewReader(raw["balance.yaml"]))
	if err != nil {
		return nil, fmt.Errorf("config: perception.LoadConfig: %w", err)
	}

	// ── 6. Parse balance.yaml into typed Balance struct ──────────────────────────
	var bal Balance
	if err := yaml.Unmarshal(raw["balance.yaml"], &bal); err != nil {
		return nil, fmt.Errorf("config: balance parse: %w", err)
	}

	// ── 7. Compute ConfigHash ────────────────────────────────────────────────────
	hash, err := computeConfigHash(raw)
	if err != nil {
		return nil, fmt.Errorf("config: hash: %w", err)
	}

	return &LoadOutput{
		StatsRegistry:     statReg,
		NeedsRegistry:     needReg,
		ActionsRegistry:   actReg,
		GatesRegistry:     gateReg,
		ValuesConfig:      valCfg,
		PerceptionConfig:  perceptCfg,
		Balance:           bal,
		configHash:        hash,
	}, nil
}

// ── Hash ───────────────────────────────────────────────────────────────────────

// computeConfigHash returns the SHA-256 hex of the raw content bytes
// concatenated in fixed lexicographic filename order, each prefixed by
// "filename:\n". Deterministic and path-independent.
func computeConfigHash(raw map[string][]byte) (string, error) {
	h := sha256.New()

	// Deterministic order: sorted filenames (D12).
	fns := make([]string, 0, len(raw))
	for fn := range raw {
		fns = append(fns, fn)
	}
	sort.Strings(fns)

	for _, fn := range fns {
		data, ok := raw[fn]
		if !ok {
			return "", fmt.Errorf("missing data for %s", fn)
		}
		// Prefix with filename so hash changes when a file is renamed.
		if _, err := io.WriteString(h, fn); err != nil {
			return "", err
		}
		if _, err := h.Write([]byte{'\n'}); err != nil {
			return "", err
		}
		if _, err := h.Write(data); err != nil {
			return "", err
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
