import type { PlantState, FxInstance, ClimateState } from '../types'
import {
  DEFAULT_FLORA_COLOR, FLORA_COVERAGE, FX_DEFS, floraSeason, variantRow,
} from '../assets/manifest'
import type { SpriteCache } from '../assets/sprites'
import { fxProgress } from './animator'
import { wx, wy, type Transform } from './transform'

// Sprite size is world-proportional (∝ plant.width) with a px floor so plants
// stay visible at fit-zoom. Stage picks the sheet frame (growth stages).
const MIN_PX = 10
// Coverage stamp: world-scaled (so it grows/shrinks with zoom, unlike the old
// fixed dot) with a small px floor so a lone tuft never vanishes when zoomed out.
const MIN_COVER_PX = 6
const MAX_WIND_BEND = 0.42

const easeOut = (t: number) => 1 - (1 - t) * (1 - t)
const clamp01 = (v: number) => Math.max(0, Math.min(1, v))

// Stable per-plant phase: neighbouring tufts do not sway in lockstep, while the
// same frame remains deterministic for (plant id, climate, clockMs).
function phaseFor(id: string): number {
  let h = 2166136261
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return ((h >>> 0) / 0xffffffff) * Math.PI * 2
}

export function drawFlora(
  ctx: CanvasRenderingContext2D,
  flora: PlantState[],
  tr: Transform,
  sprites: SpriteCache,
  clockMs: number,
  fx: FxInstance[] = [],
  climate: ClimateState | null = null,
) {
  // One seasonal variant per frame for every plant (manifest thresholds, P6-Q2).
  const season = floraSeason(climate)
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

  // Pass 2 — per-plant sprites/glyphs (trees, bushes, wind-responsive grass, …).
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

    const loaded = sprites.flora(plant.species, season)
    if (loaded?.ready) {
      const d = loaded.def
      const col = Math.min(Math.max(Math.floor(plant.stage), 0), d.stageFrames - 1)
      // Row = the season (single-file sheets) else a stable per-plant shape variant.
      const row = d.seasonRows ? d.seasonRows[season] ?? 0 : variantRow(plant.id, d.variantRows)
      if (d.windResponsive) {
        // The sprite is a side-view billboard anchored at its bottom centre. Wind
        // displaces its top in screen-space: direction chooses the bend vector,
        // magnitude chooses bend amount, and a deterministic gust adds life.
        const mag = clamp01(climate?.windMag ?? 0)
        const dir = climate?.windDir ?? 0
        const gust = mag > 0
          ? Math.sin(clockMs * (0.0018 + mag * 0.0022) + phaseFor(plant.id)) * mag * 0.18
          : 0
        const bend = Math.min(MAX_WIND_BEND, Math.max(0, mag * 0.34 + gust))
        const bendX = Math.cos(dir) * bend
        const bendY = Math.sin(dir) * bend * 0.22
        ctx.save()
        ctx.translate(cx, cy + size / 2)
        // For source y∈[-size,0], negative shear coefficients move the top
        // downwind while keeping the base exactly fixed at the plant position.
        ctx.transform(1, 0, -bendX, 1 - bendY, 0, 0)
        ctx.drawImage(loaded.image,
          col * d.frameW, row * d.frameH, d.frameW, d.frameH,
          -size / 2, -size, size, size)
        ctx.restore()
      } else {
        ctx.drawImage(loaded.image,
          col * d.frameW, row * d.frameH, d.frameW, d.frameH,
          cx - size / 2, cy - size / 2, size, size)
      }
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
