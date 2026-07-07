package persist

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── FakeRedis as LiveStore ────────────────────────────────────────────────────

func TestFakeRedisWriteTick(t *testing.T) {
	fake := NewFakeRedis()
	ctx := context.Background()
	run := core.RunID("test-run")

	if err := fake.WriteTick(ctx, run, 42); err != nil {
		t.Fatalf("WriteTick: %v", err)
	}
	if got := fake.TickOf(run); got != 42 {
		t.Errorf("TickOf = %d, want 42", got)
	}
}

func TestFakeRedisWriteSnapshot(t *testing.T) {
	fake := NewFakeRedis()
	ctx := context.Background()
	run := core.RunID("snap-test")
	blob := []byte(`{"snapshot":true}`)

	if err := fake.WriteSnapshot(ctx, run, blob); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	got := fake.SnapshotOf(run)
	if string(got) != string(blob) {
		t.Errorf("SnapshotOf = %q, want %q", string(got), string(blob))
	}
}

func TestFakeRedisReadSnapshot(t *testing.T) {
	fake := NewFakeRedis()
	ctx := context.Background()
	run := core.RunID("read-test")
	blob := []byte(`{"data":"value"}`)

	if err := fake.WriteSnapshot(ctx, run, blob); err != nil {
		t.Fatal(err)
	}
	got, err := fake.ReadSnapshot(ctx, run)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if string(got) != string(blob) {
		t.Errorf("ReadSnapshot = %q, want %q", string(got), string(blob))
	}

	_, err = fake.ReadSnapshot(ctx, "no-such-run")
	if err == nil {
		t.Fatal("expected error for non-existent run")
	}
}

func TestFakeRedisWriteAgent(t *testing.T) {
	fake := NewFakeRedis()
	ctx := context.Background()
	run := core.RunID("agent-test")

	v := AgentView{
		ID:     "agent_01",
		Pos:    core.Vec2{X: 1.5, Y: 2.5},
		Goal:   "Satiety",
		Action: "Eat",
		Mood:   0.75,
	}
	if err := fake.WriteAgent(ctx, run, v); err != nil {
		t.Fatalf("WriteAgent: %v", err)
	}

	got := fake.AgentViewOf(run, "agent_01")
	if got == "" {
		t.Fatal("AgentViewOf returned empty string")
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(got), &raw); err != nil {
		t.Fatalf("unmarshal agent view: %v", err)
	}
	if raw["id"] != "agent_01" {
		t.Errorf("id = %v, want agent_01", raw["id"])
	}
	if raw["goal"] != "Satiety" {
		t.Errorf("goal = %v, want Satiety", raw["goal"])
	}
	forbidden := []string{"real_stats", "tom", "RealStats"}
	for _, key := range forbidden {
		if _, ok := raw[key]; ok {
			t.Errorf("agent view contains forbidden key %q", key)
		}
	}
}

func TestFakeRedisInitMeta(t *testing.T) {
	fake := NewFakeRedis()
	ctx := context.Background()
	run := core.RunID("meta-test")

	m := RunMeta{
		Tick:          1,
		SchemaVersion: SchemaVersion,
		StartedAt:     "2026-06-21T00:00:00Z",
		Status:        "running",
	}
	if err := fake.InitMeta(ctx, run, m); err != nil {
		t.Fatalf("InitMeta: %v", err)
	}

	if got := fake.MetaField(run, "tick"); got != "1" {
		t.Errorf("meta tick = %q, want 1", got)
	}
	if got := fake.MetaField(run, "schema_version"); got != "1" {
		t.Errorf("meta schema_version = %q, want 1", got)
	}
	if got := fake.MetaField(run, "started_at"); got != "2026-06-21T00:00:00Z" {
		t.Errorf("meta started_at = %q, want 2026-06-21T00:00:00Z", got)
	}
	if got := fake.MetaField(run, "status"); got != "running" {
		t.Errorf("meta status = %q, want running", got)
	}
}

func TestFakeRedisExpire(t *testing.T) {
	fake := NewFakeRedis()
	ctx := context.Background()
	run := core.RunID("expire-test")

	_ = fake.WriteTick(ctx, run, 10)
	_ = fake.WriteSnapshot(ctx, run, []byte("data"))
	_ = fake.WriteAgent(ctx, run, AgentView{ID: "agent_01", Pos: core.Vec2{}, Goal: "", Action: "", Mood: 0})
	_ = fake.InitMeta(ctx, run, RunMeta{Tick: 10, SchemaVersion: 1, StartedAt: "", Status: "running"})

	if err := fake.Expire(ctx, run); err != nil {
		t.Fatalf("Expire: %v", err)
	}

	if !fake.Expired() {
		t.Error("Expected Expired() to be true after Expire call")
	}
	if fake.TickOf(run) != 0 {
		t.Error("TickOf should return 0 after expire")
	}
	if fake.SnapshotOf(run) != nil {
		t.Error("SnapshotOf should return nil after expire")
	}
	if len(fake.AgentIDs(run)) != 0 {
		t.Error("AgentIDs should be empty after expire")
	}
}

