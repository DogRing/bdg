import type { WorldObject } from '../types'
import type { ThemeTokens } from '../theme'
import { isFloraSpecies } from '../assets/manifest'
import { wx, wy, type Transform } from './transform'

// Visual style per object kind (matches content/objects.yaml kinds).
const OBJ_STYLE: Record<string, { color: string; label: string; shape: 'circle' | 'square' }> = {
  berry_bush:   { color: '#4a9030', label: 'berry',   shape: 'circle' },
  water_source: { color: '#3a86d0', label: 'water',   shape: 'circle' },
  shelter:      { color: '#9a6a3a', label: 'shelter', shape: 'square' },
}

// drawObjects renders placed resources (berry/water/shelter) at their world
// positions using the shared transform, with a colour + label per kind.
export function drawObjects(
  ctx: CanvasRenderingContext2D,
  objects: WorldObject[],
  tr: Transform,
  t: ThemeTokens,
) {
  for (const o of objects) {
    const style = OBJ_STYLE[o.kind]
    // Plants live in the object list too (grazing/forage supply) but are drawn by
    // the flora pass; skip any styleless plant so its tuft sprite isn't buried
    // under a generic muted-circle marker. A plant with an explicit style
    // (berry_bush) keeps its resource marker.
    if (!style && isFloraSpecies(o.kind)) continue
    const cx = wx(o.pos.x, tr)
    const cy = wy(o.pos.y, tr)
    const resolved = style ?? { color: t.textMuted, label: o.kind, shape: 'circle' as const }

    ctx.save()
    if (t.glow) { ctx.shadowColor = resolved.color; ctx.shadowBlur = 8 }
    ctx.fillStyle = resolved.color
    if (resolved.shape === 'square') {
      ctx.fillRect(cx - 7, cy - 7, 14, 14)
      ctx.restore()
      ctx.strokeStyle = 'rgba(0,0,0,0.35)'; ctx.lineWidth = 1
      ctx.strokeRect(cx - 7, cy - 7, 14, 14)
    } else {
      ctx.beginPath(); ctx.arc(cx, cy, 8, 0, Math.PI * 2); ctx.fill()
      ctx.restore()
      ctx.strokeStyle = 'rgba(0,0,0,0.35)'; ctx.lineWidth = 1
      ctx.beginPath(); ctx.arc(cx, cy, 8, 0, Math.PI * 2); ctx.stroke()
    }

    ctx.fillStyle = t.textMuted
    ctx.font = `9px ${t.fontMono}`
    ctx.textAlign = 'center'
    ctx.fillText(resolved.label, cx, cy + 19)
  }
}
