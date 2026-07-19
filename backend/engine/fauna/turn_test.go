package fauna

import (
	"math"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/scent"
	"github.com/dogring/bdg/engine/space/spatial"
)

type turnTestStats struct{}

func (turnTestStats) Has(core.StatID) bool { return true }

type turnTestTerrain struct{}

func (turnTestTerrain) FootprintBlocked(core.Vec2) bool { return false }
func (turnTestTerrain) TerrainAt(core.Vec2) core.Tag    { return "soil" }
func (turnTestTerrain) BaseCost(core.Vec2) float64      { return 1 }

func turnNum(t *testing.T, src string) *expr.Program {
	t.Helper()
	p, err := expr.Parse(src, expr.KindNum, turnTestStats{}, nil)
	if err != nil {
		t.Fatalf("expr.Parse(%q): %v", src, err)
	}
	return p
}

func turnRules(t *testing.T, turnRate *expr.Program) *Rules {
	t.Helper()
	return NewRules(map[SpeciesID]SpeciesRule{
		"prey": {
			Speed:       turnNum(t, "1"),
			TurnRate:    turnRate,
			SmellRadius: 5,
			SightRadius: 5,
			FovArc:      math.Pi,
			SteerChannel: map[actions.ActionID]core.Tag{
				"Flee": TagFleePred,
			},
		},
	})
}

func TestTurnRateNeutralAndEval(t *testing.T) {
	ctx := &animalContext{animal: &Animal{Species: "prey", Stats: map[core.StatID]float64{"Agility": 10}}}
	rules := turnRules(t, turnNum(t, "0.5 + Agility*0.01"))
	if got := rules.TurnRate("prey", ctx); math.Abs(got-0.6) > 1e-12 {
		t.Fatalf("TurnRate = %.12f, want 0.6", got)
	}
	if got := rules.TurnRate("missing", ctx); got != 0 {
		t.Fatalf("missing species TurnRate = %.12f, want 0", got)
	}
	if got := turnRules(t, nil).TurnRate("prey", ctx); got != 0 {
		t.Fatalf("nil program TurnRate = %.12f, want 0", got)
	}
	if got := turnRules(t, turnNum(t, "0 - 1")).TurnRate("prey", ctx); got != 0 {
		t.Fatalf("negative TurnRate = %.12f, want 0", got)
	}
	if got := (*Rules)(nil).TurnRate("prey", ctx); got != 0 {
		t.Fatalf("nil rules TurnRate = %.12f, want 0", got)
	}
}

func TestSteerFullTurnRateClampAndOffNeutral(t *testing.T) {
	run := func(turnRate *expr.Program) (core.Vec2, float64) {
		a := Animal{
			ID: "prey", Species: "prey", Pos: core.Vec2{}, Heading: 0,
			Stats: map[core.StatID]float64{"Agility": 10},
		}
		rules := turnRules(t, turnRate)
		snap := &Snapshot{
			Spatial: spatial.New(8), Terrain: turnTestTerrain{}, DT: 1,
		}
		ctx := &animalContext{animal: &a}
		return steerFull(
			a, EnvSample{}, "Flee", scent.Reading{}, &core.Vec2{X: 1, Y: 0}, nil, 1,
			snap, rules, ctx, 0, 5, 5, nil, nil,
		)
	}

	pos, heading := run(turnNum(t, "0.25"))
	if math.Abs(math.Abs(angularDiff(heading, 0))-0.25) > 1e-12 {
		t.Fatalf("clamped heading = %.12f, want turn magnitude 0.25", heading)
	}
	if math.Abs(pos.X-math.Cos(heading)) > 1e-12 || math.Abs(pos.Y-math.Sin(heading)) > 1e-12 {
		t.Fatalf("movement did not follow clamped heading: pos=%+v heading=%.12f", pos, heading)
	}
	pos2, heading2 := run(turnNum(t, "0.25"))
	if pos2 != pos || heading2 != heading {
		t.Fatalf("turn clamp not deterministic: pos=%+v/%+v heading=%.12f/%.12f", pos, pos2, heading, heading2)
	}

	_, neutralHeading := run(nil)
	if math.Abs(math.Abs(angularDiff(neutralHeading, 0))-math.Pi) > 1e-12 {
		t.Fatalf("unauthored turn_rate should not clamp: heading=%.12f", neutralHeading)
	}
}
