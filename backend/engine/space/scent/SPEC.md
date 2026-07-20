# SPEC — `engine/space/scent`

> Status: `DRAFT`
> Leaf level: `L1` (`core`-only; a shared spatial index, spatial/navmap kin)  ·  Owner agent: `<filled by implementer>`
> Scope: the shared scent field. Consumers: `engine/fauna` (animal sensing, F20–F24/F32–F34/F44/F45),
> later `engine/mind/perception` (agent Smell coexists — shared emitter tags). Drivers: `engine/world`.

## Purpose
The **scent field**: a single uniform, multi-channel **auxiliary index** (spatial-hash / navmap-cost-field
kin) over continuous space (D11 — the grid is an *index*, not the world; entities keep continuous `Pos`
and only READ the cell they fall in + neighbors, never snapped to it). It is a **shared world index**, NOT
fauna-owned (promoted out of `engine/fauna`): `world` deposits from any **`scent:<channel>`-tagged emitter**
(edible flora → food, prey animals → prey, predators → predator, rotting lots/carcass → future carrion …),
and consumers read it — `engine/fauna` (animal sensing) now, `engine/mind/perception` (agent Smell)
coexisting on the SAME emitter tags (§ Notes). Each cell carries a **per-channel scalar intensity**
(`float64 ≥ 0` — F21 REVISED from binary presence to **scalar concentration**: stronger near a bigger/closer
source, fading with distance + wind). It owns: **deposit** (add intensity ∝ source magnitude to a cell's
channel), **wind-spread** (a deterministic fixed-order diffusion stencil carries intensity downwind with
distance falloff on the `tick % Ns` bulk cadence, **next-tick latency**), **read** (a consumer reads its
cell + neighbors within a smell radius → per-channel intensity + a gradient direction toward the source),
and the O(1) own-cell **`IntensityAt`** probe (the parent's F45 predator-scent wake gate — wake when
intensity exceeds a parent threshold). It does **not** own *when* deposit/spread/commit run (cadence =
`engine/world`, F33/F41), the decision that consumes a `Reading` (parent consumer), the wind VALUE
(injected by world from climate, neutral in P1), or any object/entity mutation. No IO, no wall-clock,
**no RNG** (spread is a fixed-order stencil, D12). Concept: `docs/plans/fauna.md §1.1` + F20–F24/F32–F34/F44/F45,
`docs/plans/resources.md`/`docs/plans/flora.md`/`docs/plans/materials.md` (emitter sources via `scent:<channel>` tags).

