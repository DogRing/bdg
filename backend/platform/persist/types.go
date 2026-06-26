package persist

import (
	"context"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── Redis live store (data-contracts §2) ───────────────────────────────────────

// AgentView is the render/observation-visible subset written to sim:{run}:agent:{id}
// (§2). It deliberately has NO RealStats / ToM field — the type is the god-view
// boundary.
type AgentView struct {
	ID     core.AgentID `json:"id"`
	Pos    core.Vec2    `json:"pos"`
	Goal   string       `json:"goal"`
	Action string       `json:"action"`
	Mood   float64      `json:"mood"`
}

// RunMeta is the sim:{run}:meta hash payload (§2).
type RunMeta struct {
	Tick          core.Tick `json:"tick"`
	SchemaVersion int       `json:"schema_version"`
	StartedAt     string    `json:"started_at"` // RFC3339; supplied by caller (no wall-clock here)
	Status        string    `json:"status"`     // "running" | "completed" | "failed"
}

// LiveStore writes/reads the Redis live keys. It exposes ONLY render/decision-
// visible fields (§2) — it MUST NOT write RealStats to any agent hash (god-view
// boundary).
type LiveStore interface {
	// WriteTick sets sim:{run}:tick (fast read) and refreshes meta.tick.
	WriteTick(ctx context.Context, run core.RunID, tick core.Tick) error
	// WriteSnapshot stores the latest serialized Snapshot at sim:{run}:snapshot.
	WriteSnapshot(ctx context.Context, run core.RunID, blob []byte) error
	// WriteAgent upserts the per-agent render hash (pos, goal, action, mood — §2).
	// The AgentView it accepts CANNOT carry RealStats (compile-time god-view guard).
	WriteAgent(ctx context.Context, run core.RunID, v AgentView) error
	// InitMeta writes sim:{run}:meta { tick, schema_version, started_at, status }.
	InitMeta(ctx context.Context, run core.RunID, m RunMeta) error
	// ReadSnapshot loads the latest snapshot blob (for resume / hand-off to backup).
	ReadSnapshot(ctx context.Context, run core.RunID) ([]byte, error)
	// Expire applies the TTL/expiry policy for a run (called on completion).
	Expire(ctx context.Context, run core.RunID) error
}

// ── Postgres backup (data-contracts §3) ───────────────────────────────────────

// BackupStore persists the periodic full blob + the why-trace event rows (§3 replay/
// analytics source). It is the ONLY tier that stores RealStats (inside the blob).
type BackupStore interface {
	// UpsertRun writes/updates the runs row.
	UpsertRun(ctx context.Context, r RunRecord) error
	// WriteSnapshot inserts one snapshots row (run_id, tick, blob, created_at).
	WriteSnapshot(ctx context.Context, run core.RunID, tick core.Tick, blob []byte) error
	// WriteEvents appends event rows (run_id, tick, seq, agent_id, type, payload JSONB).
	WriteEvents(ctx context.Context, run core.RunID, evs []core.Event) error
	// LatestSnapshot loads the most recent snapshots blob for a run (replay/resume).
	LatestSnapshot(ctx context.Context, run core.RunID) (tick core.Tick, blob []byte, err error)
}

// RunRecord mirrors the §3 runs table.
type RunRecord struct {
	RunID         core.RunID
	Seed          int64
	SchemaVersion int
	StartedAt     string // RFC3339, caller-supplied
	EndedAt       string // RFC3339, empty while running
	Status        string
	ConfigHash    string // hash of loaded content (reproducibility key, §3)
}

// BackupEveryTicksEnv is the env var (data-contracts §3 backup_every_ticks) that
// sets how often a snapshot row is written to Postgres. Read by the caller/wiring,
// surfaced here as the canonical name.
const BackupEveryTicksEnv = "BACKUP_EVERY_TICKS"
