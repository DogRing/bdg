package persist

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dogring/bdg/engine/kernel/core"
)

// pgxClient is the minimal subset of pgx operations PgBackupStore needs.
type pgxClient interface {
	Exec(ctx context.Context, sql string, args ...any) (pgCommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgxRow
}

type pgCommandTag interface {
	RowsAffected() int64
}

type pgxRow interface {
	Scan(dest ...any) error
}

// PgBackupStore implements BackupStore using a pgx-compatible client.
type PgBackupStore struct {
	client pgxClient
}

// NewPgBackupStore creates a PgBackupStore with the given pgx client.
func NewPgBackupStore(client pgxClient) *PgBackupStore {
	return &PgBackupStore{client: client}
}

func (p *PgBackupStore) UpsertRun(ctx context.Context, r RunRecord) error {
	const sql = `
		INSERT INTO runs (run_id, seed, schema_version, started_at, ended_at, status, config_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (run_id) DO UPDATE SET
			seed = EXCLUDED.seed,
			schema_version = EXCLUDED.schema_version,
			started_at = EXCLUDED.started_at,
			ended_at = EXCLUDED.ended_at,
			status = EXCLUDED.status,
			config_hash = EXCLUDED.config_hash
	`
	_, err := p.client.Exec(ctx, sql,
		string(r.RunID), r.Seed, r.SchemaVersion, r.StartedAt, r.EndedAt, r.Status, r.ConfigHash,
	)
	if err != nil {
		return fmt.Errorf("pg.UpsertRun: %w", err)
	}
	return nil
}

func (p *PgBackupStore) WriteSnapshot(ctx context.Context, run core.RunID, tick core.Tick, blob []byte) error {
	const sql = `
		INSERT INTO snapshots (run_id, tick, blob, created_at)
		VALUES ($1, $2, $3, NOW())
	`
	_, err := p.client.Exec(ctx, sql, string(run), int64(tick), blob)
	if err != nil {
		return fmt.Errorf("pg.WriteSnapshot: %w", err)
	}
	return nil
}

func (p *PgBackupStore) WriteEvents(ctx context.Context, run core.RunID, evs []core.Event) error {
	if len(evs) == 0 {
		return nil
	}
	const sql = `
		INSERT INTO events (run_id, tick, seq, agent_id, type, payload)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
	`
	for _, e := range evs {
		var payloadJSON []byte
		if e.Payload != nil {
			var err error
			payloadJSON, err = json.Marshal(e.Payload)
			if err != nil {
				return fmt.Errorf("pg.WriteEvents: marshal payload: %w", err)
			}
		}
		_, err := p.client.Exec(ctx, sql,
			string(run), int64(e.Tick), e.Seq, string(e.AgentID), e.Type, payloadJSON,
		)
		if err != nil {
			return fmt.Errorf("pg.WriteEvents: %w", err)
		}
	}
	return nil
}

func (p *PgBackupStore) LatestSnapshot(ctx context.Context, run core.RunID) (core.Tick, []byte, error) {
	const sql = `
		SELECT tick, blob FROM snapshots
		WHERE run_id = $1
		ORDER BY tick DESC
		LIMIT 1
	`
	var tick int64
	var blob []byte
	err := p.client.QueryRow(ctx, sql, string(run)).Scan(&tick, &blob)
	if err != nil {
		return 0, nil, fmt.Errorf("pg.LatestSnapshot: %w", err)
	}
	return core.Tick(tick), blob, nil
}

// Compile-time checks.
var _ BackupStore = (*PgBackupStore)(nil)
var _ BackupStore = (*FakePg)(nil)
