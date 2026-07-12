package persist

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ── Fake goRedisClient for testing RedisLiveStore ─────────────────────────────

// fakeGoRedisClient implements goRedisClient in memory.
type fakeGoRedisClient struct {
	mu     sync.Mutex
	data   map[string]string
	hashes map[string]map[string]string
}

func newFakeGoRedisClient() *fakeGoRedisClient {
	return &fakeGoRedisClient{
		data:   make(map[string]string),
		hashes: make(map[string]map[string]string),
	}
}

func (f *fakeGoRedisClient) Set(_ context.Context, key string, value any, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = fmt.Sprintf("%v", value)
	return nil
}

func (f *fakeGoRedisClient) Get(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	val, ok := f.data[key]
	if !ok {
		return "", fmt.Errorf("fake: key %q not found", key)
	}
	return val, nil
}

func (f *fakeGoRedisClient) Del(_ context.Context, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, key := range keys {
		delete(f.data, key)
		delete(f.hashes, key)
	}
	return nil
}

func (f *fakeGoRedisClient) HSet(_ context.Context, key string, fields ...string) error {
	if len(fields)%2 != 0 {
		return fmt.Errorf("fake: HSet requires even number of field/value args")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hashes[key] == nil {
		f.hashes[key] = make(map[string]string)
	}
	for i := 0; i < len(fields); i += 2 {
		f.hashes[key][fields[i]] = fields[i+1]
	}
	return nil
}

func (f *fakeGoRedisClient) Expire(_ context.Context, key string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Touch the key: add it to data if not present, so we can verify Expire was called.
	if _, ok := f.data[key]; !ok {
		if _, ok2 := f.hashes[key]; !ok2 {
			f.data[key] = ""
		}
	}
	return nil
}

var _ goRedisClient = (*fakeGoRedisClient)(nil)

// ── Fake pgxClient for testing PgBackupStore ──────────────────────────────────

// fakePgxResult implements pgCommandTag.
type fakePgxResult struct{ rowsAffected int64 }

func (r fakePgxResult) RowsAffected() int64 { return r.rowsAffected }

// fakePgxRow implements pgxRow.
type fakePgxRow struct {
	values []any
}

func (r *fakePgxRow) Scan(dest ...any) error {
	if len(r.values) != len(dest) {
		return fmt.Errorf("fake: Scan expects %d dest args, got %d", len(r.values), len(dest))
	}
	for i, v := range r.values {
		switch d := dest[i].(type) {
		case *string:
			if s, ok := v.(string); ok {
				*d = s
			} else {
				return fmt.Errorf("fake: Scan type mismatch at index %d", i)
			}
		case *int64:
			switch n := v.(type) {
			case int64:
				*d = n
			case int:
				*d = int64(n)
			default:
				return fmt.Errorf("fake: Scan type mismatch at index %d", i)
			}
		case *[]byte:
			if b, ok := v.([]byte); ok {
				*d = b
			} else {
				return fmt.Errorf("fake: Scan type mismatch at index %d", i)
			}
		default:
			return fmt.Errorf("fake: Scan unsupported dest type at index %d", i)
		}
	}
	return nil
}

// fakePgxClient records SQL calls and returns pre-configured results.
type fakePgxClient struct {
	mu         sync.Mutex
	execCalls  []execCall
	queryCalls []execCall
	rows       map[string]*fakePgxRow // sql pattern -> row result (matched by substring)
	result     fakePgxResult

	// Failure injection: Exec returns failErr when the SQL contains failOn
	// (tests use this to fail one statement inside a transaction).
	failOn  string
	failErr error
}

type execCall struct {
	SQL  string
	Args []any
}

func newFakePgxClient() *fakePgxClient {
	return &fakePgxClient{
		rows: make(map[string]*fakePgxRow),
	}
}

func (f *fakePgxClient) Exec(_ context.Context, sql string, args ...any) (pgCommandTag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execCalls = append(f.execCalls, execCall{SQL: sql, Args: args})
	if f.failOn != "" && strings.Contains(sql, f.failOn) {
		return f.result, f.failErr
	}
	return f.result, nil
}

// InTx runs fn against the same recorder, prefixed/suffixed with BEGIN/COMMIT
// markers in execCalls so tests can assert the transactional envelope. Rollback
// semantics are NOT simulated here — FakePg is the all-or-nothing semantic fake.
func (f *fakePgxClient) InTx(_ context.Context, fn func(pgxClient) error) error {
	f.mu.Lock()
	f.execCalls = append(f.execCalls, execCall{SQL: "BEGIN"})
	f.mu.Unlock()
	if err := fn(f); err != nil {
		f.mu.Lock()
		f.execCalls = append(f.execCalls, execCall{SQL: "ROLLBACK"})
		f.mu.Unlock()
		return err
	}
	f.mu.Lock()
	f.execCalls = append(f.execCalls, execCall{SQL: "COMMIT"})
	f.mu.Unlock()
	return nil
}

func (f *fakePgxClient) QueryRow(_ context.Context, sql string, args ...any) pgxRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queryCalls = append(f.queryCalls, execCall{SQL: sql, Args: args})
	// Find the best matching row by SQL substring match.
	// The real SQL may contain leading whitespace from raw string literals.
	for pattern, row := range f.rows {
		if strings.Contains(sql, pattern) {
			return row
		}
	}
	return &fakePgxRow{}
}

var _ pgxClient = (*fakePgxClient)(nil)
