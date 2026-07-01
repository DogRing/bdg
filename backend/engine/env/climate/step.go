package climate

import (
	"math"

	"github.com/dogring/bdg/engine/kernel/rng"
)

// Step advances the whole climate field ONE climate step (= 1 game-hour; cadence is world's).
//
// Pure function of (prev, f, rules, rng): does NOT mutate prev, Rules, or Forcing.
// Returns the next State and the terrain Transitions that fired (in sorted GridCell order, D12).
//
// RNG draw order within Step (FIXED — D12 byte-stable):
//  1. Rain process (1a): trigger float (always when not raining), then duration int (on rain start).
//  2. Wind random-walk (CA2): NormFloat64 for Dir, then NormFloat64 for Mag.
//
// Temperature (1b/CA1, °C — NOT clamped):
//
//	T = AnnualMid + AnnualAmp·sin(2π·YearFraction+AnnualPhase) + dailyDelta(HourOfDay) − TempRainDrop·raining
//
// Moisture ∈ [0,1] (clamped): +MoistureRainRate while raining;
// −(EvapBaseRate + EvapTempScale·max(0,T)) per dry game-hour.
func Step(prev *State, f Forcing, rules *Rules, r *rng.RNG) (*State, []Transition) {
	cfg := prev.cfg

	// Deep-copy the grid so prev is never mutated.
	cells := make([][]CellState, cfg.GridRows)
	for y := range prev.cells {
		cells[y] = make([]CellState, cfg.GridCols)
		copy(cells[y], prev.cells[y])
	}
	next := &State{
		cells: cells,
		rain:  prev.rain,
		wind:  prev.wind,
		cfg:   cfg,
	}

	// ── 1. Rain process ───────────────────────────────────────────────────────
	// Draw order: trigger float (always when not raining), then duration int (if rain starts).
	rain := &next.rain
	if rain.Raining {
		// Check whether rain ends this step (no RNG draw needed — determined by stored RainEndsAtHour).
		if f.AbsHour >= rain.RainEndsAtHour {
			rain.Raining = false
			rain.PRain = 0
			rain.HoursSinceRain = 0
		}
	} else {
		// Accumulate probability.
		rain.HoursSinceRain++
		rain.PRain = math.Min(rain.PRain+cfg.RainProbPerHour, 1.0)

		// Draw trigger (always consumed when not raining, even if forced — D12 byte-stable order).
		trigger := r.Float64()
		forced := rain.HoursSinceRain >= cfg.RainHardCapHours
		if trigger < rain.PRain || forced {
			// Rain starts: draw duration Uniform[RainDurMin, RainDurMax] (inclusive).
			durRange := int(cfg.RainDurMaxHours - cfg.RainDurMinHours + 1)
			dur := int64(r.Intn(durRange)) + cfg.RainDurMinHours
			rain.Raining = true
			rain.PRain = 0
			rain.HoursSinceRain = 0
			rain.RainEndsAtHour = f.AbsHour + dur
		}
	}

	// ── 2. Wind random-walk (CA2) ─────────────────────────────────────────────
	// Dir: mean-reverting walk, Gaussian step scale = WindDirDrift, reversion toward WindPrevailingDir.
	// Wrap result to [0, 2π).
	//
	// Documented choice: simple linear mean-reversion (not shortest-arc) + Gaussian noise.
	// This may have mild artifacts when Dir and WindPrevailingDir straddle the 0/2π boundary,
	// but is deterministic and consistent. See SPEC Notes on the random-walk design.
	newDir := prev.wind.Dir*(1-cfg.WindDirReversion) + cfg.WindPrevailingDir*cfg.WindDirReversion
	newDir += r.NormFloat64() * cfg.WindDirDrift
	newDir = math.Mod(newDir, 2*math.Pi)
	if newDir < 0 {
		newDir += 2 * math.Pi
	}

	// Mag: mean + Gaussian noise, clamped [0,1].
	newMag := cfg.WindMagMean + r.NormFloat64()*cfg.WindMagNoise
	if newMag < 0 {
		newMag = 0
	} else if newMag > 1 {
		newMag = 1
	}
	next.wind = Wind{Dir: newDir, Mag: newMag}

	// ── 3. Temperature (world-uniform per step) and Moisture (per cell) ───────
	// Temperature formula (CA1/CA3, NOT clamped):
	//   T = AnnualMid + AnnualAmp·sin(2π·YearFraction+AnnualPhase) + dailyDelta(HourOfDay) − TempRainDrop·raining
	annualT := cfg.AnnualMid + cfg.AnnualAmp*math.Sin(2*math.Pi*f.YearFraction+cfg.AnnualPhase)
	daily := dailyDelta(f.HourOfDay, cfg.TempNightLow, cfg.TempDayPeak)
	isRaining := next.rain.Raining
	rainDrop := 0.0
	if isRaining {
		rainDrop = cfg.TempRainDrop
	}
	temperature := annualT + daily - rainDrop

	// Moisture is clamped to [0, 1]. Evaporation: EvapBaseRate + EvapTempScale·max(0,T) per dry hour.
	// The temperature-scale term is floored at 0 (CA3): a sub-zero cell never adds moisture via evap.
	evapRate := 0.0
	if !isRaining {
		evapRate = cfg.EvapBaseRate + cfg.EvapTempScale*math.Max(0, temperature)
		if evapRate < 0 {
			evapRate = 0 // total evap is floored at 0 per SPEC
		}
	}

	for y := 0; y < cfg.GridRows; y++ {
		for x := 0; x < cfg.GridCols; x++ {
			cell := &next.cells[y][x]
			cell.Temperature = temperature
			if isRaining {
				m := cell.Moisture + cfg.MoistureRainRate
				if m > 1 {
					m = 1
				}
				cell.Moisture = m
			} else {
				m := cell.Moisture - evapRate
				if m < 0 {
					m = 0
				}
				cell.Moisture = m
			}
		}
	}

	// ── 4. Transition evaluation (sorted GridCell order: Y-major then X, D12) ─
	// Transitions fire AFTER moisture/temperature updates (rules see the new state).
	// Each cell is evaluated independently; Terrain update is applied in-order.
	var transitions []Transition
	for y := 0; y < cfg.GridRows; y++ {
		for x := 0; x < cfg.GridCols; x++ {
			cell := &next.cells[y][x]
			if to, fired := rules.Eval(cell.Terrain, *cell); fired {
				transitions = append(transitions, Transition{
					Cell: GridCell{X: x, Y: y},
					From: cell.Terrain,
					To:   to,
				})
				cell.Terrain = to
			}
		}
	}

	return next, transitions
}

