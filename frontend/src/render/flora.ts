import type { PlantState, FxInstance } from '../types'
import { DEFAULT_FLORA_COLOR, FLORA_COVERAGE, FX_DEFS } from '../assets/manifest'
import type { SpriteCache } from '../assets/sprites'
import { fxProgress } from './animator'
import { wx, wy, type Transform } from './transform'

// Sprite size is world-proportional (∝ plant.width) with a px floor so plants
// stay visible at fit-zoom. Stage picks the sheet frame (growth stages).
const MIN_PX = 10
// Coverage stamp: world-scaled (so it grows/shrinks with zoom, unlike the old
// fixed dot) with a small px floor so a lone tuft never vanishes when zoomed out.
const MIN_COVER_PX = 6

const easeOut = (t: number) => 1 - (1 - t) * (1 - t)

export function drawFlora(
  ctx: CanvasRenderingContext2D,
  flora: PlantState[],
  tr: Transform,
  sprites: SpriteCache,
  clockMs: number,
  fx: FxInstance[] = [],
) {
  // Active per-plant fx this frame (spawn alpha ramp / grow scale tween).
  const spawnP = new Map<string, number>()
  const growP = new Map<string, number>()
  for (const f of fx) {
    if (f.kind === 'spawn' || f.kind === 'grow') {
      const p = fxProgress(f, FX_DEFS[f.kind], clockMs)
      if (p !== null) (f.kind === 'spawn' ? spawnP : growP).set(f.id, p)
    }
  }

  // Pass 1 — ground-cover density wash (drawn UNDER sprites so a tree standing in
  // a pasture occludes it). Overlapping stamps accumulate via source-over: the
  // denser the clump, the more opaque → a continuous meadow, not discrete dots.
  for (const plant of flora) {
    const cover = FLORA_COVERAGE[plant.species]
    if (!cover) continue
    const cx = wx(plant.pos.x, tr)
    const cy = wy(plant.pos.y, tr)
    let r = Math.max(MIN_COVER_PX, cover.radiusUnits * tr.sx)
    const gp = growP.get(plant.id)
    if (gp !== undefined) r *= 0.8 + 0.2 * easeOut(gp)
    // Fainter when young (sprout) → fuller when a mature tuft; spawn fx ramps in.
    let a = cover.alpha * Math.min(1, 0.5 + 0.25 * Math.max(0, plant.stage))
    const spP = spawnP.get(plant.id)
    if (spP !== undefined) a *= spP
    if (a <= 0) continue
    // Solid core out to `plateau`, then a soft rim to 0 — adjacent stamps' cores
    // overlap into one continuous sheet (a painted meadow), not separate dabs.
    const g = ctx.createRadialGradient(cx, cy, 0, cx, cy, r)
    g.addColorStop(0, `rgba(${cover.color},${a})`)
    g.addColorStop(cover.plateau, `rgba(${cover.color},${a})`)
    g.addColorStop(1, `rgba(${cover.color},0)`)
    ctx.fillStyle = g
    ctx.beginPath()
    ctx.arc(cx, cy, r, 0, Math.PI * 2)
    ctx.fill()
  }

  // Pass 2 — per-plant sprites/glyphs (trees, bushes, …).
  for (const plant of flora) {
    if (FLORA_COVERAGE[plant.species]) continue
    const cx = wx(plant.pos.x, tr)
    const cy = wy(plant.pos.y, tr)
    let size = Math.max(MIN_PX, plant.width * tr.sx)

    // Grow tween (plan Q4): the plant eases up to its new size on stage change.
    const gp = growP.get(plant.id)
    if (gp !== undefined) size *= 0.8 + 0.2 * easeOut(gp)

    const spP = spawnP.get(plant.id)
    const hasAlpha = spP !== undefined
    if (hasAlpha) { ctx.save(); ctx.globalAlpha = spP }

    const loaded = sprites.flora(plant.species)
    if (loaded?.ready) {
      const d = loaded.def
      const frame = Math.min(Math.max(Math.floor(plant.stage), 0), d.stageFrames - 1)
      ctx.drawImage(loaded.image,
        frame * d.frameW, 0, d.frameW, d.frameH,
        cx - size / 2, cy - size / 2, size, size)
    } else {
      // Fallback glyph: species-agnostic green circle (assets SPEC chain tail).
      ctx.fillStyle = DEFAULT_FLORA_COLOR
      ctx.beginPath()
      ctx.arc(cx, cy, size / 2, 0, Math.PI * 2)
      ctx.fill()
    }

    if (hasAlpha) ctx.restore()
  }
}
