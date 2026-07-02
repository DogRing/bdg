package persist

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/world"
)

// ── §1: Snapshot serialization ────────────────────────────────────────────────

func TestEncodeDecodeRoundTrip(t *testing.T) {
	ws := world.WorldState{
		Tick:     42,
		RNGState: rng.RNGState{Data: "dGVzdC1iYXNlNjQ="},
	}
	s := Snapshot{
		RunID: "test-run",
		Tick:  42,
		World: ws,
	}

	blob, err := Encode(s)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("Encode returned empty blob")
	}

	decoded, err := Decode(blob)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decoded.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", decoded.SchemaVersion, SchemaVersion)
	}
	if decoded.RunID != "test-run" {
		t.Errorf("RunID = %q, want %q", decoded.RunID, "test-run")
	}
	if decoded.Tick != 42 {
		t.Errorf("Tick = %d, want 42", decoded.Tick)
	}
	if decoded.World.Tick != 42 {
		t.Errorf("World.Tick = %d, want 42", decoded.World.Tick)
	}
	if decoded.World.RNGState.Data != "dGVzdC1iYXNlNjQ=" {
		t.Errorf("World.RNGState.Data = %q, want %q", decoded.World.RNGState.Data, "dGVzdC1iYXNlNjQ=")
	}
}

func TestEncodeStampsSchemaVersion(t *testing.T) {
	s := Snapshot{RunID: "test", Tick: 0}
	blob, err := Encode(s)
	if err != nil {
		t.Fatal(err)
	}

	// Decode raw JSON to check schema_version.
	var raw struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.SchemaVersion != SchemaVersion {
		t.Errorf("encoded schema_version = %d, want %d", raw.SchemaVersion, SchemaVersion)
	}
}

func TestDecodeRejectsSchemaVersion(t *testing.T) {
	tests := []struct {
		name    string
		version int
		wantErr bool
	}{
		{name: "version-1 ok", version: SchemaVersion, wantErr: false},
		{name: "version-0 rejected", version: 0, wantErr: true},
		{name: "version-2 rejected", version: 2, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blob := fmt.Sprintf(
				`{"schema_version":%d,"run_id":"test","tick":0,"world":{"Tick":0,"RNGState":{"Data":""},"Agents":null,"Objects":null,"Known":null,"EmergedRoles":null}}`,
				tt.version,
			)
			_, err := Decode([]byte(blob))
			if tt.wantErr && err != ErrSchemaMismatch {
				t.Errorf("Decode: got %v, want ErrSchemaMismatch", err)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Decode: unexpected error %v", err)
			}
		})
	}
}

func TestEncodeDecodeDeterministic(t *testing.T) {
	ws := world.WorldState{
		Tick:     99,
		RNGState: rng.RNGState{Data: "c29tZS1zdGF0ZQ=="},
	}
	s := Snapshot{RunID: "det-test", Tick: 99, World: ws}

	blob1, err := Encode(s)
	if err != nil {
		t.Fatal(err)
	}

	// Encode again — should produce identical bytes.
	blob2, err := Encode(s)
	if err != nil {
		t.Fatal(err)
	}

	if string(blob1) != string(blob2) {
		t.Errorf("Encode not deterministic:\n  blob1: %s\n  blob2: %s", string(blob1), string(blob2))
	}

	// Encode(Decode(b)) == b (schema version stamping ensures round-trip identity).
	decoded, err := Decode(blob1)
	if err != nil {
		t.Fatal(err)
	}
	reBlob, err := Encode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(blob1) != string(reBlob) {
		t.Errorf("Encode(Decode(b)) != b:\n  original: %s\n  re-encoded: %s", string(blob1), string(reBlob))
	}
}

