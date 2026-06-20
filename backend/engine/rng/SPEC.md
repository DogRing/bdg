# SPEC — `engine/rng`

> Status: `READY`
> Leaf level: `L0`  ·  Owner agent: `implementer`

## Purpose

Provides a deterministic, seeded PRNG wrapper (`*RNG`) that the entire simulation uses
exclusively for all random draws. Enforces D12: no global `rand`, no `time.Now()`,
no external entropy. Also exposes `RNGState` for snapshot/resume so that a
paused simulation can resume byte-identically (testing.md §1 resume invariant).

## Public Interface

```go
package rng

// RNG is a deterministic, seeded pseudo-random number generator.
// Not safe for concurrent use — the simulation's single-threaded apply phase
// guarantees this at the call site (engine/world).
type RNG struct{ /* opaque: wraps *rand.Rand (PCG source, math/rand/v2) */ }

// New returns a fresh RNG seeded with seed. Same seed → same sequence.
func New(seed int64) *RNG

// Float64 returns a uniformly distributed value in [0.0, 1.0).
func (r *RNG) Float64() float64

// Intn returns a uniformly distributed integer in [0, n). Panics if n ≤ 0.
func (r *RNG) Intn(n int) int

// Shuffle performs a Fisher-Yates shuffle of n elements using swap.
// Equivalent to rand.Shuffle with the same internal state.
func (r *RNG) Shuffle(n int, swap func(i, j int))

// NormFloat64 returns a normally distributed float64 with mean 0, stddev 1.
// Useful for stat perturbation and perception noise.
func (r *RNG) NormFloat64() float64

// State captures the full internal state for deterministic resume.
func (r *RNG) State() RNGState

// Restore sets the RNG to a previously-captured state.
// After Restore(s), the RNG produces the same sequence as immediately after
// the call that produced s.
func (r *RNG) Restore(s RNGState)

// RNGState holds the serialized PCG state as a base64-encoded binary blob.
// JSON-serializable trivially via the single Data string field.
// Stored in the Snapshot (data-contracts §1: rng_state field).
type RNGState struct {
    Data string // base64-encoded output of rand.PCG.MarshalBinary()
}
```

## Dependencies

None. L0 leaf — imports only `math/rand/v2` (stdlib) and `encoding/base64`.

## Owned Data

Internal `*rand.Rand` and `*rand.PCG` instances (math/rand/v2). `RNGState` is a plain
value type; the caller owns copies returned by `State()`.

## Invariants

- **No global rand**: the package must never call `rand.Float64()`, `rand.Intn()`,
  or any function on the global `rand` source. Enforce with a blank import-cycle
  check or `grep` in CI.
- **No time**: no import of the `time` package.
- **Sequence identity**: `New(s)` → k draws → `State()` = `New(s)` → `Restore(stateAfterKDraws)` → 0 draws. The next draw from both paths must be identical (D12).
- **Resume identity**: `r.Restore(r.State())` is a no-op — the next draw is unchanged.
- `Shuffle` must produce the same permutation as `(*rand.Rand).Shuffle` called
  on the same internal state, since we wrap it directly.
- `Intn(n)` panics for `n ≤ 0` (delegated to stdlib behaviour).

## Acceptance Criteria (testable)

- [ ] Golden sequence: `New(42)` → 20 calls to `Float64()` matches a recorded golden
  slice (regression guard; regenerate only on intentional stdlib version bump).
- [ ] `State()` / `Restore()` round-trip: draw 50 values, capture state, draw 50 more,
  restore, draw 50 again — second and third batches are identical.
- [ ] `Intn(n)` returns values in `[0, n)` for n = 1, 10, 1000 (table-driven, 1000 draws each).
- [ ] `Shuffle([0..9])` produces a valid permutation (all elements present, no duplicates) over 100 runs.
- [ ] `NormFloat64()` mean ≈ 0 ± 0.05, stddev ≈ 1 ± 0.05 over 10 000 draws.
- [ ] `RNGState` round-trips through `encoding/json` without loss (marshal → unmarshal → same sequence).
- [ ] No import of `time` or any global-rand call (static check via `grep` in test file).

## Implementation Notes

- Use `math/rand/v2`. Create PCG source with `rand.NewPCG(uint64(seed), 0)`, then
  `rand.New(pcg)`. Keep the `*rand.PCG` pointer separately so `MarshalBinary()` captures
  live PCG state after each draw.
- `State()`: call `pcg.MarshalBinary()` → base64-encode → store in `RNGState.Data`.
- `Restore(s)`: base64-decode `s.Data` → call `pcg.UnmarshalBinary(bytes)`.
  The `*rand.Rand` wrapping the PCG reads through the same pointer, so state is restored.

## Out of Scope

- Seeding from OS entropy, `crypto/rand`, or `time.Now()` — the caller supplies the seed.
- Thread-safety / mutex — the simulation's tick model (single-threaded apply) makes this unnecessary.
- Distribution utilities beyond `Float64`, `Intn`, `Shuffle`, `NormFloat64` — add to this SPEC if a consumer genuinely needs more.
