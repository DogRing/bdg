import { describe, it, expect } from 'vitest'
import { makeTerrainRaster, drawTerrain } from './terrain'
import { drawAmbient } from './ambient'
import { buildTransform } from './transform'
import { TERRAIN_STYLE, TERRAIN_DEFAULT } from '../assets/manifest'
import type { TerrainGrid, ClimateState } from '../types'

// ── mocks ────────────────────────────────────────────────────────────────────
function ctxMock() {
  const ops: Array<Record<string, unknown>> = []
  const ctx = {
    globalAlpha: 1,
    fillStyle: '' as unknown,
    strokeStyle: '' as unknown,
    lineWidth: 1,
    imageSmoothingEnabled: true,
    save: () => ops.push({ op: 'save' }),
    restore: () => ops.push({ op: 'restore' }),
    translate: (x: number, y: number) => ops.push({ op: 'translate', x, y }),
    rotate: (a: number) => ops.push({ op: 'rotate', a }),
    beginPath: () => {},
    moveTo: () => {},
    lineTo: () => {},
    closePath: () => {},
    stroke: () => ops.push({ op: 'stroke' }),
    fill: () => ops.push({ op: 'fill', fillStyle: ctx.fillStyle }),
    fillRect: (x: number, y: number, w: number, h: number) =>
      ops.push({ op: 'fillRect', x, y, w, h, fillStyle: ctx.fillStyle }),
    arc: (x: number, y: number, r: number) => ops.push({ op: 'arc', x, y, r, fillStyle: ctx.fillStyle }),
    drawImage: (...a: unknown[]) =>
      ops.push({ op: 'drawImage', x: a[1], y: a[2], w: a[3], h: a[4] }),
    createRadialGradient: () => {
      ops.push({ op: 'radialGradient' })
      return { addColorStop: () => {} }
    },
  }
  return { ctx: ctx as unknown as CanvasRenderingContext2D, ops }
}

function fakeDoc() {
  const contexts: Array<ReturnType<typeof ctxMock>> = []
  return {
    contexts,
    createElement: () => {
      const c = ctxMock()
      contexts.push(c)
      return { width: 0, height: 0, getContext: () => c.ctx } as unknown as HTMLElement
    },
  }
}

// flat-top hex offset grid, 2×2 (row-major offset i=row·cols+col)
const grid: TerrainGrid = {
  cellSize: 8, cols: 2, rows: 2, orientation: 'flat',
  terrain: ['plain', 'water', 'forest', '???unknown'],
}

// world (0,0) → canvas (400,300); 10 px per world unit
const tr = buildTransform({ center: { x: 0, y: 0 }, zoom: 10, follow: null }, 800, 600)

describe('makeTerrainRaster', () => {
  it('fills one HEX per cell from TERRAIN_STYLE (offset order); unknown → default', () => {
    const doc = fakeDoc()
    const raster = makeTerrainRaster(grid, doc)!
    expect(raster.grid).toBe(grid)
    const fills = doc.contexts[0].ops.filter(o => o.op === 'fill')
    expect(fills).toHaveLength(4) // one hex fill per cell (not a per-pixel rect)
    expect(fills[0].fillStyle).toBe(TERRAIN_STYLE.plain)
    expect(fills[1].fillStyle).toBe(TERRAIN_STYLE.water)
    expect(fills[2].fillStyle).toBe(TERRAIN_STYLE.forest)
    expect(fills[3].fillStyle).toBe(TERRAIN_DEFAULT)
    // raster carries its world placement (drawTerrain blits by it)
    expect(raster.worldW).toBeGreaterThan(0)
    expect(raster.worldH).toBeGreaterThan(0)
  })

  it('overlays wear trails with alpha ∝ wear; no DOM ⇒ null', () => {
    const doc = fakeDoc()
    const withWear: TerrainGrid = { ...grid, wear: Float32Array.from([0, 0.5, 0, 0]) }
    makeTerrainRaster(withWear, doc)
    const fills = doc.contexts[0].ops.filter(o => o.op === 'fill')
    expect(fills).toHaveLength(5) // 4 hex cells + 1 wear overlay hex
    expect(String(fills[4].fillStyle)).toContain('0.500')
    expect(makeTerrainRaster(grid, null)).toBeNull()
  })
})

