# SPEC — `platform/api/godview`

> Status: `DRAFT`
> Leaf level: `L8` (platform — child of `platform/api`; depends only on engine/persist public interfaces; architecture §3/§5 stage 9)  ·  Owner agent: `<filled by implementer>`

## Purpose

The **god-view inspection endpoints** of `platform/api` — four read-only HTTP routes that expose the
engine's *hidden* state (real stats, cross-agent Theory-of-Mind, the social graph, and the persisted
why-trace) to a deploy-time-authorized operator. It exists as a sub-SPEC so the parent
[`platform/api` SPEC](../SPEC.md) stays under ~400 lines (CLAUDE.md §5). The four routes share one
gate (startup `GodMode` AND request `?god=true`), one data-availability model (the snapshot blob's
`TomDigest`, or `206 Partial Content` when absent), and one determinism contract (D12). This SPEC
owns their wire detail, response types (`GodViewResponse` group), and per-route D6/D8/D12 compliance;
the parent owns the route-table rows, the `GodViewStore` injection, and the gate invariant.

## Public Interface

> These are the four routes' response shapes. The handlers, the `Server`, and the injected
> `GodViewStore` are owned by the parent [`platform/api` SPEC](../SPEC.md); this child adds only the
> serialized response group. Import path root is `github.com/dogring/bdg` (the `backend/` dir is the
> module root, not part of the path). For P1 these types MAY live in the `api` package itself
> (sub-folder split is documentary — to keep one file under ~400 lines); promote to a `godview`
> package only if the parent file grows further.