## Public Interface
```go
package scent

import "github.com/dogring/bdg/engine/kernel/core"

// Channel selects one scent layer (F22 — multi-channel; one scalar intensity per channel per cell). The
// constant order is FIXED (array index / determinism, D12); a new channel APPENDS, never re-orders. Each
// channel is fed by entities carrying the matching `scent:<channel>` tag.
type Channel uint8

const (
    ChanFood     Channel = iota // edible-flora / food scent (herbivore homing → Graze); tag scent:food
    ChanPrey                    // prey-animal scent (carnivore homing → Hunt);          tag scent:prey
    ChanPredator                // predator scent (early-warning → Wary + F45 wake);      tag scent:predator
    ChanCarrion                 // carcass/rot scent (scavenger/predator homing → Feed, FC10); tag scent:carrion
    NumChannels  = iota         // count (array width) — now 4
)
// FC10 (docs/plans/fauna.md 클러스터 9): ChanCarrion added for the combat/carcass round. Reading (below) gains a
// Carrion ChannelReading and Sense maps it; the world's tag-driven deposit routes scent:carrion here with
// ZERO further engine change (that is why classification is tag-driven). Appended AFTER ChanPredator so the
// existing 3 channels keep their index (no golden churn on food/prey/predator).

// Wind is the local wind VALUE world injects from climate (operands wind.dir / wind.mag, CA2). Dir = the
// direction the wind BLOWS TOWARD (radians); Mag = strength (≥ 0). P1 climate OFF ⇒ Wind{0,0} ⇒ spread
// stays local (isotropic short-range) and read gradient falls back to the neighbor-intensity gradient
// (input contract). The grid never invents wind.
type Wind struct {
    Dir float64 // radians; direction the wind blows toward
    Mag float64 // ≥ 0; 0 ⇒ no downwind transport (P1 neutral)
}

// Grid is the uniform multi-channel scent field. cellSize ∝ the consuming species' smell radius (F32 —
// balance data, spatial.cellSize parity; injected, NOT hardcoded, D10), lower-bounded by max-speed·Ns so
// an upwind step never skips a cell. DOUBLE-BUFFERED for the next-tick latency (F33): READS see the
// COMMITTED snapshot (stable for the whole read phase); Deposit/Spread write PENDING; Commit swaps
// pending→committed and clears pending. World-owned (one per run), world DRIVES the cadence. Not safe for
// concurrent mutation; the single-threaded apply phase guarantees that (the parallel score phase only READS).
type Grid struct{ /* opaque: cellSize float64; committed + pending sparse cell→[NumChannels]float64 buffers. */ }

// New creates an empty grid with the given square cell edge length (world units). Panics if cellSize ≤ 0.
// Both buffers start empty (intensity 0 in every channel everywhere).
func New(cellSize float64) *Grid

// ── Deposit (apply phase; world-driven, fixed order) ─────────────────────────────────
// Deposit ADDS `intensity` (≥ 0, ∝ source magnitude — a big carcass / dense food patch deposits more)
// to ch at the cell containing pos in the PENDING buffer (accumulative — overlapping sources stack).
// world calls it in the serial apply phase in fixed (sorted source ObjectID) order (D12), once per
// `scent:<channel>`-tagged emitter, with `intensity` derived from the emitter's magnitude tag/§6. Per F33
// the predator channel is deposited EVERY tick at each predator's cell (kept fresh for early-warning);
// food/prey on the bulk cadence. pos is continuous (D11); cell = floor(pos/cellSize) per axis (negative-safe).
func (g *Grid) Deposit(ch Channel, pos core.Vec2, intensity float64)

// ── Spread (bulk pass; world-driven on tick % Ns) ────────────────────────────────────
// Spread runs ONE deterministic fixed-order diffusion stencil over the PENDING buffer (F33): visiting
// on-cells in sorted cell-key order, it moves a fraction of each cell's channel intensity to neighbor
// cells, **biased downwind** by `wind` (more mass + farther reach as wind.Mag grows; isotropic short-range
// when Mag==0) and attenuated by a distance falloff (intensity strictly decreases away from source ⇒ the
// read gradient points back toward it). No RNG; fixed sorted-key traversal + fixed neighbor order ⇒
// byte-identical regardless of deposit order. world calls Spread on `tick % Ns` (Ns = balance, F24).
func (g *Grid) Spread(wind Wind)

// ── Static layer (클러스터 8b) ────────────────────────────────────────────────────────
// DepositStatic/CommitStatic are Deposit/Commit for occupants that DO NOT MOVE (flora, objects,
// carcasses). Deposits accumulate into a rebuild buffer that only becomes visible on CommitStatic —
// which also diffuses it once, under `wind` — so a half-finished rebuild is never read. Between
// rebuilds the previous static field STAYS IN PLACE: a plant that has not changed keeps smelling.
// That persistence is the point. Before the split, static sources were re-laid on the bulk cadence
// but cleared every tick, so food scent existed on 1 tick in 6 (measured 4.939 / 0.000 / 0.000 / …)
// and herbivore food homing was blind 5/6 of the time.
func (g *Grid) DepositStatic(ch Channel, pos core.Vec2, intensity float64)
func (g *Grid) CommitStatic(wind Wind)

// ── Trail / spoor layer (PD9) ────────────────────────────────────────────────────────
// GROUND scent left behind by a passing source, as opposed to the airborne plume the other two
// layers model. ConfigureTrail enables it; `strength` is the per-CHANNEL fraction of each Deposit
// also laid on the ground, so which scents linger is content's call (D4/D10) — a channel left at 0
// leaves no trail. decay ≤ 0 ⇒ layer off and the grid is byte-identical to one that never had it.
//
// Why it exists: the dynamic layer is rebuilt from scratch every Commit, so without this the world
// keeps NO record that an animal was ever anywhere — a predator can only sense prey that are live
// and within a cell or two, which is why the live 500² world produced zero kills in 6000 ticks (PD8).
//
// Two rules distinguish it from the airborne layers, both physical:
//   1. NEVER DIFFUSED. Wind-spreading a persistent layer every tick is the heat equation: repeated
//      diffusion smears a track into a uniform haze and destroys the gradient that makes it followable.
//      Ground scent also stays put — it is the plume that drifts.
//   2. FOLLOWED, NOT WINDED. Read resolves the trail's direction from its spatial gradient and the
//      airborne layers' from wind, then blends by each part's share of the reading. Applying the
//      upwind rule to a trail would send a tracker crosswise to the tracks under its feet.
// DecayTrail fades the layer one multiplicative step and drops cells below an internal floor, which
// is what bounds its memory to recently-visited ground. World calls it once per tick, BEFORE that
// tick's deposits (so fresh sign is laid at full strength and only older sign ages).
//
// TUNING INVARIANT: an always-occupied cell saturates at strength/(1−decay). Keep that — and `cap` —
// BELOW a live source's magnitude, or a long-used bedding site out-shouts a live animal and a
// predator abandons the deer in front of it to sniff a path. Raising strength is also NOT monotonic:
// once saturation greatly exceeds `cap` every visited cell pins to the clamp, the field goes flat,
// and the gradient disappears (measured: kills 21 → 5 between strength 0.010 and 0.030).
func (g *Grid) ConfigureTrail(strength [NumChannels]float64, decay, cap float64)
func (g *Grid) DecayTrail()

// ── Commit (per-tick; world-driven) ──────────────────────────────────────────────────
// Commit swaps PENDING into COMMITTED (read) and clears the new pending buffer — realizing the 1-tick
// latency (a deposit/spread at tick T is visible to reads at T+1). Because pending is rebuilt from current
// emitters each cadence, a vanished source's scent fades out (no stale accumulation). Deterministic; no RNG.
func (g *Grid) Commit()

// ── Read (read phase; pure; the consumer controller consumes this) ────────────────────
// IntensityAt is the O(1) own-cell channel intensity over the COMMITTED buffer (NO neighbor scan, NO
// direction): the cheap per-tick probe the parent runs for EVERY animal as the F45 wake gate —
// `IntensityAt(ChanPredator, Pos) > wakeThreshold` (parent's threshold) wakes a DORMANT herbivore "when
// predator scent reaches its cell". Pure (committed buffer), no RNG.
func (g *Grid) IntensityAt(ch Channel, pos core.Vec2) float64

// Read returns the multi-channel reading at pos over the COMMITTED buffer, scanning the cell containing
// pos plus neighbor cells within smellRadius (F34). For each channel it reports the aggregate Intensity at
// pos and a gradient Dir toward higher intensity (= toward the source; UPWIND when wind.Mag>0, else the
// neighbor-intensity gradient, F34(c)). PURE (read phase), committed buffer only ⇒ safe under the parallel
// score phase. The parent maps it onto §6 operands scent.{food,prey,predator} (= Intensity, scalar) and
// the per-channel steer dir (toward for food/prey, reversed for predator Flee/Wary). Scent-only (F44): no
// forward-FOV sight here — sight.predator/dist.predator come from the parent's spatial-hash query.
func (g *Grid) Read(pos core.Vec2, smellRadius float64, wind Wind) Reading

// Reading is the per-channel scent result at one position (the parent fills §6 operands from it).
type Reading struct {
    Food     ChannelReading
    Prey     ChannelReading
    Predator ChannelReading // Intensity → scent.predator (early-warning/Wary); see Read note re: sight
    Carrion  ChannelReading // Intensity → scent.carrion (scavenger/predator homing → Feed, FC10)
}

// ChannelReading is one channel's intensity + gradient direction at a read position.
type ChannelReading struct {
    Intensity float64   // aggregate scalar concentration at pos (0 ⇒ absent → scent.<ch> operand 0);
                        // higher ⇒ stronger/closer/bigger source. The §6 utility reads this directly.
    Dir       core.Vec2 // unit direction toward higher intensity (= toward source; upwind if wind, else
                        // neighbor-intensity gradient); zero vector when Intensity==0. Parent reverses for Flee.
}
```

