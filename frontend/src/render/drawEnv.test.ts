import { describe, it, expect } from 'vitest'
import { drawFauna } from './fauna'
import { drawFlora } from './flora'
import { drawFx } from './fx'
import { buildTransform } from './transform'
import { DEFAULT_FLORA_COLOR, type SheetDef, type FloraSheetDef } from '../assets/manifest'
import type { SpriteCache } from '../assets/sprites'
import type { AnimalState, PlantState, FxInstance } from '../types'

// ── minimal 2D-context mock: records the ops the SPEC ACs assert on ─────────
function ctxMock() {
  const ops: Array<Record<string, unknown>> = []
  const ctx = {
    globalAlpha: 1,
    fillStyle: '' as unknown,
    strokeStyle: '' as unknown,
    lineWidth: 1,
    save: () => ops.push({ op: 'save' }),
    restore: () => ops.push({ op: 'restore' }),
    translate: (x: number, y: number) => ops.push({ op: 'translate', x, y }),
    rotate: (a: number) => ops.push({ op: 'rotate', a }),
    beginPath: () => {},
    moveTo: () => ops.push({ op: 'moveTo' }),
    lineTo: () => {},
    closePath: () => {},
    arc: (cx: number, cy: number, r: number) => ops.push({ op: 'arc', cx, cy, r, fillStyle: ctx.fillStyle }),
    fill: () => ops.push({ op: 'fill', fillStyle: ctx.fillStyle }),
    createRadialGradient: (..._a: number[]) => {
      ops.push({ op: 'radialGradient' })
      return { addColorStop: (_o: number, c: string) => ops.push({ op: 'colorStop', c }) } as unknown as CanvasGradient
    },
    stroke: () => ops.push({ op: 'stroke' }),
    drawImage: (...a: unknown[]) =>
      ops.push({ op: 'drawImage', sx: a[1], sy: a[2], dw: a[7], alpha: ctx.globalAlpha }),
    font: '',
    textAlign: '',
    textBaseline: '',
    strokeText: (text: string, x: number, y: number) => ops.push({ op: 'strokeText', text, x, y }),
    fillText: (text: string, x: number, y: number) =>
      ops.push({ op: 'fillText', text, x, y, alpha: ctx.globalAlpha }),
  }
  return { ctx: ctx as unknown as CanvasRenderingContext2D, ops }
}

const SHEET: SheetDef = {
  url: '/x.png', frameW: 32, frameH: 32,
  poses: {
    idle:   { row: 0, frames: 4, fps: 3 },
    walk:   { row: 1, frames: 4, fps: 8 },
    attack: { row: 4, frames: 4, fps: 10 },
    dying:  { row: 5, frames: 4, fps: 6 },
  },
}
const WALK_ONLY: SheetDef = { url: '/y.png', frameW: 32, frameH: 32, poses: { walk: { row: 1, frames: 4, fps: 8 } } }
const FLORA_SHEET: FloraSheetDef = { url: '/t.png', frameW: 32, frameH: 32, stageFrames: 4, variantRows: 1 }
// FE-P6 layouts: single-file season rows (bush) and per-plant shape-variant rows (trees).
const SEASON_SHEET: FloraSheetDef = {
  url: '/b.png', frameW: 32, frameH: 32, stageFrames: 4, variantRows: 1,
  seasonRows: { leaf: 0, bare: 1, snow: 2 },
}
const VARIANT_SHEET: FloraSheetDef = { url: '/v.png', frameW: 32, frameH: 32, stageFrames: 4, variantRows: 4 }

const cache = (fauna: Record<string, SheetDef> = {}, ready = true): SpriteCache => ({
  fauna: (s) => (fauna[s] ? { image: {} as HTMLImageElement, def: fauna[s], ready } : null),
  flora: (s) => {
    const def = s === 'tree' ? FLORA_SHEET : s === 'bush' ? SEASON_SHEET : s === 'oak' ? VARIANT_SHEET : null
    return def ? { image: {} as HTMLImageElement, def, ready } : null
  },
  preload: async () => {},
})