```go
package api // (or a future package godview — see Notes)

import "github.com/dogring/bdg/engine/kernel/core"

// ── GodViewResponse group (the four /api/god/* response shapes) ───────────────────

// StatTriple is the per-stat 3-way divergence for /api/god/agent/{id}/divergence.
// D8: Real ≠ SelfEstimate ≠ OthersEstimateMean are SEPARATE channels — the handler MUST NOT blend
// them. Real reads RealStats (god view); SelfEstimate reads SelfEstStats (ToM[self]); OthersEstimateMean
// is the mean across all OTHER agents' Belief.EstStats about this subject (cross-agent ToM).
type StatTriple struct {
    Real               float64 `json:"real"`                 // RealStats[stat] (snapshot blob, god view)
    SelfEstimate       float64 `json:"self_estimate"`        // SelfEstStats[stat].Mean (ToM[self], D8)
    OthersEstimateMean float64 `json:"others_estimate_mean"` // mean of ToM_X[id][stat].Mean over X≠id
}

// DivergenceResponse is GET /api/god/agent/{id}/divergence.
type DivergenceResponse struct {
    AgentID core.AgentID          `json:"agent_id"`
    Tick    core.Tick             `json:"tick"`
    PerStat map[core.StatID]StatTriple `json:"per_stat"` // keyed by StatID (glossary); JSON sorts keys (D12)
}

// FactionRep is one observer-cluster's mean opinion of a subject for one stat (reputation breakdown).
// `FactionID` is a DERIVED cluster label "cluster_<holder_agent_id>" (D2: no Faction type) inferred
// from the snapshot's reliance clustering (EmergedRoles + per-agent RelyOn). `Agents` is the cluster's
// member ids in sorted order (D12).
type FactionRep struct {
    FactionID string         `json:"faction_id"` // "cluster_<holder>" — derived, never a stored type (D2)
    Agents    []core.AgentID `json:"agents"`     // cluster members, sorted (D12)
    Mean      float64        `json:"mean"`       // mean of this cluster's EstStats[stat].Mean about subject
}

// StatReputation is the D6 reputation distribution for one stat: an aggregate over the DISTRIBUTION of
// observer beliefs, NEVER a stored scalar. Mean = across-observer mean of means; Variance = across-observer
// spread (high = contested). PerFaction breaks the spread down by reliance cluster.
type StatReputation struct {
    Mean       float64      `json:"mean"`        // across-observer mean of EstStats[stat].Mean (derived)
    Variance   float64      `json:"variance"`    // across-observer variance (contested-reputation signal)
    PerFaction []FactionRep `json:"per_faction"` // per reliance cluster, sorted by FactionID (D12)
}

// ReputationResponse is GET /api/god/reputation/{id}. D6: there is NO top-level reputation scalar —
// only the per-stat {mean, variance, per_faction} aggregate, derived on demand from the TomDigest.
type ReputationResponse struct {
    SubjectID core.AgentID                   `json:"subject_id"`
    Tick      core.Tick                      `json:"tick"`
    PerStat   map[core.StatID]StatReputation `json:"per_stat"` // keyed by StatID; JSON sorts keys (D12)
}

// RelationEdge is one directed (from→to) social-graph edge for /api/god/relations.
type RelationEdge struct {
    From     core.AgentID       `json:"from"`
    To       core.AgentID       `json:"to"`
    Affinity float64            `json:"affinity"` // ToM_from[to].Affinity
    Trust    float64            `json:"trust"`    // ToM_from[to].Trust
    RelyOn   map[string]float64 `json:"rely_on"`  // ToM_from[to].RelyOn (Function id → share); sorted keys
}

// RelationsResponse is GET /api/god/relations. Edges are emitted only when at least one of
// {Affinity, Trust, any RelyOn value} is non-zero, and are sorted by (From, To) ascending (D12).
type RelationsResponse struct {
    Tick  core.Tick      `json:"tick"`
    Edges []RelationEdge `json:"edges"` // sorted by (from, to) lexicographic (D12)
}

// GoalCandidate is one (competing or chosen) goal candidate in a why-trace.
type GoalCandidate struct {
    Dimension core.Dimension `json:"dimension"`
    Target    *core.ObjectID `json:"target"`    // nullable — an abstract value goal may have no object (D1)
    EffValue  float64        `json:"eff_value"`
}

// GoalSelectedView mirrors the GoalSelected event payload (data-contracts §4) plus the new
// competing_candidates[] (the prerequisite payload extension flagged in Open Questions).
type GoalSelectedView struct {
    Dimension          core.Dimension  `json:"dimension"`
    Target             *core.ObjectID  `json:"target"`
    EffValue           float64         `json:"eff_value"`
    CompetingCandidates []GoalCandidate `json:"competing_candidates"` // present iff the event payload carries it
}

// PlanBuiltView mirrors the PlanBuilt event payload (data-contracts §4).
type PlanBuiltView struct {
    Steps       []string `json:"steps"`        // e.g. ["MoveTo(forest)","Gather(berry_bush_1)"]
    TotalCost   float64  `json:"total_cost"`
    Provisioned []string `json:"provisioned"`  // forward-sim provisioning summary (D9)
}

// WhyResponse is GET /api/god/why/{agent_id}/{tick}, reconstructed from the Postgres `events` rows.
type WhyResponse struct {
    AgentID      core.AgentID      `json:"agent_id"`
    Tick         core.Tick         `json:"tick"`
    GoalSelected *GoalSelectedView `json:"goal_selected"` // nil if no GoalSelected event for (agent,tick)
    PlanBuilt    *PlanBuiltView    `json:"plan_built"`    // nil if no PlanBuilt event for (agent,tick)
}

// PartialResponse is returned with 206 Partial Content by divergence/reputation/relations when the
// snapshot blob has no TomDigest (the cross-agent ToM is not available — see Open Questions). The
// client learns the data is degraded rather than silently receiving zeros.
type PartialResponse struct {
    Partial bool   `json:"partial"` // always true
    Reason  string `json:"reason"`  // e.g. "tom_digest not available in snapshot"
}
```

> godview DEFINES no simulation vocabulary. `core.StatID`/`Dimension`/`AgentID`/`ObjectID`/`Tick`
> are `engine/kernel/core`'s; the reputation/Influence semantics are `engine/mind/tom`'s (`ReputationDist`,
> `Belief`); `RealStats`/`SelfEstStats`/`EmergedRoles` live in `engine/world`'s `WorldState`.
> `faction_id` is a *derived* label, not a type (D2). This module reads a digest and serializes it.

## Routes (god-view; gate enforced by the parent before the handler runs)

All four require startup `GodMode == true` AND request `?god=true`; otherwise the parent responds
`403` + `{"error":"god mode disabled"}` **before** any store read (parent SPEC Invariants).

### `GET /api/god/agent/{id}/divergence` — wire detail (D8)
- Read the snapshot blob (`rds.Get(keyer.SnapshotKey())` → `persist.Snapshot`). `404` +
  `{"error":"snapshot not found"}` if absent.
- Locate the subject `{id}`'s `agentDigest`: `Real` ← `RealStats[stat]`; `SelfEstimate` ←
  `SelfEstStats[stat].Mean`. These two are always available from the snapshot.
