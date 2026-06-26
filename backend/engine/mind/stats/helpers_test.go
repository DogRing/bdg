package stats

import (
	"os"
	"strings"
	"testing"
)

// validYAML is a minimal valid stats document used throughout unit tests.
const validYAML = `
schema_version: 1
stats:
  - id: Strength
    label: Strength
    kind: capability
    range: [0.0, 1.0]
    default: 0.5
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Agility
    label: Agility
    kind: capability
    range: [0.0, 1.0]
    default: 0.5
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Intelligence
    label: Intelligence
    kind: capability
    range: [0.0, 1.0]
    default: 0.5
    gen: { dist: normal, mean: 0.5, sd: 0.18 }
    inherit: 0.5
  - id: Honesty
    label: Honesty
    kind: disposition
    range: [0.0, 1.0]
    default: 0.6
    gen: { dist: normal, mean: 0.6, sd: 0.2 }
    inherit: 0.4
  - id: Greed
    kind: disposition
    range: [0.0, 1.0]
    default: 0.5
    gen: { dist: normal, mean: 0.5, sd: 0.2 }
    inherit: 0.4
`

func mustLoadYAML(t *testing.T, yamlContent string) *Registry {
	t.Helper()
	reg, err := Load(strings.NewReader(yamlContent))
	if err != nil {
		t.Fatalf("mustLoadYAML: unexpected error: %v", err)
	}
	return reg
}

func mustLoadContentFile(t *testing.T) *Registry {
	t.Helper()
	// Read the actual content/stats.yaml file. The package lives at
	// backend/engine/mind/stats, so repo-root content/ is four levels up.
	data, err := os.ReadFile("../../../../content/stats.yaml")
	if err != nil {
		t.Fatalf("read content/stats.yaml: %v", err)
	}
	reg, err := Load(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("Load(content/stats.yaml): %v", err)
	}
	return reg
}
