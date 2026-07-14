package climate_test

import (
	"testing"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

func iceCfg(temperature float64) climate.Config {
	cfg := testCfg()
	cfg.GridCols = 1
	cfg.GridRows = 1
	cfg.RainProbPerHour = 0
	cfg.AnnualMid = temperature
	cfg.AnnualAmp = 0
	cfg.TempDayPeak = 0
	cfg.TempNightLow = 0
	cfg.TempRainDrop = 0
	cfg.IceType = "ice"
	return cfg
}

func iceRules(t *testing.T) *climate.Rules {
	t.Helper()
	return climate.NewRules([]climate.TransitionRule{
		{From: "lake", When: mustParseBool(t, "temperature < 0"), To: "ice"},
		{From: "river", When: mustParseBool(t, "temperature < 0"), To: "ice"},
		{From: "ice", When: mustParseBool(t, "temperature > 2"), To: climate.OriginTerrain},
	})
}

func stepAt(t *testing.T, s *climate.State, rules *climate.Rules, temperature float64, seed int64) (*climate.State, []climate.Transition) {
	t.Helper()
	// Temperature is Config-derived, so restore the dynamic cells into a base carrying the
	// desired fixed-temperature configuration before taking the next step.
	base := climate.New(iceCfg(temperature), fixedTerrainAt("unused"))
	s = climate.Restore(base, s.Cells(), s.Rain(), s.Wind(), s.SnowCover())
	return climate.Step(s, climate.Forcing{AbsHour: 1, HourOfDay: 12}, rules, rng.New(seed))
}

func TestIceFreezeCapturesOrigin(t *testing.T) {
	rules := iceRules(t)
	for _, origin := range []core.Tag{"lake", "river"} {
		t.Run(string(origin), func(t *testing.T) {
			s := climate.New(iceCfg(-1), fixedTerrainAt(origin))
			next, transitions := climate.Step(s, climate.Forcing{HourOfDay: 12}, rules, rng.New(1))
			if len(transitions) != 1 || transitions[0].From != origin || transitions[0].To != "ice" {
				t.Fatalf("freeze transitions = %+v, want %s->ice", transitions, origin)
			}
			cell := next.Cells()[0].State
			if cell.Terrain != "ice" || cell.FrozenFrom != origin {
				t.Fatalf("frozen cell = %+v, want Terrain=ice FrozenFrom=%s", cell, origin)
			}
		})
	}
}

func TestIceThawRestoresExactOriginAndClearsIt(t *testing.T) {
	rules := iceRules(t)
	for _, origin := range []core.Tag{"lake", "river"} {
		t.Run(string(origin), func(t *testing.T) {
			s := climate.New(iceCfg(-1), fixedTerrainAt(origin))
			s, _ = climate.Step(s, climate.Forcing{HourOfDay: 12}, rules, rng.New(1))
			next, transitions := stepAt(t, s, rules, 3, 2)
			if len(transitions) != 1 || transitions[0].From != "ice" || transitions[0].To != origin {
				t.Fatalf("thaw transitions = %+v, want ice->%s", transitions, origin)
			}
			cell := next.Cells()[0].State
			if cell.Terrain != origin || cell.FrozenFrom != "" {
				t.Fatalf("thawed cell = %+v, want Terrain=%s FrozenFrom empty", cell, origin)
			}
		})
	}
}

func TestIceOriginlessCellDoesNotThaw(t *testing.T) {
	s := climate.New(iceCfg(3), fixedTerrainAt("ice"))
	next, transitions := climate.Step(s, climate.Forcing{HourOfDay: 12}, iceRules(t), rng.New(1))
	if len(transitions) != 0 {
		t.Fatalf("origin-less ice emitted transitions: %+v", transitions)
	}
	if cell := next.Cells()[0].State; cell.Terrain != "ice" || cell.FrozenFrom != "" {
		t.Fatalf("origin-less ice changed: %+v", cell)
	}
}

func TestIceHysteresisGapDoesNotTransition(t *testing.T) {
	rules := iceRules(t)
	for _, terrain := range []core.Tag{"lake", "ice"} {
		s := climate.New(iceCfg(1), fixedTerrainAt(terrain))
		next, transitions := climate.Step(s, climate.Forcing{HourOfDay: 12}, rules, rng.New(1))
		if len(transitions) != 0 || next.Cells()[0].State.Terrain != terrain {
			t.Fatalf("terrain %s transitioned inside hysteresis gap: %+v, state=%+v", terrain, transitions, next.Cells()[0].State)
		}
	}
}

func TestIceResumePreservesOrigin(t *testing.T) {
	rules := iceRules(t)
	s := climate.New(iceCfg(-1), fixedTerrainAt("river"))
	s, _ = climate.Step(s, climate.Forcing{HourOfDay: 12}, rules, rng.New(1))
	base := climate.New(iceCfg(3), fixedTerrainAt("unused"))
	resumed := climate.Restore(base, s.Cells(), s.Rain(), s.Wind(), s.SnowCover())
	next, transitions := climate.Step(resumed, climate.Forcing{AbsHour: 1, HourOfDay: 12}, rules, rng.New(2))
	if len(transitions) != 1 || transitions[0].To != "river" || next.Cells()[0].State.Terrain != "river" {
		t.Fatalf("resumed thaw lost origin: transitions=%+v state=%+v", transitions, next.Cells()[0].State)
	}
}

func TestIceSentinelNeverStoredOrEmitted(t *testing.T) {
	rules := iceRules(t)
	s := climate.New(iceCfg(-1), fixedTerrainAt("lake"))
	for i, temperature := range []float64{-1, 1, 3} {
		var transitions []climate.Transition
		s, transitions = stepAt(t, s, rules, temperature, int64(i+1))
		for _, tr := range transitions {
			if tr.From == climate.OriginTerrain || tr.To == climate.OriginTerrain {
				t.Fatalf("sentinel emitted in transition: %+v", tr)
			}
		}
		for _, gcs := range s.Cells() {
			if gcs.State.Terrain == climate.OriginTerrain || gcs.State.FrozenFrom == climate.OriginTerrain {
				t.Fatalf("sentinel stored in cell: %+v", gcs)
			}
		}
	}
}
