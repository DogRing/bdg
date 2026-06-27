# SPEC — `engine/fauna/scent`

> Status: `DRAFT`
> Leaf level: `L4` (sub-module of `engine/fauna`)  ·  Owner agent: `<filled by implementer>`
> Scope: **P_fa1** (`docs/fauna.md §2`, §1.1 + F20–F24, F32–F34/F44; `PresentAt` wake probe for F45). Parent: `backend/engine/fauna/SPEC.md`.

## Purpose
The **scent grid**: a single uniform, multi-channel **auxiliary index** (spatial-hash / navmap-cost-field
kin) over continuous space (D11 — the grid is an *index*, not the world; animals keep continuous `Pos`
and only READ the cell they fall in + neighbors, never snapped to it). Each cell carries a `uint8` bitset
of binary presence flags for the channels `{food, prey, predator}` (F21/F22 — presence + wind direction,
NOT scalar concentration). It owns these operations: **deposit** (a scent source sets its cell's channel
bit), **wind-spread** (a deterministic fixed-order stencil carries non-predator flags downwind on the
`tick % Ns` bulk cadence, with a **next-tick (1-tick) latency**), **read** (an animal reads its cell +
neighbors within its smell radius → per-channel presence + a coarse upwind / neighbor-on direction +
coarse distance proxy, F34), and the O(1) own-cell **`PresentAt`** probe the parent runs as the F45
predator-scent wake check. It does **not** own *when* deposit/spread/commit run (cadence is
`engine/world`'s, F33/F41), the animal decision that consumes a `Reading` (parent `engine/fauna`), the
wind VALUE (injected by world from climate, neutral in P1), or any object/animal mutation. No IO, no
wall-clock, **no RNG** (spread is a fixed-order stencil, D12). Concept & rationale: `docs/fauna.md §1.1`
+ F20–F24 (single uniform binary multi-channel grid) + F32–F34 (cell shape / spread algorithm / read
rule) + F44 (sight is separate — this grid is *scent-only*: food/prey homing + predator early-warning)
+ F45 (the wake probe).

## Public Interface
```go
package scent

import "github.com/dogring/bdg/engine/kernel/core"

// Channel selects one scent layer (F22 — multi-channel, one presence bit per channel per cell). The
// constant order is FIXED (it is the bit position in the cell's uint8 bitset, D12); a new channel
// (later phase) appends a new bit, never re-orders.
type Channel uint8

const (
    ChanFood     Channel = iota // edible-flora / food scent (herbivore homing → Graze)
    ChanPrey                    // prey-animal scent (carnivore homing → Hunt)
    ChanPredator               // predator scent (early-warning → Wary + F45 wake; close Flee = sight, F44)
)

// Wind is the local wind VALUE world injects from climate (operands wind.dir / wind.mag, CA2). Dir is
// the direction the wind BLOWS TOWARD, in radians; Mag is its strength (≥ 0). In P1 climate is OFF →
// the zero Wind{0,0} ⇒ spread stays local (source cell + immediate neighbors) and read falls back to
// the neighbor-on coarse direction (F21/F33 input contract). The grid never invents wind.
type Wind struct {
    Dir float64 // radians; direction the wind blows toward
    Mag float64 // ≥ 0; 0 ⇒ no downwind transport (P1 neutral)
}

// Grid is the uniform multi-channel scent index. cellSize ∝ the consuming species' smell radius
// (F32 — balance data, spatial.cellSize parity; injected, NOT hardcoded, D10), with a lower bound of
// max-speed·Ns so an upwind step never skips a cell. It is DOUBLE-BUFFERED for the next-tick latency
// (F33): READS see the COMMITTED snapshot buffer (stable for the whole tick's read phase), while
// Deposit and Spread write the PENDING buffer; Commit swaps pending→committed and clears pending. The
// grid is fauna-owned for P1 (F36); world DRIVES the cadence (deposit each apply, spread on tick%Ns,
// commit per tick). Not safe for concurrent mutation; the single-threaded apply phase guarantees that.
type Grid struct{ /* opaque: cellSize float64; committed + pending sparse cell→uint8 buffers. */ }

// New creates an empty grid with the given square cell edge length (world units). Panics if cellSize ≤ 0
// (degenerate bucketing, spatial parity). Both buffers start empty (all channels absent everywhere).
func New(cellSize float64) *Grid

// ── Deposit (apply phase; world-driven, fixed order) ─────────────────────────────────

// Deposit sets ch's presence bit in the PENDING buffer at the cell containing pos (binary — idempotent;
// re-depositing the same cell/channel is a no-op). world calls it in the serial apply phase in fixed
// (sorted source ObjectID) order (D12). Per F33: the predator channel is deposited EVERY tick at each
// predator's source cell (so it is always fresh for early-warning), while food/prey are deposited in
// bulk; the per-channel cadence is world's (P_fa2), not the grid's. pos is continuous (D11); the grid
// computes its cell as floor(pos/cellSize) per axis (negative-coordinate safe, spatial parity).
func (g *Grid) Deposit(ch Channel, pos core.Vec2)

// ── Spread (bulk pass; world-driven on tick % Ns) ────────────────────────────────────

// Spread runs ONE deterministic fixed-order stencil pass over the PENDING buffer (F33(a)): it iterates
// the on-cells in sorted cell-key order and, for the NON-predator channels (food/prey), propagates each
// on-flag to the cell(s) downwind of it per `wind` (range grows with wind.Mag); with Wind.Mag == 0 it
// propagates only to the immediate neighbor cells (local short-range, the P1 neutral fallback). The
// PREDATOR channel is NOT spread here (it is re-deposited every tick at source, F33). No RNG, no
// map-iteration for logic (cells visited in sorted key order). world calls Spread on the `tick % Ns`
// bulk cadence (Ns = balance, F24); the latency from a deposit to its visibility is one tick (the
// pending→committed Commit, below) — reads always see a clean prior-tick snapshot (navmap parity).
func (g *Grid) Spread(wind Wind)

// ── Commit (per-tick; world-driven) ──────────────────────────────────────────────────

// Commit swaps the PENDING buffer into the COMMITTED (read) buffer and clears the new pending buffer,
// realizing the 1-tick latency (F33 — a deposit/spread at tick T is visible to Reads at tick T+1). world
// calls it once per tick at the tick boundary (P_fa2 wiring). Deterministic; no RNG.
func (g *Grid) Commit()

// ── Read (read phase; pure; the parent controller consumes this) ──────────────────────

// PresentAt is the O(1) own-cell channel-bit test over the COMMITTED buffer: true iff ch's presence bit
// is set in the single cell containing pos (NO neighbor scan, NO direction). It is the cheap per-tick
// probe the parent runs for EVERY animal as the F45 wake check — `PresentAt(ChanPredator, Pos)` wakes a
// DORMANT herbivore "exactly when predator scent reaches its cell" without a neighbor scan. Pure
// (committed buffer), no RNG. (A full sense uses Read; this is only the wake gate.)
func (g *Grid) PresentAt(ch Channel, pos core.Vec2) bool

// Read returns the multi-channel reading at pos over the COMMITTED buffer, scanning the cell containing
// pos plus the neighbor cells within smellRadius (F34). For each channel it reports presence + a coarse
// direction toward the aggregate on-cells (UPWIND of pos when Wind.Mag > 0, else the average offset of
// the on-cells — the "neighbor-on coarse direction", F34(c)) + a coarse distance proxy (0 for the own
// cell, ~cellSize per neighbor ring, a large sentinel when absent). It is PURE (read phase) and reads
// only the committed buffer — concurrent reads from the parallel score phase are safe. The parent maps
// the result onto the §6 operands scent.{food,prey,predator} (presence 1/0), dist.{food,prey} (coarse),
// and the per-channel steer direction (toward for food/prey, reversed for predator Flee/Wary). This
// grid is scent-only (F44): it does NOT model the forward-FOV sight channel (that is the parent's
// spatial-hash predator query) — `dist.predator`/`sight.predator` come from sight, not from here.
func (g *Grid) Read(pos core.Vec2, smellRadius float64, wind Wind) Reading

// Reading is the per-channel scent result at one position (the parent fills §6 operands from it).
type Reading struct {
    Food     ChannelReading
    Prey     ChannelReading
    Predator ChannelReading // presence → scent.predator (early-warning/Wary); see Read note re: sight
}

// ChannelReading is one channel's presence + coarse bearing + coarse distance at a read position.
type ChannelReading struct {
    Present    bool      // any on-cell within smellRadius ⇒ true (→ scent.<ch> operand 1/0)
    Dir        core.Vec2 // unit direction toward the aggregate source (upwind if wind, else neighbor-on);
                        // zero vector when Present is false. The parent reverses it for predator Flee.
    CoarseDist float64   // coarse distance proxy: 0 (own cell) … ~k·cellSize (k rings out); large sentinel
                        // when Present is false (→ dist.<ch> operand). Binary grid ⇒ ring-coarse, not exact.
}
```

## Dependencies
- `engine/kernel/core` — `Vec2` (cell math), `ObjectID` (none stored — deposit is positional). No IO,
  no RNG, no other engine import. This keeps the sub-module a clean leaf the parent `engine/fauna`
  composes (parent import set unchanged: core/expr/rng/actions/spatial/fauna·scent).
- *(NOT `engine/kernel/rng`)* — spread is a deterministic fixed-order stencil; the grid draws no
  randomness (D12). *(NOT `engine/env/climate`)* — `Wind` is an injected value (world samples climate),
  never imported. *(NOT `engine/world`)* — world DRIVES deposit/spread/commit cadence; the grid never
  schedules itself and mutates no world state.

## Owned Data
- The two sparse **cell buffers** (committed + pending), keyed by integer cell coords (`struct{cx,cy int}`,
  not a formatted string — alloc-free + deterministic, spatial parity), each value a `uint8` channel
  bitset. The grid OWNS these; no other module reads or mutates them directly — callers interact only
  through Deposit/Spread/Commit/Read/PresentAt. `Reading` is a fresh value the caller may retain freely.
- `cellSize` is fixed at `New` (injected from balance, `cellSize ∝ smell radius`, F32) — never mutated.
- The grid is fauna-owned for P1 (F36); world holds one per run and drives its cadence. It is DERIVED
  state — rebuilt from positions on resume (like the spatial hash), so it is not separately serialized
  (`docs/data-contracts.md` — the animal/source positions are the source of truth).

## Invariants
- **D11 — index, not the world** — the grid is an auxiliary index over continuous space; `Deposit`/`Read`/
  `PresentAt` take continuous `Vec2` and compute `floor(p/cellSize)` per axis; animals are NEVER snapped to
  a cell. The grid drives no agent/animal logic by itself — it is a scent *data field* the parent reads
  (navmap wear/terrain-pass parity), exactly the D11 "grids are indices, not the world" rule.
- **D12 determinism, no RNG** — `Spread` is a fixed-order stencil over the sorted on-cell keys with no
  randomness; `Deposit` is idempotent bit-set; `Read` scans the neighbor ring in a fixed nested order;
  `PresentAt` is a pure single-cell lookup. Same deposit/spread/commit sequence ⇒ byte-identical buffers,
  `Reading`s, and `PresentAt` results, every run, every process.
- **D12 no map-iteration for logic** — every buffer traversal (spread, read, any digest) iterates cell
  keys in **sorted order**, never raw map-range. Float accumulation in the read's direction average is in
  the fixed sorted-cell order.
- **Binary presence, not concentration (F21/F22)** — a cell channel is one bit (present / absent); the
  grid stores NO scalar intensity. Distance is therefore only ring-coarse (`CoarseDist`) and direction is
  upwind-or-neighbor-on — sufficient for steering; a future scalar-concentration upgrade is a deliberate
  later phase (frontier), not P1.
- **Next-tick latency is fixed (F33)** — `Read`/`PresentAt` see the COMMITTED buffer only; `Deposit`/
  `Spread` write PENDING; `Commit` swaps. A deposit at tick T is invisible until T+1 (clean prior-tick
  snapshot, determinism-clear, navmap parity). The predator channel's responsiveness (incl. the F45 wake)
  comes from being re-deposited EVERY tick (so it is fresh at T+1), not from same-tick reads.
- **Predator channel: deposit-only, never spread (F33)** — `Spread` propagates only food/prey; predator
  presence is the every-tick source deposit. (Wind-carried predator scent is a later-phase choice.)
- **Wind is an input contract (F21/F33)** — `Wind.Mag == 0` (P1 climate-OFF) ⇒ spread is local
  (source + immediate neighbors) and `Read` uses the neighbor-on coarse direction; `Wind.Mag > 0`
  (climate shipped) ⇒ downwind spread + upwind read direction. The grid reads `Wind` as a value only.
- **`cellSize > 0` always** (panic at `New` otherwise); `cellSize ∝ smell radius` with a `≥ max-speed·Ns`
  lower bound so an upwind step never skips a cell (F32). Injected, never hardcoded (D10).
- **No IO / no wall-clock / no global rand** — imports only `core` (+ stdlib `sort`/`math`); the cadence
  is `tick % Ns` (world's), never wall-clock.

## Acceptance Criteria (testable)
- [ ] **Deposit + read presence (F20–F22)** — after `Deposit(ChanFood, p)` + `Commit`, a `Read(p, r,
  wind)` reports `Food.Present == true` and (with another channel absent) `Prey.Present == false`; the
  bitset packs the three channels independently (setting food does not set prey/predator). Table-driven
  over the three channels.
- [ ] **`PresentAt` O(1) own-cell wake probe (F45)** — after `Deposit(ChanPredator, p)` + `Commit`,
  `PresentAt(ChanPredator, p)` is true for any `Pos` inside p's cell and false one cell away (no neighbor
  scan, unlike `Read`); before `Commit` it is false (next-tick latency); it agrees with
  `Read(...).Predator.Present` for the OWN cell but not for neighbor-only presence (Read sees neighbors,
  PresentAt does not). Table-driven.
- [ ] **Next-tick latency (F33)** — a `Deposit` is NOT visible to a `Read`/`PresentAt` BEFORE `Commit`; it
  IS visible after `Commit`; a second `Commit` with no new deposits clears the field (food/prey not
  re-deposited disappear; predator persists only if re-deposited each tick). Table-driven.
- [ ] **Spread = fixed-order stencil, downwind / local (F33)** — with `Wind.Mag > 0`, `Spread` propagates
  a food on-flag to the downwind neighbor cell(s) (range grows with Mag) and NOT upwind; with
  `Wind{0,0}`, it propagates only to the immediate neighbor cells (local); the PREDATOR channel is never
  spread (an isolated predator deposit stays a single cell after Spread). Deterministic: two runs of the
  same deposit set + wind yield byte-identical buffers; shuffling deposit order yields the same result.
- [ ] **Read coarse direction (F34)** — with `Wind.Mag > 0`, `Read.Dir` points UPWIND (toward the source
  against the wind); with `Wind{0,0}` and an on-cell to the east, `Read.Dir` points east (neighbor-on
  average); for an absent channel `Present == false`, `Dir` is the zero vector and `CoarseDist` is the
  large sentinel. `CoarseDist` grows with ring distance (own cell 0 < one ring < two rings). Table-driven.
- [ ] **Scent-only (F44)** — `Reading` exposes no forward-FOV sight result; `Predator.Present` is purely
  the omni scent read (early-warning), and the grid offers no API for the Heading-relative cone (that is
  the parent's spatial query). Documents the F34↔F44 split.
- [ ] **Continuous-coordinate cell math (D11)** — deposits at `(-1000.5, 7.2)` and a read/`PresentAt`
  centered there agree (negative + fractional coords bucket correctly); an animal whose `Pos` lies
  anywhere inside a cell reads that cell (no snapping); a far read does not see it.
- [ ] **Empty-grid neutrality (the P_fa1 lever)** — with no deposits, every `Read` returns all channels
  `Present == false` / zero `Dir` / sentinel `CoarseDist` and every `PresentAt` is false; `Spread`/`Commit`
  on an empty grid are no-ops; the parent emits no scent-driven behaviour (fauna-OFF neutrality). Guard.
- [ ] **Determinism golden (D12)** — a fixed `(deposit/spread/commit/read/PresentAt sequence, wind
  sequence)` over N ticks yields a byte-identical digest of the committed buffer + the per-read `Reading`s
  + `PresentAt` results; a second run reproduces it (cross-process). No `time`/global-rand/RNG anywhere.
- [ ] **`New` guards + injected cellSize (D10)** — `New(cellSize ≤ 0)` panics; no cell-size / channel /
  radius literal appears in logic (grep guard) — `cellSize` is injected, the channel bits are the fixed
  `Channel` constants.
- [ ] **No forbidden import (guard)** — grep/import guard: imports only `engine/kernel/core` (+ stdlib);
  NO `rng`, `climate`, `world`, `navmap`, `spatial`, or `actions` import in the scent sub-module.

## Out of Scope
- **The cadence** — *when* to `Deposit` (each apply, predator every tick / food·prey bulk), `Spread`
  (`tick % Ns`), and `Commit` (per tick) → `engine/world` (P_fa2, F33/F41). The grid exposes the
  operations; world schedules them. (The parent runs `PresentAt`/`Read` in its own read phase.)
- **The `Wind` SOURCE** — climate generates `Wind{dir,mag}` (CA2) and world injects it into Spread + the
  parent's `apparent_temp`/read; climate-OFF P1 ⇒ `Wind{0,0}` (`docs/climate.md §1c`). The grid never
  imports or computes wind.
- **Who deposits what** — which source object/animal carries which channel (a predator deposits
  `ChanPredator`, edible flora `ChanFood`, prey `ChanPrey`) is the parent/world's classification from
  content tags (D4/D10); the grid only sets the bit it is told to.
- **Consuming a `Reading` / the F45 wake decision** — turning presence/direction into a drive set, a
  utility score, a steer vector, or the dormant→active wake → the parent `engine/fauna` controller
  (`backend/engine/fauna/SPEC.md`). The grid only reports the bit; the parent decides.
- **The forward-FOV sight channel** — `sight.predator`/`dist.predator` come from the parent's
  spatial-hash predator query (F44(c-ii)), NOT this grid. The grid is scent-only.
- **Scalar-concentration upgrade** — promoting the binary flags to a scalar gradient (for precise
  distance) is a parked frontier (F21 honest trade-off), not P1.
- **Serialization** — the grid is derived state (rebuilt from positions on resume), not separately
  serialized (`docs/data-contracts.md`); positions are the source of truth.

## Open Questions
> None new. The scent grid is fully determined by `docs/fauna.md §1.1` + F20–F24 (single uniform binary
> multi-channel grid), F32 (cellSize ∝ smell radius, uint8 bitset), F33 (predator-every-tick deposit +
> fixed-order stencil bulk spread on `tick%Ns` + next-tick latency), F34 (omni neighbor/upwind read,
> scent-only after F44), F45 (the `PresentAt` wake probe), all RESOLVED. This SPEC invents no mechanism.
> (`cellSize`/`Ns`/wind units are balance/climate data owned elsewhere — Out of Scope, not open choices.)

## Notes
- Double-buffer (committed/pending) is the concrete realization of F33's next-tick latency — it is exactly
  the "read sees the prior-tick snapshot" rule navmap/climate use, made explicit so determinism is
  trivial to reason about: the read phase never observes a mid-tick partial deposit.
- `PresentAt` is deliberately the cheapest possible probe (one cell lookup) so the F45 per-tick wake check
  over EVERY animal stays O(N), not O(N · neighborhood) — the full `Read` is paid only by ACTIVE animals.
- Keep the cell key a comparable `struct{cx,cy int}` (spatial-hash Notes parity) — no string formatting on
  the hot path, alloc-free, and deterministic. Iterate keys via a sorted slice for spread/digest (D12).
- The binary-presence honesty trade-off (F21): the grid knows *whether* and *roughly which way*, not
  *how far*. That is enough for upwind/neighbor-on steering; if precise distance is ever needed, promote to
  a scalar field then (frontier) — do not smuggle pseudo-distance into the binary grid now.
- Reference paths: `docs/fauna.md §1.1` + F20–F24/F32–F34/F44/F45 (binding), `backend/engine/fauna/SPEC.md`
  (the parent controller that consumes `Read`/`PresentAt`), `backend/engine/space/spatial/SPEC.md` (the
  cell-key / continuous-coordinate / determinism patterns this mirrors), `docs/climate.md §1c` (the `Wind`
  input contract), `docs/glossary.md` (`scent grid` / `scent channel` / `wind.dir`/`wind.mag` terms).
