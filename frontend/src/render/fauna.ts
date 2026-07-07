import type { AnimalState, FxInstance } from '../types'
import { FAUNA_SHEETS, DEFAULT_FAUNA, FX_DEFS, poseFor, type Pose } from '../assets/manifest'
import { frameRect, type SpriteCache } from '../assets/sprites'
import { displayPos, displayHeading, isRunning, fxProgress } from './animator'
import { wx, wy, type Transform } from './transform'

// Animal sprite footprint in world units, with a px floor for legibility at
// fit-zoom (presentation constants, not species data).
const WORLD_UNITS = 3
const MIN_PX = 20
// Attack lunge amplitude (world units) — the forward jab of the attack fx.
export const LUNGE_UNITS = 1.5
// Status label (screen-space): small, outlined so it stays legible on any terrain.
const LABEL_FONT_PX = 9
const LABEL_GAP_PX = 3

export function faunaSizePx(tr: Transform): number {
  return Math.max(MIN_PX, WORLD_UNITS * tr.sx)
}

// Pose fallback order when a sheet lacks the requested row (assets SPEC):
// requested → walk → idle → glyph. `dying` is drawn only via the death FX;
// poseFor never maps a live action to it.
const POSE_FALLBACK: Pose[] = ['walk', 'idle']

export function drawFauna(
  ctx: CanvasRenderingContext2D,
  animals: Iterable<AnimalState>,
  tr: Transform,
  sprites: SpriteCache,
  clockMs: number,
  fx: FxInstance[] = [],
) {
  // Active per-entity fx this frame (spawn alpha ramp / attack lunge).
  const spawnP = new Map<string, number>()
  const attack = new Map<string, number>()
  for (const f of fx) {
    if (f.kind === 'spawn' || f.kind === 'attack') {
      const p = fxProgress(f, FX_DEFS[f.kind], clockMs)
      if (p !== null) (f.kind === 'spawn' ? spawnP : attack).set(f.id, p)
    }
  }

  for (const animal of animals) {
    const dp = displayPos(animal, clockMs)
    const dh = displayHeading(animal, clockMs)
    let cx = wx(dp.x, tr)
    let cy = wy(dp.y, tr)
    const size = faunaSizePx(tr)
    // Unknown species → prey glyph (assets SPEC fallback chain).
    const style = FAUNA_SHEETS[animal.species] ?? DEFAULT_FAUNA.prey

    // Attack lunge (plan Q4): forward jab along heading, peaking mid-fx.
    const atkP = attack.get(animal.id)
    if (atkP !== undefined) {
      const off = Math.sin(Math.PI * atkP) * LUNGE_UNITS * tr.sx
      cx += Math.cos(dh) * off
      cy += Math.sin(dh) * off
    }

    // Pose from the ActionID pattern table, refined by actual displacement rate
    // (walk↔run) so a barely-moving "flee" reads as walking.
    let pose = poseFor(animal.action)
    if (pose === 'walk' && isRunning(animal)) pose = 'run'
    else if (pose === 'run' && !isRunning(animal)) pose = 'walk'

    ctx.save()
    let alpha = Math.max(0.3, Math.min(1, animal.stamina ?? 1)) // tiring predator dims
    const spP = spawnP.get(animal.id)
    if (spP !== undefined) alpha *= spP // spawn fade-in
    ctx.globalAlpha = alpha
    ctx.translate(cx, cy)
    ctx.rotate(dh) // sheets face +x at heading 0

    const loaded = sprites.fauna(animal.species)
    let rect: ReturnType<typeof frameRect> = null
    if (loaded?.ready) {
      rect = frameRect(loaded.def, pose, clockMs)
      for (let i = 0; rect === null && i < POSE_FALLBACK.length; i++) {
        rect = frameRect(loaded.def, POSE_FALLBACK[i], clockMs)
      }
    }

    if (loaded && rect) {
      ctx.drawImage(loaded.image, rect.sx, rect.sy, rect.sw, rect.sh,
        -size / 2, -size / 2, size, size)
    } else {
      drawGlyph(ctx, style.category, style.glyphColor, size)
    }
    ctx.restore()

    // Status label: the current ActionID shown verbatim (`_`→space) above the
    // sprite — data display only, no per-action branching (open-content
    // invariant). Drawn outside the rotated frame so text stays upright.
    const label = animal.action.replace(/_/g, ' ')
    if (label) {
      ctx.save()
      ctx.globalAlpha = alpha
      ctx.font = `${LABEL_FONT_PX}px sans-serif`
      ctx.textAlign = 'center'
      ctx.textBaseline = 'bottom'
      const ly = cy - size / 2 - LABEL_GAP_PX
      ctx.lineWidth = 2.5
      ctx.strokeStyle = 'rgba(10,10,10,0.75)'
      ctx.strokeText(label, cx, ly)
      ctx.fillStyle = 'rgba(255,255,255,0.95)'
      ctx.fillText(label, cx, ly)
      ctx.restore()
    }
  }
}

// Category glyph (fallback chain tail): a heading-oriented chevron; predators
// sharper + red-tinted so predator/prey stay distinct without art.
function drawGlyph(
  ctx: CanvasRenderingContext2D,
  category: 'predator' | 'prey',
  color: string,
  sizePx: number,
) {
  const s = sizePx / (category === 'predator' ? 4 : 5)
  ctx.fillStyle = color
  ctx.beginPath()
  ctx.moveTo(s * 1.5, 0)
  ctx.lineTo(-s, -s)
  ctx.lineTo(-s * 0.5, 0)
  ctx.lineTo(-s, s)
  ctx.closePath()
  ctx.fill()
}
