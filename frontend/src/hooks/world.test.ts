import { describe, it, expect } from 'vitest'
import { worldReducer, initialWorldState } from './useWorld'
import type { SimEvent, WorldState, TerrainGrid } from '../types'

const ev = (type: string, payload: Record<string, unknown>, tick = 1): SimEvent =>
  ({ schema_version: 1, tick, seq: 0, agent_id: null, type, payload })

const dispatch = (state: WorldState, e: SimEvent, atMs: number): WorldState =>
  worldReducer(state, { type: 'EVENT', payload: e, atMs })

const frame = (animals: Array<Record<string, unknown>>, flora: Array<Record<string, unknown>> = []) =>
  ev('WorldFrame', { agents: [], animals, flora_delta: flora, wind: { dir: 0, mag: 0 } })

const deer = (over: Record<string, unknown> = {}) =>
  ({ id: 'd1', pos: { x: 0, y: 0 }, species: 'deer', action: 'graze', heading: 0, ...over })

describe('WorldFrame interpolation stamps (Q3)', () => {
  it('first frame has no stamps; the second shifts pos→prevPos and stamps the window', () => {
    let s = dispatch(initialWorldState, frame([deer()]), 1000)
    expect(s.animals.get('d1')!.prevPos).toBeUndefined()

    s = dispatch(s, frame([deer({ pos: { x: 10, y: 0 }, heading: 1 })]), 1400)
    const a = s.animals.get('d1')!
    expect(a.prevPos).toEqual({ x: 0, y: 0 })
    expect(a.prevHeading).toBe(0)
    expect(a.prevFrameAtMs).toBe(1400)
    expect(a.frameAtMs).toBe(1400 + 500) // no measured gap yet → default window

    s = dispatch(s, frame([deer({ pos: { x: 20, y: 0 } })]), 1800)
    const b = s.animals.get('d1')!
    expect(b.prevPos).toEqual({ x: 10, y: 0 })
    expect(b.frameAtMs).toBe(1800 + 400) // measured gap 1800-1400
  })
})

