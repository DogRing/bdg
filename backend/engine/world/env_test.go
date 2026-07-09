package world

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/navmap"
)

type envTestStats struct{}

func (envTestStats) Has(core.StatID) bool { return true }

func testNumProgram(t *testing.T, src string) *expr.Program {
	t.Helper()
	p, err := expr.Parse(src, expr.KindNum, envTestStats{}, nil)
	if err != nil {
		t.Fatalf("expr.Parse(%q): %v", src, err)
	}
	return p
}

func testBoolProgram(t *testing.T, src string) *expr.Program {
	t.Helper()
	p, err := expr.Parse(src, expr.KindBool, envTestStats{}, nil)
	if err != nil {
		t.Fatalf("expr.Parse(%q): %v", src, err)
	}
	return p
}

func testEnvConfig() EnvConfig {
	return EnvConfig{
		Min:             core.Vec2{X: 0, Y: 0},
		Max:             core.Vec2{X: 20, Y: 20},
		NavmapCellSize:  5,
		ClimateGridCols: 2,
		ClimateGridRows: 2,
		ClimateStep:     1,
		FloraStep:       1,
		DecayStep:       1,
		ScentCellSize:   5,
		ScentSpread:     1,
		FaunaDT:         1,
		MaxSpeed:        1,
	}
}

func testNavMap() *navmap.NavMap {
	cfg := navmap.Config{
		CellSize:    5,
		MinX:        0,
		MinY:        0,
		MaxX:        20,
		MaxY:        20,
		WearCostMin: 0.5,
		WearMax:     1,
	}
	types := map[navmap.TerrainID]navmap.TerrainType{
		"plain": {BaseCost: 1, Passable: true},
		"wet":   {BaseCost: 2, Passable: true},
	}
	return navmap.New(cfg, func(core.Vec2) navmap.TerrainID { return "plain" }, types)
}

func TestEnvOffNeutralityShadeEmpty(t *testing.T) {
	fx := newFixtureSeeded(t, 11)
	before := worldDigest(fx.world)

	fx.world.Tick()
	after := worldDigest(fx.world)

	fx2 := newFixtureSeeded(t, 11)
	fx2.world.Tick()
	if after != worldDigest(fx2.world) {
		t.Fatalf("env-off run changed existing deterministic world digest")
	}
	if before == after {
		t.Fatalf("expected tick advancement in digest")
	}
	if got := fx.world.currentSnap.ShadeOccluders(core.Vec2{}, 10); len(got) != 0 {
		t.Fatalf("ShadeOccluders with flora off len=%d, want 0", len(got))
	}
}

func TestClimateCellToNavCellsSortedAndExact(t *testing.T) {
	fx := newFixtureSeeded(t, 12)
	cfg := testEnvConfig()
	nav := testNavMap()
	fx.world.InstallEnv(cfg, nav, nil, nil, nil, nil, nil, nil)

	gc := climate.GridCell{X: 1, Y: 0}
	got := fx.world.climateCellToNavCells(gc)

	// Independently derive the expected set (NOT via the fine-sampling under test): every hex whose
	// CENTRE ∈ coarse cell {1,0}'s continuous region, enumerated exhaustively over navmap's offset grid
	// and sorted in the D12 hex order (R-major then Q). This guards that fine-sampling discovers exactly
	// the same hexes as full offset enumeration — no misses, no strays (docs/plans/hex-grid.md).
	cellW := (cfg.Max.X - cfg.Min.X) / float64(cfg.ClimateGridCols)
	cellH := (cfg.Max.Y - cfg.Min.Y) / float64(cfg.ClimateGridRows)
	x0 := cfg.Min.X + float64(gc.X)*cellW
	y0 := cfg.Min.Y + float64(gc.Y)*cellH
	x1, y1 := x0+cellW, y0+cellH
	cols, rows := nav.OffsetDims()
	var want []navmap.Cell
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			c := nav.OffsetToCell(col, row)
			if nav.TerrainAt(c) == "" {
				continue
			}
			if pointInClimateRect(nav.CellCenter(c), x0, y0, x1, y1, gc, cfg) {
				want = append(want, c)
			}
		}
	}
	sortNavCells(want)

	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("climateCellToNavCells = %v, want %v", got, want)
	}
	if len(got) == 0 {
		t.Fatalf("expected a non-empty hex set for a covered climate cell")
	}
	for i := 1; i < len(got); i++ { // explicit D12 hex sort assertion (R-major then Q)
		if got[i-1].R > got[i].R || (got[i-1].R == got[i].R && got[i-1].Q >= got[i].Q) {
			t.Fatalf("climateCellToNavCells not R-major/Q sorted at %d: %v", i, got)
		}
	}
}

