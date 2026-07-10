package fauna_test

import (
	"math"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
)

// TestThermalStressComfortBand covers FA5 (docs/plans/scenarios-world.md): the
// thermal drive is a SYMMETRIC comfort-band stress derived from apparent_temp
// (F40) — clamp01(|apparent_temp − comfort_temp| / thermal_band). Both a cold
// night AND a hot noon push apparent_temp away from comfort → thermal ↑ → the
// thermal-scoring Rest/shelter action outbids grazing. apparent_temp itself
// stays °C (render, CA3); the band is what normalizes it into the [0,1] drive.
//
// Rules: Graze scores a flat 0.3; Rest scores thermal·0.5, so Rest wins iff
// thermal > 0.6. AppTemp = "temperature" isolates the band (apparent_temp == °C).
func TestThermalStressComfortBand(t *testing.T) {
	const comfort, band = 12.0, 20.0
	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities: map[actions.ActionID]*expr.Program{
				actGraze: mustNum(t, "0.3"),           // flat forage pull
				actRest:  mustNum(t, "thermal * 0.5"), // thermal-driven shelter/rest (FA5)
			},
			Drives:      []fauna.DriveRule{{ID: "thermal"}},
			AppTemp:     mustNum(t, "temperature"), // apparent_temp = °C directly
			ComfortTemp: comfort,
			ThermalBand: band,
			Speed:       mustNum(t, "0"),
			SmellRadius: 5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{
				actGraze: fauna.TagSteerFood,
				actRest:  fauna.TagNoLoco,
			},
		},
	})

	cases := []struct {
		name  string
		tempC float64
		want  float64 // expected thermal drive after one Step (clamp01 of the band ratio)
		rest  bool    // Rest (thermal·0.5) should out-score Graze (0.3) ⇔ thermal > 0.6
	}{
		{"comfort → no stress", 12, 0.0, false},            // |12−12|/20 = 0
		{"cool day → grazes on", 20, 0.4, false},           // |20−12|/20 = 0.4 → Rest 0.20
		{"mild cool → grazes on", 2, 0.5, false},           // |2−12|/20 = 0.5 → Rest 0.25
		{"cold night → shelters", -8, 1.0, true},           // |−8−12|/20 = 1.0 → Rest 0.50
		{"hot noon → shelters (symmetric)", 32, 1.0, true}, // |32−12|/20 = 1.0 (heat, not just cold)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := herbAnimal("h1", core.Vec2{}, map[fauna.DriveID]float64{"thermal": 0})
			a.ActiveUntil = 1000 // ACTIVE → full DriveUpdate + re-arbitration this tick
			env := map[core.ObjectID]fauna.EnvSample{"h1": {Temperature: c.tempC}}
			snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, env)

			intents := fauna.Step(snap, rules, rng.New(1))
			if len(intents) != 1 {
				t.Fatalf("want 1 intent, got %d", len(intents))
			}
			if got := intents[0].Drives["thermal"]; math.Abs(got-c.want) > 1e-9 {
				t.Errorf("thermal at %.0f°C: got %.4f, want %.4f (|%.0f−%.0f|/%.0f)",
					c.tempC, got, c.want, c.tempC, comfort, band)
			}
			gotRest := intents[0].Action == actRest
			if gotRest != c.rest {
				t.Errorf("at %.0f°C: Rest-chosen=%v, want %v (thermal %.2f, Rest wins iff >0.6)",
					c.tempC, gotRest, c.rest, intents[0].Drives["thermal"])
			}
		})
	}
}

// TestThermalBandUnsetIsNeutral guards the fauna-off / no-comfort lever: a
// species with a thermal drive but no comfort_temp/thermal_band (band ≤ 0)
// feels ZERO thermal stress at any temperature — apparent_temp still emits °C
// (render), but the drive never leaves 0. This is the back-compat path for the
// synthetic test rules and any species that opts out of the thermal band.
func TestThermalBandUnsetIsNeutral(t *testing.T) {
	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities:   map[actions.ActionID]*expr.Program{actRest: mustNum(t, "0.1")},
			Drives:      []fauna.DriveRule{{ID: "thermal"}},
			AppTemp:     mustNum(t, "temperature"), // apparent_temp swings, but no band
			Speed:       mustNum(t, "0"),
			SmellRadius: 5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{actRest: fauna.TagNoLoco},
		},
	})
	for _, tempC := range []float64{-30, 0, 40} {
		a := herbAnimal("h1", core.Vec2{}, map[fauna.DriveID]float64{"thermal": 0})
		a.ActiveUntil = 1000
		env := map[core.ObjectID]fauna.EnvSample{"h1": {Temperature: tempC}}
		snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, env)
		intents := fauna.Step(snap, rules, rng.New(1))
		if got := intents[0].Drives["thermal"]; got != 0 {
			t.Errorf("thermal at %.0f°C with no band: got %.4f, want 0 (neutral lever)", tempC, got)
		}
	}
}
