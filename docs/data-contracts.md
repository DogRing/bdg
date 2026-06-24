# Data Contracts — Serialization · Redis · Postgres · Events

> Contracts that cross module boundaries. **Freeze before implementing consumers.** Bump `schema_version` and start here for any change.
> The below defines *shapes*. Exact field types are finalized by the architect against the `core` types.

## 0. Common
- Every payload carries `schema_version` (int). Bump +1 on any backward-incompatible change.
- Serialization: JSON during early debugging; may switch to a deterministic dense encoding (gob/protobuf) after it stabilizes — but **byte-determinism** must hold (sorted map-key order).
- IDs: `RunID`, `AgentID`, `ObjectID` are strings. Tick is an integer (game-minutes).

## 1. Simulation snapshot (engine → persist)
The complete deterministic state for one tick. Same snapshot + same seed → same next tick.
```
Snapshot {                        // persist.Snapshot — JSON, snake_case keys (Go field tags)
  schema_version, run_id, tick
  world {                          // engine world.WorldState
    tick, rng_state               // rng_state: for deterministic resume
    agents[] {
      id, pos
      real_stats   { StatID: float }            // god view (live exposure policy in §4)
      stamina, mood, adrenaline
      need_intensities { Dimension: float }
      inventory { Tag: int }, goal
      plan_actions[], plan_horizon, plan_idx, elapsed, coping, latent[]
      self_est_stats { StatID: { mean, variance } }   // ToM[self] (D8 self-channel)
      agent_cfg
    }
    objects[] { ... }
    known[]   { agent_id, objects[] }            // per-agent known-object set
    emerged_roles[] { function, holder }         // P6 (D2-derived)
    tom_digest {                                 // cross-agent ToM, god-view projection (D6/D8)
      <observerID>: { <subjectID>: { est_stats: { StatID: { mean, variance } }, rely_on: { Function: float } } }
    }
  }
}
```
- `tom_digest` is **capture-only** (NOT consumed by resume; the running sim rebuilds beliefs). It is the
  source for the god-view "others" channel: `real` (real_stats) ≠ `self` (self_est_stats) ≠ `others`
  (mean over `tom_digest[X][subject].est_stats`) are kept SEPARATE (D6/D8) — never a single reputation scalar.
- All keys are snake_case so the read API (`platform/api`) parses the live snapshot blob directly.

## 2. Redis — live state
Keyspace (`{run}` = RunID):
```
sim:{run}:meta          HASH    { tick, schema_version, started_at, status }
sim:{run}:tick          STRING  current tick (fast read)
sim:{run}:snapshot      STRING  latest Snapshot (serialized)  // or chunked
sim:{run}:agent:{id}    HASH     live agent summary (render): pos, goal, action, mood
sim:{run}:events        STREAM   events (§4). XADD append; SSE tails
```
- Live keys hold **only what render/observation needs.** Full state lives in the snapshot key.
- TTL: keep only active runs; on completion, back up to Postgres then expire.

## 3. Postgres — periodic backup (replay / analytics)
```
runs       ( run_id PK, seed, schema_version, started_at, ended_at, status, config_hash )
snapshots  ( run_id FK, tick, blob BYTEA, created_at )            // every N ticks
events     ( run_id FK, tick, seq, agent_id, type, payload JSONB ) // why-trace persisted
```
- Backup interval from config (`backup_every_ticks`). Reproduce from `seed + config_hash + last snapshot`.
- `events` is the source for why-trace, emergence-metric analysis, and replay.

## 4. Event / SSE schema (observability + frontend)
The engine emits via the `events` interface. why-trace and SSE are **two views of the same stream.**
```
Event {
  schema_version, tick, seq, agent_id?, type, payload
}
```
Representative `type`s (payload gist):
| type | payload |
|------|---------|
| `Perceived` | sense, object_id, salience_delta |
| `GoalSelected` | dimension, target, priority, eff_value |
| `PlanBuilt` | steps[], total_cost, provisioned[] |
| `ActionStarted` / `ActionDone` | action, progress / result |
| `Interacted` | with, signal_kind, claimed_value, accepted |
| `BeliefUpdated` | about, stat, old, new, cause(`observe|gossip`) |
| `ReputationGossip` | about, from, trust, delta |
| `CopingEntered` | mode(`rebind|longing|latent|apathy`) |
| `RoleEmerged` | function, holder, reliance_share |

- **why-trace** = NFR-3. Put the *selection rationale* (competing candidates, gates, costs) into `GoalSelected` / `PlanBuilt` so "why did it do this" is reconstructable.
- **SSE view** = the frontend-graphics subset (positions, actions, major events). Sensitive god-view fields (`real_stats`) are not sent over SSE (controlled by an observation-mode flag).

## 5. Determinism & versioning
- Resuming from a snapshot must be **byte-identical** to running from the start (test: `docs/testing.md`).
- On `schema_version` mismatch, persist refuses to load and demands a migration path.

## 6. Navmap / terrain (map subsystem)
- Navmap wire = **building footprints** + **sparse `wear`** + **terrain state**. Per `design.md §5`, terrain is **dynamic** (moisture/transition), so it streams like wear — **periodic full + sparse deltas**, NOT a one-time static layout.
- Determinism (D12): the snapshot is copy-on-write; the running tick deposits `wear` and applies terrain transitions in the **serial apply** phase (sorted cell order), never during plan. Bulk terrain recompute is `tick`-triggered (`tick % N`), never wall-clock.
- The exact navmap snapshot shape is finalized when `engine/navmap` serialization lands — see `docs/map-plan.md` M5 + `docs/climate.md`.