func TestClimateCadenceAndBridgeUpdatesNavmap(t *testing.T) {
	fx := newFixtureSeeded(t, 13)
	cfg := testEnvConfig()
	cfg.ClimateStep = 2
	nav := testNavMap()
	climCfg := climate.Config{
		GridCols: 2, GridRows: 2,
		WorldMin: cfg.Min, WorldMax: cfg.Max,
		InitMoisture: 0.2, InitTemperature: 10,
		RainHardCapHours: 1000, RainDurMinHours: 1, RainDurMaxHours: 1,
		AnnualMid: 10,
	}
	state := climate.New(climCfg, func(core.Vec2) core.Tag { return "plain" })
	rules := climate.NewRules([]climate.TransitionRule{
		{From: "plain", When: testBoolProgram(t, "moisture < 1"), To: "wet"},
	})
	fx.world.InstallEnv(cfg, nav, state, rules, nil, nil, nil, nil)

	fx.world.Tick()
	if got := nav.TerrainAt(navmap.Cell{Q: 0, R: 0}); got != "wet" {
		t.Fatalf("tick 0 climate bridge terrain=%s, want wet", got)
	}
	nav.SetTerrain([]navmap.Cell{{Q: 0, R: 0}}, "plain")
	fx.world.Tick()
	if got := nav.TerrainAt(navmap.Cell{Q: 0, R: 0}); got != "plain" {
		t.Fatalf("tick 1 should skip climate cadence, terrain=%s", got)
	}
}

func TestFloraStepApplyAndShadeProjection(t *testing.T) {
	fx := newFixtureSeeded(t, 14)
	plant := flora.Plant{ID: "oak_1", Species: "oak", Pos: core.Vec2{X: 5, Y: 5}, Length: 2, Width: 1}
	dying := flora.Plant{ID: "dead_1", Species: "dead", Pos: core.Vec2{X: 6, Y: 5}, Length: 0, Width: 0}
	spawner := flora.Plant{ID: "seed_1", Species: "seed", Pos: core.Vec2{X: 7, Y: 5}, Length: 10, Width: 1}
	state := flora.New([]flora.Plant{plant, dying, spawner})
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		"oak": {
			Suitability:    testNumProgram(t, "1"),
			LengthRate:     testNumProgram(t, "2"),
			WidthRate:      testNumProgram(t, "3"),
			ShadeRadius:    testNumProgram(t, "width"),
			ShadeOpacity:   testNumProgram(t, "0.5"),
			Stages:         []float64{100},
			PropagateStage: 2,
			PropRadius:     testNumProgram(t, "1"),
			PropChance:     testNumProgram(t, "0"),
		},
		"dead": {
			Suitability:     testNumProgram(t, "0"),
			LengthRate:      testNumProgram(t, "0"),
			WidthRate:       testNumProgram(t, "0"),
			ShadeRadius:     testNumProgram(t, "0"),
			ShadeOpacity:    testNumProgram(t, "0"),
			DeathThreshold:  0.5,
			DeathHysteresis: 1,
			PropRadius:      testNumProgram(t, "0"),
			PropChance:      testNumProgram(t, "0"),
		},
		"seed": {
			Suitability:    testNumProgram(t, "1"),
			LengthRate:     testNumProgram(t, "0"),
			WidthRate:      testNumProgram(t, "0"),
			ShadeRadius:    testNumProgram(t, "0"),
			ShadeOpacity:   testNumProgram(t, "0"),
			Stages:         []float64{1},
			PropagateStage: 1,
			PropRadius:     testNumProgram(t, "1"),
			PropChance:     testNumProgram(t, "1"),
		},
	})
	fx.world.PlaceObject(plant.ID, "oak", plant.Pos, nil)
	fx.world.PlaceObject(dying.ID, "dead", dying.Pos, nil)
	fx.world.PlaceObject(spawner.ID, "seed", spawner.Pos, nil)
	fx.world.InstallEnv(testEnvConfig(), testNavMap(), nil, nil, state, rules, nil, nil)

	fx.world.Tick()

	plants := fx.world.floraState.Plants()
	if _, ok := fx.world.objects["dead_1"]; ok {
		t.Fatalf("flora death delta did not remove dead_1 object")
	}
	if _, ok := fx.world.objects["obj:1"]; !ok {
		t.Fatalf("flora spawn delta did not place minted obj:1")
	}
	var oak flora.Plant
	for _, p := range plants {
		if p.ID == "oak_1" {
			oak = p
		}
	}
	if oak.Length != 4 || oak.Width != 4 {
		t.Fatalf("oak growth = %+v, want length=4 width=4", oak)
	}
	shades := fx.world.currentSnap.ShadeOccluders(core.Vec2{X: 5, Y: 5}, 10)
	if len(shades) != 1 || shades[0].ID != "oak_1" || shades[0].Radius != 4 || shades[0].Opacity != 0.5 {
		t.Fatalf("ShadeOccluders = %+v, want oak_1 radius=4 opacity=0.5", shades)
	}
}

