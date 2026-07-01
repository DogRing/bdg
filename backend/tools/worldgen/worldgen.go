package worldgen

import (
	"fmt"
	"math"
	"sort"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/kernel/worldtime"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/engine/space/spatial"
	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/platform/config"
)

type Fixture struct {
	SchemaVersion   int                         `yaml:"schema_version"`
	Seed            int64                       `yaml:"seed"`
	Bounds          *Bounds                     `yaml:"bounds,omitempty"`
	Terrain         *TerrainLayout              `yaml:"terrain,omitempty"`
	Objects         []ObjectPlacement           `yaml:"objects,omitempty"`
	Agents          []AgentPlacement            `yaml:"agents,omitempty"`
	Animals         []AnimalPlacement           `yaml:"animals,omitempty"`
	AnimalTemplates map[core.Tag]AnimalTemplate `yaml:"animal_templates,omitempty"`
	Flora           []FloraPlacement            `yaml:"flora,omitempty"`
	Lots            []LotPlacement              `yaml:"lots,omitempty"`
}

type Bounds struct {
	Min Vec2 `yaml:"min"`
	Max Vec2 `yaml:"max"`
}

type TerrainLayout struct {
	Cols  int        `yaml:"cols"`
	Rows  int        `yaml:"rows"`
	Cells []core.Tag `yaml:"cells"`
}

type ObjectPlacement struct {
	ID        core.ObjectID `yaml:"id"`
	Kind      core.Tag      `yaml:"kind"`
	Pos       Vec2          `yaml:"pos"`
	Remaining int           `yaml:"remaining,omitempty"`
	Owner     core.AgentID  `yaml:"owner,omitempty"`
}

type AgentPlacement struct {
	ID     core.AgentID               `yaml:"id"`
	Pos    Vec2                       `yaml:"pos"`
	Values map[core.Dimension]float64 `yaml:"values,omitempty"`
}

type AnimalPlacement struct {
	ID      core.ObjectID `yaml:"id"`
	Species core.Tag      `yaml:"species"`
	Pos     Vec2          `yaml:"pos"`
	Heading float64       `yaml:"heading,omitempty"`
}

type AnimalTemplate struct {
	Stats         map[core.StatID]float64   `yaml:"stats"`
	Drives        map[fauna.DriveID]float64 `yaml:"drives,omitempty"`
	Stamina       float64                   `yaml:"stamina,omitempty"`
	Vital         float64                   `yaml:"vital,omitempty"`
	CurrentAction string                    `yaml:"current_action,omitempty"`
	ActiveUntil   core.Tick                 `yaml:"active_until,omitempty"`
}

type FloraPlacement struct {
	ID      core.ObjectID `yaml:"id"`
	Species core.Tag      `yaml:"species"`
	Pos     Vec2          `yaml:"pos"`
	Length  float64       `yaml:"length,omitempty"`
	Width   float64       `yaml:"width,omitempty"`
}

type LotPlacement struct {
	ID       core.ObjectID `yaml:"id"`
	Kind     core.Tag      `yaml:"kind"`
	Qty      int           `yaml:"qty"`
	DecayAge float64       `yaml:"decay_age,omitempty"`
	Location string        `yaml:"location,omitempty"`
}

type Vec2 [2]float64

func (v Vec2) Core() core.Vec2 { return core.Vec2{X: v[0], Y: v[1]} }

type loadOptions struct {
	emitter core.EventEmitter
}

type Option func(*loadOptions)

func WithEmitter(emitter core.EventEmitter) Option {
	return func(o *loadOptions) { o.emitter = emitter }
}

type nopEmitter struct{}

func (nopEmitter) Emit(core.Event) {}

