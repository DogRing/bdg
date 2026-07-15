import type { CameraFocus } from '../gl/worldGL'
import { groundForward } from '../gl/cameraGeom'

// Pure geometry for the Minimap (components/Minimap.tsx) — extracted so the camera-cone direction /
// sizing math is unit-testable without a canvas or a live GL context.

export const MM_MAX_DIM = 190 // px — the minimap's longer side
export const MM_MIN_DIM = 90

// minimapSize: the minimap's CSS px size for a world of `worldW × worldH` — MM_MAX_DIM on the longer
// world axis, the shorter axis scaled by aspect (floored at MM_MIN_DIM). Degenerate world ⇒ square.
export function minimapSize(worldW: number, worldH: number): { w: number; h: number } {
  const aspect = worldW > 0 && worldH > 0 ? worldW / worldH : 1
  return aspect >= 1
    ? { w: MM_MAX_DIM, h: Math.max(MM_MIN_DIM, Math.round(MM_MAX_DIM / aspect)) }
    : { w: Math.max(MM_MIN_DIM, Math.round(MM_MAX_DIM * aspect)), h: MM_MAX_DIM }
}

// The minimap uses an isotropic, positive-scale world→screen transform (buildTransform: sx = sy =
// zoom > 0), so a world direction maps to screen with its angle preserved — the cone angle is a pure
// function of the GL camera yaw, no transform needed.
export const CAMERA_CONE_HALF_ANGLE = 0.5 // rad (~28°)

// cameraCone: the "where the 3D camera looks" marker on the minimap. `angle` is the screen-space
// direction the view opens toward. It reuses `gl/cameraGeom.groundForward` (the SAME yaw convention
// the 3D camera's eye/basis use) so the two can't diverge; that vector is (x, z==world y), and the
// minimap maps world x → screen +x (right) and world y → screen +y (DOWN), so under the isotropic
// positive-scale transform the screen angle is atan2(gf.z, gf.x). yaw 0 points UP the screen (camera
// south, looking north), yaw +π/2 points LEFT. `radius` grows with the camera's framing:
// dist/fitDist == 1 fills ~half the minimap (whole-world framing), zoomed-in shrinks it (floored so
// it never vanishes, capped so it never overflows the box).
export function cameraCone(f: CameraFocus, mmW: number, mmH: number): { angle: number; radius: number; halfAngle: number } {
  const gf = groundForward(f.yaw)
  const angle = Math.atan2(gf.z, gf.x)
  const span = Math.min(mmW, mmH) * 0.45
  const ratio = f.dist / (f.fitDist || f.dist)
  const radius = Math.max(10, Math.min(span, ratio * span))
  return { angle, radius, halfAngle: CAMERA_CONE_HALF_ANGLE }
}
