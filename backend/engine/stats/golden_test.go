package stats

import (
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/dogring/bdg/engine/core"
)

// updateGolden is set by -update flag or UPDATE_GOLDEN=1 env var.
var updateGolden = flag.Bool("update", false, "regenerate golden files in place")

const goldenPath = "testdata/golden/registry/content_stats_v1.json"

// goldenSnapshot is the JSON shape written to / read from the golden file.
type goldenSnapshot struct {
	IDs          []core.StatID            `json:"ids"`
	Capabilities []core.StatID            `json:"capabilities"`
	Dispositions []core.StatID            `json:"dispositions"`
	Defaults     map[core.StatID]float64  `json:"defaults"`
}

// TestGoldenRegistry loads the actual content/stats.yaml, builds a snapshot,
// and diffs it against testdata/golden/registry/content_stats_v1.json.
// Run with -update (or UPDATE_GOLDEN=1) to regenerate the golden file.
func TestGoldenRegistry(t *testing.T) {
	reg := mustLoadContentFile(t)

	// Build the snapshot from the live registry.
	snap := goldenSnapshot{
		IDs:          reg.IDs(),
		Capabilities: reg.Kinds(Capability),
		Dispositions: reg.Kinds(Disposition),
		Defaults:     reg.Defaults(),
	}

	// Marshal actual output.
	actualBytes, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("marshal actual snapshot: %v", err)
	}
	// Ensure trailing newline for clean diffs.
	actualBytes = append(actualBytes, '\n')

	// Decide whether to update.
	wantUpdate := *updateGolden || os.Getenv("UPDATE_GOLDEN") == "1"
	if wantUpdate {
		if err := os.MkdirAll("testdata/golden/registry", 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(goldenPath, actualBytes, 0o644); err != nil {
			t.Fatalf("write golden file %s: %v", goldenPath, err)
		}
		t.Logf("golden file updated: %s", goldenPath)
		return
	}

	// Load the existing golden file.
	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file %s: %v (run with -update or UPDATE_GOLDEN=1 to create it)", goldenPath, err)
	}

	// Compare byte-for-byte (both are canonical JSON with sorted keys via struct field order).
	if string(actualBytes) != string(goldenBytes) {
		t.Errorf("registry output does not match golden file %s\n--- want (golden) ---\n%s\n--- got (actual) ---\n%s",
			goldenPath, string(goldenBytes), string(actualBytes))
	}
}
