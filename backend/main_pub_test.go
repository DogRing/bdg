package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/platform/persist"
)

// recordingLiveStore wraps persist.FakeRedis and records the baseline-relevant
// calls IN ORDER (snapshot/terrain writes, revision publications), with
// injectable write failures — the observability worldPub's publish-last
// ordering tests need (persist SPEC "restart vs regen" steps 4–5).
type recordingLiveStore struct {
	*persist.FakeRedis
	mu                sync.Mutex
	calls             []string
	failSnapshotWrite int // fail the next N WriteSnapshot calls
	failPublish       error
}

func newRecordingLiveStore() *recordingLiveStore {
	return &recordingLiveStore{FakeRedis: persist.NewFakeRedis()}
}

func (r *recordingLiveStore) log(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

func (r *recordingLiveStore) WriteSnapshot(ctx context.Context, run core.RunID, blob []byte) error {
	r.mu.Lock()
	fail := r.failSnapshotWrite > 0
	if fail {
		r.failSnapshotWrite--
	}
	r.mu.Unlock()
	if fail {
		r.log("snapshot:FAIL")
		return context.DeadlineExceeded
	}
	r.log("snapshot")
	return r.FakeRedis.WriteSnapshot(ctx, run, blob)
}

func (r *recordingLiveStore) WriteTerrain(ctx context.Context, run core.RunID, v persist.TerrainView) error {
	r.log("terrain")
	return r.FakeRedis.WriteTerrain(ctx, run, v)
}

func (r *recordingLiveStore) PublishWorldRevision(ctx context.Context, run core.RunID, rev int64, terrainOn, floraOn bool) error {
	if r.failPublish != nil {
		r.log("publish:FAIL")
		return r.failPublish
	}
	r.log(fmt.Sprintf("publish:%d:%t", rev, terrainOn))
	return r.FakeRedis.PublishWorldRevision(ctx, run, rev, terrainOn, floraOn)
}

func (r *recordingLiveStore) publishes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		if strings.HasPrefix(c, "publish:") {
			out = append(out, c)
		}
	}
	return out
}

func (r *recordingLiveStore) callList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

type recordingBackupStore struct {
	*persist.FakePg
	blobs [][]byte
}

func newRecordingBackupStore() *recordingBackupStore {
	return &recordingBackupStore{FakePg: persist.NewFakePg()}
}

func (r *recordingBackupStore) WriteBackup(ctx context.Context, run core.RunID, tick core.Tick, blob []byte, evs []core.Event) error {
	r.blobs = append(r.blobs, append([]byte(nil), blob...))
	return r.FakePg.WriteBackup(ctx, run, tick, blob, evs)
}

// Redis receives a publication-stamped live wrapper, while Postgres receives
// byte-stable deterministic state. Changing only publication metadata must not
// alter the backup blob for an otherwise unchanged world.
func TestFlushSnapshotSeparatesLiveWrapperFromBackupBlob(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}

	live := newRecordingLiveStore()
	backup := newRecordingBackupStore()
	cursor := "123-4"
	pub := &worldPub{live: live, runID: "test-run", revision: 7,
		cursorFn: func() string { return cursor }}

	flushSnapshot(context.Background(), w, "test-run", pub, live, backup, nil)
	firstLive := append([]byte(nil), live.SnapshotOf("test-run")...)

	pub.revision = 8
	cursor = "456-7"
	flushSnapshot(context.Background(), w, "test-run", pub, live, backup, nil)
	secondLive := live.SnapshotOf("test-run")

	if len(backup.blobs) != 2 {
		t.Fatalf("backup writes = %d, want 2", len(backup.blobs))
	}
	if !bytes.Equal(backup.blobs[0], backup.blobs[1]) {
		t.Error("publication-only changes altered deterministic Postgres backup bytes")
	}
	if bytes.Equal(firstLive, secondLive) {
		t.Error("Redis live snapshot did not change with publication metadata")
	}

	backupSnapshot, err := persist.Decode(backup.blobs[0])
	if err != nil {
		t.Fatalf("decode backup snapshot: %v", err)
	}
	if backupSnapshot.WorldRevision != 0 || backupSnapshot.StreamCursor != "" || backupSnapshot.TerrainStatus != "" {
		t.Errorf("backup wrapper = {revision:%d cursor:%q terrain:%q}, want zero values",
			backupSnapshot.WorldRevision, backupSnapshot.StreamCursor, backupSnapshot.TerrainStatus)
	}

	liveSnapshot, err := persist.Decode(secondLive)
	if err != nil {
		t.Fatalf("decode live snapshot: %v", err)
	}
	if liveSnapshot.WorldRevision != 8 || liveSnapshot.StreamCursor != "456-7" || liveSnapshot.TerrainStatus != "on" {
		t.Errorf("live wrapper = {revision:%d cursor:%q terrain:%q}, want {8 %q on}",
			liveSnapshot.WorldRevision, liveSnapshot.StreamCursor, liveSnapshot.TerrainStatus, "456-7")
	}
}