func Load(fx Fixture, cfg *config.LoadOutput, opts ...Option) (*world.World, error) {
	if cfg == nil {
		return nil, fmt.Errorf("worldgen: nil config")
	}
	if err := validateFixtureShape(fx); err != nil {
		return nil, err
	}
	if cfg.WorldEnv == nil || cfg.NavCfg == nil || cfg.ClimateCfg == nil {
		return nil, fmt.Errorf("worldgen: config missing world env/nav/climate config")
	}

	options := loadOptions{emitter: nopEmitter{}}
	for _, opt := range opts {
		opt(&options)
	}
	if options.emitter == nil {
		options.emitter = nopEmitter{}
	}

	envCfg, navCfg, climateCfg := fixtureConfigs(fx, cfg)
	if err := validateTerrain(fx, envCfg, navCfg, cfg); err != nil {
		return nil, err
	}
	if err := validateAnimalTemplates(fx, cfg); err != nil {
		return nil, err
	}

	clock, err := worldtime.NewClock(cfg.Balance.ClockConfig())
	if err != nil {
		return nil, fmt.Errorf("worldgen: clock: %w", err)
	}
	thePlanner := planner.New(
		cfg.ActionsRegistry, cfg.GatesRegistry, cfg.NeedsRegistry,
		cfg.StatsRegistry, cfg.Balance.PlannerConfig(),
	)
	sensor := perception.NewSensor(spatial.New(cfg.Balance.World.SpatialHashCell), cfg.PerceptionConfig)
	svc := agent.Services{
		Sensor: sensor, Planner: thePlanner, Values: cfg.ValuesConfig, Needs: cfg.NeedsRegistry,
		Stats: cfg.StatsRegistry, Actions: cfg.ActionsRegistry,
	}
	root := rng.New(fx.Seed)
	w := world.New(cfg.Balance.WorldConfig(), clock, root, svc, cfg.ActionsRegistry, options.emitter)

	for _, obj := range sortedObjects(fx.Objects) {
		w.PlaceObject(obj.ID, obj.Kind, obj.Pos.Core(), nil)
	}

	plants := make([]flora.Plant, 0, len(fx.Flora))
	for _, fp := range sortedFlora(fx.Flora) {
		p := flora.Plant{ID: fp.ID, Species: flora.SpeciesID(fp.Species), Pos: fp.Pos.Core(), Length: fp.Length, Width: fp.Width}
		plants = append(plants, p)
		w.PlaceObject(fp.ID, fp.Species, fp.Pos.Core(), nil)
	}

	lots := make([]decay.Lot, 0, len(fx.Lots))
	for _, lp := range sortedLots(fx.Lots) {
		lots = append(lots, decay.Lot{ID: lp.ID, Kind: decay.KindID(lp.Kind), Qty: lp.Qty, DecayAge: lp.DecayAge})
	}

	terrainAt := terrainSampler(fx, envCfg)
	nav := navmap.New(navCfg, func(p core.Vec2) navmap.TerrainID { return navmap.TerrainID(terrainAt(p)) }, cfg.TerrainTypes)
	clim := climate.New(climateCfg, terrainAt)
	fl := flora.New(plants)
	dec := decay.New(lots)
	w.InstallEnv(envCfg, nav, clim, cfg.ClimateRules, fl, cfg.FloraRules, dec, cfg.DecayRules)

	animals, err := buildAnimals(fx, cfg)
	if err != nil {
		return nil, err
	}
	if len(animals) > 0 {
		w.InstallFauna(envCfg, cfg.FaunaRules, cfg.ScentEmitters, animals)
		if envCfg.RespawnCadence > 0 && len(cfg.RespawnTargets) > 0 {
			templates, anchors := respawnInputs(fx)
			w.InstallRespawn(templates, cfg.RespawnTargets, anchors, envCfg.RespawnCadence)
		}
	}

	agentCfg := cfg.Balance.AgentConfig(cfg.NeedsRegistry, cfg.StatsRegistry)
	for i, ap := range sortedAgents(fx.Agents) {
		w.Spawn(ap.ID, ap.Pos.Core(), agentCfg, rng.New(fx.Seed+int64(i)+1))
	}
	return w, nil
}

func fixtureConfigs(fx Fixture, cfg *config.LoadOutput) (world.EnvConfig, navmap.Config, climate.Config) {
	envCfg := *cfg.WorldEnv
	navCfg := *cfg.NavCfg
	climateCfg := *cfg.ClimateCfg
	if fx.Bounds != nil {
		min, max := fx.Bounds.Min.Core(), fx.Bounds.Max.Core()
		envCfg.Min, envCfg.Max = min, max
		navCfg.MinX, navCfg.MinY, navCfg.MaxX, navCfg.MaxY = min.X, min.Y, max.X, max.Y
		climateCfg.WorldMin, climateCfg.WorldMax = min, max
	}
	return envCfg, navCfg, climateCfg
}

func buildAnimals(fx Fixture, cfg *config.LoadOutput) ([]fauna.Animal, error) {
	animals := make([]fauna.Animal, 0, len(fx.Animals))
	for _, ap := range sortedAnimals(fx.Animals) {
		if cfg.FaunaRules == nil || len(cfg.FaunaRules.Candidates(fauna.SpeciesID(ap.Species))) == 0 {
			return nil, fmt.Errorf("worldgen: animal %s references unknown fauna species %s", ap.ID, ap.Species)
		}
		tpl, ok := fx.AnimalTemplates[ap.Species]
		if !ok {
			return nil, fmt.Errorf("worldgen: animal %s species %s missing animal template", ap.ID, ap.Species)
		}
		stamina := tpl.Stamina
		if stamina <= 0 {
			stamina = 1
		}
		vital := tpl.Vital
		if vital <= 0 {
			vital = 1
		}
		animals = append(animals, fauna.Animal{
			ID: ap.ID, Species: fauna.SpeciesID(ap.Species), Pos: ap.Pos.Core(), Heading: ap.Heading,
			Stats: cloneStats(tpl.Stats), Drives: cloneDrives(tpl.Drives), Stamina: stamina, Vital: vital,
			CurrentAction: actionsID(tpl.CurrentAction), ActiveUntil: tpl.ActiveUntil,
		})
	}
	return animals, nil
}