const animal = (over: Partial<AnimalState>): AnimalState =>
  ({ id: 'a', pos: { x: 0, y: 0 }, species: 'wolf', action: 'wander', heading: 0, stamina: 1, ...over })

const plant = (over: Partial<PlantState>): PlantState =>
  ({ id: 'p', pos: { x: 0, y: 0 }, species: 'tree', stage: 0, width: 3, ...over })

// world (0,0) → canvas (400,300); 10 px per world unit
const tr = buildTransform({ center: { x: 0, y: 0 }, zoom: 10, follow: null }, 800, 600)

describe('drawFauna', () => {
  it('hunt action draws the attack row, rotated by heading, dimmed by stamina', () => {
    const { ctx, ops } = ctxMock()
    drawFauna(ctx, [animal({ action: 'hunt_deer', heading: 1.2, stamina: 0.5 })], tr, cache({ wolf: SHEET }), 0)
    const img = ops.find(o => o.op === 'drawImage')!
    expect(img.sy).toBe(4 * 32) // attack row
    expect(img.alpha).toBeCloseTo(0.5)
    expect(ops.find(o => o.op === 'rotate')!.a).toBe(1.2)
  })

  it('cycles the sprite frame with clockMs', () => {
    const { ctx: c1, ops: o1 } = ctxMock()
    const { ctx: c2, ops: o2 } = ctxMock()
    const a = [animal({ action: 'attack' })]
    drawFauna(c1, a, tr, cache({ wolf: SHEET }), 0)
    drawFauna(c2, a, tr, cache({ wolf: SHEET }), 150) // fps 10 → frame 1
    expect(o1.find(o => o.op === 'drawImage')!.sx).toBe(0)
    expect(o2.find(o => o.op === 'drawImage')!.sx).toBe(32)
  })

  it('interpolates the position between frame stamps (Q3)', () => {
    const { ctx, ops } = ctxMock()
    const a = animal({
      pos: { x: 10, y: 0 }, prevPos: { x: 0, y: 0 },
      prevFrameAtMs: 1000, frameAtMs: 2000,
    })
    drawFauna(ctx, [a], tr, cache({ wolf: SHEET }), 1500)
    expect(ops.find(o => o.op === 'translate')!.x).toBeCloseTo(400 + 5 * 10) // midpoint
  })

  it('refines walk→run by displacement rate', () => {
    const { ctx, ops } = ctxMock()
    const fleeingSlowly = animal({
      action: 'flee', // pose run by pattern…
      pos: { x: 0.5, y: 0 }, prevPos: { x: 0, y: 0 }, // …but 0.5 u/s → walk
      prevFrameAtMs: 1000, frameAtMs: 2000,
    })
    drawFauna(ctx, [fleeingSlowly], tr, cache({ wolf: SHEET }), 2000)
    expect(ops.find(o => o.op === 'drawImage')!.sy).toBe(1 * 32) // walk row, not run
  })

  it('writes the action as a small status label above the sprite, upright (post-restore)', () => {
    const { ctx, ops } = ctxMock()
    drawFauna(ctx, [animal({ action: 'hunt_deer', heading: 1.2 })], tr, cache({ wolf: SHEET }), 0)
    const label = ops.find(o => o.op === 'fillText')!
    expect(label.text).toBe('hunt deer')                     // `_` → space, verbatim otherwise
    expect(label.y as number).toBeLessThan(300)              // above the sprite centre (canvas y 300)
    expect(ops.some(o => o.op === 'strokeText')).toBe(true)  // outlined for legibility
    // Drawn outside the rotated frame: restore precedes the label op.
    expect(ops.findIndex(o => o.op === 'restore')).toBeLessThan(ops.indexOf(label))
  })

  it('falls back pose→walk when the sheet lacks the row, and to the glyph for unknown species / unready sheets', () => {
    const { ctx, ops } = ctxMock()
    drawFauna(ctx, [animal({ action: 'hunt' })], tr, cache({ wolf: WALK_ONLY }), 0)
    expect(ops.find(o => o.op === 'drawImage')!.sy).toBe(1 * 32) // walk row

    const { ctx: cg, ops: og } = ctxMock()
    expect(() =>
      drawFauna(cg, [animal({ species: 'unknown_beast' })], tr, cache({}), 0),
    ).not.toThrow()
    expect(og.some(o => o.op === 'drawImage')).toBe(false)
    expect(og.some(o => o.op === 'fill')).toBe(true) // chevron glyph

    const { ctx: cu, ops: ou } = ctxMock()
    drawFauna(cu, [animal({})], tr, cache({ wolf: SHEET }, false), 0) // sheet not ready
    expect(ou.some(o => o.op === 'drawImage')).toBe(false)
    expect(ou.some(o => o.op === 'fill')).toBe(true)
  })
})