- `OthersEstimateMean[stat]` ← the mean of `ToM_X[id].EstStats[stat].Mean` over every other agent
  `X != id`. **This needs the cross-agent ToM (the `TomDigest`)**, which the current snapshot does
  not carry (Open Questions). When `TomDigest` is absent: respond `206 Partial Content` with
  `PartialResponse{Partial:true, Reason:"tom_digest not available in snapshot"}` (the `real` and
  `self_estimate` channels alone are not the contracted 3-way response, so the endpoint degrades
  rather than emit a misleading 2-of-3).
- **D8 separation**: `real`, `self_estimate`, `others_estimate_mean` are three distinct reads; the
  handler MUST NOT substitute `RealStats` for `self_estimate` (which reads `SelfEstStats`, ToM[self])
  or vice versa. Stats iterate `stats.Registry.IDs()` / sorted `StatID` order (D12); JSON map keys
  sort.
- `404` if `{id}` is not an agent in the snapshot.

### `GET /api/god/reputation/{id}` — wire detail (D6)
- Read the snapshot blob. Gather every observer's `Belief` about `{id}` (`ToM_X[id]` for all
  `X != id`) from the `TomDigest`. `206` Partial if `TomDigest` absent.
- Per stat, derive the D6 reputation distribution — exactly `tom.ReputationDist(subject, observers)`
  semantics: `Mean` = across-observer mean of `EstStats[stat].Mean`; `Variance` = across-observer
  variance of those means (the contested-reputation spread). **There is NO top-level reputation
  scalar** — only the per-stat `{mean, variance, per_faction}` aggregate, derived on demand (never
  stored, D6).
- `per_faction`: partition the observers into **reliance clusters** derived from the snapshot's
  `EmergedRoles` + per-agent `RelyOn` (agents that rely on the same holder belong to one cluster).
  Label each `FactionID = "cluster_<holder_agent_id>"` (D2 — a derived label, no Faction type).
  For each cluster, `Mean` = that cluster's mean opinion of the subject for the stat. `per_faction`
  is sorted by `FactionID` (D12); each cluster's `Agents` list is sorted (D12).
- The per-cluster split is what makes `variance > 0` measurable on a scenario-C seed where two
  clusters hold divergent beliefs (one cluster received gossip, the other did not — see ACs).
- `404` if `{id}` is not an agent in the snapshot.

### `GET /api/god/relations` — wire detail (D12)
- Read the snapshot blob's `TomDigest`. `206` Partial if absent.
- For every ordered pair `(from, to)` with `from != to`, read `ToM_from[to]` → `Affinity`, `Trust`,
  `RelyOn`. Emit a `RelationEdge` **only when** at least one of `{Affinity != 0, Trust != 0, any
  RelyOn value != 0}` (the digest is top-K, so most pairs are absent → emitted as no edge).
- Edges are sorted by `(From, To)` ascending lexicographic (D12). Within each edge, `RelyOn` map
  keys serialize in sorted Function-id order (D12). **Same snapshot → byte-identical JSON** (the
  D12 guard AC below).

### `GET /api/god/why/{agent_id}/{tick}` — wire detail
- Parse `{tick}` as an integer game-tick; `400` + `{"error":"invalid tick"}` on parse failure.
- `gv.QueryEvents(ctx, runID, agent_id, tick)` → `[]core.Event` in seq order. (This needs
  `BackupStore.QueryEvents` — Open Questions.) `503` + `{"error":"why-trace store unavailable"}`
  if `gv == nil` (god-view enabled but no event store wired).
- Pick the `GoalSelected` and `PlanBuilt` events for `(agent_id, tick)` and decode their payloads
  into `GoalSelectedView` / `PlanBuiltView`. `competing_candidates` is populated **iff** the
  `GoalSelected` payload carries the field (the prerequisite payload extension — Open Questions);
  on an old row without it, `CompetingCandidates` is an empty slice (not the contracted full block).
- `404` + `{"error":"no why-trace for agent at tick"}` if the query returns no `GoalSelected` and no
  `PlanBuilt` for `(agent_id, tick)`.
- This is a **Postgres read**; api never writes events (read-only HTTP invariant).

## Dependencies

- `platform/api` (parent) — the `Server`, the `RedisReader`/`GodViewStore` injection, the
  `persist.Keyer`/`persist.Snapshot` access, and the `(GodMode && god=true)` gate. This child is
  served by the parent's mux; see [`../SPEC.md`](../SPEC.md).
