package fauna

import (
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
)

// TestTerrainAttrOperand — habitat preference has to be expressible in §6 at all. Before PD10 fauna
// had NO terrain operand: `terrain_cost` only slowed an animal down and `hazard_avoidance` only
// pushed it away from rough ground, so content could express avoidance but never attraction, and
// "a goat belongs on rocky ground" had nowhere to live except hardcoded in Go (which D2/D10 forbid).
func TestTerrainAttrOperand(t *testing.T) {
	ctx := &animalContext{
		terrainAttrs: map[core.Tag]float64{"slope": 0.8, "moisture": 0.2},
	}

	// The prefix is load-bearing: bare `moisture` is already the CLIMATE operand, and terrain.yaml
	// defines a `moisture` attr too. They must not collide.
	ctx.env.Moisture = 0.9
	if v, ok := ctx.Attr("moisture"); !ok || v != 0.9 {
		t.Errorf("bare moisture must stay the climate operand: got %v (ok=%v)", v, ok)
	}
	if v, ok := ctx.Attr("terrain.moisture"); !ok || v != 0.2 {
		t.Errorf("terrain.moisture = %v (ok=%v), want 0.2", v, ok)
	}
	if v, ok := ctx.Attr("terrain.slope"); !ok || v != 0.8 {
		t.Errorf("terrain.slope = %v (ok=%v), want 0.8", v, ok)
	}
	// An attr this terrain does not define reads 0 rather than failing — the attr set is open content.
	if v, ok := ctx.Attr("terrain.salinity"); !ok || v != 0 {
		t.Errorf("undefined terrain attr = %v (ok=%v), want 0", v, ok)
	}
}

// TestTerrainAttrsAbsentAreNeutral — worlds with no §5 attr table (unit beds, ecosim) must read every
// terrain operand as 0 rather than diverging, so the operand is an off-lever.
func TestTerrainAttrsAbsentAreNeutral(t *testing.T) {
	ctx := &animalContext{}
	if v, ok := ctx.Attr("terrain.slope"); !ok || v != 0 {
		t.Fatalf("terrain operand with no attr table = %v (ok=%v), want 0", v, ok)
	}
}
