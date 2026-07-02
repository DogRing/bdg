import { describe, it, expect } from 'vitest'
import { displayPos, displayHeading, isRunning, fxProgress, RUN_SPEED_THRESHOLD } from './animator'
import { FX_DEFS } from '../assets/manifest'
import type { FxInstance } from '../types'

const deg = (d: number) => (d * Math.PI) / 180

// The render-SPEC AC fixture: prevPos=(0,0)@1000ms, pos=(10,0)@2000ms.
const tweening = {
  pos: { x: 10, y: 0 }, heading: 0,
  prevPos: { x: 0, y: 0 }, prevHeading: 0,
  prevFrameAtMs: 1000, frameAtMs: 2000,
}

describe('displayPos (adaptive lerp, Q3)', () => {
  it('lerps inside the window and clamps past it (no overshoot)', () => {
    expect(displayPos(tweening, 1500)).toEqual({ x: 5, y: 0 })
    expect(displayPos(tweening, 2500)).toEqual({ x: 10, y: 0 })
    expect(displayPos(tweening, 500)).toEqual({ x: 0, y: 0 }) // clamped below too
  })

  it('falls back to the raw pos without stamps (fresh entity / agents)', () => {
    expect(displayPos({ pos: { x: 3, y: 4 } }, 999)).toEqual({ x: 3, y: 4 })
  })
})

describe('displayHeading (shortest arc)', () => {
  it('350°→10° passes through 0°, never the long way', () => {
    const e = { ...tweening, heading: deg(10), prevHeading: deg(350) }
    const mid = displayHeading(e, 1500)
    // midpoint of the short +20° arc from 350°: 360° (≡ 0°)
    expect(mid).toBeCloseTo(deg(360), 9)
    expect(displayHeading(e, 2500)).toBeCloseTo(deg(370), 9) // ≡ 10°, clamped
  })

  it('takes the negative arc when that is shorter', () => {
    const e = { ...tweening, heading: deg(350), prevHeading: deg(10) }
    expect(displayHeading(e, 1500)).toBeCloseTo(deg(0), 9)
  })
})

describe('isRunning (walk↔run refinement)', () => {
  it('reads displacement rate against the threshold', () => {
    // 10 units over 1s = 10 u/s > threshold
    expect(isRunning(tweening)).toBe(true)
    const slow = { ...tweening, pos: { x: RUN_SPEED_THRESHOLD * 0.5, y: 0 } } // 1.5 u/s
    expect(isRunning(slow)).toBe(false)
    expect(isRunning({ pos: { x: 0, y: 0 } })).toBe(false) // no stamps
  })
})

describe('fxProgress', () => {
  const fx: FxInstance = { kind: 'death', at: 1000, pos: { x: 0, y: 0 }, id: 'd1' }

  it('sweeps [0,1] over the duration and expires to null', () => {
    expect(fxProgress(fx, FX_DEFS.death, 1000)).toBe(0)
    expect(fxProgress(fx, FX_DEFS.death, 1750)).toBeCloseTo(0.5)
    expect(fxProgress(fx, FX_DEFS.death, 2500)).toBe(1)
    expect(fxProgress(fx, FX_DEFS.death, 2501)).toBeNull()
    expect(fxProgress(fx, FX_DEFS.death, 900)).toBe(0) // clock before at → clamped
  })
})
