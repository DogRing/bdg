package persist

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dogring/bdg/engine/kernel/core"
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

// WriteAnimal upserts the per-animal render hash (WI-P4, §2). Mirrors
// WriteAgent exactly — same TTL policy, same god-view boundary shape (no
// Stats/Drives/Vital field to write, by construction of AnimalView).
func (r *RedisLiveStore) WriteAnimal(ctx context.Context, run core.RunID, v AnimalView) error {
	keyer := Keyer{Run: run}
	key := keyer.Animal(v.ID)
	if err := r.client.HSet(ctx, key,
		"pos_x", fmt.Sprintf("%.4f", v.Pos.X),
		"pos_y", fmt.Sprintf("%.4f", v.Pos.Y),
		"species", v.Species,
		"action", v.Action,
		"heading", fmt.Sprintf("%.6f", v.Heading),
		"stamina", fmt.Sprintf("%.4f", v.Stamina),
		"cover_id", string(v.CoverID),
	); err != nil {
		return fmt.Errorf("redis.WriteAnimal: %w", err)
	}
	if r.ttl > 0 {
		if err := r.client.Expire(ctx, key, r.ttl); err != nil {
			return fmt.Errorf("redis.WriteAnimal.Expire: %w", err)
		}
	}
	return nil
}

// WriteFlora replaces sim:{run}:flora with the full live plant render set
// (WI-P4, §2) — a JSON array, periodic-full (not a delta).
func (r *RedisLiveStore) WriteFlora(ctx context.Context, run core.RunID, plants []FloraView) error {
	if plants == nil {
		plants = []FloraView{} // JSON "[]", not "null" — an empty-but-installed set
	}
	blob, err := json.Marshal(plants)
	if err != nil {
		return fmt.Errorf("redis.WriteFlora: marshal: %w", err)
	}
	keyer := Keyer{Run: run}
	if err := r.client.Set(ctx, keyer.Flora(), string(blob), r.ttl); err != nil {
		return fmt.Errorf("redis.WriteFlora: %w", err)
	}
	return nil
}

// WriteClimate upserts the sim:{run}:climate ambient hash (WI-P4, §2).
func (r *RedisLiveStore) WriteClimate(ctx context.Context, run core.RunID, v ClimateView) error {
	fields := []string{
		"temperature", fmt.Sprintf("%.4f", v.Temperature),
		"moisture", fmt.Sprintf("%.4f", v.Moisture),
		"raining", fmt.Sprintf("%t", v.Raining),
		"snow_cover", fmt.Sprintf("%.4f", v.SnowCover),
		"wind_dir", fmt.Sprintf("%.6f", v.WindDir),
		"wind_mag", fmt.Sprintf("%.4f", v.WindMag),
		"hour_of_day", fmt.Sprintf("%d", v.HourOfDay),
		"day_night", v.DayNight,
		"year_fraction", fmt.Sprintf("%.6f", v.YearFraction),
	}
	if v.ApparentTemp != nil {
		fields = append(fields, "apparent_temp", fmt.Sprintf("%.4f", *v.ApparentTemp))
	}
	keyer := Keyer{Run: run}
	if err := r.client.HSet(ctx, keyer.Climate(), fields...); err != nil {
		return fmt.Errorf("redis.WriteClimate: %w", err)
	}
	return nil
}

// WriteTerrain replaces sim:{run}:terrain with the full render terrain grid
// (WI-P4, §2) — the SAME JSON shape GET /api/terrain returns (platform/api
// forwards the stored bytes verbatim, no reshaping).
func (r *RedisLiveStore) WriteTerrain(ctx context.Context, run core.RunID, v TerrainView) error {
	blob, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("redis.WriteTerrain: marshal: %w", err)
	}
	keyer := Keyer{Run: run}
	if err := r.client.Set(ctx, keyer.Terrain(), string(blob), r.ttl); err != nil {
		return fmt.Errorf("redis.WriteTerrain: %w", err)
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

// PublishWorldRevision publishes the single-world revision marker: one HSET of
// {world_revision, terrain} onto sim:{run}:meta (data-contracts §2). Called by
// the run-driver only AFTER the revision's live baselines were written, so a
// reader observing the new value finds matching baselines servable. InitMeta
// never touches these fields.
func (r *RedisLiveStore) PublishWorldRevision(ctx context.Context, run core.RunID, rev int64, terrainOn bool) error {
	keyer := Keyer{Run: run}
	terrain := "off"
	if terrainOn {
		terrain = "on"
	}
	if err := r.client.HSet(ctx, keyer.Meta(),
		"world_revision", fmt.Sprintf("%d", rev),
		"terrain", terrain,
	); err != nil {
		return fmt.Errorf("redis.PublishWorldRevision: %w", err)
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
		// WI-P4: the bounded (single-key-per-run) env keys. Per-animal keys
		// (sim:{run}:animal:{id}) are NOT enumerable here without a SCAN — same
		// pre-existing limitation as sim:{run}:agent:{id} (both rely on the ttl
		// passed to NewRedisLiveStore, or a future SCAN-based sweep).
		keyer.Flora(),
		keyer.Climate(),
		keyer.Terrain(),
	}
	if err := r.client.Del(ctx, keys...); err != nil {
		return fmt.Errorf("redis.Expire: %w", err)
	}
	return nil
}

// Compile-time checks.
var _ LiveStore = (*RedisLiveStore)(nil)
var _ LiveStore = (*FakeRedis)(nil)
