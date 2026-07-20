package worldgen

import (
	"fmt"
	"sort"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/env/exposure"
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
	CellSize        float64                     `yaml:"cell_size,omitempty"`
	Bounds          *Bounds                     `yaml:"bounds,omitempty"`
	Terrain         *TerrainLayout              `yaml:"terrain,omitempty"`
	Objects         []ObjectPlacement           `yaml:"objects,omitempty"`
	Agents          []AgentPlacement            `yaml:"agents,omitempty"`
	Animals         []AnimalPlacement           `yaml:"animals,omitempty"`
	AnimalTemplates map[core.Tag]AnimalTemplate `yaml:"animal_templates,omitempty"`
	Flora           []FloraPlacement            `yaml:"flora,omitempty"`
	Lots            []LotPlacement              `yaml:"lots,omitempty"`
	// RespawnTargets is a per-run OVERRIDE of the content per-species respawn_target
	// (F9 carrying capacity); merged over config.RespawnTargets at Load (cfg untouched).
	RespawnTargets map[core.Tag]int `yaml:"respawn_targets,omitempty"`
	// FloraDensity / AnimalDensity request procedural by-species population at materialize
	// time (scaling.md SC4): count = round(density · bounds area), placed deterministically
	// over the terrain layout — flora weighted by §6 carrying-capacity, animals filtered by
	// terrain passability (F18 allow-list). Density-placed entities are APPENDED to the
	// explicit Flora/Animals lists. Requires a terrain layout + a Load-supplied config.
	// density is entities per world unit² (bounds are continuous, D11).
	FloraDensity  map[core.Tag]float64 `yaml:"flora_density,omitempty"`
	AnimalDensity map[core.Tag]float64 `yaml:"animal_density,omitempty"`
}

type Bounds struct {
	Min Vec2 `yaml:"min"`
	Max Vec2 `yaml:"max"`
}

