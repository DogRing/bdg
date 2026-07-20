package scent_test

import (
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/scent"
)

func trailStrength(ch scent.Channel, v float64) [scent.NumChannels]float64 {
	var s [scent.NumChannels]float64
	s[ch] = v
	return s
}

// TestTrailOutlivesTheSource is the point of the spoor layer: the dynamic layer is rebuilt from
// scratch every Commit, so without a trail the world keeps no record that an animal was ever
// anywhere. A predator arriving one tick late finds nothing.
func TestTrailOutlivesTheSource(t *testing.T) {
	g := scent.New(10)
	g.ConfigureTrail(trailStrength(scent.ChanPrey, 0.5), 0.9, 0)
	at := core.Vec2{X: 5, Y: 5}

	g.Deposit(scent.ChanPrey, at, 1)
	g.Commit()
	live := g.IntensityAt(scent.ChanPrey, at)

	// The animal moves on: nothing is deposited here again.
	for range 5 {
		g.DecayTrail()
		g.Commit()
	}
	after := g.IntensityAt(scent.ChanPrey, at)

	if after <= 0 {
		t.Fatalf("nothing left where the animal had been: the trail did not outlive the source")
	}
	if after >= live {
		t.Fatalf("a cold trail (%.4f) must be fainter than the live animal was (%.4f)", after, live)
	}
}

// TestTrailFadesToNothing pins the other half: memory must be bounded, or every cell an animal ever
// stepped on smells forever and the map turns into a uniform haze with no gradient to follow.
func TestTrailFadesToNothing(t *testing.T) {
	g := scent.New(10)
	g.ConfigureTrail(trailStrength(scent.ChanPrey, 0.5), 0.5, 0)
	at := core.Vec2{X: 5, Y: 5}
	g.Deposit(scent.ChanPrey, at, 1)
	g.Commit()
	g.Commit() // flush the live deposit; what is left here is only the trail
	for range 200 {
		g.DecayTrail()
	}
	if got := g.IntensityAt(scent.ChanPrey, at); got != 0 {
		t.Fatalf("trail never expires: %.8f still present after 200 decay steps", got)
	}
}

// TestTrailIsCappedForAStationarySource — an animal that never moves re-deposits on the same cell
// every tick, so without a clamp its trail grows toward deposit/(1-decay) and can dwarf every live
// signal on the map.
func TestTrailIsCappedForAStationarySource(t *testing.T) {
	const cap = 2.0
	g := scent.New(10)
	g.ConfigureTrail(trailStrength(scent.ChanPrey, 0.5), 0.99, cap)
	at := core.Vec2{X: 5, Y: 5}
	for range 2000 {
		g.Deposit(scent.ChanPrey, at, 1)
		g.DecayTrail()
		g.Commit()
	}
	// IntensityAt includes the live deposit too; the trail portion is what must be capped.
	g.Commit() // flush the last live deposit out of committed
	if got := g.IntensityAt(scent.ChanPrey, at); got > cap+1e-9 {
		t.Fatalf("stationary source accumulated trail %.4f beyond the cap %.1f", got, cap)
	}
}

// TestTrailIsPerChannelOptIn — whether a kind of scent lingers is content's call. Leaving the
// predator channel off is load-bearing: fear is SET from scent.predator, so a lingering predator
// trail would leave prey permanently wary anywhere a wolf had ever walked.
func TestTrailIsPerChannelOptIn(t *testing.T) {
	g := scent.New(10)
	g.ConfigureTrail(trailStrength(scent.ChanPrey, 0.5), 0.9, 0)
	at := core.Vec2{X: 5, Y: 5}
	g.Deposit(scent.ChanPrey, at, 1)
	g.Deposit(scent.ChanPredator, at, 1)
	g.Commit()
	g.Commit() // both live deposits are gone now

	if got := g.IntensityAt(scent.ChanPrey, at); got <= 0 {
		t.Errorf("opted-in channel left no trail")
	}
	if got := g.IntensityAt(scent.ChanPredator, at); got != 0 {
		t.Errorf("channel with strength 0 left a trail anyway: %.4f", got)
	}
}

// TestTrailIsFollowedNotWinded — the direction rule. Airborne scent is located by walking upwind;
// a trail is not blowing anywhere, so the only way to use it is to move along its gradient. If the
// upwind rule were applied to it, a tracker would walk crosswise to the tracks under its feet.
func TestTrailIsFollowedNotWinded(t *testing.T) {
	g := scent.New(10)
	g.ConfigureTrail(trailStrength(scent.ChanPrey, 1), 0.99, 0)
	reader := core.Vec2{X: 5, Y: 5}
	// Lay a trail to the EAST of the reader, with the wind blowing from the north (so "upwind" is
	// north — a direction with nothing in it).
	g.Deposit(scent.ChanPrey, core.Vec2{X: 25, Y: 5}, 1)
	g.Commit()
	g.Commit() // live deposit gone; only the trail remains

	wind := scent.Wind{Dir: -1.5707963267948966, Mag: 1} // blowing south ⇒ upwind is north
	dir := g.Read(reader, 40, wind).Prey.Dir
	if dir.X <= 0.5 {
		t.Fatalf("tracker did not head along the trail (east): dir=%+v", dir)
	}
}

// TestLiveScentStillOutweighsASaturatedTrail — the blend must not let ground scent overrule a live
// animal standing in the open, or a predator would abandon the deer in front of it to go sniff a path.
//
// This is the constraint that governs the content values. A cell occupied every tick saturates at
// strength/(1−decay), so those two numbers together — not either alone — decide whether a well-used
// bedding site can out-shout a live animal. Here the trail is run to full saturation on purpose;
// the live animal must still win. (The per-cell cap is the belt-and-braces version of the same
// invariant: keep it below a live source's magnitude and saturation cannot invert the comparison
// no matter what strength and decay are set to.)
func TestLiveScentStillOutweighsASaturatedTrail(t *testing.T) {
	const strength, decay = 0.004, 0.99 // saturates at 0.4, well under a live animal's 1.0
	g := scent.New(10)
	g.ConfigureTrail(trailStrength(scent.ChanPrey, strength), decay, 0)
	reader := core.Vec2{X: 5, Y: 5}
	// A herd bedded down to the west for a long time, then left.
	for range 3000 {
		g.Deposit(scent.ChanPrey, core.Vec2{X: -25, Y: 5}, 1)
		g.DecayTrail()
		g.Commit()
	}
	g.Commit() // the westerly herd is gone; only its trail remains
	// ...and a live animal to the east, right now.
	g.Deposit(scent.ChanPrey, core.Vec2{X: 25, Y: 5}, 1)
	g.Commit()

	if dir := g.Read(reader, 40, scent.Wind{}).Prey.Dir; dir.X <= 0 {
		t.Fatalf("saturated trail out-pulled the live animal: dir=%+v "+
			"(strength/(1-decay) = %.2f must stay below a live source's magnitude)",
			dir, strength/(1-decay))
	}
}