describe('drawFlora', () => {
  it('picks the stage frame (clamped) and scales by width', () => {
    const { ctx, ops } = ctxMock()
    drawFlora(ctx, [plant({ stage: 7, width: 3 })], tr, cache(), 0)
    const img = ops.find(o => o.op === 'drawImage')!
    expect(img.sx).toBe(3 * 32)      // stage 7 → clamped to last frame (3)
    expect(img.dw).toBe(3 * 10)      // width × zoom
  })

  it('unknown species / unready sheet falls back to the colour glyph', () => {
    const { ctx, ops } = ctxMock()
    drawFlora(ctx, [plant({ species: 'unknown_weed' })], tr, cache(), 0) // no sheet, not ground-cover
    expect(ops.some(o => o.op === 'drawImage')).toBe(false)
    const arc = ops.find(o => o.op === 'arc')!
    expect(arc.fillStyle).toBe(DEFAULT_FLORA_COLOR)
  })

  it('ground-cover (grass) paints a world-scaled density wash, not a fixed dot or sprite', () => {
    const { ctx, ops } = ctxMock()
    // radiusUnits 4.5 × zoom 10 = 45 px stamp (scales with zoom, unlike the old 10px floor).
    drawFlora(ctx, [plant({ species: 'grass', stage: 2, width: 0.3 })], tr, cache(), 0)
    expect(ops.some(o => o.op === 'drawImage')).toBe(false)          // no sprite
    expect(ops.some(o => o.op === 'radialGradient')).toBe(true)      // gradient wash
    const arc = ops.find(o => o.op === 'arc')!
    expect(arc.r).toBeCloseTo(45)                                    // world-scaled, not MIN_PX
    expect(arc.fillStyle).not.toBe(DEFAULT_FLORA_COLOR)              // gradient, not the glyph colour
  })

  it('season rows: a single-file sheet picks the row from climate temperature', () => {
    const clim = (temperature: number) => ({
      temperature, apparentTemp: null, moisture: 0.5, raining: false,
      windDir: 0, windMag: 0, hourOfDay: 12, dayNight: 'day' as const, yearFraction: 0,
    })
    const at = (temp: number | null) => {
      const { ctx, ops } = ctxMock()
      drawFlora(ctx, [plant({ species: 'bush', stage: 0 })], tr, cache(), 0, [],
        temp === null ? null : clim(temp))
      return ops.find(o => o.op === 'drawImage')!.sy
    }
    expect(at(null)).toBe(0)      // no climate → leaf row
    expect(at(12)).toBe(0)        // warm → leaf
    expect(at(3)).toBe(1 * 32)    // chilly → bare row
    expect(at(-3)).toBe(2 * 32)   // freezing → snow row
  })

  it('variant rows: the row is a stable id hash, differing across ids', () => {
    const rowOf = (id: string) => {
      const { ctx, ops } = ctxMock()
      drawFlora(ctx, [plant({ id, species: 'oak', stage: 0 })], tr, cache(), 0)
      return ops.find(o => o.op === 'drawImage')!.sy as number
    }
    expect(rowOf('oak_1')).toBe(rowOf('oak_1')) // same id, same row (purity)
    const distinct = new Set(['oak_1', 'oak_2', 'oak_3', 'oak_4', 'oak_5', 'oak_6'].map(rowOf))
    expect(distinct.size).toBeGreaterThan(1)    // ids spread across shape rows
  })

  it('coverage stamps accumulate: a denser clump emits more wash ops than a lone tuft', () => {
    const lone = ctxMock()
    drawFlora(lone.ctx, [plant({ id: 'g1', species: 'grass', pos: { x: 0, y: 0 } })], tr, cache(), 0)
    const clump = ctxMock()
    drawFlora(clump.ctx, [
      plant({ id: 'g1', species: 'grass', pos: { x: 0, y: 0 } }),
      plant({ id: 'g2', species: 'grass', pos: { x: 0.5, y: 0 } }),
      plant({ id: 'g3', species: 'grass', pos: { x: 0, y: 0.5 } }),
    ], tr, cache(), 0)
    const washes = (ops: Array<Record<string, unknown>>) => ops.filter(o => o.op === 'fill').length
    expect(washes(clump.ops)).toBeGreaterThan(washes(lone.ops)) // overlap builds density
  })
})

