// Package persist serializes, versions, and stores the simulation's deterministic
// state. It defines the on-the-wire Snapshot (JSON), the Redis live-keyspace writer
// (LiveStore), and the Postgres backup writer (BackupStore). This module performs
// all IO — the engine never touches storage.
package persist

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/world"
)

// ── Versioning ─────────────────────────────────────────────────────────────────

// SchemaVersion is the current snapshot/contract version (data-contracts §0).
// Bumped +1 on any backward-incompatible change to Snapshot/agent/event shape.
// Load REJECTS a blob whose schema_version != SchemaVersion (no silent migration).
const SchemaVersion int = 1

// ErrSchemaMismatch is returned by Decode/RestoreInto when a blob's schema_version
// does not equal SchemaVersion.
var ErrSchemaMismatch = errors.New("persist: snapshot schema_version mismatch")

// ── Snapshot (data-contracts §1) ───────────────────────────────────────────────

// Snapshot is the complete deterministic state for one tick — the unit persist
// serializes/deserializes. Same Snapshot + same seed leads to byte-identical next
// tick. World carries the engine's serializable state (world.WorldState: tick,
// rng_state, agents incl. RealStats, objects, known sets, emerged roles).
//
// Go 1.26+ json.Marshal sorts map keys for string-keyed maps, so encoding is
// deterministic at every nesting level. No proxy types needed.
type Snapshot struct {
	SchemaVersion int              `json:"schema_version"`
	RunID         string           `json:"run_id"`
	Tick          int64            `json:"tick"`
	World         world.WorldState `json:"world"`
}

// Encode serializes a Snapshot to a deterministic byte blob (JSON for P1; the
// encoding is byte-stable — Go 1.26+ json.Marshal sorts string-keyed map keys).
// It stamps s.SchemaVersion = SchemaVersion before encoding.
func Encode(s Snapshot) ([]byte, error) {
	s.SchemaVersion = SchemaVersion
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("persist.Encode: %w", err)
	}
	return data, nil
}

// Decode parses a blob back into a Snapshot. It REJECTS (returns ErrSchemaMismatch)
// when the decoded schema_version != SchemaVersion. No partial/lossy load.
func Decode(blob []byte) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(blob, &s); err != nil {
		return Snapshot{}, fmt.Errorf("persist.Decode: %w", err)
	}
	if s.SchemaVersion != SchemaVersion {
		return Snapshot{}, ErrSchemaMismatch
	}
	return s, nil
}

// CaptureSnapshot builds a Snapshot from the live world (calls world.State()).
// Pure (no IO); the only place the god-view (RealStats) is read out of the engine.
func CaptureSnapshot(runID core.RunID, w *world.World) Snapshot {
	ws := w.State()
	return Snapshot{
		SchemaVersion: SchemaVersion,
		RunID:         string(runID),
		Tick:          int64(ws.Tick),
		World:         ws,
	}
}

// RestoreInto applies a decoded Snapshot back into a constructed (empty) world via
// world.RestoreState — the spatial hash is rebuilt from positions; the root rng_state
// round-trips (resume invariant). Returns an error on schema mismatch.
func RestoreInto(w *world.World, s Snapshot) error {
	if s.SchemaVersion != SchemaVersion {
		return ErrSchemaMismatch
	}
	w.RestoreState(s.World)
	return nil
}