describe('fx queue (Q4)', () => {
  it('attack-pose entry enqueues ONE attack fx (not re-armed while it stays attack)', () => {
    let s = dispatch(initialWorldState, frame([deer({ id: 'w1', species: 'wolf', action: 'wander' })]), 0)
    s = dispatch(s, frame([deer({ id: 'w1', species: 'wolf', action: 'hunt_deer' })]), 500)
    s = dispatch(s, frame([deer({ id: 'w1', species: 'wolf', action: 'hunt_deer' })]), 600)
    expect(s.fx.filter(f => f.kind === 'attack' && f.id === 'w1')).toHaveLength(1)
  })

  it('AnimalDied removes the animal and enqueues a death fx with its last pos/heading', () => {
    let s = dispatch(initialWorldState, frame([deer({ pos: { x: 7, y: 8 }, heading: 2 })]), 0)
    s = dispatch(s, ev('AnimalDied', { object_id: 'd1', cause: 'hunted' }), 100)
    expect(s.animals.has('d1')).toBe(false)
    const death = s.fx.find(f => f.kind === 'death')!
    expect(death).toMatchObject({ id: 'd1', species: 'deer', heading: 2, pos: { x: 7, y: 8 }, at: 100 })
  })

  it('AnimalBorn / PlantSpawned add the entity + a spawn fx; PlantDied removes + death fx', () => {
    let s = dispatch(initialWorldState,
      ev('AnimalBorn', { object_id: 'd2', species: 'deer', pos: { x: 1, y: 2 } }), 0)
    expect(s.animals.get('d2')).toMatchObject({ species: 'deer', pos: { x: 1, y: 2 } })
    s = dispatch(s, ev('PlantSpawned', { object_id: 'p1', species: 'tree', pos: { x: 3, y: 4 } }), 10)
    expect(s.flora.find(f => f.id === 'p1')).toBeTruthy()
    expect(s.fx.filter(f => f.kind === 'spawn')).toHaveLength(2)
    s = dispatch(s, ev('PlantDied', { object_id: 'p1' }), 20)
    expect(s.flora.find(f => f.id === 'p1')).toBeUndefined()
    expect(s.fx.find(f => f.kind === 'death' && f.id === 'p1')).toMatchObject({ species: 'tree' })
  })

  it('a flora stage increase enqueues a grow fx', () => {
    let s = dispatch(initialWorldState,
      ev('PlantSpawned', { object_id: 'p1', species: 'tree', pos: { x: 0, y: 0 }, stage: 1 }), 0)
    s = dispatch(s, frame([], [{ id: 'p1', pos: { x: 0, y: 0 }, stage: 2 }]), 100)
    expect(s.fx.find(f => f.kind === 'grow')).toMatchObject({ id: 'p1', at: 100 })
    // same stage again → no second grow
    s = dispatch(s, frame([], [{ id: 'p1', pos: { x: 0, y: 0 }, stage: 2 }]), 200)
    expect(s.fx.filter(f => f.kind === 'grow')).toHaveLength(1)
  })

  it('terrain deltas mutate cells + wear and replace the grid object; deltas without a base grid drop', () => {
    const grid: TerrainGrid = { cellSize: 8, w: 2, h: 2, terrain: ['plain', 'plain', 'plain', 'plain'] }
    let s = worldReducer(initialWorldState, { type: 'TERRAIN_LOADED', payload: grid })
    expect(s.terrain).toBe(grid)

    s = dispatch(s, ev('WorldFrame', {
      agents: [], animals: [], flora_delta: [], wind: { dir: 0, mag: 0 },
      terrain_delta: [
        { cell: { x: 1, y: 0 }, terrain: 'water' },
        { cell: { x: 0, y: 1 }, wear: 0.5 },
        { cell: { x: 9, y: 9 }, terrain: 'water' }, // out of bounds → ignored
      ],
    }), 100)
    expect(s.terrain).not.toBe(grid)             // identity replaced → raster rebuild
    expect(s.terrain!.terrain[1]).toBe('water')
    expect(s.terrain!.wear![2]).toBeCloseTo(0.5)
    expect(grid.terrain[1]).toBe('plain')        // base grid untouched (no mutation)

    const dropped = dispatch(initialWorldState, ev('WorldFrame', {
      agents: [], animals: [], flora_delta: [], wind: { dir: 0, mag: 0 },
      terrain_delta: [{ cell: { x: 0, y: 0 }, terrain: 'water' }],
    }), 0)
    expect(dropped.terrain).toBeNull()
  })

  it('expired fx are pruned on a later reduce', () => {
    let s = dispatch(initialWorldState, ev('AnimalDied', { object_id: 'x', species: 'deer', pos: { x: 0, y: 0 } }), 0)
    expect(s.fx).toHaveLength(1)
    s = dispatch(s, frame([deer()]), 1400)   // death still inside 1500ms
    expect(s.fx).toHaveLength(1)
    s = dispatch(s, frame([deer()]), 1600)   // past it → pruned
    expect(s.fx).toHaveLength(0)
  })
})

describe('RenderConfig derivation from terrain (camera anchor)', () => {
  it('TERRAIN_LOADED derives render.bounds from the grid geometry', () => {
    const grid: TerrainGrid = { cellSize: 12, w: 16, h: 16, terrain: Array(256).fill('soil') }
    const s = worldReducer(initialWorldState, { type: 'TERRAIN_LOADED', payload: grid })
    expect(s.render).not.toBeNull()
    expect(s.render!.bounds.min).toEqual({ x: 0, y: 0 })
    expect(s.render!.bounds.max).toEqual({ x: 192, y: 192 })
  })

  it('TERRAIN_LOADED never clobbers an already-present RenderConfig', () => {
    const grid: TerrainGrid = { cellSize: 12, w: 16, h: 16, terrain: Array(256).fill('soil') }
    const preset = { bounds: { min: { x: -5, y: -5 }, max: { x: 5, y: 5 } }, pixelsPerUnit: 40 }
    const s0: WorldState = { ...initialWorldState, render: preset }
    const s = worldReducer(s0, { type: 'TERRAIN_LOADED', payload: grid })
    expect(s.render).toBe(preset)
  })
})
