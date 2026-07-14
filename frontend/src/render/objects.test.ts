import { describe, it, expect } from 'vitest'
import { drawObjects } from './objects'
import { buildTransform } from './transform'
import type { WorldObject } from '../types'
import type { ThemeTokens } from '../theme'

// Minimal 2D-context mock recording the marker ops drawObjects emits.
function ctxMock() {
  const ops: Array<Record<string, unknown>> = []
  const ctx = {
    globalAlpha: 1, fillStyle: '' as unknown, strokeStyle: '' as unknown, lineWidth: 1,
    font: '', textAlign: '', shadowColor: '', shadowBlur: 0,
    save: () => {}, restore: () => {},
    beginPath: () => {}, arc: () => ops.push({ op: 'arc' }), fill: () => {},
    stroke: () => {}, fillRect: () => ops.push({ op: 'fillRect' }), strokeRect: () => {},
    fillText: (text: string) => ops.push({ op: 'fillText', text }),
  }
  return { ctx: ctx as unknown as CanvasRenderingContext2D, ops }
}

const t = { glow: false, textMuted: '#888', fontMono: 'monospace' } as unknown as ThemeTokens
const tr = buildTransform({ center: { x: 0, y: 0 }, zoom: 10, follow: null }, 100, 100)
const at = (x: number, y: number) => ({ x, y })

describe('drawObjects', () => {
  it('skips styleless plant objects (drawn by the flora pass, not as markers)', () => {
    const objs: WorldObject[] = [{ id: 'grass_1', kind: 'grass', pos: at(10, 10) }]
    const { ctx, ops } = ctxMock()
    drawObjects(ctx, objs, tr, t)
    // No circle, no "grass" label — the flora pass owns it.
    expect(ops.find(o => o.op === 'fillText')).toBeUndefined()
    expect(ops.find(o => o.op === 'arc')).toBeUndefined()
  })

  it('keeps a plant that has an explicit resource style (berry_bush → berry marker)', () => {
    const objs: WorldObject[] = [{ id: 'berry_1', kind: 'berry_bush', pos: at(10, 10) }]
    const { ctx, ops } = ctxMock()
    drawObjects(ctx, objs, tr, t)
    expect(ops.some(o => o.op === 'fillText' && o.text === 'berry')).toBe(true)
  })

  it('still renders non-plant objects: shelter square + a labelled fallback circle for unknown kinds', () => {
    const objs: WorldObject[] = [
      { id: 's1', kind: 'shelter', pos: at(20, 20) },
      { id: 'o1', kind: 'ore_node', pos: at(30, 30) }, // unknown, non-plant → generic marker
    ]
    const { ctx, ops } = ctxMock()
    drawObjects(ctx, objs, tr, t)
    expect(ops.some(o => o.op === 'fillRect')).toBe(true)             // shelter square
    expect(ops.some(o => o.op === 'fillText' && o.text === 'shelter')).toBe(true)
    expect(ops.some(o => o.op === 'fillText' && o.text === 'ore_node')).toBe(true) // fallback keeps open content
  })
})