// A successful regen bumps the revision BEFORE the fresh flush (the baselines
// are tagged with it) and publishes it LAST — strictly after the snapshot and
// terrain writes of the same flush. The superseded boot revision is never
// published (no reuse, no ambiguous publication).
func TestWorldPubRegenPublishesLastAfterBaseline(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}
	rec := newRecordingLiveStore()
	pub := &worldPub{live: rec, runID: "test-run", revision: 1,
		cursorFn: func() string { return "123-4" }}

	regen := make(chan int64, 1)
	regen <- 42
	_ = runLoop(context.Background(), w, 1, "test-run", 0, rec, nil, nil, 0, loopControl{
		regen:   regen,
		rebuild: build,
		reset:   func(context.Context, *world.World, int64) error { return nil },
		pub:     pub,
	})

	pubs := rec.publishes()
	if len(pubs) != 1 || pubs[0] != "publish:2:true" {
		t.Fatalf("publishes = %v, want exactly [publish:2:true] (bumped rev, terrain on, boot rev 1 never published)", pubs)
	}
	calls := rec.callList()
	pubIdx, snapIdx, terrIdx := -1, -1, -1
	for i, c := range calls {
		switch {
		case strings.HasPrefix(c, "publish:"):
			pubIdx = i
		case c == "snapshot":
			snapIdx = i
		case c == "terrain":
			terrIdx = i
		}
	}
	if pubIdx < snapIdx || pubIdx < terrIdx {
		t.Errorf("publication not LAST: calls = %v", calls)
	}
	if !pub.published || pub.revision != 2 {
		t.Errorf("pub state = {rev:%d published:%t}, want {2 true}", pub.revision, pub.published)
	}

	// The published baselines are revision-tagged (data-contracts §1/§2).
	blob := string(rec.SnapshotOf("test-run"))
	for _, want := range []string{`"world_revision":2`, `"stream_cursor":"123-4"`, `"terrain":"on"`} {
		if !strings.Contains(blob, want) {
			t.Errorf("published snapshot missing %s", want)
		}
	}
	if terr := rec.TerrainOf("test-run"); !strings.Contains(terr, `"world_revision":2`) {
		t.Errorf("published terrain missing revision tag: %s", terr)
	}
	if got := rec.MetaField("test-run", "world_revision"); got != "2" {
		t.Errorf("meta world_revision = %q, want 2", got)
	}
	if got := rec.MetaField("test-run", "terrain"); got != "on" {
		t.Errorf("meta terrain = %q, want on", got)
	}
}

// Failed rebuilds and failed mandatory resets never change the revision — the
// old published revision keeps identifying the still-running world.
func TestWorldPubUnchangedOnFailedRegen(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}

	for name, ctl := range map[string]loopControl{
		"rebuild-failure": {
			rebuild: func(int64) (*world.World, error) { return nil, context.DeadlineExceeded },
		},
		"reset-failure": {
			rebuild: build,
			reset:   func(context.Context, *world.World, int64) error { return context.DeadlineExceeded },
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := newRecordingLiveStore()
			pub := &worldPub{live: rec, runID: "test-run", revision: 5, published: true}
			regen := make(chan int64, 1)
			regen <- 42
			ctl.regen = regen
			ctl.pub = pub
			_ = runLoop(context.Background(), w, 1, "test-run", 0, rec, nil, nil, 0, ctl)

			if pub.revision != 5 || !pub.published {
				t.Errorf("pub state = {rev:%d published:%t}, want unchanged {5 true}", pub.revision, pub.published)
			}
			if pubs := rec.publishes(); len(pubs) != 0 {
				t.Errorf("publishes = %v, want none after an aborted regen", pubs)
			}
		})
	}
}