// respawnInputs builds the per-species canonical template (from the fixture GenSpec) and the per-species
// spawn anchor (centroid of the initial placements — used to revive an extinct species) for InstallRespawn.
func respawnInputs(fx Fixture) (map[core.Tag]fauna.Animal, map[core.Tag]core.Vec2) {
	templates := make(map[core.Tag]fauna.Animal, len(fx.AnimalTemplates))
	for sp, tpl := range fx.AnimalTemplates {
		templates[core.Tag(sp)] = fauna.Animal{
			Species:       fauna.SpeciesID(sp),
			Stats:         cloneStats(tpl.Stats),
			Drives:        cloneDrives(tpl.Drives),
			Stamina:       tpl.Stamina,
			Vital:         tpl.Vital,
			CurrentAction: actionsID(tpl.CurrentAction),
			ActiveUntil:   tpl.ActiveUntil,
		}
	}
	sums := make(map[core.Tag]core.Vec2)
	counts := make(map[core.Tag]int)
	for _, ap := range fx.Animals {
		sp := core.Tag(ap.Species)
		p := ap.Pos.Core()
		s := sums[sp]
		sums[sp] = core.Vec2{X: s.X + p.X, Y: s.Y + p.Y}
		counts[sp]++
	}
	anchors := make(map[core.Tag]core.Vec2, len(sums))
	for sp, s := range sums {
		anchors[sp] = core.Vec2{X: s.X / float64(counts[sp]), Y: s.Y / float64(counts[sp])}
	}
	return templates, anchors
}

func terrainSampler(fx Fixture, cfg world.EnvConfig) func(core.Vec2) core.Tag {
	if fx.Terrain == nil || len(fx.Terrain.Cells) == 0 {
		return func(core.Vec2) core.Tag { return "soil" }
	}
	layout := *fx.Terrain
	return func(p core.Vec2) core.Tag {
		x := int(math.Floor((p.X - cfg.Min.X) / ((cfg.Max.X - cfg.Min.X) / float64(layout.Cols))))
		y := int(math.Floor((p.Y - cfg.Min.Y) / ((cfg.Max.Y - cfg.Min.Y) / float64(layout.Rows))))
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		if x >= layout.Cols {
			x = layout.Cols - 1
		}
		if y >= layout.Rows {
			y = layout.Rows - 1
		}
		return layout.Cells[y*layout.Cols+x]
	}
}

func validateTerrain(fx Fixture, envCfg world.EnvConfig, navCfg navmap.Config, cfg *config.LoadOutput) error {
	if fx.Terrain == nil {
		return nil
	}
	for i, id := range fx.Terrain.Cells {
		if _, ok := cfg.TerrainTypes[navmap.TerrainID(id)]; !ok {
			return fmt.Errorf("worldgen: terrain cell %d references unknown terrain %s", i, id)
		}
	}
	wantCols := int(math.Round((envCfg.Max.X - envCfg.Min.X) / navCfg.CellSize))
	wantRows := int(math.Round((envCfg.Max.Y - envCfg.Min.Y) / navCfg.CellSize))
	if fx.Terrain.Cols != wantCols || fx.Terrain.Rows != wantRows {
		return fmt.Errorf("worldgen: terrain grid %dx%d disagrees with bounds/cell size %dx%d", fx.Terrain.Cols, fx.Terrain.Rows, wantCols, wantRows)
	}
	return nil
}

func validateAnimalTemplates(fx Fixture, cfg *config.LoadOutput) error {
	for species, tpl := range fx.AnimalTemplates {
		if len(tpl.Stats) == 0 {
			return fmt.Errorf("worldgen: animal template %s has no stats", species)
		}
		for id := range tpl.Stats {
			if !cfg.StatsRegistry.Has(id) {
				return fmt.Errorf("worldgen: animal template %s references unknown stat %s", species, id)
			}
		}
	}
	return nil
}

func cloneStats(in map[core.StatID]float64) map[core.StatID]float64 {
	out := make(map[core.StatID]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneDrives(in map[fauna.DriveID]float64) map[fauna.DriveID]float64 {
	out := make(map[fauna.DriveID]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func actionsID(id string) actions.ActionID {
	return actions.ActionID(id)
}

func sortedObjects(in []ObjectPlacement) []ObjectPlacement {
	out := append([]ObjectPlacement(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedAgents(in []AgentPlacement) []AgentPlacement {
	out := append([]AgentPlacement(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedAnimals(in []AnimalPlacement) []AnimalPlacement {
	out := append([]AnimalPlacement(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedFlora(in []FloraPlacement) []FloraPlacement {
	out := append([]FloraPlacement(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedLots(in []LotPlacement) []LotPlacement {
	out := append([]LotPlacement(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
