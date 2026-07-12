package persist

import (
	"context"

	"github.com/jackc/pgx/v5"
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

// InTx runs fn inside one pgx transaction (WriteBackup's atomic flush). Rollback
// on error or panic; commit on nil return. The deferred Rollback after a
// successful Commit is a no-op (pgx returns ErrTxClosed, discarded).
func (a pgxPoolAdapter) InTx(ctx context.Context, fn func(pgxClient) error) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(pgxTxAdapter{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// pgxTxAdapter exposes an open pgx.Tx through the same pgxClient surface, so
// store methods run unchanged inside or outside a transaction.
type pgxTxAdapter struct{ tx pgx.Tx }

func (a pgxTxAdapter) Exec(ctx context.Context, sql string, args ...any) (pgCommandTag, error) {
	return a.tx.Exec(ctx, sql, args...)
}

func (a pgxTxAdapter) QueryRow(ctx context.Context, sql string, args ...any) pgxRow {
	return a.tx.QueryRow(ctx, sql, args...)
}

// InTx on an open transaction joins it (no nesting/savepoints needed here).
func (a pgxTxAdapter) InTx(_ context.Context, fn func(pgxClient) error) error {
	return fn(a)
}

var _ pgxClient = pgxPoolAdapter{}
var _ pgxClient = pgxTxAdapter{}

// NewPgBackupStorePool builds a BackupStore backed by a live pgx connection pool.
// This is the real-infrastructure counterpart to NewFakePg used in tests, and the only
// way main/platform wiring obtains a Postgres-backed BackupStore (pgxClient is unexported).
func NewPgBackupStorePool(pool *pgxpool.Pool) *PgBackupStore {
	return NewPgBackupStore(pgxPoolAdapter{pool: pool})
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
	id             BIGSERIAL PRIMARY KEY,
	run_id         TEXT NOT NULL,
	tick           BIGINT NOT NULL,
	blob           BYTEA NOT NULL,
	created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	last_event_seq BIGINT
);
-- Additive column for tables created before last_event_seq existed (idempotent;
-- still bootstrap, not a migration tool — the column is nullable and backfilled
-- as NULL, which is the correct "no why-trace boundary recorded" value).
ALTER TABLE snapshots ADD COLUMN IF NOT EXISTS last_event_seq BIGINT;
CREATE INDEX IF NOT EXISTS snapshots_run_tick_idx ON snapshots (run_id, tick DESC);
CREATE INDEX IF NOT EXISTS snapshots_run_created_idx ON snapshots (run_id, created_at);
CREATE TABLE IF NOT EXISTS events (
	id         BIGSERIAL PRIMARY KEY,
	run_id     TEXT NOT NULL,
	tick       BIGINT NOT NULL,
	seq        BIGINT NOT NULL,
	agent_id   TEXT,
	type       TEXT NOT NULL,
	payload    JSONB,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Existing event rows predate retention timestamps and cannot be aged reliably.
-- They are non-authoritative why-trace data, so discard those legacy rows instead
-- of assigning NOW() and retaining an unknown amount of old data for three days.
ALTER TABLE events ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ;
DELETE FROM events WHERE created_at IS NULL;
ALTER TABLE events ALTER COLUMN created_at SET DEFAULT NOW();
ALTER TABLE events ALTER COLUMN created_at SET NOT NULL;
CREATE INDEX IF NOT EXISTS events_run_tick_seq_idx ON events (run_id, tick, seq);
CREATE INDEX IF NOT EXISTS events_run_created_idx ON events (run_id, created_at);
`
	_, err := pool.Exec(ctx, ddl)
	return err
}
