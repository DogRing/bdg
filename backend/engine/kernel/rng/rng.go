package rng

import (
	"encoding/base64"
	"math/rand/v2"
)

// RNG is a deterministic, seeded pseudo-random number generator.
// Not safe for concurrent use — the simulation's single-threaded apply phase
// guarantees this at the call site (engine/world).
type RNG struct {
	r   *rand.Rand
	pcg *rand.PCG // kept separately so we can MarshalBinary
}

// RNGState holds the serialized PCG state as a base64-encoded binary blob.
// JSON-serializable trivially via the single Data string field.
type RNGState struct {
	Data string // base64-encoded output of rand.PCG.MarshalBinary()
}

// New returns a fresh RNG seeded with seed. Same seed → same sequence.
func New(seed int64) *RNG {
	pcg := rand.NewPCG(uint64(seed), 0)
	return &RNG{
		r:   rand.New(pcg),
		pcg: pcg,
	}
}

// Float64 returns a uniformly distributed value in [0.0, 1.0).
func (r *RNG) Float64() float64 {
	return r.r.Float64()
}

// Intn returns a uniformly distributed integer in [0, n). Panics if n ≤ 0.
func (r *RNG) Intn(n int) int {
	return r.r.IntN(n)
}

// Shuffle performs a Fisher-Yates shuffle of n elements using swap.
func (r *RNG) Shuffle(n int, swap func(i, j int)) {
	r.r.Shuffle(n, swap)
}

// NormFloat64 returns a normally distributed float64 with mean 0, stddev 1.
func (r *RNG) NormFloat64() float64 {
	return r.r.NormFloat64()
}

// State captures the full internal PCG state for deterministic resume.
func (r *RNG) State() RNGState {
	b, err := r.pcg.MarshalBinary()
	if err != nil {
		// rand.PCG.MarshalBinary never returns an error in stdlib; panic if it somehow does.
		panic("rng: MarshalBinary failed: " + err.Error())
	}
	return RNGState{
		Data: base64.StdEncoding.EncodeToString(b),
	}
}

// Restore sets the RNG to a previously-captured state.
// After Restore(s), the RNG produces the same sequence as immediately after
// the State() call that produced s.
func (r *RNG) Restore(s RNGState) {
	b, err := base64.StdEncoding.DecodeString(s.Data)
	if err != nil {
		panic("rng: base64 decode failed: " + err.Error())
	}
	if err := r.pcg.UnmarshalBinary(b); err != nil {
		panic("rng: UnmarshalBinary failed: " + err.Error())
	}
}
