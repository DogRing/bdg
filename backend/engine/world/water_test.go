package world

import (
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
)

// FM4 world adapter: buildWaterField floods a static attraction field from the drinkable-terrain cells, so
// a sample point east of a river is pulled WEST (−X) up the gradient toward the water (integration through
// the world/fauna adapter — the field it injects is what a thirsty animal steers along).
func TestBuildWaterFieldAttractsTowardDrinkableTerrain(t *testing.T) {
	fx := newFixtureSeeded(t, 41)
	cfg := testEnvConfig()
	cfg.Min = core.Vec2{X: 0, Y: 0}
	cfg.Max = core.Vec2{X: 30, Y: 20}
	cfg.NavmapCellSize = 5
	cfg.DrinkableTerrains = map[core.Tag]bool{"river": true}
	cfg.WaterFieldDecay = 0.03

	types := map[navmap.TerrainID]navmap.TerrainType{
		"plain": {BaseCost: 1, Passable: true},
		"river": {BaseCost: 3, Passable: true},
	}
	navCfg := navmap.Config{CellSize: 5, MinX: 0, MinY: 0, MaxX: 30, MaxY: 20, WearCostMin: 0.5, WearMax: 1}
	// River occupies the −X strip (x < 10); plain elsewhere.
	nav := navmap.New(navCfg, func(p core.Vec2) navmap.TerrainID {
		if p.X < 10 {
			return "river"
		}
		return "plain"
	}, types)
	fx.world.nav = nav
	fx.world.envCfg = cfg

	wf := fx.world.buildWaterField()
	if wf == nil {
		t.Fatal("buildWaterField should return a field when drinkable terrain exists")
	}
	g := wf.Gradient(core.Vec2{X: 20, Y: 10})
	if g.X >= 0 {
		t.Errorf("water gradient east of the river should point −X (toward water), got %+v", g)
	}
}

// FM4 off-lever: no drinkable terrain configured (and zero decay) ⇒ nil field ⇒ no water steering,
// byte-identical to pre-FM4.
func TestBuildWaterFieldNilWhenOff(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := testEnvConfig() // DrinkableTerrains nil, WaterFieldDecay 0
	fx.world.nav = testNavMap()
	fx.world.envCfg = cfg
	if wf := fx.world.buildWaterField(); wf != nil {
		t.Error("no drinkable terrain / zero decay ⇒ nil water field, got non-nil")
	}
}