func TestFloraNeighborCountUsesSpeciesPropRadius(t *testing.T) {
	fx := newFixtureSeeded(t, 19)
	main := flora.Plant{ID: "main", Species: "grass", Pos: core.Vec2{X: 0, Y: 0}}
	nearOutsidePropRadius := flora.Plant{ID: "near", Species: "grass", Pos: core.Vec2{X: 4, Y: 0}}
	state := flora.New([]flora.Plant{main, nearOutsidePropRadius})
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		"grass": {
			Suitability:    testNumProgram(t, "1"),
			LengthRate:     testNumProgram(t, "0"),
			WidthRate:      testNumProgram(t, "0"),
			ShadeRadius:    testNumProgram(t, "0"),
			ShadeOpacity:   testNumProgram(t, "0"),
			PropRadius:     testNumProgram(t, "1"),
			PropChance:     testNumProgram(t, "0"),
			PropagateStage: 1,
		},
	})
	fx.world.PlaceObject(main.ID, "grass", main.Pos, nil)
	fx.world.PlaceObject(nearOutsidePropRadius.ID, "grass", nearOutsidePropRadius.Pos, nil)
	fx.world.InstallEnv(testEnvConfig(), testNavMap(), nil, nil, state, rules, nil, nil)

	inputs := fx.world.floraSiteInputs()
	if got := inputs[main.ID].NeighborCount; got != 0 {
		t.Fatalf("NeighborCount used a radius wider than species PropRadius: got %d, want 0", got)
	}
}

func TestDecayStepApplyTransformAndGone(t *testing.T) {
	fx := newFixtureSeeded(t, 15)
	lot := decay.Lot{ID: "food_1", Kind: "food", Qty: 2}
	goneLot := decay.Lot{ID: "gone_1", Kind: "gone_food", Qty: 1}
	state := decay.New([]decay.Lot{lot, goneLot})
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		"food": {
			BaseRate: 2,
			Accel:    testNumProgram(t, "1"),
			States: []decay.StateRule{
				{Threshold: 0},
				{Threshold: 1, Transform: []decay.TransformRule{{Item: "compost", Qty: 3}}},
				{Threshold: 10},
			},
		},
		"gone_food": {
			BaseRate: 2,
			Accel:    testNumProgram(t, "1"),
			States: []decay.StateRule{
				{Threshold: 0},
				{Threshold: 1},
			},
		},
	})
	fx.world.PlaceObject(lot.ID, "food", core.Vec2{X: 7, Y: 8}, nil)
	fx.world.PlaceObject(goneLot.ID, "gone_food", core.Vec2{X: 1, Y: 2}, nil)
	fx.world.InstallEnv(testEnvConfig(), nil, nil, nil, nil, nil, state, rules)

	fx.world.Tick()

	if _, ok := fx.world.objects["food_1"]; !ok {
		t.Fatalf("non-terminal decay transition removed source lot")
	}
	if obj, ok := fx.world.objects["obj:1"]; !ok || obj.Kind != "compost" || obj.Pos.X != 7 || obj.Pos.Y != 8 {
		t.Fatalf("decay transform object = %+v ok=%v, want compost at source position", obj, ok)
	}
	if _, ok := fx.world.objects["gone_1"]; ok {
		t.Fatalf("terminal decay transition did not remove gone_1")
	}
	found := false
	for _, e := range fx.emit.events {
		if e.Type == "Decayed" && strings.Contains(fmt.Sprint(e.Payload), "compost") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing Decayed event for transform")
	}
}

