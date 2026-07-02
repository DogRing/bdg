import type { PlantState, FxInstance } from '../types'
import { DEFAULT_FLORA_COLOR, FX_DEFS } from '../assets/manifest'
import type { SpriteCache } from '../assets/sprites'
import { fxProgress } from './animator'
import { wx, wy, type Transform } from './transform'

// Sprite size is world-proportional (∝ plant.width) with a px floor so plants
// stay visible at fit-zoom. Stage picks the sheet frame (growth stages).
const MIN_PX = 10

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

  for (const plant of flora) {
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