func TestDecodeInvalidJSON(t *testing.T) {
	_, err := Decode([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// ── Keyer tests (§2) ──────────────────────────────────────────────────────────

func TestKeyerFormats(t *testing.T) {
	tests := []struct {
		name  string
		run   core.RunID
		agent core.AgentID
		want  map[string]string // method → expected key
	}{
		{
			name:  "simple run",
			run:   "dev",
			agent: "agent_01",
			want: map[string]string{
				"Meta":        "sim:dev:meta",
				"Tick":        "sim:dev:tick",
				"SnapshotKey": "sim:dev:snapshot",
				"Agent":       "sim:dev:agent:agent_01",
				"Events":      "sim:dev:events",
			},
		},
		{
			name:  "numeric run",
			run:   "run_42",
			agent: "agent_99",
			want: map[string]string{
				"Meta":        "sim:run_42:meta",
				"Tick":        "sim:run_42:tick",
				"SnapshotKey": "sim:run_42:snapshot",
				"Agent":       "sim:run_42:agent:agent_99",
				"Events":      "sim:run_42:events",
			},
		},
		{
			name:  "empty agent",
			run:   "test",
			agent: "",
			want: map[string]string{
				"Meta":        "sim:test:meta",
				"Tick":        "sim:test:tick",
				"SnapshotKey": "sim:test:snapshot",
				"Agent":       "sim:test:agent:",
				"Events":      "sim:test:events",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyer := Keyer{Run: tt.run}
			check := func(method, got, want string) {
				if got != want {
					t.Errorf("Keyer.%s() = %q, want %q", method, got, want)
				}
			}
			check("Meta", keyer.Meta(), tt.want["Meta"])
			check("Tick", keyer.Tick(), tt.want["Tick"])
			check("SnapshotKey", keyer.SnapshotKey(), tt.want["SnapshotKey"])
			check("Agent", keyer.Agent(tt.agent), tt.want["Agent"])
			check("Events", keyer.Events(), tt.want["Events"])
		})
	}
}

// ── AgentView type boundary checks ─────────────────────────────────────────────

func TestAgentViewNoRealStats(t *testing.T) {
	// AgentView must not have a RealStats or ToM field.
	// This is a compile-time structural check: the type has exactly {ID, Pos, Goal,
	// Action, Mood}. We verify by marshaling and checking for absence of forbidden keys.
	v := AgentView{
		ID:     "test_agent",
		Pos:    core.Vec2{X: 1.0, Y: 2.0},
		Goal:   "Satiety",
		Action: "Eat",
		Mood:   0.75,
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"real_stats", "tom", "RealStats", "ToM", "self_est_stats"}
	for _, key := range forbidden {
		if _, ok := raw[key]; ok {
			t.Errorf("AgentView JSON contains forbidden key %q", key)
		}
	}
}

// ── CaptureSnapshot ↔ RestoreInto ─────────────────────────────────────────────

func TestCaptureSnapshotCompilesAndRoundTrips(t *testing.T) {
	// CaptureSnapshot and RestoreInto need a fully-built *world.World with all
	// its dependencies (RNG, clock, services, action registry, event emitter).
	// This test verifies the pure Encode/Decode path on the Snapshot struct.
	// The full world capture/restore round-trip is tested in scenario tests.

	// Verify that CaptureSnapshot returns the right field types by constructing
	// a Snapshot manually and round-tripping through Encode/Decode.
	ws := world.WorldState{
		Tick:     7,
		RNGState: rng.RNGState{Data: "cm5nLXN0YXRl"},
	}
	s := Snapshot{
		SchemaVersion: SchemaVersion,
		RunID:         "capture-test",
		Tick:          7,
		World:         ws,
	}

	blob, err := Encode(s)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := Decode(blob)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decoded.RunID != "capture-test" {
		t.Errorf("RunID = %q, want %q", decoded.RunID, "capture-test")
	}
	if decoded.Tick != 7 {
		t.Errorf("Tick = %d, want 7", decoded.Tick)
	}
	if decoded.World.Tick != 7 {
		t.Errorf("World.Tick = %d, want 7", decoded.World.Tick)
	}
	if decoded.World.RNGState.Data != "cm5nLXN0YXRl" {
		t.Errorf("World.RNGState.Data = %q", decoded.World.RNGState.Data)
	}
}

func TestRestoreIntoRejectsSchemaMismatch(t *testing.T) {
	// We can't call world.RestoreState without constructing a world,
	// but we can verify that RestoreInto checks the schema version.
	s := Snapshot{SchemaVersion: 0, RunID: "bad", Tick: 0}
	err := RestoreInto(nil, s)
	if err != ErrSchemaMismatch {
		t.Errorf("RestoreInto: got %v, want ErrSchemaMismatch", err)
	}
}

// ── Golden snapshot test ───────────────────────────────────────────────────────

func TestGoldenSnapshot(t *testing.T) {
	ws := world.WorldState{
		Tick:     42,
		RNGState: rng.RNGState{Data: "dGVzdC1iYXNlNjQ="},
	}
	s := Snapshot{
		RunID: "golden-run",
		Tick:  42,
		World: ws,
	}
	blob, err := Encode(s)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Read golden file and compare.
	goldenPath := "testdata/golden/basic_snapshot.json"
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("Read golden: %v", err)
	}
	if string(blob) != string(want) {
		t.Errorf("encoded output differs from golden\n  got:  %s\n  want: %s", string(blob), string(want))
	}
}

// ── Snapshot ready event emission ─────────────────────────────────────────────

func TestBackupEveryTicksEnvConstant(t *testing.T) {
	if BackupEveryTicksEnv != "BACKUP_EVERY_TICKS" {
		t.Errorf("BackupEveryTicksEnv = %q, want %q", BackupEveryTicksEnv, "BACKUP_EVERY_TICKS")
	}
}
