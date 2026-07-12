package persist

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dogring/bdg/engine/kernel/core"
)

// Postgres retention/downsample policy (data-contracts §3): delete snapshots and
// events older than three days. Within the surviving window, keep the full snapshot
// cadence for the most recent hour, one row per 10-minute bucket up to a day old,
// and one row per 1-day bucket thereafter. Buckets are aligned to the Unix epoch;
// within a bucket the NEWEST row survives (latest created_at, ties by tick, then id).
const (
	pruneKeepAllWindow = time.Hour
	pruneMidWindow     = 24 * time.Hour
	pruneMidBucket     = 10 * time.Minute
	pruneCoarseBucket  = 24 * time.Hour
	pruneMaxAge        = 3 * 24 * time.Hour
	pruneInterval      = 6 * time.Hour
)

// pgxClient is the minimal subset of pgx operations PgBackupStore needs.
type pgxClient interface {
	Exec(ctx context.Context, sql string, args ...any) (pgCommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgxRow
	// InTx runs fn inside one transaction: fn's statements commit together on
	// nil return, or roll back together on error. The pgxClient passed to fn is
	// the transaction (no nesting — a nested InTx joins the same transaction).
	InTx(ctx context.Context, fn func(pgxClient) error) error
}

type pgCommandTag interface {
	RowsAffected() int64
}

type pgxRow interface {
	Scan(dest ...any) error
}

// PgBackupStore implements BackupStore using a pgx-compatible client.
type PgBackupStore struct {
	client    pgxClient
	pruneMu   sync.Mutex
	lastPrune map[core.RunID]time.Time
}

// NewPgBackupStore creates a PgBackupStore with the given pgx client.
func NewPgBackupStore(client pgxClient) *PgBackupStore {
	return &PgBackupStore{client: client, lastPrune: make(map[core.RunID]time.Time)}
}

func (p *PgBackupStore) claimPrune(run core.RunID, now time.Time) bool {
	p.pruneMu.Lock()
	defer p.pruneMu.Unlock()
	if p.lastPrune == nil {
		p.lastPrune = make(map[core.RunID]time.Time)
	}
	last, ok := p.lastPrune[run]
	if ok && now.Before(last.Add(pruneInterval)) {
		return false
	}
	p.lastPrune[run] = now
	return true
}

const upsertRunSQL = `
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

// execUpsertRun runs the runs-row upsert on c — the shared body of UpsertRun
// (standalone) and ResetRunData (inside its transaction).
func execUpsertRun(ctx context.Context, c pgxClient, r RunRecord) error {
	_, err := c.Exec(ctx, upsertRunSQL,
		string(r.RunID), r.Seed, r.SchemaVersion, r.StartedAt, r.EndedAt, r.Status, r.ConfigHash,
	)
	return err
}

func (p *PgBackupStore) UpsertRun(ctx context.Context, r RunRecord) error {
	if err := execUpsertRun(ctx, p.client, r); err != nil {
		return fmt.Errorf("pg.UpsertRun: %w", err)
	}
	return nil
}

// WriteBackup persists one backup flush atomically: the why-trace event batch
// and its snapshot row — stamped with the batch's max seq (NULL when empty) —
// commit in ONE transaction or roll back together. On error nothing was
// written; the caller re-buffers evs and retries on the next flush cadence.
func (p *PgBackupStore) WriteBackup(ctx context.Context, run core.RunID, tick core.Tick, blob []byte, evs []core.Event) error {
	const eventSQL = `
		INSERT INTO events (run_id, tick, seq, agent_id, type, payload)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
	`
	const snapshotSQL = `
		INSERT INTO snapshots (run_id, tick, blob, created_at, last_event_seq)
		VALUES ($1, $2, $3, NOW(), $4)
	`
	err := p.client.InTx(ctx, func(c pgxClient) error {
		var lastSeq *int64
		for _, e := range evs {
			var payloadJSON []byte
			if e.Payload != nil {
				var err error
				payloadJSON, err = json.Marshal(e.Payload)
				if err != nil {
					return fmt.Errorf("marshal payload: %w", err)
				}
			}
			if _, err := c.Exec(ctx, eventSQL,
				string(run), int64(e.Tick), e.Seq, string(e.AgentID), e.Type, payloadJSON,
			); err != nil {
				return err
			}
			if lastSeq == nil || e.Seq > *lastSeq {
				s := e.Seq
				lastSeq = &s
			}
		}
		_, err := c.Exec(ctx, snapshotSQL, string(run), int64(tick), blob, lastSeq)
		return err
	})
	if err != nil {
		return fmt.Errorf("pg.WriteBackup: %w", err)
	}
	return nil
}

func (p *PgBackupStore) LatestSnapshot(ctx context.Context, run core.RunID) (core.Tick, []byte, error) {
	// Storage recency (created_at, id), NOT tick: a restart rewind resets tick
	// to 0 while history is preserved, so the newest persisted row is the
	// resume point even when its tick is lower than a pre-restart row's.
	const sql = `
		SELECT tick, blob FROM snapshots
		WHERE run_id = $1
		ORDER BY created_at DESC, id DESC
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

// pruneBandSQL deletes, within one retention band [lower, upper) of created_at,
// every snapshots row of the run that is NOT the newest of its time bucket
// (PARTITION BY floor(epoch/bucketSeconds); newest = latest created_at, ties by
// tick, then by row id — the same ordering FakePg mirrors).
const pruneBandSQL = `
		DELETE FROM snapshots WHERE id IN (
			SELECT id FROM (
				SELECT id, row_number() OVER (
					PARTITION BY (floor(extract(epoch FROM created_at)))::bigint / $4
					ORDER BY created_at DESC, tick DESC, id DESC
				) AS rn
				FROM snapshots
				WHERE run_id = $1 AND created_at >= $2 AND created_at < $3
			) ranked
			WHERE ranked.rn > 1
		)
	`

func (p *PgBackupStore) PruneSnapshots(ctx context.Context, run core.RunID, now time.Time) error {
	if !p.claimPrune(run, now) {
		return nil
	}
	recentCutoff := now.Add(-pruneKeepAllWindow)
	coarseCutoff := now.Add(-pruneMidWindow)
	retentionCutoff := now.Add(-pruneMaxAge)
	if _, err := p.client.Exec(ctx,
		`DELETE FROM events WHERE run_id = $1 AND created_at < $2`, string(run), retentionCutoff); err != nil {
		return fmt.Errorf("pg.PruneSnapshots: expired events: %w", err)
	}
	if _, err := p.client.Exec(ctx,
		`DELETE FROM snapshots WHERE run_id = $1 AND created_at < $2`, string(run), retentionCutoff); err != nil {
		return fmt.Errorf("pg.PruneSnapshots: expired snapshots: %w", err)
	}
	// Mid band [now-24h, now-1h): one row per 10-minute bucket.
	if _, err := p.client.Exec(ctx, pruneBandSQL,
		string(run), coarseCutoff, recentCutoff, int64(pruneMidBucket/time.Second)); err != nil {
		return fmt.Errorf("pg.PruneSnapshots: mid band: %w", err)
	}
	// Coarse band [now-3d, now-24h): one row per 1-day bucket.
	if _, err := p.client.Exec(ctx, pruneBandSQL,
		string(run), retentionCutoff, coarseCutoff, int64(pruneCoarseBucket/time.Second)); err != nil {
		return fmt.Errorf("pg.PruneSnapshots: coarse band: %w", err)
	}
	return nil
}

// ResetRunData resets one run in ONE transaction: delete its events, delete
// its snapshots, upsert the runs row to the regenerated record — commit
// together or roll back together (a failure AFTER the deletes still restores
// the old history AND the old runs metadata). The regen path's mandatory step.
func (p *PgBackupStore) ResetRunData(ctx context.Context, r RunRecord) error {
	err := p.client.InTx(ctx, func(c pgxClient) error {
		if _, err := c.Exec(ctx, `DELETE FROM events WHERE run_id = $1`, string(r.RunID)); err != nil {
			return err
		}
		if _, err := c.Exec(ctx, `DELETE FROM snapshots WHERE run_id = $1`, string(r.RunID)); err != nil {
			return err
		}
		return execUpsertRun(ctx, c, r)
	})
	if err != nil {
		return fmt.Errorf("pg.ResetRunData: %w", err)
	}
	return nil
}

// Compile-time checks.
var _ BackupStore = (*PgBackupStore)(nil)
var _ BackupStore = (*FakePg)(nil)