describe('transition fx (FE-P3, Q4)', () => {
  it('attack fx lunges the sprite along heading, peaking mid-timeline', () => {
    const fx: FxInstance[] = [{ kind: 'attack', at: 0, pos: { x: 0, y: 0 }, id: 'a', heading: 0 }]
    const { ctx, ops } = ctxMock()
    drawFauna(ctx, [animal({ action: 'hunt' })], tr, cache({ wolf: SHEET }), 150, fx) // p=0.5
    expect(ops.find(o => o.op === 'translate')!.x).toBeCloseTo(400 + 1.5 * 10) // sin(π/2)·LUNGE·zoom
  })

  it('spawn fx ramps the sprite alpha 0→1', () => {
    const fx: FxInstance[] = [{ kind: 'spawn', at: 0, pos: { x: 0, y: 0 }, id: 'a' }]
    const { ctx, ops } = ctxMock()
    drawFauna(ctx, [animal({})], tr, cache({ wolf: SHEET }), 250, fx) // p=0.5
    expect(ops.find(o => o.op === 'drawImage')!.alpha).toBeCloseTo(0.5)
  })

  it('death fx sweeps the dying row fading, marks a corpse late, then expires', () => {
    const fx: FxInstance[] = [{ kind: 'death', at: 0, pos: { x: 0, y: 0 }, id: 'd', species: 'wolf', heading: 0 }]
    const c = cache({ wolf: SHEET })

    const { ctx: c1, ops: o1 } = ctxMock()
    drawFx(c1, fx, tr, c, 750) // p=0.5 → frame 2, no corpse yet
    const img = o1.find(o => o.op === 'drawImage')!
    expect(img.sy).toBe(5 * 32)
    expect(img.sx).toBe(2 * 32)
    expect(img.alpha).toBeCloseTo(0.5)
    expect(o1.some(o => o.op === 'stroke')).toBe(false)

    const { ctx: c2, ops: o2 } = ctxMock()
    drawFx(c2, fx, tr, c, 1200) // p=0.8 → final third: corpse mark
    expect(o2.some(o => o.op === 'stroke')).toBe(true)

    const { ctx: c3, ops: o3 } = ctxMock()
    drawFx(c3, fx, tr, c, 1600) // expired → nothing drawn
    expect(o3).toHaveLength(0)
  })

  it('a plant death (no fauna sheet) draws the fading patch fallback', () => {
    const fx: FxInstance[] = [{ kind: 'death', at: 0, pos: { x: 0, y: 0 }, id: 'p', species: 'tree' }]
    const { ctx, ops } = ctxMock()
    drawFx(ctx, fx, tr, cache(), 750)
    expect(ops.some(o => o.op === 'drawImage')).toBe(false)
    expect(ops.some(o => o.op === 'fill')).toBe(true)
  })

  it('grow fx eases the plant scale up from 80%', () => {
    const fx: FxInstance[] = [{ kind: 'grow', at: 0, pos: { x: 0, y: 0 }, id: 'p' }]
    const { ctx, ops } = ctxMock()
    drawFlora(ctx, [plant({ width: 3 })], tr, cache(), 0, fx) // p=0 → 0.8×
    expect(ops.find(o => o.op === 'drawImage')!.dw).toBeCloseTo(30 * 0.8)
  })
})
