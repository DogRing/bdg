package persist

import (
	"context"
	"fmt"
	"time"

	"github.com/dogring/bdg/engine/core"
)

// goRedisClient is the minimal subset of operations RedisLiveStore needs.
// The concrete go-redis client satisfies this adapter; tests use FakeRedis.
type goRedisClient interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	Get(ctx context.Context, key string) (string, error)
	Del(ctx context.Context, keys ...string) error
	HSet(ctx context.Context, key string, fields ...string) error
	Expire(ctx context.Context, key string, expiration time.Duration) error
}

// RedisLiveStore implements LiveStore using a Redis-like client.
type RedisLiveStore struct {
	client goRedisClient
	ttl    time.Duration // TTL for snapshot and agent keys (zero = no expiry)
}

// NewRedisLiveStore creates a RedisLiveStore with the given client and optional TTL.
// Pass 0 for ttl to disable automatic key expiry (caller manages lifecycle via Expire).
func NewRedisLiveStore(client goRedisClient, ttl time.Duration) *RedisLiveStore {
	return &RedisLiveStore{
		client: client,
		ttl:    ttl,
	}
}

func (r *RedisLiveStore) WriteTick(ctx context.Context, run core.RunID, tick core.Tick) error {
	keyer := Keyer{Run: run}
	if err := r.client.Set(ctx, keyer.Tick(), fmt.Sprintf("%d", tick), 0); err != nil {
		return fmt.Errorf("redis.WriteTick: %w", err)
	}
	return nil
}

func (r *RedisLiveStore) WriteSnapshot(ctx context.Context, run core.RunID, blob []byte) error {
	keyer := Keyer{Run: run}
	if err := r.client.Set(ctx, keyer.SnapshotKey(), string(blob), r.ttl); err != nil {
		return fmt.Errorf("redis.WriteSnapshot: %w", err)
	}
	return nil
}

func (r *RedisLiveStore) WriteAgent(ctx context.Context, run core.RunID, v AgentView) error {
	keyer := Keyer{Run: run}
	key := keyer.Agent(v.ID)
	if err := r.client.HSet(ctx, key,
		"pos_x", fmt.Sprintf("%.4f", v.Pos.X),
		"pos_y", fmt.Sprintf("%.4f", v.Pos.Y),
		"goal", v.Goal,
		"action", v.Action,
		"mood", fmt.Sprintf("%.4f", v.Mood),
	); err != nil {
		return fmt.Errorf("redis.WriteAgent: %w", err)
	}
	if r.ttl > 0 {
		if err := r.client.Expire(ctx, key, r.ttl); err != nil {
			return fmt.Errorf("redis.WriteAgent.Expire: %w", err)
		}
	}
	return nil
}

func (r *RedisLiveStore) InitMeta(ctx context.Context, run core.RunID, m RunMeta) error {
	keyer := Keyer{Run: run}
	if err := r.client.HSet(ctx, keyer.Meta(),
		"tick", fmt.Sprintf("%d", m.Tick),
		"schema_version", fmt.Sprintf("%d", m.SchemaVersion),
		"started_at", m.StartedAt,
		"status", m.Status,
	); err != nil {
		return fmt.Errorf("redis.InitMeta: %w", err)
	}
	return nil
}

func (r *RedisLiveStore) ReadSnapshot(ctx context.Context, run core.RunID) ([]byte, error) {
	keyer := Keyer{Run: run}
	val, err := r.client.Get(ctx, keyer.SnapshotKey())
	if err != nil {
		return nil, fmt.Errorf("redis.ReadSnapshot: %w", err)
	}
	return []byte(val), nil
}

func (r *RedisLiveStore) Expire(ctx context.Context, run core.RunID) error {
	keyer := Keyer{Run: run}
	keys := []string{
		keyer.Meta(),
		keyer.Tick(),
		keyer.SnapshotKey(),
		keyer.Events(),
	}
	if err := r.client.Del(ctx, keys...); err != nil {
		return fmt.Errorf("redis.Expire: %w", err)
	}
	return nil
}

// Compile-time checks.
var _ LiveStore = (*RedisLiveStore)(nil)
var _ LiveStore = (*FakeRedis)(nil)