// ── RedisLiveStore via fakeGoRedisClient ──────────────────────────────────────

func TestRedisLiveStoreWriteTick(t *testing.T) {
	client := newFakeGoRedisClient()
	store := NewRedisLiveStore(client, 0)
	ctx := context.Background()
	run := core.RunID("live-tick")

	if err := store.WriteTick(ctx, run, 7); err != nil {
		t.Fatal(err)
	}
	key := Keyer{Run: run}.Tick()
	val, err := client.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if val != "7" {
		t.Errorf("tick = %q, want 7", val)
	}
}

func TestRedisLiveStoreWriteAndReadSnapshot(t *testing.T) {
	client := newFakeGoRedisClient()
	store := NewRedisLiveStore(client, 0)
	ctx := context.Background()
	run := core.RunID("live-snap")

	blob := []byte(`{"hello":"world"}`)
	if err := store.WriteSnapshot(ctx, run, blob); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadSnapshot(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(blob) {
		t.Errorf("snapshot = %q, want %q", string(got), string(blob))
	}
}

func TestRedisLiveStoreWriteAgent(t *testing.T) {
	client := newFakeGoRedisClient()
	store := NewRedisLiveStore(client, 0)
	ctx := context.Background()
	run := core.RunID("live-agent")

	v := AgentView{ID: "a1", Pos: core.Vec2{X: 1, Y: 2}, Goal: "Rest", Action: "Sleep", Mood: 0.5}
	if err := store.WriteAgent(ctx, run, v); err != nil {
		t.Fatal(err)
	}

	// Verify the agent key was written as a hash in the underlying client.
	key := Keyer{Run: run}.Agent("a1")
	// The fake stores hashes; we can verify by checking the hash fields.
	// Expire on the key should succeed if the key exists.
	if err := client.Expire(ctx, key, 0); err != nil {
		t.Errorf("agent key %q not found (Expire failed): %v", key, err)
	}
}

func TestRedisLiveStoreInitMeta(t *testing.T) {
	client := newFakeGoRedisClient()
	store := NewRedisLiveStore(client, 0)
	ctx := context.Background()
	run := core.RunID("live-meta")

	m := RunMeta{Tick: 7, SchemaVersion: SchemaVersion, StartedAt: "now", Status: "running"}
	if err := store.InitMeta(ctx, run, m); err != nil {
		t.Fatal(err)
	}

	// Verify the meta key was written by checking Expire succeeds on the hash.
	key := Keyer{Run: run}.Meta()
	if err := client.Expire(ctx, key, 0); err != nil {
		t.Errorf("meta key %q not found (Expire failed): %v", key, err)
	}
}

func TestRedisLiveStoreExpire(t *testing.T) {
	client := newFakeGoRedisClient()
	store := NewRedisLiveStore(client, 0)
	ctx := context.Background()
	run := core.RunID("live-expire")

	// Write some data.
	_ = store.WriteTick(ctx, run, 10)
	_ = store.WriteSnapshot(ctx, run, []byte("data"))

	// Expire should succeed.
	if err := store.Expire(ctx, run); err != nil {
		t.Fatal(err)
	}

	// After expire, ReadSnapshot should fail.
	_, err := store.ReadSnapshot(ctx, run)
	if err == nil {
		t.Error("expected error after Expire")
	}
}

// ── RedisLiveStore WI-P4 env render keys via fakeGoRedisClient ────────────────

func TestRedisLiveStoreWriteAnimal(t *testing.T) {
	client := newFakeGoRedisClient()
	store := NewRedisLiveStore(client, 0)
	ctx := context.Background()
	run := core.RunID("live-animal")

	v := AnimalView{ID: "an:d1", Pos: core.Vec2{X: 1.5, Y: -2.25}, Species: "deer",
		Action: "Graze", Heading: 0.5, Stamina: 0.875}
	if err := store.WriteAnimal(ctx, run, v); err != nil {
		t.Fatal(err)
	}

	key := Keyer{Run: run}.Animal("an:d1")
	h := client.hashes[key]
	if h == nil {
		t.Fatalf("animal hash %q not written", key)
	}
	want := map[string]string{
		"pos_x": "1.5000", "pos_y": "-2.2500",
		"species": "deer", "action": "Graze",
		"heading": "0.500000", "stamina": "0.8750",
	}
	for f, w := range want {
		if h[f] != w {
			t.Errorf("field %s = %q, want %q", f, h[f], w)
		}
	}
	for _, forbidden := range []string{"stats", "drives", "vital", "real_stats"} {
		if _, ok := h[forbidden]; ok {
			t.Errorf("animal hash contains forbidden god-view field %q", forbidden)
		}
	}
}

func TestRedisLiveStoreWriteFlora(t *testing.T) {
	client := newFakeGoRedisClient()
	store := NewRedisLiveStore(client, 0)
	ctx := context.Background()
	run := core.RunID("live-flora")

	plants := []FloraView{
		{ID: "pl:1", Species: "oak", Pos: core.Vec2{X: 3, Y: 4}, Stage: 2, Width: 1.5},
	}
	if err := store.WriteFlora(ctx, run, plants); err != nil {
		t.Fatal(err)
	}

	blob, err := client.Get(ctx, Keyer{Run: run}.Flora())
	if err != nil {
		t.Fatalf("Get flora key: %v", err)
	}
	var got []FloraView
	if err := json.Unmarshal([]byte(blob), &got); err != nil {
		t.Fatalf("flora key not a FloraView array: %v", err)
	}
	if len(got) != 1 || got[0] != plants[0] {
		t.Errorf("flora round-trip = %+v, want %+v", got, plants)
	}

	// nil plants ⇒ JSON "[]" (empty-but-installed), never "null".
	if err := store.WriteFlora(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	blob, _ = client.Get(ctx, Keyer{Run: run}.Flora())
	if blob != "[]" {
		t.Errorf("nil plants stored as %q, want []", blob)
	}
}

func TestRedisLiveStoreWriteClimate(t *testing.T) {
	client := newFakeGoRedisClient()
	store := NewRedisLiveStore(client, 0)
	ctx := context.Background()
	run := core.RunID("live-climate")

	v := ClimateView{Temperature: 18.5, Moisture: 0.4, Raining: true,
		WindDir: 1.5, WindMag: 0.6, HourOfDay: 10, DayNight: "day", YearFraction: 0.25}
	if err := store.WriteClimate(ctx, run, v); err != nil {
		t.Fatal(err)
	}

	h := client.hashes[Keyer{Run: run}.Climate()]
	if h == nil {
		t.Fatal("climate hash not written")
	}
	want := map[string]string{
		"temperature": "18.5000", "moisture": "0.4000", "raining": "true",
		"wind_dir": "1.500000", "wind_mag": "0.6000",
		"hour_of_day": "10", "day_night": "day", "year_fraction": "0.250000",
	}
	for f, w := range want {
		if h[f] != w {
			t.Errorf("field %s = %q, want %q", f, h[f], w)
		}
	}
	// ApparentTemp nil ⇒ field absent, not zero.
	if _, ok := h["apparent_temp"]; ok {
		t.Error("apparent_temp written despite nil ApparentTemp")
	}
}

func TestRedisLiveStoreWriteTerrain(t *testing.T) {
	client := newFakeGoRedisClient()
	store := NewRedisLiveStore(client, 0)
	ctx := context.Background()
	run := core.RunID("live-terrain")

	v := TerrainView{CellSize: 2, Orientation: "flat", Size: TerrainSize{Cols: 2, Rows: 1},
		Terrain: []string{"grass", "water"}, Wear: []float64{0, 0.5}}
	if err := store.WriteTerrain(ctx, run, v); err != nil {
		t.Fatal(err)
	}

	blob, err := client.Get(ctx, Keyer{Run: run}.Terrain())
	if err != nil {
		t.Fatalf("Get terrain key: %v", err)
	}
	// The stored bytes ARE the GET /api/terrain response (forwarded verbatim) —
	// assert the exact wire shape the frontend TerrainGrid contract expects.
	var got map[string]any
	if err := json.Unmarshal([]byte(blob), &got); err != nil {
		t.Fatalf("terrain key not JSON: %v", err)
	}
	if got["cell_size"] != 2.0 {
		t.Errorf("cell_size = %v, want 2", got["cell_size"])
	}
	if got["orientation"] != "flat" {
		t.Errorf("orientation = %v, want flat", got["orientation"])
	}
	size, _ := got["size"].(map[string]any)
	if size == nil || size["cols"] != 2.0 || size["rows"] != 1.0 {
		t.Errorf("size = %v, want {cols:2,rows:1}", got["size"])
	}
	terrain, _ := got["terrain"].([]any)
	if len(terrain) != 2 || terrain[0] != "grass" || terrain[1] != "water" {
		t.Errorf("terrain = %v, want [grass water]", got["terrain"])
	}
}
