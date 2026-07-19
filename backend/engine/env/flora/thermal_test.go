package flora_test

// Flora 1l — `thermal_stress`, the symmetric comfort band (docs/plans/flora.md §1l;
// docs/decisions/flora-thermal-comfort.md). Mirrors the fauna FA5 band with the same polarity:
// 0 = comfortable, 1 = maximally stressed, cold and heat symmetric.
//
// The operand is only observable through a §6 formula, so every case drives it via Suitability
// with the formula text supplied by the test (never hardcoded in flora logic).

import (
	"math"
	"testing"

	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/kernel/core"
)

// bandSpecies builds a minimal species whose suitability is the raw `thermal_stress` operand,
// so Suitability reads back the band value directly (it is already clamped to [0,1]).
func bandSpecies(t *testing.T, comfort, band float64) flora.SpeciesRule {
	t.Helper()
	return flora.SpeciesRule{
		Suitability: mustNum(t, "thermal_stress", noStats{}),
		ComfortTemp: comfort,
		ThermalBand: band,
	}
}

// stressAt evaluates the species' thermal_stress at one temperature.
func stressAt(rules *flora.Rules, sp flora.SpeciesID, temperature float64) float64 {
	return rules.Suitability(sp, flora.SiteInput{Temperature: temperature})
}

// AC (1l): optimum ⇒ 0; equal deviations cold/hot ⇒ equal stress; beyond one band ⇒ exactly 1.
func TestThermalStressIsSymmetricComfortBand(t *testing.T) {
	const (
		comfort = 14.0
		band    = 16.0
		deviate = 8.0
	)
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		"shrub": bandSpecies(t, comfort, band),
	})

	if got := stressAt(rules, "shrub", comfort); got != 0 {
		t.Errorf("stress at the optimum = %v, want 0 (comfortable)", got)
	}

	cold := stressAt(rules, "shrub", comfort-deviate)
	hot := stressAt(rules, "shrub", comfort+deviate)
	if cold != hot {
		t.Errorf("band is not symmetric: %v °C below = %v, same above = %v", deviate, cold, hot)
	}
	if want := deviate / band; math.Abs(cold-want) > 1e-12 {
		t.Errorf("stress %v °C off the optimum = %v, want %v (|Δ|/band)", deviate, cold, want)
	}
	if cold <= 0 || cold >= 1 {
		t.Errorf("a deviation inside the band should be strictly between 0 and 1, got %v", cold)
	}

	for _, temperature := range []float64{comfort - band - 1, comfort + band + 1, comfort + 100} {
		if got := stressAt(rules, "shrub", temperature); got != 1 {
			t.Errorf("stress beyond the band at %v °C = %v, want exactly 1 (clamped)", temperature, got)
		}
	}
}

// AC (1l): thermal_band ≤ 0 is the neutrality lever — 0 at every temperature.
func TestThermalStressBandOffIsNeutral(t *testing.T) {
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		"unbanded": bandSpecies(t, 14.0, 0),
		"negative": bandSpecies(t, 14.0, -5),
	})
	for _, sp := range []flora.SpeciesID{"unbanded", "negative"} {
		for _, temperature := range []float64{-40, -5, 0, 14, 30, 120} {
			if got := stressAt(rules, sp, temperature); got != 0 {
				t.Errorf("%s: stress at %v °C = %v, want 0 (band ≤ 0 is neutral)", sp, temperature, got)
			}
		}
	}
}

// AC (1l): the band is per-species state — the SAME site stresses two species differently.
func TestThermalStressIsPerSpeciesAtTheSameSite(t *testing.T) {
	const temperature = 26.0
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		"cool_shrub":   bandSpecies(t, 14.0, 16.0), // optimum well below the site
		"warm_bloom":   bandSpecies(t, 18.0, 14.0), // optimum nearer the site
		"heat_lover":   bandSpecies(t, 26.0, 10.0), // sitting exactly at its optimum
		"unresponsive": bandSpecies(t, 14.0, 0),    // no band at all
	})

	cool := stressAt(rules, "cool_shrub", temperature)
	warm := stressAt(rules, "warm_bloom", temperature)
	if cool == warm {
		t.Errorf("two species with different bands got the same stress %v at one site", cool)
	}
	if got := stressAt(rules, "heat_lover", temperature); got != 0 {
		t.Errorf("species sitting at its optimum = %v, want 0", got)
	}
	if got := stressAt(rules, "unresponsive", temperature); got != 0 {
		t.Errorf("band-less species = %v, want 0", got)
	}
}

// Regression guard for the live defect: a °C site that the OLD `(1 - temperature)` form drove
// below the death threshold must now keep the species comfortably alive (the berry_shrub wipe —
// 200 plants gone by tick 250 on the shipped rabbit_meadow fixture).
func TestComfortBandKeepsTemperateSpeciesAliveAtRealisticCelsius(t *testing.T) {
	const (
		deathThreshold = 0.20
		moisture       = 0.35
		ambient        = 12.5 // the fixture's annual midline, °C
	)
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		// The shipped berry_shrub shape, with the band term in place of the broken one.
		"berry_shrub": {
			Suitability:    mustNum(t, "moisture*0.6 + (1 - thermal_stress)*0.2 + (1 - slope)*0.2", noStats{}),
			ComfortTemp:    14.0,
			ThermalBand:    16.0,
			DeathThreshold: deathThreshold,
		},
		// The pre-fix formula, kept as the contrast case: `temperature` in °C read as [0,1].
		"broken_shrub": {
			Suitability:    mustNum(t, "moisture*0.6 + (1 - temperature)*0.2 + (1 - slope)*0.2", noStats{}),
			DeathThreshold: deathThreshold,
		},
	})

	site := flora.SiteInput{
		Moisture:     moisture,
		Temperature:  ambient,
		TerrainAttrs: map[core.Tag]float64{"slope": 0},
	}

	fixed := rules.Suitability("berry_shrub", site)
	if fixed <= deathThreshold {
		t.Errorf("banded suitability at %v °C = %v, want > death threshold %v (the species must survive)", ambient, fixed, deathThreshold)
	}
	if broken := rules.Suitability("broken_shrub", site); broken > deathThreshold {
		t.Errorf("pre-fix formula = %v; the contrast case is supposed to be below %v", broken, deathThreshold)
	}

	// The band still bites at the extremes: growth is slower in deep winter than at the optimum.
	winter := rules.Suitability("berry_shrub", flora.SiteInput{
		Moisture: moisture, Temperature: -5, TerrainAttrs: map[core.Tag]float64{"slope": 0},
	})
	optimum := rules.Suitability("berry_shrub", flora.SiteInput{
		Moisture: moisture, Temperature: 14, TerrainAttrs: map[core.Tag]float64{"slope": 0},
	})
	if !(winter < optimum) {
		t.Errorf("winter suitability %v should be below the optimum %v (seasonal modulation)", winter, optimum)
	}
	if winter <= deathThreshold {
		t.Errorf("winter suitability %v dropped to lethal; the band should slow growth, not wipe the species", winter)
	}
}