// dailyDelta returns the daily temperature delta (°C) at the given hour-of-day.
//
// Documented functional-form choice (SPEC specifies "interpolating TempNightLow..TempDayPeak
// over the day" but not the exact curve shape):
//
//	dailyDelta(h) = mid + amp · cos(2π · (h − 14) / 24)
//	  where mid = (TempNightLow + TempDayPeak) / 2
//	        amp = (TempDayPeak  − TempNightLow) / 2
//
// This gives TempDayPeak at hour 14 (2 pm) and TempNightLow at hour 2 (2 am),
// matching the standard meteorological diurnal temperature cycle (peak in the afternoon,
// trough in the pre-dawn hours). Both TempDayPeak and TempNightLow are signed °C deltas
// around the annual midline: TempDayPeak > 0, TempNightLow < 0.
func dailyDelta(hourOfDay int, nightLow, dayPeak float64) float64 {
	mid := (nightLow + dayPeak) / 2
	amp := (dayPeak - nightLow) / 2
	// peak at hour 14 (14 - 14 = 0 → cos(0) = 1 → mid + amp = dayPeak)
	// trough at hour 2  (2  - 14 = -12 → cos(-π) = -1 → mid - amp = nightLow)
	return mid + amp*math.Cos(2*math.Pi*float64(hourOfDay-14)/24)
}
