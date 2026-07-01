// Package ecosim is the G5 ecosystem integration test harness.
// This file hand-compiles the content/objects.yaml fauna: blocks into fauna.Rules
// (production uses platform/config; here we do it directly for the harness).
// All §6 formulas are verbatim from objects.yaml (D10 — data drives behaviour).
package ecosim

import (
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
)

// agilityStatSet satisfies expr.StatSet.
// All G5 species speed/appTemp formulas reference only "Agility" from the Stat channel.
type agilityStatSet struct{}

func (agilityStatSet) Has(id core.StatID) bool { return id == "Agility" }

// mustParseNum compiles a §6 numeric formula; panics on error.
// Production: platform/config validates at load. Harness: panic surfaces bugs early.
func mustParseNum(text string) *expr.Program {
	p, err := expr.Parse(text, expr.KindNum, agilityStatSet{}, nil)
	if err != nil {
		panic("ecosim: §6 parse error [" + text + "]: " + err.Error())
	}
	return p
}

// buildRules hand-compiles fauna.Rules for all G5 species from objects.yaml fauna: blocks.
func buildRules() *fauna.Rules {
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer":   deerRule(),
		"wolf":   wolfRule(),
		"rabbit": rabbitRule(),
		"goat":   goatRule(),
		"bear":   bearRule(),
		"fish":   fishRule(),
	})
}

func deerRule() fauna.SpeciesRule {
	return fauna.SpeciesRule{
		Utilities: map[actions.ActionID]*expr.Program{
			"Graze":  mustParseNum("hunger * (0.3 + scent.food) - fear * 1.5"),
			"Wary":   mustParseNum("fear"),
			"Flee":   mustParseNum("(fear - 0.6) * 5"),
			"Rest":   mustParseNum("fatigue * 0.4 + thermal * 0.3 - hunger * 0.5 - fear * 2"),
			"MoveTo": mustParseNum("0.1"),
		},
		Drives: []fauna.DriveRule{
			{ID: "hunger", Rate: 0.0008},
			{ID: "fatigue", Rate: 0.0004},
			{ID: "fear", Decay: 0.05, WaryLevel: 0.5, FleeLevel: 1.0},
			{ID: "thermal"},
		},
		AppTemp: mustParseNum("temperature - wind.mag * 6 - moisture * 3"),
		Speed:   mustParseNum("Agility * 1.2 + fear * 0.8 - fatigue * 0.4 - thermal * 0.6"),
		Diet:    []core.Tag{"forage"}, IsPredator: false,
		SmellRadius: 10.0, SightRadius: 14.0, FovArc: 1.05,
		TerrainCost: map[core.Tag]float64{"river": 1.8, "mountain": 1.6},
		Impassable:  []core.Tag{"sea"},
		SteerChannel: map[actions.ActionID]core.Tag{
			"Graze": fauna.TagSteerFood, "Wary": fauna.TagWaryPred,
			"Flee": fauna.TagFleePred, "Rest": fauna.TagNoLoco,
		},
	}
}

func wolfRule() fauna.SpeciesRule {
	return fauna.SpeciesRule{
		Utilities: map[actions.ActionID]*expr.Program{
			"Hunt":   mustParseNum("hunger * (0.3 + scent.prey)"),
			"Rest":   mustParseNum("fatigue * 0.5 + thermal * 0.3 - hunger * 0.6"),
			"MoveTo": mustParseNum("0.15"),
		},
		Drives: []fauna.DriveRule{
			{ID: "hunger", Rate: 0.0006},
			{ID: "fatigue", Rate: 0.0005},
			{ID: "thermal"},
		},
		AppTemp: mustParseNum("temperature - wind.mag * 5 - moisture * 2"),
		Speed:   mustParseNum("Agility * 1.5 - fatigue * 0.5 - thermal * 0.5"),
		Diet:    []core.Tag{"game"}, IsPredator: true,
		SmellRadius: 12.0, SightRadius: 20.0, FovArc: 0.79,
		TerrainCost: map[core.Tag]float64{"river": 1.4, "mountain": 1.2},
		Impassable:  []core.Tag{"sea"},
		SteerChannel: map[actions.ActionID]core.Tag{
			"Hunt": fauna.TagSteerPrey, "Rest": fauna.TagNoLoco,
		},
	}
}

func rabbitRule() fauna.SpeciesRule {
	return fauna.SpeciesRule{
		Utilities: map[actions.ActionID]*expr.Program{
			"Graze":  mustParseNum("hunger * (0.3 + scent.food) - fear * 1.8"),
			"Wary":   mustParseNum("fear * 1.1"),
			"Flee":   mustParseNum("(fear - 0.5) * 6"),
			"Rest":   mustParseNum("fatigue * 0.4 + thermal * 0.3 - hunger * 0.5 - fear * 2.5"),
			"MoveTo": mustParseNum("0.1"),
		},
		Drives: []fauna.DriveRule{
			{ID: "hunger", Rate: 0.0012},
			{ID: "fatigue", Rate: 0.0006},
			{ID: "fear", Decay: 0.04, WaryLevel: 0.6, FleeLevel: 1.0},
			{ID: "thermal"},
		},
		AppTemp: mustParseNum("temperature - wind.mag * 7 - moisture * 3"),
		Speed:   mustParseNum("Agility * 1.6 + fear * 1.0 - fatigue * 0.4 - thermal * 0.7"),
		Diet:    []core.Tag{"forage"}, IsPredator: false,
		SmellRadius: 8.0, SightRadius: 16.0, FovArc: 1.4,
		TerrainCost: map[core.Tag]float64{"river": 2.5, "mountain": 2.0},
		Impassable:  []core.Tag{"sea"},
		SteerChannel: map[actions.ActionID]core.Tag{
			"Graze": fauna.TagSteerFood, "Wary": fauna.TagWaryPred,
			"Flee": fauna.TagFleePred, "Rest": fauna.TagNoLoco,
		},
	}
}

