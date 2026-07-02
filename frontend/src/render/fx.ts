import type { FxInstance } from '../types'
import { FX_DEFS } from '../assets/manifest'
import type { SpriteCache } from '../assets/sprites'
import { fxProgress } from './animator'
import { faunaSizePx, LUNGE_UNITS } from './fauna'
import { wx, wy, type Transform } from './transform'

// Transient FX that outlive / accompany their entity (plan Q4). spawn + grow
// are applied inside drawFauna/drawFlora (alpha / scale on the live entity);
// this layer draws what has no live entity anymore (death) and impact accents
// (attack flash). Everything is a pure function of (fx, clockMs).

const CORPSE_COLOR = '#802020'

export function drawFx(
  ctx: CanvasRenderingContext2D,
  fx: FxInstance[],
  tr: Transform,
  sprites: SpriteCache,
  clockMs: number,
) {
  for (const f of fx) {
    const p = fxProgress(f, FX_DEFS[f.kind], clockMs)
    if (p === null) continue
    if (f.kind === 'death') drawDeath(ctx, f, p, tr, sprites)
    else if (f.kind === 'attack') drawImpact(ctx, f, p, tr)
  }
}

// Death (1500 ms): the removed entity keeps drawing — the sheet's `dying` row
// swept once across the fx timeline (not fps-looped), fading out; the final
// third leaves a corpse mark, then nothing (the reducer prunes the entry).
function drawDeath(
  ctx: CanvasRenderingContext2D,
  f: FxInstance,
  p: number,
  tr: Transform,
  sprites: SpriteCache,
) {
  const cx = wx(f.pos.x, tr)
  const cy = wy(f.pos.y, tr)

  ctx.save()
  ctx.globalAlpha = 1 - p

  const loaded = f.species ? sprites.fauna(f.species) : null
  const clip = loaded?.ready ? loaded.def.poses.dying : undefined
  if (loaded && clip) {
    const size = faunaSizePx(tr)
    // Progress-driven frame sweep (one pass across the row, not fps-looped).
    const frame = Math.min(clip.frames - 1, Math.floor(p * clip.frames))
    const rect = {
      sx: frame * loaded.def.frameW, sy: clip.row * loaded.def.frameH,
      sw: loaded.def.frameW, sh: loaded.def.frameH,
    }
    ctx.save()
    ctx.translate(cx, cy)
    ctx.rotate(f.heading ?? 0)
    ctx.drawImage(loaded.image, rect.sx, rect.sy, rect.sw, rect.sh,
      -size / 2, -size / 2, size, size)
    ctx.restore()
  } else {
    // Flora / glyph-only species: a shrinking, fading patch.
    const size = Math.max(10, 2 * tr.sx) * (1 - p * 0.5)
    ctx.fillStyle = CORPSE_COLOR
    ctx.beginPath()
    ctx.arc(cx, cy, size / 2, 0, Math.PI * 2)
    ctx.fill()
  }

  // Corpse mark in the final third of the timeline.
  if (p > 2 / 3) {
    const s = 5
    ctx.strokeStyle = CORPSE_COLOR
    ctx.lineWidth = 2
    ctx.beginPath()
    ctx.moveTo(cx - s, cy - s); ctx.lineTo(cx + s, cy + s)
    ctx.moveTo(cx + s, cy - s); ctx.lineTo(cx - s, cy + s)
    ctx.stroke()
  }
  ctx.restore()
}

// Attack impact (300 ms): an expanding, fading flash ring at the lunge tip —
// the visible "hit" beside the attacker's forward jab (drawFauna).
function drawImpact(ctx: CanvasRenderingContext2D, f: FxInstance, p: number, tr: Transform) {
  const dir = f.heading ?? 0
  const cx = wx(f.pos.x, tr) + Math.cos(dir) * LUNGE_UNITS * tr.sx
  const cy = wy(f.pos.y, tr) + Math.sin(dir) * LUNGE_UNITS * tr.sy
  ctx.save()
  ctx.globalAlpha = 1 - p
  ctx.strokeStyle = '#d44444'
  ctx.lineWidth = 2
  ctx.beginPath()
  ctx.arc(cx, cy, 4 + p * 10, 0, Math.PI * 2)
  ctx.stroke()
  ctx.restore()
}