// A successful restart keeps the revision: same marker, no re-publication.
func TestWorldPubRestartDoesNotChangeRevision(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}
	rec := newRecordingLiveStore()
	pub := &worldPub{live: rec, runID: "test-run", revision: 3, published: true}

	restart := make(chan struct{}, 1)
	restart <- struct{}{}
	_ = runLoop(context.Background(), w, 1, "test-run", 0, rec, nil, nil, 0, loopControl{
		restart: restart,
		rebuild: build,
		pub:     pub,
	})

	if pub.revision != 3 || !pub.published {
		t.Errorf("pub state = {rev:%d published:%t}, want unchanged {3 true}", pub.revision, pub.published)
	}
	if pubs := rec.publishes(); len(pubs) != 0 {
		t.Errorf("publishes = %v, want none on restart (revision already visible)", pubs)
	}
	// The restart flush still tags the baseline with the SAME revision.
	if blob := string(rec.SnapshotOf("test-run")); !strings.Contains(blob, `"world_revision":3`) {
		t.Errorf("restart baseline lost its revision tag: %s", blob)
	}
}

// A pending revision is NOT published while the baseline write fails; the next
// cadence flush retries flush+publication (self-healing, publish-after-ready).
func TestWorldPubPublishDeferredUntilBaselineReady(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}
	rec := newRecordingLiveStore()
	rec.failSnapshotWrite = 1                                   // first cadence flush: baseline not ready
	pub := &worldPub{live: rec, runID: "test-run", revision: 1} // boot: pending

	_ = runLoop(context.Background(), w, 2, "test-run", 1, rec, nil, nil, 0, loopControl{pub: pub})

	pubs := rec.publishes()
	if len(pubs) != 1 || pubs[0] != "publish:1:true" {
		t.Fatalf("publishes = %v, want exactly [publish:1:true] (deferred past the failed flush)", pubs)
	}
	calls := rec.callList()
	if calls[0] != "snapshot:FAIL" {
		t.Fatalf("calls = %v, want the first flush to fail", calls)
	}
	pubIdx, okSnapIdx := -1, -1
	for i, c := range calls {
		if c == "snapshot" && okSnapIdx == -1 {
			okSnapIdx = i
		}
		if strings.HasPrefix(c, "publish:") {
			pubIdx = i
		}
	}
	if pubIdx < okSnapIdx {
		t.Errorf("publication before the first SUCCESSFUL snapshot write: %v", calls)
	}
	if !pub.published {
		t.Error("pub not marked published after the successful retry")
	}
}

// Back-to-back regens claim strictly increasing revisions — no reuse.
func TestWorldPubRepeatedRegensNeverReuseARevision(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}
	rec := newRecordingLiveStore()
	pub := &worldPub{live: rec, runID: "test-run", revision: 1, published: true}

	for i, seed := range []int64{41, 43} {
		regen := make(chan int64, 1)
		regen <- seed
		w = runLoop(context.Background(), w, 1, "test-run", 0, rec, nil, nil, 0, loopControl{
			regen:   regen,
			rebuild: build,
			reset:   func(context.Context, *world.World, int64) error { return nil },
			pub:     pub,
		})
		want := int64(2 + i)
		if pub.revision != want || !pub.published {
			t.Fatalf("after regen %d: pub = {rev:%d published:%t}, want {%d true}", i+1, pub.revision, pub.published, want)
		}
	}
	pubs := rec.publishes()
	if len(pubs) != 2 || pubs[0] != "publish:2:true" || pubs[1] != "publish:3:true" {
		t.Errorf("publishes = %v, want [publish:2:true publish:3:true]", pubs)
	}
}
