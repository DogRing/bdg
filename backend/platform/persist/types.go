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

// ── Env render views (WI-P4, data-contracts §2/§10) ───────────────────────────
// Built by engine/world.RenderView() (the god-view filter lives there — persist
// just writes what it's given); these types fix the wire shape persist owns.

// AnimalView is the render-visible subset written to sim:{run}:animal:{id}
// (§2): pos, species, action, heading, stamina. NO Stats/Drives/Vital — the
// god-view boundary, mirroring AgentView.
type AnimalView struct {
	ID      core.ObjectID `json:"id"`
	Pos     core.Vec2     `json:"pos"`
	Species string        `json:"species"`
	Action  string        `json:"action"`
	Heading float64       `json:"heading"`
	Stamina float64       `json:"stamina"`
}

// FloraView is one live plant's render row within the sim:{run}:flora STRING
// (§2, a JSON array of these). Stage is DERIVED from length (D9) — the raw
// length is not part of the render-visible shape.
type FloraView struct {
	ID      core.ObjectID `json:"object_id"`
	Species string        `json:"species"`
	Pos     core.Vec2     `json:"pos"`
	Stage   int           `json:"stage"`
	Width   float64       `json:"width"`
}

// ClimateView is the sim:{run}:climate ambient hash payload (§2). ApparentTemp
// is optional (fauna F40 — only meaningful once fauna is active); nil ⇒ the
// field is omitted, not zero.
type ClimateView struct {
	Temperature  float64  `json:"temperature"`
	ApparentTemp *float64 `json:"apparent_temp,omitempty"`
	Moisture     float64  `json:"moisture"`
	Raining      bool     `json:"raining"`
	WindDir      float64  `json:"wind_dir"`
	WindMag      float64  `json:"wind_mag"`
	HourOfDay    int      `json:"hour_of_day"`
	DayNight     string   `json:"day_night"`
	YearFraction float64  `json:"year_fraction"`
}

// TerrainSize is the nested {w,h} shape the GET /api/terrain contract expects
// (frontend/src/hooks/useWorld.ts loadTerrain; frontend/SPEC.md TerrainGrid).
type TerrainSize struct {
	W int `json:"w"`
	H int `json:"h"`
}

// TerrainView is the sim:{run}:terrain STRING payload (§2) — base layout +
// climate overrides + wear, already shaped exactly as GET /api/terrain returns
// it (platform/api forwards the stored bytes verbatim, no reshaping).
type TerrainView struct {
	CellSize float64     `json:"cell_size"`
	Size     TerrainSize `json:"size"`
	Terrain  []string    `json:"terrain"`
	Wear     []float64   `json:"wear,omitempty"`
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
	// WriteAnimal upserts the per-animal render hash sim:{run}:animal:{id} (§2).
	// The AnimalView it accepts CANNOT carry Stats/Drives/Vital (god-view guard,
	// WI-P4). Called only when env/fauna is installed.
	WriteAnimal(ctx context.Context, run core.RunID, v AnimalView) error
	// WriteFlora replaces sim:{run}:flora with the full live plant render set
	// (§2, WI-P4) — periodic-full, not a delta. Called only when flora is installed.
	WriteFlora(ctx context.Context, run core.RunID, plants []FloraView) error
	// WriteClimate upserts the sim:{run}:climate ambient hash (§2, WI-P4).
	// Called only when climate is installed.
	WriteClimate(ctx context.Context, run core.RunID, v ClimateView) error
	// WriteTerrain replaces sim:{run}:terrain with the full render terrain grid
	// (§2, WI-P4; also the GET /api/terrain source). Called only when navmap is
	// installed.
	WriteTerrain(ctx context.Context, run core.RunID, v TerrainView) error
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
