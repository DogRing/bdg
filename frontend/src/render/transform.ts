import type { AgentPos } from '../types'
import type { CameraState } from './camera'

// ONE world→canvas mapping per frame — every layer (terrain, flora, objects,
// animals, agents, fx) draws through the same Transform (D11: continuous coords).
export interface Transform { sx: number; sy: number; ox: number; oy: number }

// buildTransform derives the shared mapping from the camera: zoom is px per
// world unit, and the camera centre lands on the viewport centre.
export function buildTransform(cam: CameraState, viewW: number, viewH: number): Transform {
  return {
    sx: cam.zoom,
    sy: cam.zoom,
    ox: viewW / 2 - cam.center.x * cam.zoom,
    oy: viewH / 2 - cam.center.y * cam.zoom,
  }
}

export function wx(worldX: number, tr: Transform): number { return worldX * tr.sx + tr.ox }
export function wy(worldY: number, tr: Transform): number { return worldY * tr.sy + tr.oy }

export function toWorld(px: number, py: number, tr: Transform): AgentPos {
  return { x: (px - tr.ox) / tr.sx, y: (py - tr.oy) / tr.sy }
}
