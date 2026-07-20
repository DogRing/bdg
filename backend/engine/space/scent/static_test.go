package scent

import (
	"math"
	"testing"
)

func TestStaticLayerDiffusesOnRebuild(t *testing.T) {
	const sourceIntensity = 10.0
	source := v(0.5, 0.5)
	east := v(1.5, 0.5)
	west := v(-0.5, 0.5)

	for _, tc := range []struct {
		name  string
		wind  Wind
		check func(t *testing.T, source, east, west float64)
	}{
		{
			name: "isotropic halo",
			wind: Wind{},
			check: func(t *testing.T, source, east, west float64) {
				t.Helper()
				if east <= 0 || west <= 0 {
					t.Fatalf("static halo missing: east=%v west=%v", east, west)
				}
				if east >= source || west >= source {
					t.Fatalf("static halo must be weaker than source: source=%v east=%v west=%v", source, east, west)
				}
				if east != west {
					t.Fatalf("zero-wind halo is not isotropic: east=%v west=%v", east, west)
				}
			},
		},
		{
			name: "downwind bias",
			wind: Wind{Dir: 0, Mag: 0.5},
			check: func(t *testing.T, _, east, west float64) {
				t.Helper()
				if east <= west {
					t.Fatalf("downwind static halo must exceed upwind halo: east=%v west=%v", east, west)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := New(1)
			g.DepositStatic(ChanFood, source, sourceIntensity)
			g.CommitStatic(tc.wind)
			tc.check(t,
				g.IntensityAt(ChanFood, source),
				g.IntensityAt(ChanFood, east),
				g.IntensityAt(ChanFood, west),
			)
		})
	}
}

func TestDiffusingLayersAreIsolated(t *testing.T) {
	t.Run("static does not touch dynamic", func(t *testing.T) {
		g := New(1)
		oldSource := v(0.5, 0.5)
		oldHalo := v(1.5, 0.5)
		newSource := v(20.5, 0.5)

		g.DepositStatic(ChanFood, oldSource, 10)
		g.CommitStatic(Wind{})
		g.DepositStatic(ChanFood, newSource, 10)
		g.CommitStatic(Wind{})
		g.Commit()

		if got := g.IntensityAt(ChanFood, oldSource); got != 0 {
			t.Fatalf("replaced static source leaked through dynamic layer: %v", got)
		}
		if got := g.IntensityAt(ChanFood, oldHalo); got != 0 {
			t.Fatalf("static halo leaked through dynamic layer: %v", got)
		}
	})

	t.Run("dynamic does not touch static", func(t *testing.T) {
		g := New(1)
		staticSource := v(0.5, 0.5)
		dynamicSource := v(20.5, 0.5)
		g.DepositStatic(ChanFood, staticSource, 10)
		g.CommitStatic(Wind{})
		want := g.IntensityAt(ChanFood, staticSource)

		g.Deposit(ChanFood, dynamicSource, 10)
		g.Spread(Wind{})
		g.Commit()
		g.Commit() // flush dynamic; the static field must remain unchanged

		if got := g.IntensityAt(ChanFood, staticSource); got != want {
			t.Fatalf("dynamic diffusion changed static layer: got=%v want=%v", got, want)
		}
		if got := g.IntensityAt(ChanFood, dynamicSource); got != 0 {
			t.Fatalf("dynamic source persisted in static layer: %v", got)
		}
	})
}

func TestStaticLayerPersistsBetweenRebuilds(t *testing.T) {
	g := New(1)
	source := v(0.5, 0.5)
	halo := v(1.5, 0.5)
	g.DepositStatic(ChanFood, source, 10)
	g.CommitStatic(Wind{})
	wantSource := g.IntensityAt(ChanFood, source)
	wantHalo := g.IntensityAt(ChanFood, halo)

	for tick := 1; tick <= 8; tick++ {
		g.Commit()
		if got := g.IntensityAt(ChanFood, source); got != wantSource {
			t.Fatalf("tick %d source changed: got=%v want=%v", tick, got, wantSource)
		}
		if got := g.IntensityAt(ChanFood, halo); got != wantHalo {
			t.Fatalf("tick %d halo changed: got=%v want=%v", tick, got, wantHalo)
		}
	}
}

func TestCommitStaticIsAtomicReplacement(t *testing.T) {
	g := New(1)
	oldSource := v(0.5, 0.5)
	keptSource := v(10.5, 0.5)
	newSource := v(20.5, 0.5)
	g.DepositStatic(ChanFood, oldSource, 10)
	g.DepositStatic(ChanFood, keptSource, 10)
	g.CommitStatic(Wind{})
	wantKept := g.IntensityAt(ChanFood, keptSource)

	// Building the replacement is invisible until the atomic swap.
	g.DepositStatic(ChanFood, keptSource, 10)
	g.DepositStatic(ChanFood, newSource, 10)
	if got := g.IntensityAt(ChanFood, oldSource); got == 0 {
		t.Fatal("old static field disappeared before CommitStatic")
	}
	if got := g.IntensityAt(ChanFood, newSource); got != 0 {
		t.Fatalf("half-rebuilt static field became visible: %v", got)
	}

	g.CommitStatic(Wind{})
	if got := g.IntensityAt(ChanFood, oldSource); got != 0 {
		t.Fatalf("dropped source survived replacement: %v", got)
	}
	if got := g.IntensityAt(ChanFood, keptSource); got != wantKept {
		t.Fatalf("retained source changed: got=%v want=%v", got, wantKept)
	}
	if got := g.IntensityAt(ChanFood, newSource); got == 0 {
		t.Fatal("new source missing after CommitStatic")
	}
}

func TestTrailIsNeverDiffused(t *testing.T) {
	g := New(1)
	var strength [NumChannels]float64
	strength[ChanPrey] = 1
	g.ConfigureTrail(strength, 0.5, 0)
	source := v(0.5, 0.5)
	neighbor := v(1.5, 0.5)

	g.Deposit(ChanPrey, source, 1)
	g.Spread(Wind{Dir: 0, Mag: 1})
	g.Commit()
	g.Commit() // flush the airborne layer, leaving only trail
	g.CommitStatic(Wind{Dir: 0, Mag: 1})
	g.Spread(Wind{Dir: 0, Mag: 1})
	g.Commit()

	if got := g.IntensityAt(ChanPrey, source); got <= 0 {
		t.Fatal("trail source disappeared")
	}
	if got := g.IntensityAt(ChanPrey, neighbor); got != 0 {
		t.Fatalf("trail diffused into neighbor: %v", got)
	}
	before := g.IntensityAt(ChanPrey, source)
	g.DecayTrail()
	if got := g.IntensityAt(ChanPrey, source); math.Abs(got-before*0.5) > 1e-12 {
		t.Fatalf("trail decay got=%v want=%v", got, before*0.5)
	}
}
