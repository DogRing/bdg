# SPEC — `platform/events`

> Status: `DRAFT` (P4 — first wiring of the 4 unwired event types)
> Leaf level: `L1`  ·  Owner agent: `implementer`

## Purpose

Implements `core.EventEmitter` — the engine's **sole IO escape hatch for observability**.
Serializes each `core.Event` to JSON (data-contracts §4) and appends it to the run's Redis
STREAM (`sim:{run}:events`, data-contracts §2) via `XADD` (data-contracts §4). Provides the
why-trace + SSE event source. Contains **no simulation logic** — it only serializes and
transports the events the engine emits. It is a dumb forwarder (dependency inversion: the engine
imports `core`, never this package).

## Public Interface

```go
package events

import (
    "context"

    "github.com/dogring/bdg/engine/kernel/core"
)

// Emitter implements core.EventEmitter and writes to a Redis STREAM.
// It is created once per run and injected into the engine (dependency inversion;
// architecture §1 — the engine imports core, not platform).
type Emitter struct { /* opaque: redis client, run id, ctx, seq counter */ }

// New creates an Emitter for the given run. client is a live Redis client (injected via the
// minimal RedisClient interface). ctx scopes the run's XADD calls. By default seq starts at 0
// and each Emit call atomically increments it; options adjust construction.
func New(ctx context.Context, client RedisClient, runID core.RunID, opts ...Option) (*Emitter, error)

// Option configures an Emitter at construction.
type Option func(*Emitter)

// WithCallerSeq disables the Emitter's internal seq stamping: the caller (the run-driver's
// fan-out emitter, backend/main.go multiEmitter) has already stamped Event.Seq with ONE shared
// monotone numbering, so the Redis stream and the Postgres why-trace rows carry the SAME seq
// for the same event (data-contracts §4 — why-trace and SSE are two views of the same stream).
// The Emitter then serializes ev.Seq verbatim.
func WithCallerSeq() Option

// Emit satisfies core.EventEmitter (signature MUST match core.EventEmitter.Emit, which returns
// nothing — see Open Questions on the error channel). It serializes the event to JSON
// (data-contracts §4), stamps the next monotone seq, strips any real_stats field, and calls
// XADD sim:{runID}:events * payload <json>. Emit is synchronous: it blocks until XADD completes.
// A transport error is recorded on the Emitter (retrievable via Err) and does NOT panic — the
// run-driver inspects Err to decide retry/abort. Emit NEVER modifies the event payload beyond
// stamping seq and stripping real_stats.
func (e *Emitter) Emit(ev core.Event)

// EmitErr is the error-returning form used by the run-driver when it wants the failure inline
// (Emit routes through this and stores the result). Provided because core.EventEmitter.Emit
// cannot return an error; callers that are NOT the engine (platform/api, cmd/run) may use this.
func (e *Emitter) EmitErr(ev core.Event) error

// Err returns the first transport error observed by Emit since the last reset (nil if healthy).
// The run-driver checks this after a tick batch to decide whether to abort the run.
func (e *Emitter) Err() error

// LastStreamID returns the Redis STREAM entry ID of the LAST successfully appended event
// ("" before the first success). This is the transport replay cursor the run-driver stamps
// onto the snapshot wrapper as stream_cursor (data-contracts §1/§2): a failed XADD never
// advances it, so a snapshot cursor can never point past an entry that was not durably
// appended. It is the Redis ENTRY ID, deliberately distinct from Event.Seq (which is
// process-local and repeats across process restarts). Safe for concurrent use.
func (e *Emitter) LastStreamID() string

// RedisClient is the minimal interface Emitter needs from a Redis client. Using an interface
// (not a concrete *redis.Client) keeps the package testable without a live Redis (a stub
// satisfies RedisClient in tests). The concrete implementation is injected by the run-driver
// (platform/api or cmd/run). XAdd returns the entry ID Redis assigned (XADD with *), which
// feeds LastStreamID.
type RedisClient interface {
    XAdd(ctx context.Context, stream string, values map[string]string) (id string, err error)
}

// ── Event type constants (the full list from data-contracts §4, used as Event.Type values) ──
// Plain string constants — no iota enum, no bespoke type — so content additions never require a
// code change (D10 spirit). Listed here so callers (engine emitters, platform readers) have one
// import point and never type a raw string literal.

const (
    TypePerceived        = "Perceived"
    TypeGoalSelected     = "GoalSelected"
    TypePlanBuilt        = "PlanBuilt"
    TypeActionStarted    = "ActionStarted"
    TypeActionDone       = "ActionDone"
    TypeInteracted       = "Interacted"
    TypeBeliefUpdated    = "BeliefUpdated"
    TypeReputationGossip = "ReputationGossip"
    TypeCopingEntered    = "CopingEntered"
    TypeRoleEmerged      = "RoleEmerged"
    TypeTickDone         = "TickDone"
    TypeAgentFrame       = "AgentFrame"
    TypeWorldFrame       = "WorldFrame"
    TypeSnapshotReady    = "SnapshotReady"

    // WI-P4 ecosystem lifecycle events (data-contracts §4).
    TypeAnimalBorn   = "AnimalBorn"
    TypeAnimalDied   = "AnimalDied"
    TypePlantSpawned = "PlantSpawned"
    TypePlantDied    = "PlantDied"
)
```