## Dependencies
- `engine/kernel/core` — `Vec2` (cell math). No IO, no RNG, no other engine import. A clean shared leaf in
  `engine/space` (spatial/navmap kin) that `engine/fauna` (and later `perception`) read; `world` drives.
- *(NOT `engine/kernel/rng`)* — spread is a deterministic fixed-order diffusion stencil (D12).
  *(NOT `engine/env/climate`)* — `Wind` is an injected value (world samples climate), never imported.
  *(NOT `engine/world`/`engine/fauna`/`engine/mind/perception`)* — those are DRIVERS/CONSUMERS; the index
  imports no consumer (no cycle).

## Owned Data
- **Three layers**, all sparse cell buffers keyed by integer cell coords (`struct{cx,cy int}`, alloc-free +
  deterministic, spatial parity), each value a `[NumChannels]float64` intensity vector. A read is the **sum**
  of all three — a tile smells of everything in it, and of what has passed through:
  - **dynamic** (committed + pending): moving occupants; rebuilt from scratch every `Commit`.
  - **static** (static + staticPend): non-moving occupants; rebuilt on the bulk cadence, persists between.
  - **trail** (spoor): ground scent; accumulates, fades by `DecayTrail`, never rebuilt and never diffused.
  Callers interact only through Deposit\*/Spread/Commit\*/ConfigureTrail/DecayTrail/Read/IntensityAt.
  `Reading` is a fresh value the caller keeps freely.
