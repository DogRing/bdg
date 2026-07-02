import { describe, it, expect } from 'vitest'
import { buildTransform, wx, wy, toWorld } from './transform'
import {
  initialCamera, cameraZoom, cameraPan, cameraFollow, cameraTick,
  ZOOM_MIN, ZOOM_MAX, type CameraState,
} from './camera'
import { hitTest } from './hitTest'
import type { AgentState, AnimalState, RenderConfig } from '../types'

const cam = (over: Partial<CameraState> = {}): CameraState =>
  ({ center: { x: 256, y: 256 }, zoom: 2, follow: null, ...over })

const agent = (id: string, x: number, y: number): AgentState =>
  ({ id, pos: { x, y }, goal: '', action: '', mood: 0.5, cluster: null, copingMode: null })

const animal = (id: string, x: number, y: number): AnimalState =>
  ({ id, pos: { x, y }, species: 'deer', action: 'graze', heading: 0, stamina: 1 })

describe('transform', () => {
  it('round-trips world↔canvas at several zooms', () => {
    for (const zoom of [ZOOM_MIN, 0.7, 2, 13.37, ZOOM_MAX]) {
      const tr = buildTransform(cam({ zoom }), 800, 600)
      for (const p of [{ x: 0, y: 0 }, { x: 256, y: 256 }, { x: -3.2, y: 511.9 }]) {
        const back = toWorld(wx(p.x, tr), wy(p.y, tr), tr)
        expect(back.x).toBeCloseTo(p.x, 9)
        expect(back.y).toBeCloseTo(p.y, 9)
      }
    }
  })

  it('camera centre lands on the viewport centre', () => {
    const tr = buildTransform(cam(), 800, 600)
    expect(wx(256, tr)).toBeCloseTo(400)
    expect(wy(256, tr)).toBeCloseTo(300)
  })
})

describe('cameraZoom', () => {
  it('keeps the world point under the cursor fixed', () => {
    const c0 = cam()
    const cursor = { x: 123, y: 456 }
    const before = toWorld(cursor.x, cursor.y, buildTransform(c0, 800, 600))
    const c1 = cameraZoom(c0, cursor, 1.5, 800, 600)
    const after = toWorld(cursor.x, cursor.y, buildTransform(c1, 800, 600))
    expect(after.x).toBeCloseTo(before.x, 9)
    expect(after.y).toBeCloseTo(before.y, 9)
    expect(c1.zoom).toBeCloseTo(3)
  })

  it('clamps at both ends and is a no-op at the clamp', () => {
    const atMax = cameraZoom(cam({ zoom: ZOOM_MAX }), { x: 0, y: 0 }, 2, 800, 600)
    expect(atMax.zoom).toBe(ZOOM_MAX)
    expect(atMax).toEqual(cam({ zoom: ZOOM_MAX })) // unchanged state, no centre drift
    const atMin = cameraZoom(cam({ zoom: ZOOM_MIN }), { x: 0, y: 0 }, 0.5, 800, 600)
    expect(atMin.zoom).toBe(ZOOM_MIN)
  })
})

describe('cameraPan / cameraFollow / cameraTick', () => {
  it('pan shifts the centre by delta/zoom and breaks follow', () => {
    const c0 = cam({ follow: { kind: 'agent', id: 'a1' } })
    const c1 = cameraPan(c0, 20, -10)
    expect(c1.center).toEqual({ x: 256 - 10, y: 256 + 5 })
    expect(c1.follow).toBeNull()
  })

  it('tick centres on the followed entity and clears follow when it is gone', () => {
    const c0 = cameraFollow(cam(), { kind: 'agent', id: 'a1' })
    const present = cameraTick(c0, [agent('a1', 40, 50)], [])
    expect(present.center).toEqual({ x: 40, y: 50 })
    expect(present.follow).toEqual({ kind: 'agent', id: 'a1' })
    const gone = cameraTick(c0, [agent('other', 1, 1)], [])
    expect(gone.follow).toBeNull()
    expect(gone.center).toEqual(c0.center) // camera stays put
  })

  it('tick follows animals from the animal pool', () => {
    const c0 = cameraFollow(cam(), { kind: 'animal', id: 'd1' })
    const c1 = cameraTick(c0, [agent('d1', 999, 999)], [animal('d1', 7, 8)])
    expect(c1.center).toEqual({ x: 7, y: 8 }) // animal pool, not the same-id agent
  })
})

describe('initialCamera', () => {
  const render: RenderConfig = { bounds: { min: { x: 0, y: 0 }, max: { x: 512, y: 512 } }, pixelsPerUnit: 2 }

  it('anchors to RenderConfig bounds when present', () => {
    const c = initialCamera(render, [], 800, 600)!
    expect(c.center).toEqual({ x: 256, y: 256 })
    expect(c.zoom).toBeCloseTo((600 / 512) * 0.95)
    expect(c.follow).toBeNull()
  })

  it('falls back to the entity bbox, and to null when nothing is known', () => {
    const c = initialCamera(null, [agent('a', 100, 100), agent('b', 300, 200)], 800, 600)!
    expect(c.center).toEqual({ x: 200, y: 150 })
    expect(c.zoom).toBeGreaterThan(0)
    expect(initialCamera(null, [], 800, 600)).toBeNull()
    expect(initialCamera(render, [], 0, 0)).toBeNull() // viewport not laid out yet
  })

  it('guards the degenerate all-at-one-point bbox', () => {
    const c = initialCamera(null, [agent('a', 50, 50)], 800, 600)!
    expect(Number.isFinite(c.zoom)).toBe(true)
    expect(c.zoom).toBeLessThanOrEqual(ZOOM_MAX)
  })
})

describe('hitTest', () => {
  const tr = buildTransform(cam({ center: { x: 0, y: 0 }, zoom: 1 }), 800, 600) // world 0,0 → 400,300

  it('selects the nearest entity within 15px; agents win ties', () => {
    const agents = [agent('a1', 0, 0)]
    const animals = [animal('d1', 0, 0)] // same spot — agent must win
    expect(hitTest(agents, animals, tr, 400, 300)).toEqual({ kind: 'agent', id: 'a1' })
    expect(hitTest([], animals, tr, 405, 300)).toEqual({ kind: 'animal', id: 'd1' })
  })

  it('returns null on empty space (outside the radius)', () => {
    expect(hitTest([agent('a1', 0, 0)], [], tr, 400 + 16, 300)).toBeNull()
    expect(hitTest([], [], tr, 400, 300)).toBeNull()
  })

  it('prefers the closer of two animals', () => {
    const animals = [animal('far', 10, 0), animal('near', 2, 0)]
    expect(hitTest([], animals, tr, 400, 300)).toEqual({ kind: 'animal', id: 'near' })
  })
})
