import { describe, it, expect } from 'vitest'
import type { CameraFocus } from '../gl/worldGL'
import { minimapSize, cameraCone, MM_MAX_DIM, MM_MIN_DIM } from './minimapGeom'

const focus = (o: Partial<CameraFocus>): CameraFocus => ({ x: 0, z: 0, yaw: 0, dist: 100, fitDist: 100, ...o })

describe('minimapSize', () => {
  it('a square world is a square minimap at MM_MAX_DIM', () => {
    expect(minimapSize(500, 500)).toEqual({ w: MM_MAX_DIM, h: MM_MAX_DIM })
  })

  it('a wide world keeps MM_MAX_DIM width, shrinks height by aspect', () => {
    expect(minimapSize(400, 200)).toEqual({ w: MM_MAX_DIM, h: Math.round(MM_MAX_DIM / 2) })
  })

  it('a tall world keeps MM_MAX_DIM height, shrinks width by aspect', () => {
    expect(minimapSize(200, 400)).toEqual({ w: Math.round(MM_MAX_DIM * 0.5), h: MM_MAX_DIM })
  })

  it('floors the short axis at MM_MIN_DIM for extreme aspect ratios', () => {
    expect(minimapSize(4000, 100).h).toBe(MM_MIN_DIM)
    expect(minimapSize(100, 4000).w).toBe(MM_MIN_DIM)
  })

  it('a degenerate (zero) world falls back to a square', () => {
    expect(minimapSize(0, 0)).toEqual({ w: MM_MAX_DIM, h: MM_MAX_DIM })
  })
})

describe('cameraCone direction (screen space; +x right, +y down)', () => {
  const dir = (yaw: number) => {
    const { angle } = cameraCone(focus({ yaw }), 190, 190)
    return { dx: Math.cos(angle), dy: Math.sin(angle) }
  }

  it('yaw 0 (camera south, looking north) opens UP the screen', () => {
    const { dx, dy } = dir(0)
    expect(dx).toBeCloseTo(0, 6)
    expect(dy).toBeCloseTo(-1, 6) // up
  })

  it('yaw +π/2 opens LEFT', () => {
    const { dx, dy } = dir(Math.PI / 2)
    expect(dx).toBeCloseTo(-1, 6)
    expect(dy).toBeCloseTo(0, 6)
  })

  it('yaw π opens DOWN', () => {
    const { dx, dy } = dir(Math.PI)
    expect(dx).toBeCloseTo(0, 6)
    expect(dy).toBeCloseTo(1, 6) // down
  })

  it('yaw -π/2 opens RIGHT', () => {
    const { dx, dy } = dir(-Math.PI / 2)
    expect(dx).toBeCloseTo(1, 6)
    expect(dy).toBeCloseTo(0, 6)
  })
})

describe('cameraCone radius (grows with the camera framing / zoom-out)', () => {
  const span = Math.min(190, 190) * 0.45

  it('dist == fitDist (whole-world framing) fills ~half the minimap', () => {
    expect(cameraCone(focus({ dist: 100, fitDist: 100 }), 190, 190).radius).toBeCloseTo(span, 6)
  })

  it('zoomed in (dist < fitDist) shrinks proportionally', () => {
    expect(cameraCone(focus({ dist: 50, fitDist: 100 }), 190, 190).radius).toBeCloseTo(span * 0.5, 6)
  })

  it('zoomed out past the fit is capped at the span (never overflows the box)', () => {
    expect(cameraCone(focus({ dist: 400, fitDist: 100 }), 190, 190).radius).toBeCloseTo(span, 6)
  })

  it('is floored so a very tight zoom never vanishes', () => {
    expect(cameraCone(focus({ dist: 0.01, fitDist: 100 }), 190, 190).radius).toBe(10)
  })
})