- `platform/persist` — `persist.Snapshot` (decoded for `RealStats`/`SelfEstStats`/`TomDigest`/
  `EmergedRoles`), `persist.Keyer` (`SnapshotKey()`), and `persist.BackupStore.QueryEvents`
  (satisfies `GodViewStore` — a persist SPEC change flagged below). Public contract only.
- `engine/world` — the `WorldState` shape inside the blob: `RealStats`, `SelfEstStats`,
  `EmergedRoles`, and the **prerequisite `TomDigest`** (an `engine/world` SPEC change, below). The
  reliance-cluster membership for `per_faction` is derived from `EmergedRoles` + per-agent `RelyOn`.
- `engine/mind/tom` — the reputation/Influence *semantics* this module mirrors: `ReputationDist`
  (D6 aggregate), `Belief{EstStats, Trust, Affinity, RelyOn}`, `StatDist{Mean, Variance}`. api
  derives the aggregate from the digest; it does not import tom for logic if the digest is
  pre-shaped, but the math MUST match tom's `ReputationDist` definition (across-observer mean/variance).
- `engine/kernel/core` — `StatID`, `Dimension`, `AgentID`, `ObjectID`, `Tick`, `Event`, `RunID`.
- **Contract** — `data-contracts.md` §1 (snapshot digest + the new `TomDigest` field), §3 (Postgres
  `events` table — the `/why` source), §4 (event payloads — the new `GoalSelected.competing_candidates`).

## Owned Data

- The `GodViewResponse` type group (the four response shapes + `PartialResponse`) and their JSON tags.
- The per-route serialization logic: the 3-way divergence assembly, the D6 reputation aggregation,
  the reliance-cluster `faction_id` derivation, the relations edge filter+sort, and the why-trace
  event-to-view decode.
- The `206 Partial Content` fallback policy for divergence/reputation/relations when `TomDigest`
  is absent.
- This module OWNS no simulation state and mutates nothing. It reads the snapshot blob + the events
  table and serializes.

## Invariants

> A violation here is a bug — these are mechanically checkable.