func TestEnvPhaseOrderClimateBeforeFlora(t *testing.T) {
	fx := newFixtureSeeded(t, 18)
	cfg := testEnvConfig()
	nav := testNavMap()
	climCfg := climate.Config{
		GridCols: 1, GridRows: 1,
		WorldMin: cfg.Min, WorldMax: cfg.Max,
		InitMoisture: 0.5, InitTemperature: 10,
		RainHardCapHours: 1000, RainDurMinHours: 1, RainDurMaxHours: 1,
		EvapBaseRate: 0.2,
		AnnualMid:    10,
	}
	climState := climate.New(climCfg, func(core.Vec2) core.Tag { return "plain" })
	climRules := climate.NewRules(nil)
	plant := flora.Plant{ID: "order_plant", Species: "order", Pos: core.Vec2{X: 5, Y: 5}}
	flState := flora.New([]flora.Plant{plant})
	flRules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		"order": {
			Suitability:    testNumProgram(t, "1"),
			LengthRate:     testNumProgram(t, "moisture"),
			WidthRate:      testNumProgram(t, "0"),
			ShadeRadius:    testNumProgram(t, "0"),
			ShadeOpacity:   testNumProgram(t, "0"),
			PropagateStage: 1,
			PropRadius:     testNumProgram(t, "0"),
			PropChance:     testNumProgram(t, "0"),
		},
	})
	fx.world.PlaceObject(plant.ID, "order", plant.Pos, nil)
	fx.world.InstallEnv(cfg, nav, climState, climRules, flState, flRules, nil, nil)

	fx.world.Tick()

	got := fx.world.floraState.Plants()[0].Length
	if got < 0.299999 || got > 0.300001 {
		t.Fatalf("flora sampled length rate %.3f, want post-climate moisture 0.300", got)
	}
}

func TestEnvForksAreDeterministicAndDisjointByChannel(t *testing.T) {
	fx := newFixtureSeeded(t, 16)
	a := fx.world.envFork(4, "climate").Float64()
	b := fx.world.envFork(4, "climate").Float64()
	c := fx.world.envFork(4, "flora").Float64()
	if a != b {
		t.Fatalf("envFork same tick/channel not deterministic: %.12f vs %.12f", a, b)
	}
	if a == c {
		t.Fatalf("envFork channels produced same first draw: %.12f", a)
	}
}

func TestEnvForkIndependentOfAgentCount(t *testing.T) {
	run := func(agentCount int) float64 {
		fx := newFixtureSeeded(t, 20)
		for i := 0; i < agentCount; i++ {
			id := core.AgentID(fmt.Sprintf("agent_%d", i))
			fx.world.Spawn(id, core.Vec2{X: float64(i), Y: 0}, agent.DefaultConfig(), rng.New(int64(200+i)))
		}
		for range 2 {
			fx.world.Tick()
		}
		return fx.world.envFork(fx.world.CurrentTick(), "climate").Float64()
	}

	none := run(0)
	many := run(3)
	if none != many {
		t.Fatalf("envFork changed with agent count: no agents %.12f, three agents %.12f", none, many)
	}
}

func TestEnvPhaseDeterminismGolden(t *testing.T) {
	run := func() string {
		fx := newFixtureSeeded(t, 17)
		plant := flora.Plant{ID: "oak_1", Species: "oak", Pos: core.Vec2{X: 5, Y: 5}, Length: 2, Width: 1}
		flState := flora.New([]flora.Plant{plant})
		flRules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
			"oak": {
				Suitability:    testNumProgram(t, "1"),
				LengthRate:     testNumProgram(t, "1"),
				WidthRate:      testNumProgram(t, "1"),
				ShadeRadius:    testNumProgram(t, "width"),
				ShadeOpacity:   testNumProgram(t, "0.25"),
				Stages:         []float64{100},
				PropagateStage: 2,
				PropRadius:     testNumProgram(t, "1"),
				PropChance:     testNumProgram(t, "0"),
			},
		})
		fx.world.PlaceObject(plant.ID, "oak", plant.Pos, nil)
		fx.world.InstallEnv(testEnvConfig(), testNavMap(), nil, nil, flState, flRules, nil, nil)
		for range 3 {
			fx.world.Tick()
		}
		p := fx.world.floraState.Plants()[0]
		return fmt.Sprintf("%s\nflora %.3f %.3f\nroot %s", worldDigest(fx.world), p.Length, p.Width, fx.rootRNG.State().Data)
	}
	a := run()
	b := run()
	if a != b {
		t.Fatalf("env determinism mismatch\nA:\n%s\nB:\n%s", a, b)
	}
}