// TerrainLayout is either EXPLICIT ({Cols,Rows,Cells[,Elevation]}) or RANDOM
// ({Random:true}, no cells): Load materializes the random form into an explicit
// GenerateTerrain cells+Elevation layout over navmap.OffsetDimsOf(bounds) from an
// fx.Seed-derived rng (SPEC §Fixture; D12). Elevation is optional per-cell relief
// ∈[0,1] (len Cols*Rows) — render-only downstream (3D hex height); engine behavior
// never reads it.
type TerrainLayout struct {
	Cols      int        `yaml:"cols,omitempty"`
	Rows      int        `yaml:"rows,omitempty"`
	Cells     []core.Tag `yaml:"cells,omitempty"`
	Elevation []float64  `yaml:"elevation,omitempty"`
	Random    bool       `yaml:"random,omitempty"`
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

// AnimalPlacement.Pos is optional (nil): Load places a pos-less animal on a random soil
// hex from the fx.Seed-derived rng (requires a terrain layout; SPEC §Fixture).
type AnimalPlacement struct {
	ID      core.ObjectID `yaml:"id"`
	Species core.Tag      `yaml:"species"`
	Pos     *Vec2         `yaml:"pos,omitempty"`
	Heading float64       `yaml:"heading,omitempty"`
	// PD11/§7 aging: initial Age in ticks; 0 = newborn. Explicit placements default to 0 (an authored
	// animal is exactly what the author says it is); DENSITY-generated ones get a drawn age — see
	// placeAnimalDensity.
	Age float64 `yaml:"age,omitempty"`
}

type AnimalTemplate struct {
	Stats         map[core.StatID]float64   `yaml:"stats"`
	Drives        map[fauna.DriveID]float64 `yaml:"drives,omitempty"`
	Stamina       float64                   `yaml:"stamina,omitempty"`
	Vital         float64                   `yaml:"vital,omitempty"`
	CurrentAction string                    `yaml:"current_action,omitempty"`
	ActiveUntil   core.Tick                 `yaml:"active_until,omitempty"`
}

// FloraPlacement.Pos is optional (nil): same random-soil placement rule as AnimalPlacement.
type FloraPlacement struct {
	ID      core.ObjectID `yaml:"id"`
	Species core.Tag      `yaml:"species"`
	Pos     *Vec2         `yaml:"pos,omitempty"`
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
	fx, initMoisture, err := materialize(fx, navCfg, cfg)
	if err != nil {
		return nil, err
	}
	// Generated worlds only (SPEC §Load): the stage-5 water-proximity moisture field seeds
	// climate's initial per-cell moisture (WG1-a stage 4). Explicit fixtures keep the
	// uniform InitMoisture — existing goldens unchanged.
	if initMoisture != nil && fx.Terrain != nil {
		climateCfg.InitMoistureAt = gridSampler(initMoisture, fx.Terrain.Cols, fx.Terrain.Rows, navCfg)
	}
	if err := validateTerrain(fx, navCfg, cfg); err != nil {
		return nil, err
	}
	if err := validateAnimalTemplates(fx, cfg); err != nil {
		return nil, err
	}
	if err := validateRespawnTargets(fx, cfg); err != nil {
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

	terrainAt := terrainSampler(fx, navCfg)
	nav := navmap.New(navCfg, func(p core.Vec2) navmap.TerrainID { return navmap.TerrainID(terrainAt(p)) }, cfg.TerrainTypes)
	clim := climate.New(climateCfg, terrainAt)
	fl := flora.New(plants)
	dec := decay.New(lots)
	w.InstallEnv(envCfg, nav, clim, cfg.ClimateRules, fl, cfg.FloraRules, dec, cfg.DecayRules)
	w.SetTerrainAttrs(cfg.TerrainAttrs) // §5 terrain attrs → flora SiteInput (suitability + carrying_capacity §6)
	if fx.Terrain != nil && len(fx.Terrain.Elevation) > 0 {
		w.SetTerrainElevation(fx.Terrain.Elevation) // render-only relief (3D hex height)
	}
	// SH1 shelter: objects tagged `blocks_wind` become wind-shadow casters (docs/plans/shelter.md).
	// No such objects ⇒ no blockers ⇒ InstallShelter skipped ⇒ shelter stays OFF (byte-identical).
	blockers := buildWindBlockers(fx.Objects, cfg.WindBlockerKinds, nav)
	coverers := buildCoverers(fx.Objects, cfg.CovererKinds, nav)
	if len(blockers) > 0 || len(coverers) > 0 {
		w.InstallShelter(shelterConfig(), blockers, coverers)
	}

	animals, err := buildAnimals(fx, cfg)
	if err != nil {
		return nil, err
	}
	if len(animals) > 0 {
		w.InstallFauna(envCfg, cfg.FaunaRules, cfg.ScentEmitters, cfg.CoverKinds, animals)
		targets := fixtureRespawnTargets(fx, cfg)
		if envCfg.RespawnCadence > 0 && len(targets) > 0 {
			templates, anchors := respawnInputs(fx, animals)
			w.InstallRespawn(templates, targets, anchors, envCfg.RespawnCadence)
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
	if fx.CellSize > 0 {
		navCfg.CellSize = fx.CellSize
		envCfg.NavmapCellSize = fx.CellSize
	}
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
			CurrentAction: actionsID(tpl.CurrentAction), ActiveUntil: tpl.ActiveUntil, Age: ap.Age,
		})
	}
	return animals, nil
}

// respawnInputs builds the per-species canonical template (from the fixture GenSpec) and the per-species
// spawn anchor (used to revive an EXTINCT species) for InstallRespawn. The anchor is the FIRST actually-
// placed member of each species — a real, passability-respecting position — taken from `placed` (the
// materialized animals) rather than the fixture: density fixtures carry no `fx.Animals`, so an
// fx-centroid anchor would be the origin, and a centroid of scattered placements (e.g. fish in disjoint
// ponds) can itself fall on impassable ground between them. A real placement is guaranteed occupiable.
func respawnInputs(fx Fixture, placed []fauna.Animal) (map[core.Tag]fauna.Animal, map[core.Tag]core.Vec2) {
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
	anchors := make(map[core.Tag]core.Vec2)
	for _, a := range placed { // `placed` is in a deterministic order (sorted-id build)
		sp := core.Tag(a.Species)
		if _, seen := anchors[sp]; !seen {
			anchors[sp] = a.Pos
		}
	}
	return templates, anchors
}

// terrainSampler is the shared navmap↔climate terrainAt(Vec2)→Tag bridge (SPEC §Load): a continuous
// index read (D11) over the authoring layout. The layout is a FLAT-TOP HEX offset(col,row) grid
// (hex-grid.md) at navmap resolution, so the point is snapped to its containing hex via navmap's
// authority helper (OffsetIndexAt), then that offset indexes Cells. navCfg carries the hex geometry
// (CellSize = circumradius, origin = Min); it agrees 1:1 with the navmap.New this feeds.
func terrainSampler(fx Fixture, navCfg navmap.Config) func(core.Vec2) core.Tag {
	if fx.Terrain == nil || len(fx.Terrain.Cells) == 0 {
		return func(core.Vec2) core.Tag { return "soil" }
	}
	return layoutSampler(*fx.Terrain, navCfg)
}

// layoutSampler is the concrete-layout half of terrainSampler, shared with materialize
// (pos-less placement needs to sample the freshly generated layout before Load's env
// construction reaches terrainSampler).
func layoutSampler(layout TerrainLayout, navCfg navmap.Config) func(core.Vec2) core.Tag {
	return func(p core.Vec2) core.Tag {
		col, row := navmap.OffsetIndexAt(navCfg, p)
		if col < 0 {
			col = 0
		}
		if row < 0 {
			row = 0
		}
		if col >= layout.Cols {
			col = layout.Cols - 1
		}
		if row >= layout.Rows {
			row = layout.Rows - 1
		}
		return layout.Cells[row*layout.Cols+col]
	}
}

func validateTerrain(fx Fixture, navCfg navmap.Config, cfg *config.LoadOutput) error {
	if fx.Terrain == nil {
		return nil
	}
	for i, id := range fx.Terrain.Cells {
		if _, ok := cfg.TerrainTypes[navmap.TerrainID(id)]; !ok {
			return fmt.Errorf("worldgen: terrain cell %d references unknown terrain %s", i, id)
		}
	}
	// The authoring layout must be exactly the navmap's flat-top hex offset grid (1:1 authoring↔navmap,
	// hex-grid.md) so each hex samples its own cell and terrain_delta's offset index stays consistent.
	wantCols, wantRows := navmap.OffsetDimsOf(navCfg)
	if fx.Terrain.Cols != wantCols || fx.Terrain.Rows != wantRows {
		return fmt.Errorf("worldgen: terrain grid %dx%d disagrees with navmap hex offset dims %dx%d", fx.Terrain.Cols, fx.Terrain.Rows, wantCols, wantRows)
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

// SH1 shelter defaults (docs/plans/shelter.md). SH1 casts a uniform-strength wind shadow per blocker;
// per-kind height/opacity from tag data is a follow-up, and moving these into balance.yaml is the
// eventual tuning home. Kept as constants so the OFF path (no `blocks_wind` objects) needs no data.
const (
	shelterShadowCellsPerHeight = 1.0
	shelterShadowFalloff        = 0.5
	shelterMinEpsilon           = 0.0
	shelterBlockerHeight        = 3.0
	shelterBlockerOpacity       = 1.0
	// SH3 overhead cover: uniform coverage per `covers` object (full overhead ⇒ ε_cover → 0).
	shelterCoverCoverage = 1.0
)

func shelterConfig() exposure.Config {
	return exposure.Config{
		ShadowCellsPerHeight: shelterShadowCellsPerHeight,
		ShadowFalloff:        shelterShadowFalloff,
		MinEpsilon:           shelterMinEpsilon,
	}
}

// buildWindBlockers turns each placed object whose kind is tagged `blocks_wind` into one exposure
// wind-shadow caster, its footprint the single navmap cell under the object's position. Iterated in
// sorted-ID order (D12). No tagged kinds or no navmap ⇒ nil ⇒ caller keeps shelter OFF.
func buildWindBlockers(objects []ObjectPlacement, kinds map[core.Tag]bool, nav *navmap.NavMap) []exposure.Blocker {
	if len(kinds) == 0 || nav == nil {
		return nil
	}
	var out []exposure.Blocker
	for _, obj := range sortedObjects(objects) {
		if !kinds[obj.Kind] {
			continue
		}
		c := nav.CellOf(obj.Pos.Core())
		out = append(out, exposure.Blocker{
			ID:        obj.ID,
			Footprint: []exposure.Cell{{Q: c.Q, R: c.R}},
			Height:    shelterBlockerHeight,
			Opacity:   shelterBlockerOpacity,
		})
	}
	return out
}

// buildCoverers turns each placed object whose kind is tagged `covers` into one overhead-cover caster
// (SH3), footprint = the object's navmap cell, in sorted-ID order (D12). SH3 uses a uniform coverage
// strength (per-kind strength from tag data is a follow-up). No tagged kinds / no navmap ⇒ nil ⇒ SH3 OFF.
func buildCoverers(objects []ObjectPlacement, kinds map[core.Tag]bool, nav *navmap.NavMap) []exposure.Coverer {
	if len(kinds) == 0 || nav == nil {
		return nil
	}
	var out []exposure.Coverer
	for _, obj := range sortedObjects(objects) {
		if !kinds[obj.Kind] {
			continue
		}
		c := nav.CellOf(obj.Pos.Core())
		out = append(out, exposure.Coverer{
			ID:        obj.ID,
			Footprint: []exposure.Cell{{Q: c.Q, R: c.R}},
			Coverage:  shelterCoverCoverage,
		})
	}
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