> The numeric scale constants and stream keyspace come from data-contracts (§2, §4); this package
> hardcodes no simulation constant. The STREAM key is composed from the injected `runID`.

## Dependencies

- `engine/kernel/core` — `EventEmitter`, `Event`, `RunID`. The engine imports `core`; `platform/events`
  *implements* the `EventEmitter` interface. This is the **only** engine package `platform/events`
  imports (architecture §1).
- `encoding/json` — serialization of `Event` → the `payload` stream field.
- A Redis client — injected via the `RedisClient` interface (concrete:
  `github.com/redis/go-redis/v9` or equivalent), wired by the run-driver. The package depends on
  the interface, not the concrete client.
- `context` — scopes the run's XADD calls.
- **Contract — NOT imported**: no other `engine/*` package. A grep guard enforces this (AC #9).

## Owned Data

- The monotone `seq` counter (`int64`, reset to 0 on construction); incremented atomically per
  `Emit`. This is the simulation-sequence counter (data-contracts §4 `seq`), **not** the Redis
  entry ID.
- The Redis STREAM key (`sim:{runID}:events`), composed once from the injected `runID`.
- The serialization logic (`Event` → `payload` JSON, with the real_stats strip).
- **NOT owned**: the `Event` / `EventEmitter` definitions (those are `engine/kernel/core` +
  data-contracts), the payload *shapes* (those are the emitting engine module's responsibility),
  the concrete Redis client (injected), and the Redis STREAM consumer/SSE fan-out (`platform/api`).

## Invariants

- **Seq is monotone within one process lifetime**: each `Emit` atomically increments `seq` and
  stamps it on the serialized event; two events in the same tick are disambiguated by `seq`
  (data-contracts §4 `seq`). The counter starts at 0 with the EMITTER (i.e. the simulation
  process), not the run: `POST /api/restart` rebuilds in-process so the sequence continues,
  while a real process restart begins a new seq epoch at 0 under the same run_id
  (data-contracts §4 "Scope"). Under `WithCallerSeq` the stamping moves to the caller (one
  shared numbering across all sinks — Redis stream, stderr log, Postgres why-trace buffer); the
  Emitter forwards the pre-stamped `ev.Seq` verbatim and the (process-lifetime) monotonicity
  guarantee is the caller's.
- **No `real_stats` on the stream (data-contracts §4 SSE policy)**: before `XADD`, strip any
  `real_stats` key from the serialized payload. God-view fields live only in the simulation
  snapshot (data-contracts §1); they NEVER enter the event stream. This holds regardless of which
  event type carries it.
- **`XADD` with `*` (auto-id)**: Redis assigns the STREAM entry ID. The `seq` field in the payload
  is the simulation-sequence counter, distinct from the Redis entry ID.
- **Synchronous emit, no buffering**: `Emit` blocks until `XADD` returns. No background goroutine,
  no internal queue — the run-driver owns retry/abort policy (it inspects `Err`). This keeps event
  ORDER on the stream identical to the engine's deterministic emit order (D12: the engine already
  emits in fixed agent-ID / sorted order).
- **JSON serialization**: via `encoding/json`. Sorted map-key order is **not** enforced at this
  layer — the payload is an opaque string to Redis (and opaque JSONB to Postgres, data-contracts
  §3). Event *ORDER* determinism is guaranteed upstream by the engine's sorted iteration; payload
  *key order* is a formatting concern. A test that needs byte-determinism may sort keys before
  comparison; the `Emitter` itself does not sort.
- **No simulation logic / dumb forwarder**: the `Emitter` does NOT interpret event types, does NOT
  filter (beyond the `real_stats` strip), does NOT aggregate, and does NOT enforce payload shapes.
  It serializes and forwards. The only `Type`-aware code path is none — the constants exist for
  callers, not for branching here.
- **No engine import beyond `core`**: enforced by AC #9 (grep guard). This preserves the one-way
  `engine → core ← platform/events` dependency inversion (architecture §1).

## P4 additions — the 4 newly wired event types

For each newly-wired event type, the SPEC documents the **expected payload shape** so the emitting
engine module and the downstream readers (`platform/api`, analytics) agree. The `Emitter` does
**not** enforce these shapes (it is a dumb forwarder); the engine is responsible for emitting the
correct payload.

| Type | Emitted by (where) | Payload (data-contracts §4 + P4) |
|------|--------------------|----------------------------------|
| `Perceived` | `engine/agent` (tick phase 1, perceive) | `{sense: string, object_id: string, salience_delta: float64}` |
| `Interacted` | `engine/agent` (tick phase 6, signal) | `{with: AgentID, signal_kind: string, claimed_value: float64, accepted: bool}` |
| `ReputationGossip` | `engine/agent` (after each `GossipUpdate` call) | `{about: AgentID, from: AgentID, trust: float64, stat: StatID, delta: float64}` — **one event per stat that changed**; `delta = newMean − oldMean` |
| `RoleEmerged` | `engine/world` (after apply phase, reliance scan) | `{function: string, holder: AgentID, reliance_share: float64}` |

- **`ReputationGossip` is one event per changed stat**, not one bundled event per `GossipUpdate`
  call. This keeps the payload flat and lets the stream be filtered by `stat`. The emitter (the
  engine module) iterates the per-stat delta map returned by `tom.GossipUpdate` in sorted StatID
  order (D12) and emits one `core.Event` per non-zero stat. If **no** stat changed (all deltas
  zero, e.g. the gossip was below `min_trust` or was self-directed), **nothing is emitted**.
- The `Emitter`'s job for all four is identical: serialize, stamp `seq`, strip `real_stats`,
  `XADD`. The table is a documentation contract, not emitter behaviour.

## Acceptance Criteria (testable)

- [ ] **Emit writes XADD**: with a stub `RedisClient` recording calls, `Emitter.Emit(ev)` triggers
  **exactly one** `XAdd` call on stream `sim:{runID}:events`.
- [ ] **Seq is monotone**: three consecutive `Emit` calls produce `seq` 0, 1, 2 in the serialized
  payload (read back from the stub's recorded `values["payload"]`).
- [ ] **real_stats stripped**: an event whose payload JSON contains a `"real_stats"` key arrives at
  the `RedisClient` stub **without** that key (stripped before `XADD`); all other fields intact.
- [ ] **JSON payload round-trips**: the `XADD` `values` map contains a `"payload"` key whose JSON
  value round-trips back to the original `core.Event` (minus `real_stats` and with `seq` stamped).
- [ ] **Wired — Perceived**: when `engine/agent` emits `TypePerceived` (a new entity enters
  perceptual range), the `Emitter` writes it to the stream with the documented payload keys.
- [ ] **Wired — Interacted**: when `engine/agent` emits `TypeInteracted` after a Signal exchange,
  the `Emitter` writes it.
- [ ] **Wired — ReputationGossip**: after a `GossipUpdate` changes at least one stat, `engine/agent`
  emits `TypeReputationGossip` — **one event per changed stat** — and the `Emitter` writes each.
  A zero-delta gossip (below `min_trust`, or self) produces **no** event (the stub records no
  `ReputationGossip` XADD).
- [ ] **Wired — RoleEmerged**: when `engine/world`'s reliance scan crosses threshold it emits
  `TypeRoleEmerged`; the `Emitter` writes it.
- [ ] **LastStreamID tracks only successful appends**: after N successful `Emit`s,
  `LastStreamID()` equals the entry ID the stub returned for the LAST one; a failing `XAdd`
  leaves it at the previous value (a snapshot stream_cursor can never point past a lost entry);
  before any success it is `""`.
- [ ] **No real logic (import guard)**: a grep guard confirms `platform/events` imports only
  `engine/kernel/core` (no other `engine/*` package), `encoding/json`, `context`, and the redis interface
  package — nothing that performs simulation.
- [ ] **Stub RedisClient for tests**: every AC above passes against an in-memory stub that records
  `XAdd` calls; **no live Redis** is required for unit tests.

> Structural validation of the Redis keyspace / SSE policy lives in data-contracts; this module
> proves only that it serializes correctly and forwards via the injected `RedisClient`.

## Out of Scope

- The Redis STREAM consumer / SSE fan-out to the frontend → `platform/api` (later).
- Postgres persistence of events (data-contracts §3 `events` table) → `platform/persist`.
- Event filtering / aggregation for the frontend SSE view → `platform/api`.
- The concrete Redis client implementation (injected via `RedisClient`; the run-driver wires it).
- Payload-schema enforcement → the `Emitter` is a dumb forwarder; each **emitting engine module**
  owns its payload shape (the table above is the agreed contract, not a runtime check).
- `BeliefUpdated` wiring → already tracked in `engine/agent/SPEC.md` (emitted there); not new for P4.
- `ReputationGossip` *production* (the `GossipUpdate` fold + per-stat delta map) → `engine/mind/tom`
  (`backend/engine/mind/tom/SPEC.md` "P4 Gossip Propagation Contract"); this module only transports the
  events the agent emits from those deltas.

## Open Questions

- **`Emit` cannot return an error (BLOCKS the error-path AC — flag to architect/human).**
  `core.EventEmitter.Emit(e Event)` returns nothing (`engine/kernel/core/SPEC.md`), so the concrete
  `Emit` must also return nothing to satisfy the interface. This SPEC resolves it by: (a) `Emit`
  records the transport error on the Emitter and the run-driver reads it via `Err()`; (b) an
  `EmitErr(ev) error` sibling is provided for non-engine callers (platform/api, cmd/run) that want
  the error inline. Confirm this two-method shape is acceptable, or change `core.EventEmitter` to
  `Emit(Event) error` (a `core` contract change requiring all engine emit sites to adopt — escalate
  before editing `core`). Until confirmed, the implementer should follow (a)+(b) and NOT change
  `core`.
- **Per-event `schema_version` source (NOT blocking).** Each `core.Event` carries
  `SchemaVersion` (set by the engine emitter). The `Emitter` passes it through unchanged; it does
  not stamp or validate the version. Confirm the engine sets `SchemaVersion` at emit time (it does,
  per `engine/kernel/core/SPEC.md` Notes) so the `Emitter` never has to.

## Notes

- **Why an interface, not `*redis.Client`.** `RedisClient` is the minimal surface (`XAdd`) the
  Emitter needs; a stub satisfies it in tests so all ACs run without a live Redis. The concrete
  go-redis client is wired by the run-driver and is invisible to this package's tests.
- **`seq` vs Redis entry ID.** `XADD` uses `*` so Redis assigns the entry ID (ordering on the
  stream). The payload's `seq` is the simulation-sequence counter — the authoritative tie-break for
  two events in the same tick (data-contracts §4). They are independent counters; do not conflate.
- **Why no payload sorting here.** Event ORDER on the stream is already deterministic (the engine
  emits in fixed agent-ID / sorted-key order, D12). Payload key order is a JSON-formatting concern;
  tests that compare bytes sort keys themselves. Keeping the Emitter free of sorting avoids
  re-implementing a determinism guarantee the engine already owns.
- **The `Payload any` field** on `core.Event` is serialized opaquely by `encoding/json`; the
  `real_stats` strip operates on the serialized form (or on a known map shape) so it works
  regardless of the concrete payload struct (data-contracts §4 SSE policy).
</content>
