import type { AgentPos, RenderConfig } from '../types'
import { buildTransform, toWorld } from './transform'
import { displayPos, type Interpolable } from './animator'

// Interactive top-view camera (docs/plans/frontend.md Q6): anchored to the world
// bounds at load, then wheel zoom (cursor-anchored), drag pan, click-to-follow.
// All reducers are pure (cam, input) → new cam; the component owns the instance.

export interface EntityRef { kind: 'agent' | 'animal'; id: string }

export interface CameraState {
  center: AgentPos   // world coords under the viewport centre
  zoom: number       // px per world unit, clamped [ZOOM_MIN, ZOOM_MAX]
  follow: EntityRef | null
}

export const ZOOM_MIN = 0.2
export const ZOOM_MAX = 64

const clampZoom = (z: number) => Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, z))

// initialCamera fits the view once: to RenderConfig.bounds when known, else to
// the bbox of the entities we have so far. Returns null when neither exists yet
// (caller retries next frame); after init only user input / follow moves it.
export function initialCamera(
  render: RenderConfig | null,
  pts: Array<{ pos: AgentPos }>,
  viewW: number,
  viewH: number,
): CameraState | null {
  if (viewW <= 0 || viewH <= 0) return null

  if (render) {
    const { min, max } = render.bounds
    const w = Math.max(max.x - min.x, 1)
    const h = Math.max(max.y - min.y, 1)
    return {
      center: { x: (min.x + max.x) / 2, y: (min.y + max.y) / 2 },
      zoom: clampZoom(Math.min(viewW / w, viewH / h) * 0.95),
      follow: null,
    }
  }

  if (pts.length === 0) return null
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity
  for (const p of pts) {
    minX = Math.min(minX, p.pos.x); maxX = Math.max(maxX, p.pos.x)
    minY = Math.min(minY, p.pos.y); maxY = Math.max(maxY, p.pos.y)
  }
  // Minimum span guards the degenerate "everything at one point" case.
  const PAD = 60
  const rangeX = Math.max(maxX - minX, 10)
  const rangeY = Math.max(maxY - minY, 10)
  return {
    center: { x: (minX + maxX) / 2, y: (minY + maxY) / 2 },
    zoom: clampZoom(Math.min((viewW - PAD * 2) / rangeX, (viewH - PAD * 2) / rangeY)),
    follow: null,
  }
}

// cameraZoom scales around the cursor: the world point under the cursor stays
// under the cursor after the zoom (solve the new centre for that).
export function cameraZoom(
  cam: CameraState,
  cursorPx: AgentPos,
  factor: number,
  viewW: number,
  viewH: number,
): CameraState {
  const zoom = clampZoom(cam.zoom * factor)
  if (zoom === cam.zoom) return cam
  const under = toWorld(cursorPx.x, cursorPx.y, buildTransform(cam, viewW, viewH))
  return {
    ...cam,
    zoom,
    center: {
      x: under.x + (viewW / 2 - cursorPx.x) / zoom,
      y: under.y + (viewH / 2 - cursorPx.y) / zoom,
    },
  }
}

// cameraPan shifts the view by a pointer delta (px). Manual pan breaks follow.
export function cameraPan(cam: CameraState, dxPx: number, dyPx: number): CameraState {
  return {
    center: { x: cam.center.x - dxPx / cam.zoom, y: cam.center.y - dyPx / cam.zoom },
    zoom: cam.zoom,
    follow: null,
  }
}

export function cameraFollow(cam: CameraState, target: EntityRef | null): CameraState {
  return { ...cam, follow: target }
}

// cameraTick re-centres on the followed entity each frame — on its
// *interpolated* position (animator, plan Q3) so the follow glides with the
// tween. When the entity is gone (despawned; a death fx may still be playing)
// the follow clears and the camera stays where it is.
export function cameraTick(
  cam: CameraState,
  agents: Iterable<{ id: string; pos: AgentPos } & Interpolable>,
  animals: Iterable<{ id: string; pos: AgentPos } & Interpolable>,
  clockMs = 0,
): CameraState {
  if (!cam.follow) return cam
  const pool = cam.follow.kind === 'agent' ? agents : animals
  for (const e of pool) {
    if (e.id === cam.follow.id) {
      const pos = displayPos(e, clockMs)
      if (pos.x === cam.center.x && pos.y === cam.center.y) return cam
      return { ...cam, center: { x: pos.x, y: pos.y } }
    }
  }
  return { ...cam, follow: null }
}
