package persist

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxPoolAdapter wraps a live *pgxpool.Pool so it satisfies the unexported pgxClient
// interface PgBackupStore depends on. Kept inside this package because pgxClient /
// pgCommandTag / pgxRow are unexported — a caller in another package cannot name them,
// so the real-client constructor must live here (the IO boundary; SPEC §"performs all IO").
//
// pgconn.CommandTag (returned by Pool.Exec) has RowsAffected() int64, so it satisfies
// pgCommandTag; pgx.Row (returned by Pool.QueryRow) has Scan(...any) error, so it
// satisfies pgxRow — both assignments are checked by the compiler below.
type pgxPoolAdapter struct{ pool *pgxpool.Pool }

func (a pgxPoolAdapter) Exec(ctx context.Context, sql string, args ...any) (pgCommandTag, error) {
	return a.pool.Exec(ctx, sql, args...)
}

func (a pgxPoolAdapter) QueryRow(ctx context.Context, sql string, args ...any) pgxRow {
	return a.pool.QueryRow(ctx, sql, args...)
}

var _ pgxClient = pgxPoolAdapter{}

// NewPgBackupStorePool builds a BackupStore backed by a live pgx connection pool.
// This is the real-infrastructure counterpart to NewFakePg used in tests, and the only
// way main/platform wiring obtains a Postgres-backed BackupStore (pgxClient is unexported).
func NewPgBackupStorePool(pool *pgxpool.Pool) *PgBackupStore {
	return &PgBackupStore{client: pgxPoolAdapter{pool: pool}}
}

// EnsureSchema creates the runs/snapshots/events tables (data-contracts §3) if they do
// not already exist. It is a first-run bootstrap, not a migration tool (versioned
// migrations remain out of scope — SPEC "Out of Scope"); the table shapes mirror the SQL
// in PgBackupStore exactly. Safe to call on every startup (CREATE TABLE IF NOT EXISTS).
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS runs (
	run_id         TEXT PRIMARY KEY,
	seed           BIGINT NOT NULL,
	schema_version INT NOT NULL,
	started_at     TEXT NOT NULL,
	ended_at       TEXT,
	status         TEXT NOT NULL,
	config_hash    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS snapshots (
	id         BIGSERIAL PRIMARY KEY,
	run_id     TEXT NOT NULL,
	tick       BIGINT NOT NULL,
	blob       BYTEA NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS snapshots_run_tick_idx ON snapshots (run_id, tick DESC);
CREATE TABLE IF NOT EXISTS events (
	id       BIGSERIAL PRIMARY KEY,
	run_id   TEXT NOT NULL,
	tick     BIGINT NOT NULL,
	seq      BIGINT NOT NULL,
	agent_id TEXT,
	type     TEXT NOT NULL,
	payload  JSONB
);
CREATE INDEX IF NOT EXISTS events_run_tick_seq_idx ON events (run_id, tick, seq);
`
	_, err := pool.Exec(ctx, ddl)
	return err
}