- `cellSize` fixed at `New` (injected, `cellSize ∝ smell radius`, F32) — never mutated.
- World-owned (one per run); DERIVED state — rebuilt from emitter positions on resume (like the spatial
  hash), so NOT separately serialized (`docs/core/data-contracts.md`; positions are the source of truth).

## Invariants
- **D11 — index, not the world** — auxiliary index over continuous space; `Deposit`/`Read`/`IntensityAt`
  take continuous `Vec2` and compute `floor(p/cellSize)` per axis; entities are NEVER snapped to a cell.
  The grid drives no logic itself — it is a scent *data field* consumers read (navmap parity).
- **Scalar intensity (F21 REVISED), not binary** — a cell channel is a `float64 ≥ 0` concentration; deposit
  is magnitude-weighted + accumulative; spread strictly attenuates with distance so the gradient points to
  the source. `scent.<ch>` §6 operands are SCALAR (not 1/0). (Supersedes the original binary-presence F21.)
- **D12 determinism, no RNG** — `Spread` is a fixed-order diffusion over sorted on-cell keys with a fixed
  neighbor order and fixed falloff/wind weights (no randomness); `Deposit` is an additive write; `Read`
  scans the neighbor ring in fixed nested order; `IntensityAt` is a pure single-cell lookup. Float
  accumulation order is fixed (sorted keys) ⇒ byte-identical buffers/`Reading`s/`IntensityAt` every run.
- **D12 no map-iteration for logic** — every buffer traversal iterates cell keys in **sorted order**, never raw map-range.
- **Next-tick latency is fixed (F33)** — `Read`/`IntensityAt` see COMMITTED only; `Deposit`/`Spread` write
  PENDING; `Commit` swaps. Pending is rebuilt from current emitters each cadence ⇒ vanished sources fade
  (no stale accumulation). Predator responsiveness (incl. F45 wake) comes from being re-deposited EVERY tick.
