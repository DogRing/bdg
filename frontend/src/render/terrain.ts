import type { TerrainGrid } from '../types'
import { TERRAIN_STYLE, TERRAIN_DEFAULT, WEAR_TRAIL_COLOR } from '../assets/manifest'
import { wx, wy, type Transform } from './transform'

// Terrain renders via an offscreen raster: one pixel per cell, painted in
// TERRAIN_STYLE flat colours (+ wear trail overlay), then blitted through the
// shared transform each frame. Per-cell work happens ONLY in makeTerrainRaster
// — the caller rebuilds it when the grid object identity changes (the reducer
// replaces the grid on every terrain_delta merge).

export interface TerrainRaster {
  canvas: CanvasImageSource
  grid: TerrainGrid // the grid this raster was built from (identity check)
}

type DocLike = Pick<Document, 'createElement'>

export function makeTerrainRaster(
  grid: TerrainGrid,
  doc: DocLike | null = typeof document !== 'undefined' ? document : null,
): TerrainRaster | null {
  if (!doc) return null
  const canvas = doc.createElement('canvas') as HTMLCanvasElement
  canvas.width = grid.w
  canvas.height = grid.h
  const ctx = canvas.getContext('2d')
  if (!ctx) return null

  for (let y = 0; y < grid.h; y++) {
    for (let x = 0; x < grid.w; x++) {
      ctx.fillStyle = TERRAIN_STYLE[grid.terrain[y * grid.w + x]] ?? TERRAIN_DEFAULT
      ctx.fillRect(x, y, 1, 1)
    }
  }
  // Wear trails (desire paths): overlay alpha ∝ wear on top of the base colour.
  if (grid.wear) {
    for (let i = 0; i < grid.wear.length; i++) {
      const w = grid.wear[i]
      if (w <= 0) continue
      ctx.fillStyle = `rgba(${WEAR_TRAIL_COLOR},${Math.min(0.7, w).toFixed(3)})`
      ctx.fillRect(i % grid.w, Math.floor(i / grid.w), 1, 1)
    }
  }
  return { canvas, grid }
}

// drawTerrain only blits (no per-cell work). Crisp cell edges: smoothing off.
// grid === null ⇒ draws nothing (env-off neutrality; no decorative fallback).
export function drawTerrain(
  ctx: CanvasRenderingContext2D,
  grid: TerrainGrid | null,
  tr: Transform,
  raster: TerrainRaster | null,
) {
  if (!grid || !raster) return
  const x0 = wx(0, tr)
  const y0 = wy(0, tr)
  const wPx = grid.w * grid.cellSize * tr.sx
  const hPx = grid.h * grid.cellSize * tr.sy
  ctx.save()
  ctx.imageSmoothingEnabled = false
  ctx.drawImage(raster.canvas, x0, y0, wPx, hPx)
  ctx.restore()
}