describe('drawTerrain', () => {
  it('blits the raster through the shared transform at its world origin (no per-cell work)', () => {
    const doc = fakeDoc()
    const raster = makeTerrainRaster(grid, doc)!
    const { ctx, ops } = ctxMock()
    drawTerrain(ctx, grid, tr, raster)
    const blit = ops.find(o => o.op === 'drawImage')!
    expect(blit.x).toBe(raster.originX * tr.sx + tr.ox) // wx(originX)
    expect(blit.y).toBe(raster.originY * tr.sy + tr.oy) // wy(originY)
    expect(blit.w).toBe(raster.worldW * tr.sx)
    expect(blit.h).toBe(raster.worldH * tr.sy)
    expect(ops.filter(o => o.op === 'fill')).toHaveLength(0) // blit only
  })

  it('null grid / raster ⇒ draws nothing (env-off neutrality)', () => {
    const { ctx, ops } = ctxMock()
    drawTerrain(ctx, null, tr, null)
    drawTerrain(ctx, grid, tr, null)
    expect(ops).toHaveLength(0)
  })
})

const climate = (over: Partial<ClimateState>): ClimateState => ({
  temperature: 15, apparentTemp: null, moisture: 0, raining: false, snowCover: 0,
  windDir: 0, windMag: 0, hourOfDay: 12, minuteOfDay: 720, dayNight: 'day', dayOfRun: 0, yearFraction: 0, ...over,
})

describe('drawAmbient', () => {
  it('null climate ⇒ no ops; clear mild day ⇒ no tint fills', () => {
    const { ctx, ops } = ctxMock()
    drawAmbient(ctx, null, 800, 600, 0)
    expect(ops).toHaveLength(0)
    drawAmbient(ctx, climate({}), 800, 600, 0)
    expect(ops.filter(o => o.op === 'fillRect')).toHaveLength(0)
  })

  it('night applies the dark overlay; cold applies the vignette', () => {
    const { ctx, ops } = ctxMock()
    drawAmbient(ctx, climate({ dayNight: 'night', temperature: -5 }), 800, 600, 0)
    const tint = ops.find(o => o.op === 'fillRect')!
    expect(String(tint.fillStyle)).toContain('10,20,50')
    expect(ops.some(o => o.op === 'radialGradient')).toBe(true)
  })

  it('rain streaks animate with the clock (pure, deterministic)', () => {
    const { ctx: c1, ops: o1 } = ctxMock()
    const { ctx: c2, ops: o2 } = ctxMock()
    const { ctx: c3, ops: o3 } = ctxMock()
    drawAmbient(c1, climate({ raining: true }), 800, 600, 0)
    drawAmbient(c2, climate({ raining: true }), 800, 600, 0)    // same clock → same frame
    drawAmbient(c3, climate({ raining: true }), 800, 600, 500)  // later clock → moved
    const rects = (o: typeof o1) => o.filter(r => r.op === 'fillRect').map(r => [r.x, r.y])
    expect(rects(o1).length).toBeGreaterThan(50)
    expect(rects(o1)).toEqual(rects(o2))
    expect(rects(o1)).not.toEqual(rects(o3))
  })

  it('wind draws the HUD arrow rotated to windDir', () => {
    const { ctx, ops } = ctxMock()
    drawAmbient(ctx, climate({ windMag: 0.8, windDir: 1.1 }), 800, 600, 0)
    expect(ops.find(o => o.op === 'rotate')!.a).toBe(1.1)
    expect(ops.some(o => o.op === 'stroke')).toBe(true)
  })

  it('precip FORM follows temperature (CS1): rain streaks above freezing, snow flakes below', () => {
    // Rain streaks are 1×9 fillRects — distinct from the full-screen temperature-vignette fillRect.
    const streaks = (o: Array<Record<string, unknown>>) => o.filter(f => f.op === 'fillRect' && f.w === 1)
    const flakes = (o: Array<Record<string, unknown>>) => o.filter(f => f.op === 'arc')

    // Above the snow-fall threshold → fast vertical streaks, no flakes.
    const warm = ctxMock()
    drawAmbient(warm.ctx, climate({ raining: true, temperature: 15 }), 800, 600, 0)
    expect(streaks(warm.ops).length).toBeGreaterThan(50)
    expect(flakes(warm.ops)).toHaveLength(0)

    // Below it → soft flakes, no rain streaks (the only fillRect is the cold vignette, not a streak).
    const cold = ctxMock()
    drawAmbient(cold.ctx, climate({ raining: true, temperature: -3 }), 800, 600, 0)
    expect(flakes(cold.ops).length).toBeGreaterThan(50)
    expect(streaks(cold.ops)).toHaveLength(0)

    // Snow flakes animate with the clock, deterministically (render purity).
    const later = ctxMock()
    drawAmbient(later.ctx, climate({ raining: true, temperature: -3 }), 800, 600, 500)
    const at = (o: Array<Record<string, unknown>>) => flakes(o).map(f => [f.x, f.y])
    expect(at(cold.ops)).not.toEqual(at(later.ops))
  })
})
