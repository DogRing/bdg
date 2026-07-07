import type { TerrainGrid } from '../types'
import { TERRAIN_STYLE, TERRAIN_DEFAULT, WEAR_TRAIL_COLOR } from '../assets/manifest'
import { wx, wy, type Transform } from './transform'

// Terrain renders via an offscreen raster of FLAT-TOP HEX cells (Q5 · docs/hex-grid.md).
// Each offset(col,row) cell is filled as a hexagon at its world centre — offset→axial→
// pixel from cellSize (= hex circumradius), MIRRORING navmap's convention (navmap is the
// authority; the frontend never hardcodes it, it reads `orientation` from the payload).
// The raster is drawn ONCE per grid identity in world coordinates at TERRAIN_RASTER_SCALE
// px/unit, then blitted through the shared transform each frame (drawTerrain does no
// per-cell work). The reducer replaces the grid object on every terrain_delta merge, so an
// identity change is the rebuild signal.

const SQRT3 = Math.sqrt(3)

// Offscreen resolution (px per world unit). 4 ≈ the default camera zoom, so the blit is
// near 1:1 there (crisp); higher zoom upscales the raster (imageSmoothing off ⇒ blocky, not
// blurry). A perf/quality tuning knob (hex-grid.md H8).
export const TERRAIN_RASTER_SCALE = 4

export interface TerrainRaster {
  canvas: CanvasImageSource
  grid: TerrainGrid // the grid this raster was built from (identity check)
  originX: number   // world-x of the raster's left edge
  originY: number   // world-y of the raster's top edge
  worldW: number    // world-units the raster spans in x
  worldH: number    // world-units the raster spans in y
}

type DocLike = Pick<Document, 'createElement'>

// hexCentre: world centre of offset(col,row) for a flat-top odd-q grid whose offset(0,0)
// centre is world (0,0) — EXACTLY navmap's offsetToAxial + hexToPixel (engine hex.go).
function hexCentre(col: number, row: number, size: number): { x: number; y: number } {
  const q = col
  const r = row - (col + (col & 1)) / 2 // offset(odd-q) → axial
  return { x: size * 1.5 * q, y: size * (SQRT3 / 2 * q + SQRT3 * r) }
}

// hexPath traces a flat-top hexagon (vertices at 0°,60°,…300° ⇒ pointy left/right, flat
// top/bottom) of circumradius `r` centred at (cx,cy). Caller sets fillStyle then fill()s.
function hexPath(ctx: CanvasRenderingContext2D, cx: number, cy: number, r: number) {
  ctx.beginPath()
  for (let i = 0; i < 6; i++) {
    const a = (Math.PI / 3) * i
    const vx = cx + r * Math.cos(a)
    const vy = cy + r * Math.sin(a)
    if (i === 0) ctx.moveTo(vx, vy)
    else ctx.lineTo(vx, vy)
  }
  ctx.closePath()
}

export function makeTerrainRaster(
  grid: TerrainGrid,
  doc: DocLike | null = typeof document !== 'undefined' ? document : null,
): TerrainRaster | null {
  if (!doc) return null
  const { cols, rows, cellSize } = grid
  const S = TERRAIN_RASTER_SCALE

  // World bbox of every hex (centres ± extents; a flat-top hex spans ±cellSize in x and
  // ±√3/2·cellSize in y). Odd columns dip half a row negative, so track the real min.
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity
  for (let row = 0; row < rows; row++) {
    for (let col = 0; col < cols; col++) {
      const c = hexCentre(col, row, cellSize)
      if (c.x < minX) minX = c.x
      if (c.x > maxX) maxX = c.x
      if (c.y < minY) minY = c.y
      if (c.y > maxY) maxY = c.y
    }
  }
  const originX = minX - cellSize
  const originY = minY - (SQRT3 / 2) * cellSize
  const worldW = maxX + cellSize - originX
  const worldH = maxY + (SQRT3 / 2) * cellSize - originY

  const canvas = doc.createElement('canvas') as HTMLCanvasElement
  canvas.width = Math.max(1, Math.ceil(worldW * S))
  canvas.height = Math.max(1, Math.ceil(worldH * S))
  const ctx = canvas.getContext('2d')
  if (!ctx) return null

  for (let row = 0; row < rows; row++) {
    for (let col = 0; col < cols; col++) {
      ctx.fillStyle = TERRAIN_STYLE[grid.terrain[row * cols + col]] ?? TERRAIN_DEFAULT
      const c = hexCentre(col, row, cellSize)
      hexPath(ctx, (c.x - originX) * S, (c.y - originY) * S, cellSize * S)
      ctx.fill()
    }
  }
  // Wear trails (desire paths): overlay alpha ∝ wear on top of the base hex colour.
  if (grid.wear) {
    for (let i = 0; i < grid.wear.length; i++) {
      const w = grid.wear[i]
      if (w <= 0) continue
      ctx.fillStyle = `rgba(${WEAR_TRAIL_COLOR},${Math.min(0.7, w).toFixed(3)})`
      const c = hexCentre(i % cols, Math.floor(i / cols), cellSize)
      hexPath(ctx, (c.x - originX) * S, (c.y - originY) * S, cellSize * S)
      ctx.fill()
    }
  }
  return { canvas, grid, originX, originY, worldW, worldH }
}

// drawTerrain only blits (no per-cell work). The raster covers the world rect
// [originX, originX+worldW] × [originY, originY+worldH]; place it through the transform.
// grid/raster === null ⇒ draws nothing (env-off neutrality; no decorative fallback).
export function drawTerrain(
  ctx: CanvasRenderingContext2D,
  grid: TerrainGrid | null,
  tr: Transform,
  raster: TerrainRaster | null,
) {
  if (!grid || !raster) return
  const x0 = wx(raster.originX, tr)
  const y0 = wy(raster.originY, tr)
  ctx.save()
  ctx.imageSmoothingEnabled = false
  ctx.drawImage(raster.canvas, x0, y0, raster.worldW * tr.sx, raster.worldH * tr.sy)
  ctx.restore()
}
