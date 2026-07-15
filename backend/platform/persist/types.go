package persist

import (
	"context"
	"time"

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
	CoverID core.ObjectID `json:"cover_id,omitempty"`
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

// FloraDoc is the sim:{run}:flora STRING payload AND the byte-identical GET
// /api/flora response (§2): the full live plant render set tagged with the
// publishing world_revision (mirrors TerrainView.WorldRevision). A reader
// verifies WorldRevision against the snapshot's so a mid-regen fetch pair can't
// mix revisions. Written whenever flora is INSTALLED — an empty Flora is
// "installed, no plants" (200 with flora:[]), distinct from flora-not-installed
// (key absent ⇒ 404). platform/api forwards the stored bytes verbatim.
type FloraDoc struct {
	WorldRevision int64       `json:"world_revision"`
	Flora         []FloraView `json:"flora"`
}

// ClimateView is the sim:{run}:climate ambient hash payload (§2). ApparentTemp
// is optional (fauna F40 — only meaningful once fauna is active); nil ⇒ the
// field is omitted, not zero.
type ClimateView struct {
	Temperature  float64  `json:"temperature"`
	ApparentTemp *float64 `json:"apparent_temp,omitempty"`
	Moisture     float64  `json:"moisture"`
	Raining      bool     `json:"raining"`
	SnowCover    float64  `json:"snow_cover"` // world-uniform snowpack ∈ [0,1] (CS2b) — drives the flora snow-sprite switch (CS4)
	WindDir      float64  `json:"wind_dir"`
	WindMag      float64  `json:"wind_mag"`
	HourOfDay    int      `json:"hour_of_day"`
	DayNight     string   `json:"day_night"`
	YearFraction float64  `json:"year_fraction"`
}

// TerrainSize is the nested {cols,rows} shape the GET /api/terrain contract
// expects — the flat-top hex offset (col,row) grid dimensions (docs/plans/hex-grid.md;
// frontend/src/hooks/useWorld.ts loadTerrain; frontend/SPEC.md TerrainGrid).
type TerrainSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// TerrainView is the sim:{run}:terrain STRING payload (§2) — base layout +
// climate overrides + wear, already shaped exactly as GET /api/terrain returns
// it (platform/api forwards the stored bytes verbatim, no reshaping). Terrain is
// the flat-top hex field projected to an offset (col,row) array (i=row*Cols+col).
type TerrainView struct {
	CellSize    float64     `json:"cell_size"`   // hex circumradius
	Orientation string      `json:"orientation"` // "flat" (flat-top hex)
	Size        TerrainSize `json:"size"`
	Terrain     []string    `json:"terrain"`
	Wear        []float64   `json:"wear,omitempty"`
	Elevation   []float64   `json:"elevation,omitempty"` // per-cell relief ∈[0,1] (generated worlds);
	//                                                      static render-only — absent ⇒ frontend
	//                                                      falls back to per-type heights
	// WorldRevision tags which published single-world revision this grid was
	// written for (data-contracts §2) — a reader verifies it against the
	// snapshot's world_revision so a mid-regen fetch pair can't mix revisions.
	WorldRevision int64 `json:"world_revision,omitempty"`
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
	// WriteFlora replaces sim:{run}:flora with the FloraDoc baseline
	// (world_revision + full live plant render set, §2, WI-P4) — periodic-full,
	// not a delta; also the GET /api/flora source. Called only when flora is
	// installed (empty FloraDoc.Flora ⇒ installed-but-no-plants).
	WriteFlora(ctx context.Context, run core.RunID, v FloraDoc) error
	// WriteClimate upserts the sim:{run}:climate ambient hash (§2, WI-P4).
	// Called only when climate is installed.
	WriteClimate(ctx context.Context, run core.RunID, v ClimateView) error
	// WriteTerrain replaces sim:{run}:terrain with the full render terrain grid
	// (§2, WI-P4; also the GET /api/terrain source). Called only when navmap is
	// installed.
	WriteTerrain(ctx context.Context, run core.RunID, v TerrainView) error
	// InitMeta writes sim:{run}:meta { tick, schema_version, started_at, status }.
	// It deliberately does NOT touch the world_revision/terrain publication
	// fields (HSET of the listed fields only; the meta key is never deleted on
	// regen), so a meta refresh can never un-publish or re-publish a revision.
	InitMeta(ctx context.Context, run core.RunID, m RunMeta) error
	// PublishWorldRevision publishes the single-world revision marker
	// (data-contracts §2): ONE HSET writes {world_revision, terrain:"on"|"off",
	// flora:"on"|"off"} onto sim:{run}:meta. The run-driver calls it LAST — only
	// after the same revision's snapshot AND its terrain/flora baselines (when
	// installed) live writes succeeded — so a reader observing the new revision
	// finds matching, revision-tagged baselines already servable. NOT a run
	// generation (multi-world stays DEFERRED, docs/plans/run-generation.md).
	PublishWorldRevision(ctx context.Context, run core.RunID, rev int64, terrainOn, floraOn bool) error
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
	// WriteBackup persists ONE backup flush atomically: a single transaction
	// inserts the drained why-trace event rows (events table) AND the snapshots
	// row (run_id, tick, blob, created_at, last_event_seq), committing together
	// or rolling back together. last_event_seq is computed in-store as the max
	// Event.Seq of evs — the rows written in the same transaction, so the replay
	// boundary can never reference rows that failed to persist; evs empty ⇒
	// NULL. High-frequency render/operational events are excluded upstream and
	// never count. On error NOTHING was written; the caller re-buffers evs and
	// retries on the next flush cadence. Seq is a process-local counter (SPEC
	// Notes "Seq scope"): the (snapshot, seq ≤ last_event_seq) replay cut holds
	// within one simulation process lifetime only — seq values repeat across
	// process restarts of the same run, and cross-restart replay is unsupported.
	WriteBackup(ctx context.Context, run core.RunID, tick core.Tick, blob []byte, evs []core.Event) error
	// LatestSnapshot loads the most recently PERSISTED snapshots blob for a run
	// (replay/resume). Recency is storage order (newest created_at, ties by row
	// id) — NOT the highest tick: a /api/restart debugging rewind resets tick to
	// 0 while preserving history, so a pre-restart high-tick row must never
	// shadow a newer post-restart low-tick row. No production caller exists yet;
	// the semantics are specified ahead of the future resume path.
	LatestSnapshot(ctx context.Context, run core.RunID) (tick core.Tick, blob []byte, err error)
	// PruneSnapshots applies the retention/downsample policy (§3) to a run
	// (legacy method name retained): snapshots and events older than 3 days are
	// deleted; surviving snapshots newer than 1h stay at full cadence, rows
	// 1h–24h old keep one per 10-minute bucket, and rows 24h–3d old keep one per
	// 1-day bucket ("newest" = latest created_at, ties by tick, then row id).
	// Called after each successful backup; SQL maintenance runs immediately on
	// the first request and then at most once per run per 6 hours.
	PruneSnapshots(ctx context.Context, run core.RunID, now time.Time) error
	// ResetRunData atomically resets a run for POST /api/regen ("new map", same
	// run_id — the current single-world mode; a multi-world redesign is DEFERRED,
	// docs/plans/run-generation.md): ONE transaction
	// deletes the run's events + snapshots rows AND upserts the runs row to r
	// (the regenerated world's record). A failure at any step rolls everything
	// back — old history and old runs metadata stay intact so the run-driver can
	// abort the regen. Other runs untouched. NOT called on /api/restart (a
	// debugging rewind keeps the Postgres history).
	ResetRunData(ctx context.Context, r RunRecord) error
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