- **God-view gate (D8) — every route.** All four routes serve only when `GodMode == true` AND
  `?god=true`; otherwise `403` + `{"error":"god mode disabled"}` **before** any store read. (The
  gate is enforced by the parent; this child's handlers assume it has passed.)
- **D6 — `/reputation` is a distribution, never a scalar.** The response JSON contains **no**
  top-level reputation scalar. Only `{mean, variance, per_faction[]}` per stat is allowed, derived
  on demand from the snapshot's `TomDigest` (never from a stored reputation value). The
  across-observer `variance` (the contested-opinion spread) is a first-class output and is **not**
  averaged away — a struct/JSON guard asserts no flattening to a single number.
- **D8 — `/divergence` keeps the three channels separate.** `self_estimate` reads `SelfEstStats`
  (ToM[self]) per stat — **never** `RealStats`. `real` reads `RealStats`. `others_estimate_mean`
  reads the cross-agent ToM. The handler MUST NOT blend or substitute any of the three. (D8: self is
  calibrated by action, never "corrected" toward Real — the endpoint exposes the divergence, it does
  not collapse it.)
- **D2 — `faction_id` is a derived label, no stored type.** `faction_id = "cluster_<holder>"` is
  inferred from `EmergedRoles` + `RelyOn`; there is **no** `Faction`/`Role`/`Cluster` type in this
  module. A grep/struct guard confirms it. (Mirrors `engine/world`'s D2 invariant: roles/clusters
  are emergent statistics, not types.)
- **D12 — `/relations` is byte-deterministic.** Edges are sorted by `(from, to)` ascending
  lexicographic; each edge's `RelyOn` keys serialize in sorted Function order; per-stat maps sort by
  `StatID`; per-faction lists sort by `FactionID`. **Same snapshot → byte-identical JSON.** No `map`
  is ranged for output ordering anywhere.
- **Read-only HTTP.** No route writes a Redis key, a Postgres row, or engine state. `/why` is a
  Postgres **read** (`QueryEvents`). No tick is advanced.
- **No hand-formatted keys.** The snapshot read uses `persist.Keyer.SnapshotKey()`; this module
  formats no `sim:{run}:*` literal.
- **Graceful degradation, never silent zeros.** When `TomDigest` is absent, divergence/reputation/
  relations return `206` + `PartialResponse{partial:true, reason:...}` — they do **not** emit a
  `200` full-shape body with zeroed `others_estimate_mean`/`variance`/empty edges that a client
  could mistake for "computed and zero."
- **No naming drift.** `real`, `self_estimate`, `others_estimate_mean`, `mean`, `variance`,
  `per_faction`, `affinity`, `trust`, `rely_on`, `competing_candidates` are the contracted field
  names; stat/dimension/function ids use glossary canonical names.

## Acceptance Criteria (testable)

> The cross-cutting `(GodMode, god)` 403/403/200 table and the route-registration guard are in the
> parent [`../SPEC.md`](../SPEC.md). The per-endpoint ACs below are owned here.

### `/api/god/agent/{id}/divergence`
- [ ] **3-way present per stat (D8)**: with a snapshot carrying a `TomDigest`, the `200` response has
  `real`, `self_estimate`, AND `others_estimate_mean` present for **every** stat (decode into
  `map[StatID]map[string]float64`, assert all three keys per stat).
- [ ] **D8 channels not blended**: a fixture where `RealStats[Strength]=0.72`,
  `SelfEstStats[Strength].Mean=0.55`, and the cross-agent mean=0.60 yields exactly those three
  values — `self_estimate` is the ToM[self] value (0.55), not the Real (0.72). Table-driven over a
  stat where the three differ.
- [ ] **Partial fallback (TomDigest absent)**: with a snapshot lacking `TomDigest`, the response is
  `206 Partial Content` + `{"partial":true,"reason":"tom_digest not available in snapshot"}` — NOT
  a `200` with `others_estimate_mean` zeroed.
- [ ] **404 on unknown agent / missing snapshot**: `{id}` not in the snapshot → `404`; snapshot key
  absent → `404` + `{"error":"snapshot not found"}`.

### `/api/god/reputation/{id}`
- [ ] **D6 shape — no scalar**: the `200` response per stat is exactly `{mean, variance,
  per_faction}`; a JSON guard asserts there is no top-level reputation number (decode into a struct
  with no scalar field; assert an unexpected scalar key is absent).
- [ ] **`per_faction` variance > 0 on scenario-C seed**: on the scenario-C gossip-propagation fixture
  (cluster A received the gossip update about the subject; cluster B did not), the response's
  per-stat `variance` is strictly `> 0` and `per_faction` lists two clusters with divergent `mean`
  values (e.g. `cluster_A.mean=0.80` vs `cluster_B.mean=0.30`). Threshold ties to the
  `engine/mind/tom` "Variance preserved across cluster boundary (P4)" AC — the contested signal is
  measurable and not collapsed.
- [ ] **`faction_id` derived (D2)**: each cluster's `faction_id == "cluster_<holder>"` matches a
  holder from the snapshot's `EmergedRoles`; a grep/struct guard confirms no `Faction`/`Role` type.
- [ ] **Partial fallback (TomDigest absent)**: `206` + `PartialResponse` as for divergence.

### `/api/god/relations`
- [ ] **Edge filter**: an edge is emitted iff at least one of `{affinity, trust, any rely_on}` is
  non-zero; a pair with all-zero relation produces **no** edge (table-driven over a digest with a
  mix of zero/non-zero pairs).
- [ ] **Deterministic sort (D12)**: edges are sorted by `(from, to)` ascending; the SAME snapshot
  marshalled twice produces **byte-identical** JSON (a byte-equality golden) — no map-iteration
  ordering leaks into the output.
- [ ] **`rely_on` content**: an edge with `ToM_from[to].RelyOn = {safety:0.3, judgment:0.1}`
  serializes `"rely_on":{"judgment":0.1,"safety":0.3}` (sorted keys).
- [ ] **Partial fallback (TomDigest absent)**: `206` + `PartialResponse`.

### `/api/god/why/{agent_id}/{tick}`
- [ ] **`competing_candidates` present**: with a `GoalSelected` event whose payload carries
  `competing_candidates`, the `goal_selected.competing_candidates` block is present in the `200`
  response (decode and assert the slice is non-empty and matches the event payload).
- [ ] **GoalSelected + PlanBuilt reconstructed**: a fixture with both events for `(agent,tick)`
  yields a `200` with both `goal_selected` and `plan_built` populated from the queried rows (seq
  order preserved); the query is `gv.QueryEvents(run, agent, tick)`.
- [ ] **404 on no trace**: no `GoalSelected`/`PlanBuilt` for `(agent,tick)` → `404` +
  `{"error":"no why-trace for agent at tick"}`.
- [ ] **400 on bad tick / 503 on nil store**: a non-integer `{tick}` → `400`; `gv == nil` (god-view
  on but no event store wired) → `503`.
- [ ] **Old-row degradation**: a `GoalSelected` row WITHOUT `competing_candidates` (pre-bump
  schema) decodes with an empty `competing_candidates` slice and still returns `200` for the rest
  (the `why` endpoint does not 500 on a legacy row).

## Out of Scope

- **The Server / mux / gate / SSE / probe / snapshot / agent endpoints** → the parent
  [`platform/api` SPEC](../SPEC.md). This child adds only the four `/api/god/*` handlers' response
  shaping.
- **Capturing `TomDigest` into the snapshot** → `engine/world` (`WorldState.TomDigest`) +
  `platform/persist` (`Snapshot` carries it). This module READS the digest; it does not build it.
  Flagged as a prerequisite Open Question (BLOCKER for the full divergence/reputation/relations
  responses).
- **Implementing `BackupStore.QueryEvents`** → `platform/persist`
  (`backend/platform/persist/SPEC.md`). This module consumes it via the `GodViewStore` interface.
- **Extending the `GoalSelected` event payload with `competing_candidates`** → `data-contracts.md`
  §4 + the emitter (`platform/events`) + the producer (`engine/agent`/`engine/mind/planner`). This
  module reads the field; it does not emit it. Flagged below (BLOCKER for `competing_candidates`).
- **Authentication beyond the god-view gate** → future `platform/auth`.
- **Graph layout / rendering** of `/api/god/relations` → the frontend; api returns raw edges only.

## Open Questions

- **TomDigest prerequisite (BLOCKER for endpoints 1/2/3 full responses).** Divergence, reputation,
  and relations all need the full cross-agent ToM (`Belief.EstStats`, `Trust`, `Affinity`, `RelyOn`
  for every `(observer, subject)` pair). The current snapshot stores only `SelfEstStats` (verified:
  `world.agentDigest` carries `RealStats` + `SelfEstStats` but no per-observer beliefs about others;
  data-contracts §1 says the full O(N²) ToM is "a Postgres-only option"). Two options:
  - **Option A (RECOMMENDED) — extend `world.WorldState` with a `TomDigest`.** Add
    `TomDigest []tomDigestEntry` to `WorldState` (and surface it on `persist.Snapshot`) holding, per
    agent, the top-K highest-trust / highest-affinity / non-zero-RelyOn beliefs about others (exactly
    the "top-K relationships, strong values only" digest data-contracts §1 already anticipates). The
    god-view endpoints read this digest from the snapshot blob. **Cleanest path: no new Redis key, no
    O(N²) live writes, already anticipated in §1.** It is an `engine/world` + `platform/persist` SPEC
    change (the digest entry type + capture in `World.State()` + the `Snapshot` field + a
    `schema_version` bump), so it **escalates to the architect** before these endpoints are
    implemented.
  - **Option B — a live Redis key per agent** (`sim:{run}:tom:{agent_id}`) holding the full
    serialized ToM, written by `platform/persist` each tick. Lower read latency but O(N²) writes and
    key proliferation; rejected for P1.
  - **Until Option A lands**, endpoints 1/2/3 can only see `SelfEstStats` (no cross-agent data), so
    they return `206 Partial Content` + `{"partial":true,"reason":"tom_digest not available in
    snapshot"}`. This OQ is a **BLOCKER for the full (200) responses** of those three endpoints;
    it does NOT block the gate/403 behaviour, the `206`-partial path, or the `/why` endpoint.
  - **Also needed for `per_faction`:** the reliance-cluster *membership* (which agents form each
    cluster) is not exposed today — `WorldState.EmergedRoles` is `[]{Function, Holder}` only (no
    member list). The `TomDigest` must let api re-derive membership from per-agent `RelyOn` (each
    agent's chosen holder per Function → cluster), OR `engine/world` exposes a cluster-membership
    accessor. Fold this into the Option-A digest design (escalate together).
- **`GoalSelected.competing_candidates` payload extension (BLOCKER for `/why`'s competing_candidates).**
  data-contracts §4 `GoalSelected` is currently `{dimension, target, priority, eff_value}` — no
  competing candidates. To serve `goal_selected.competing_candidates`, add
  `"competing_candidates": [{"dimension":…, "target":…, "eff_value":…}]` to the `GoalSelected`
  payload. This is a **data-contracts §4 change requiring a `schema_version` bump** on the `events`
  table (old rows are read WITHOUT the field — the `/why` handler degrades to an empty slice; new
  rows MUST include it). It also requires the producer (`engine/agent`/`engine/mind/planner` mediation,
  which already ranks candidates by EffValue) to emit the rejected candidates, and the emitter
  (`platform/events`) to serialize them. **Escalate to the architect** (contract change) before the
  `/why` endpoint can return the full `competing_candidates` block. Until then `/why` returns the
  chosen goal + plan with an empty `competing_candidates`.
- **`BackupStore.QueryEvents` addition (prerequisite for `/why`, NOT a contract break).** The
  `/why` endpoint needs to query Postgres by `(run, agent_id, tick)`. The frozen
  `persist.BackupStore` has no read-by-key method. Add
  `QueryEvents(ctx, run, agentID, tick) ([]core.Event, error)` to `BackupStore` (or a new narrow
  `EventStore` interface — the parent injects it as `GodViewStore`). This is a `platform/persist`
  SPEC change (additive — no existing caller breaks); flag to the architect, reference
  `backend/platform/persist/SPEC.md`. P1 wires the concrete `BackupStore` as `GodViewStore` once the
  method exists.
- **Reputation `per_faction` clustering rule (NOT blocking the shape).** "Agents that share a
  RelyOn holder belong to the same cluster" is the D2-compliant derivation, but the exact rule
  (strongest single edge vs membership in any super-threshold edge) should match
  `engine/world`'s reliance-scan plurality definition so the `faction_id` labels line up with
  `EmergedRoles`. Confirm with the architect that the api-side derivation reuses the same
  argmax-holder rule as the world's `relianceScan` (no second clustering definition).
- **Within-observer variance (NOT blocking).** `StatReputation.Variance` is the *across-observer*
  spread (the D6-critical factional-disagreement signal), matching `tom.ReputationDist`. Whether to
  also fold each observer's own `EstStats[stat].Variance` (total = between + within) is the same
  open question `engine/mind/tom/SPEC.md` carries; the across-observer term is the specified default and
  can be extended without changing the response type.

## Notes

- **Why a sub-folder split.** The parent `platform/api` SPEC was at 313 lines; the four endpoints'
  wire detail + response types + invariants + ACs + the two contract-change OQs would push it well
  past ~400 (CLAUDE.md §5). The god-view block is cohesive (one gate, one data model, one
  determinism contract), so it factors cleanly into this child. The parent references it by path and
  keeps only the route-table rows, the `GodViewStore` injection, and the gate invariant.
- **Package vs folder.** For P1 the response types and handlers MAY stay in package `api` (the
  folder split is documentary, to keep one Go file under ~400 lines). Promote to a real `godview`
  Go package only if the parent file grows further; the SPEC boundary already isolates the concern.
- **`ReputationDist` parity.** The api-side reputation aggregation MUST compute the same
  across-observer mean/variance as `engine/mind/tom.ReputationDist` (`engine/mind/tom/SPEC.md` — Mean = mean of
  observer means; Variance = variance of observer means). If the `TomDigest` is pre-shaped as a list
  of `Belief`s per subject, api can call the same arithmetic; do not invent a second formula.
- **Faction labels mirror `RoleEmerged`.** `faction_id = "cluster_<holder>"` reuses the holder ids
  the world emits in `RoleEmerged{function, holder, reliance_share}` and serializes in
  `WorldState.EmergedRoles`. This keeps the god-view clusters consistent with the emergent-role
  detection (one clustering definition, D2).
- **`target` is nullable (D1).** A goal is an abstract value (Dimension); the object is a *path*,
  not the goal. So `GoalCandidate.Target` / `GoalSelectedView.Target` are `*core.ObjectID` — a value
  goal with no chosen object serializes `"target": null`.
- **`/why` reads Postgres, the others read Redis.** Divergence/reputation/relations read the live
  snapshot blob (`rds.Get(keyer.SnapshotKey())`); `/why` reads the durable `events` table via
  `gv.QueryEvents`. The why-trace is the persisted record (data-contracts §3), which is why it needs
  the Postgres store and not the live snapshot.
</content>