- **Wind is an input contract (F21/F33)** — `Wind.Mag==0` (P1 climate-OFF) ⇒ isotropic short-range spread +
  neighbor-intensity read gradient; `Wind.Mag>0` ⇒ downwind diffusion + upwind read gradient. Value only.
- **`cellSize > 0`** (panic at `New`); `cellSize ∝ smell radius`, `≥ max-speed·Ns` lower bound (F32, D10).
- **No IO / no wall-clock / no global rand** — imports only `core` (+ stdlib `sort`/`math`); cadence is `tick % Ns` (world's).

## Acceptance Criteria (testable)
- [ ] **Deposit + read intensity (F20–F22, scalar)** — after `Deposit(ChanFood, p, 1.0)` + `Commit`, `Read(p,r,wind)`
  gives `Food.Intensity > 0` and `Prey.Intensity == 0`; depositing a LARGER magnitude (or two stacked sources)
  yields a STRICTLY larger intensity (magnitude-weighted, accumulative); channels are independent. Table-driven.
- [ ] **Intensity falls off with distance** — for a single source, `Read` intensity at the source cell >
  one ring out > two rings out > 0 beyond reach; the `Dir` gradient points toward the source at every ring.
- [ ] **`IntensityAt` O(1) wake gate (F45)** — after `Deposit(ChanPredator,p,m)` + `Commit`, `IntensityAt(ChanPredator,p)>0`
  inside p's cell, 0 well away (no neighbor scan); 0 before `Commit` (latency); agrees with the own-cell
  component of `Read` (but Read also sees neighbor intensity). Table-driven.
- [ ] **Next-tick latency (F33)** — a deposit is invisible to `Read`/`IntensityAt` before `Commit`, visible after;
  a `Commit` with no new deposits fades the field (food/prey not re-deposited drop toward 0; predator persists
  only if re-deposited). Table-driven.
- [ ] **Spread = fixed-order diffusion, downwind / isotropic (F33)** — with `Wind.Mag>0`, `Spread` moves
  intensity to downwind neighbors (more/farther with Mag) and the upwind side stays lower; with `Wind{0,0}`
  it diffuses isotropically short-range; intensity strictly attenuates with distance. Deterministic: two runs
  of the same deposit set + wind ⇒ byte-identical buffers; shuffling deposit order ⇒ identical result.
- [ ] **Read gradient (F34)** — with `Wind.Mag>0`, `Read.Dir` points UPWIND (toward source); with `Wind{0,0}`
  and a stronger on-cell to the east, `Dir` points east (intensity gradient); absent channel ⇒ `Intensity==0`,
  zero `Dir`. Table-driven.
- [ ] **Scent-only (F44)** — `Reading` exposes no forward-FOV sight; `Predator.Intensity` is the omni scent
  read (early-warning); no Heading-cone API (that is the parent's spatial query). Documents F34↔F44 split.
- [ ] **Continuous-coordinate cell math (D11)** — deposits at `(-1000.5, 7.2)` and a read/`IntensityAt` there agree
  (negative + fractional bucket correctly); an entity anywhere inside a cell reads that cell (no snap); a far read does not.
- [ ] **Empty-grid neutrality (the P_fa1 lever)** — no deposits ⇒ every `Read` all-`Intensity 0`/zero `Dir`,
  every `IntensityAt` 0, `Spread`/`Commit` no-ops ⇒ no scent-driven behaviour (fauna-OFF neutrality). Guard.
- [ ] **Determinism golden (D12)** — a fixed `(deposit/spread/commit/read/IntensityAt, wind)` sequence over N
  ticks ⇒ byte-identical digest of committed intensities + `Reading`s; a second run reproduces it. No time/RNG.
- [ ] **`New` guard + injected cellSize (D10)** — `New(≤0)` panics; no cellSize/channel/radius/falloff literal
  in logic (grep guard) — cellSize injected, channels are the fixed `Channel` constants, falloff/wind weights are balance.
- [ ] **No forbidden import (guard)** — imports only `engine/kernel/core` (+ stdlib); NO `rng`, `climate`,
  `world`, `fauna`, `perception`, `navmap`, `spatial`, `actions`.

## Out of Scope
- **The cadence** — *when* to `Deposit` (each apply; predator every tick / food·prey bulk), `Spread` (`tick % Ns`),
  `Commit` (per tick) → `engine/world` (P_fa2, F33/F41). The grid exposes the ops; world schedules them.
- **The `Wind` SOURCE** — climate generates `Wind{dir,mag}` (CA2); world injects it. Climate-OFF P1 ⇒ `Wind{0,0}`. The grid never imports/computes wind.
- **Who deposits what + at what magnitude** — which emitter carries which channel and how its intensity is
  derived (a `scent:<channel>` tag + a magnitude tag/§6) is `world`'s classification from content (D4/D10):
  edible flora→`scent:food`, prey→`scent:prey`, predator→`scent:predator`, (later) carcass/rot→`scent:carrion`.
  The grid only adds the intensity it is told to. Agent `perception.Smell` reads the SAME emitter tags (§ Notes).
- **Consuming a `Reading` / the F45 wake decision** — turning intensity/direction into drives, a utility score,
  a steer vector, or the dormant→active wake → the parent `engine/fauna` controller. The grid only reports the field.
- **The forward-FOV sight channel** — `sight.predator`/`dist.predator` come from the parent's spatial-hash query (F44(c-ii)), NOT this grid.
- **Serialization** — derived state (rebuilt from emitter positions on resume), not separately serialized.

## Open Questions
> None new. The field is determined by `docs/plans/fauna.md §1.1` + F20–F24/F32–F34/F44/F45, **F21 REVISED to
> scalar intensity** (human-confirmed 2026-06-27), and the §0/① promotion to `engine/space` + ② `scent:<channel>`
> emitter tags (shared with `perception.Smell`). The `cellSize`/`Ns`/falloff/wind-weight values are
> balance/climate data owned elsewhere — Out of Scope, not open choices. No mechanism invented.

## Notes
- **Promoted out of fauna (①):** the scent field is a general world index (spatial/navmap kin), so it lives in
  `engine/space`, fed by `world` from `scent:<channel>`-tagged emitters (flora/fauna/decay/…) and read by any
  consumer — `engine/fauna` now, `engine/mind/perception` later. (Was `engine/fauna/scent`, F36 promote-path.)
- **Coexists with `perception.Smell` (②):** the agent sense `perception.Smell` (per-entity gradient with source
  identity) and this grid (cheap area/wind field) are two deliberate models for two consumers; they share the
  ONE `scent:<channel>` + magnitude authoring (D4/D10) so "what is smelly" is written once. (Future option:
  back `perception.Smell` with this field — parked.)
- **Scalar (F21 revised):** intensity gives distance/source-magnitude that binary lacked; the grid knows
  *how strong* and *which way*, the per-entity source identity stays in `perception.Smell`.
- Double-buffer = F33 next-tick latency made explicit (read never sees a mid-tick partial deposit).
- `IntensityAt` is the cheapest probe (one cell) so the F45 per-tick wake over EVERY animal stays O(N).
- Cell key = comparable `struct{cx,cy int}` (spatial parity); iterate via a sorted slice (D12).
- Reference paths: `docs/plans/fauna.md §1.1` + F20–F24/F32–F34/F44/F45/F21(revised), `backend/engine/fauna/SPEC.md`
  (consumer), `backend/engine/space/spatial/SPEC.md` (cell-key/determinism patterns), `docs/plans/climate.md §1c`
  (`Wind` contract), `backend/engine/mind/perception/SPEC.md` (`Smell` coexistence), `docs/core/glossary.md`.
