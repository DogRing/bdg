package worldgen

import (
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
)

func shelterTestNav() *navmap.NavMap {
	cfg := navmap.Config{CellSize: 5, MinX: 0, MinY: 0, MaxX: 200, MaxY: 200, WearCostMin: 0.5, WearMax: 1}
	types := map[navmap.TerrainID]navmap.TerrainType{"plain": {BaseCost: 1, Passable: true}}
	return navmap.New(cfg, func(core.Vec2) navmap.TerrainID { return "plain" }, types)
}

// No `blocks_wind` kinds (or no navmap) ⇒ nil ⇒ the caller leaves shelter OFF (byte-identical run).
func TestBuildWindBlockersOff(t *testing.T) {
	nav := shelterTestNav()
	objs := []ObjectPlacement{{ID: "o1", Kind: "boulder", Pos: Vec2{10, 10}}}

	if got := buildWindBlockers(objs, nil, nav); got != nil {
		t.Errorf("no wind-blocker kinds: got %v, want nil", got)
	}
	if got := buildWindBlockers(objs, map[core.Tag]bool{"boulder": true}, nil); got != nil {
		t.Errorf("nil navmap: got %v, want nil", got)
	}
	// A kind set that matches none of the placed objects also yields nil.
	if got := buildWindBlockers(objs, map[core.Tag]bool{"wall": true}, nav); got != nil {
		t.Errorf("no matching placed objects: got %v, want nil", got)
	}
}

// Placed objects of a tagged kind become one blocker each, footprint = the cell under the object,
// emitted in sorted-ID order (D12), with the SH1 uniform height/opacity. Untagged kinds are skipped.
func TestBuildWindBlockers(t *testing.T) {
	nav := shelterTestNav()
	kinds := map[core.Tag]bool{"wall": true}
	// Deliberately out of ID order + one untagged object interleaved.
	objs := []ObjectPlacement{
		{ID: "w2", Kind: "wall", Pos: Vec2{55, 60}},
		{ID: "tree", Kind: "oak", Pos: Vec2{5, 5}}, // untagged ⇒ skipped
		{ID: "w1", Kind: "wall", Pos: Vec2{10, 12}},
	}

	got := buildWindBlockers(objs, kinds, nav)
	if len(got) != 2 {
		t.Fatalf("blocker count = %d, want 2 (untagged skipped)", len(got))
	}
	if got[0].ID != "w1" || got[1].ID != "w2" {
		t.Errorf("blocker IDs = [%s %s], want sorted [w1 w2]", got[0].ID, got[1].ID)
	}
	for i, want := range []ObjectPlacement{
		{ID: "w1", Pos: Vec2{10, 12}},
		{ID: "w2", Pos: Vec2{55, 60}},
	} {
		b := got[i]
		wc := nav.CellOf(want.Pos.Core())
		if len(b.Footprint) != 1 || b.Footprint[0].Q != wc.Q || b.Footprint[0].R != wc.R {
			t.Errorf("%s footprint = %v, want [{%d %d}]", b.ID, b.Footprint, wc.Q, wc.R)
		}
		if b.Height != shelterBlockerHeight || b.Opacity != shelterBlockerOpacity {
			t.Errorf("%s strength = (h %v, op %v), want (%v, %v)", b.ID, b.Height, b.Opacity, shelterBlockerHeight, shelterBlockerOpacity)
		}
	}
}

// No `covers` kinds (or no navmap / no match) ⇒ nil ⇒ SH3 stays OFF.
func TestBuildCoverersOff(t *testing.T) {
	nav := shelterTestNav()
	objs := []ObjectPlacement{{ID: "o1", Kind: "hut", Pos: Vec2{10, 10}}}
	if got := buildCoverers(objs, nil, nav); got != nil {
		t.Errorf("no coverer kinds: got %v, want nil", got)
	}
	if got := buildCoverers(objs, map[core.Tag]bool{"hut": true}, nil); got != nil {
		t.Errorf("nil navmap: got %v, want nil", got)
	}
	if got := buildCoverers(objs, map[core.Tag]bool{"roof": true}, nav); got != nil {
		t.Errorf("no matching placed objects: got %v, want nil", got)
	}
}

// Placed objects of a `covers` kind become one coverer each, footprint = the object's cell, in
// sorted-ID order (D12), with the SH3 uniform coverage. Untagged kinds are skipped.
func TestBuildCoverers(t *testing.T) {
	nav := shelterTestNav()
	kinds := map[core.Tag]bool{"roof": true}
	objs := []ObjectPlacement{
		{ID: "r2", Kind: "roof", Pos: Vec2{55, 60}},
		{ID: "tree", Kind: "oak", Pos: Vec2{5, 5}}, // untagged ⇒ skipped
		{ID: "r1", Kind: "roof", Pos: Vec2{10, 12}},
	}
	got := buildCoverers(objs, kinds, nav)
	if len(got) != 2 {
		t.Fatalf("coverer count = %d, want 2 (untagged skipped)", len(got))
	}
	if got[0].ID != "r1" || got[1].ID != "r2" {
		t.Errorf("coverer IDs = [%s %s], want sorted [r1 r2]", got[0].ID, got[1].ID)
	}
	for i, want := range []Vec2{{10, 12}, {55, 60}} {
		cv := got[i]
		wc := nav.CellOf(want.Core())
		if len(cv.Footprint) != 1 || cv.Footprint[0].Q != wc.Q || cv.Footprint[0].R != wc.R {
			t.Errorf("%s footprint = %v, want [{%d %d}]", cv.ID, cv.Footprint, wc.Q, wc.R)
		}
		if cv.Coverage != shelterCoverCoverage {
			t.Errorf("%s coverage = %v, want %v", cv.ID, cv.Coverage, shelterCoverCoverage)
		}
	}
}
