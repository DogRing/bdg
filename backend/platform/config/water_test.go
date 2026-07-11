package config

import (
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"gopkg.in/yaml.v3"
)

// FM4-src: deriveDrinkableTerrains selects fresh open-water terrains (salinity ≤ max ∧ moisture ≥ min)
// from terrain.yaml attrs — river/lake in, sea (salt) and soil (dry) out — with no Go terrain-name
// hardcoding, and returns nil when disabled (moisture_min ≤ 0).
func TestDeriveDrinkableTerrains(t *testing.T) {
	var td terrainDoc
	if err := yaml.Unmarshal([]byte(`
terrains:
  - { id: river, attrs: { salinity: 0.0, moisture: 1.0 } }
  - { id: lake,  attrs: { salinity: 0.0, moisture: 1.0 } }
  - { id: sea,   attrs: { salinity: 1.0, moisture: 1.0 } }
  - { id: soil,  attrs: { salinity: 0.0, moisture: 0.5 } }
`), &td); err != nil {
		t.Fatalf("unmarshal terrainDoc: %v", err)
	}

	got := deriveDrinkableTerrains(td, 0.05, 0.9)
	if len(got) != 2 || !got[core.Tag("river")] || !got[core.Tag("lake")] {
		t.Fatalf("drinkable set = %v, want {river, lake}", got)
	}
	if got[core.Tag("sea")] {
		t.Error("sea (salinity 1.0 > 0.05) must NOT be drinkable")
	}
	if got[core.Tag("soil")] {
		t.Error("soil (moisture 0.5 < 0.9) must NOT be drinkable")
	}

	// OFF lever: moisture_min ≤ 0 ⇒ nil (no water field, byte-identical).
	if deriveDrinkableTerrains(td, 0.05, 0) != nil {
		t.Error("moisture_min ≤ 0 must return nil (off)")
	}
	// No matches ⇒ nil (not an empty map).
	if deriveDrinkableTerrains(td, 0.05, 2.0) != nil {
		t.Error("no terrain meeting the threshold must return nil")
	}
}
