import { describe, it, expect } from 'vitest'
import { createAtmosphere, LEGACY } from './atmosphere'
import { PRECIP_SNOW_BELOW_C } from '../assets/manifest'
import type { ClimateState } from '../types'

// Atmosphere driver contract (gl/SPEC.md §Atmosphere): eased state initializes to the FIRST
// climate frame (no pop), then eases toward later targets; the precip FORM gate (CS1) routes
// the one eased density to rain OR snow by temperature; snowCover (CS2b) is the eased ground
// snowpack driving the CS5b tile wash. createAtmosphere is plain TS (no GL) — testable here.

const climate = (over: Partial<ClimateState> = {}): ClimateState => ({
  temperature: 15, apparentTemp: null, moisture: 0.5, raining: false, snowCover: 0,
  windDir: 0, windMag: 0.2, hourOfDay: 12, minuteOfDay: 720, dayNight: 'day',
  dayOfRun: 0, yearFraction: 0.5, ...over,
})

describe('atmosphere snowCover (CS5b wash driver)', () => {
  it('climate null → LEGACY with snowCover 0', () => {
    const atmo = createAtmosphere()
    expect(atmo.update(null, 0)).toBe(LEGACY)
    expect(LEGACY.snowCover).toBe(0)
  })

  it('first frame initializes snowCover exactly (no ease-in pop)', () => {
    const atmo = createAtmosphere()
    const a = atmo.update(climate({ snowCover: 0.7 }), 1000)
    expect(a.snowCover).toBeCloseTo(0.7, 12)
  })

  it('eases toward a new target without overshooting, and converges', () => {
    const atmo = createAtmosphere()
    atmo.update(climate({ snowCover: 0.8 }), 0)
    const mid = atmo.update(climate({ snowCover: 0 }), 250).snowCover
    expect(mid).toBeGreaterThan(0)
    expect(mid).toBeLessThan(0.8)
    let last = mid
    for (let t = 500; t <= 30000; t += 250) last = atmo.update(climate({ snowCover: 0 }), t).snowCover
    expect(last).toBeLessThan(0.01)
  })

  it('missing snowCover field (pre-snow frame) is treated as 0', () => {
    const atmo = createAtmosphere()
    const c = climate(); delete (c as Partial<ClimateState>).snowCover
    expect(atmo.update(c, 0).snowCover).toBe(0)
  })
})

describe('atmosphere precip form gate (CS1)', () => {
  it('raining below the threshold → snow carries the density, rain is 0', () => {
    const atmo = createAtmosphere()
    const a = atmo.update(climate({ raining: true, temperature: PRECIP_SNOW_BELOW_C - 1 }), 0)
    expect(a.rain).toBe(0)
    expect(a.snow).toBeGreaterThan(0.9)
  })
  it('raining above the threshold → rain carries the density, snow is 0', () => {
    const atmo = createAtmosphere()
    const a = atmo.update(climate({ raining: true, temperature: PRECIP_SNOW_BELOW_C + 1 }), 0)
    expect(a.snow).toBe(0)
    expect(a.rain).toBeGreaterThan(0.9)
  })
})