func goatRule() fauna.SpeciesRule {
	return fauna.SpeciesRule{
		Utilities: map[actions.ActionID]*expr.Program{
			"Graze":  mustParseNum("hunger * (0.3 + scent.food) - fear * 1.2"),
			"Wary":   mustParseNum("fear * 0.9"),
			"Flee":   mustParseNum("(fear - 0.65) * 5"),
			"Rest":   mustParseNum("fatigue * 0.4 + thermal * 0.3 - hunger * 0.5 - fear * 1.8"),
			"MoveTo": mustParseNum("0.1"),
		},
		Drives: []fauna.DriveRule{
			{ID: "hunger", Rate: 0.0007},
			{ID: "fatigue", Rate: 0.0004},
			{ID: "fear", Decay: 0.06, WaryLevel: 0.4, FleeLevel: 0.9},
			{ID: "thermal"},
		},
		AppTemp: mustParseNum("temperature - wind.mag * 4 - moisture * 2"),
		Speed:   mustParseNum("Agility * 1.1 - fatigue * 0.3 - thermal * 0.3"),
		Diet:    []core.Tag{"forage"}, IsPredator: false,
		SmellRadius: 9.0, SightRadius: 13.0, FovArc: 1.0,
		TerrainCost: map[core.Tag]float64{"mountain": 0.6, "river": 2.0, "sand": 1.2},
		Impassable:  []core.Tag{"sea"},
		SteerChannel: map[actions.ActionID]core.Tag{
			"Graze": fauna.TagSteerFood, "Wary": fauna.TagWaryPred,
			"Flee": fauna.TagFleePred, "Rest": fauna.TagNoLoco,
		},
	}
}

func bearRule() fauna.SpeciesRule {
	return fauna.SpeciesRule{
		Utilities: map[actions.ActionID]*expr.Program{
			"Hunt":   mustParseNum("hunger * (0.2 + scent.prey)"),
			"Graze":  mustParseNum("hunger * (0.2 + scent.food)"),
			"Rest":   mustParseNum("fatigue * 0.5 + thermal * 0.3 - hunger * 0.6"),
			"MoveTo": mustParseNum("0.15"),
		},
		Drives: []fauna.DriveRule{
			{ID: "hunger", Rate: 0.0005},
			{ID: "fatigue", Rate: 0.0004},
			{ID: "thermal"},
		},
		AppTemp: mustParseNum("temperature - wind.mag * 3 - moisture * 1"),
		Speed:   mustParseNum("Agility * 1.1 - fatigue * 0.4 - thermal * 0.25"),
		Diet:    []core.Tag{"game", "forage"}, IsPredator: true,
		SmellRadius: 14.0, SightRadius: 16.0, FovArc: 0.9,
		TerrainCost: map[core.Tag]float64{"mountain": 0.7, "river": 1.5},
		Impassable:  []core.Tag{"sea"},
		SteerChannel: map[actions.ActionID]core.Tag{
			"Hunt": fauna.TagSteerPrey, "Graze": fauna.TagSteerFood,
			"Rest": fauna.TagNoLoco,
		},
	}
}

func fishRule() fauna.SpeciesRule {
	return fauna.SpeciesRule{
		Utilities: map[actions.ActionID]*expr.Program{
			"Wary":   mustParseNum("fear * 0.8"),
			"Flee":   mustParseNum("(fear - 0.6) * 5"),
			"Rest":   mustParseNum("fatigue * 0.5 + thermal * 0.3 - fear * 2"),
			"MoveTo": mustParseNum("0.2"),
		},
		Drives: []fauna.DriveRule{
			{ID: "fatigue", Rate: 0.0004},
			{ID: "fear", Decay: 0.06, WaryLevel: 0.5, FleeLevel: 1.0},
			{ID: "thermal"},
		},
		AppTemp:     mustParseNum("temperature - moisture * 1"),
		Speed:       mustParseNum("Agility * 1.3 - fatigue * 0.3 - thermal * 0.5"),
		IsPredator:  false,
		SmellRadius: 7.0, SightRadius: 10.0, FovArc: 1.2,
		TerrainCost: map[core.Tag]float64{"river": 0.5, "sea": 0.5},
		Impassable:  []core.Tag{"soil", "sand", "mountain", "bare_rock"},
		SteerChannel: map[actions.ActionID]core.Tag{
			"Wary": fauna.TagWaryPred, "Flee": fauna.TagFleePred,
			"Rest": fauna.TagNoLoco,
		},
	}
}
