// Pure ground-plane camera geometry — the ONE place the `yaw → ground direction` convention lives.
// Imported by both the 3D camera (worldGL.ts: the lookAt eye offset + the ray-sky camera basis) and
// the 2D minimap cone (components/minimapGeom.ts), so the two views can never drift to different sign
// conventions for "which way is the camera looking". No React / DOM / WebGL dependency.
//
// Coordinate convention:
//   • The GL ground plane is (x, z); world +y maps to GL +z (so `z` here == world y).
//   • `yaw` orbits the camera about its focus. The camera eye sits at
//         focus + dist·cosPitch·(sin yaw, cos yaw)   on the ground (worldGL lookAt)
//     and looks AT the focus, so the direction it looks along the ground is
//         forward = focus − eye ∝ −(sin yaw, cos yaw).
//   • (The minimap then maps this ground vector to screen where +x is right and +y is DOWN — see
//     components/minimapGeom.ts `cameraCone`.)

export interface GroundVec { x: number; z: number }

// groundForward: unit ground-plane vector the camera looks along, as a function of yaw.
// yaw 0 → (0, −1): camera south of focus, looking toward −z (== −world y). +π/2 → (−1, 0).
export function groundForward(yaw: number): GroundVec {
  return { x: -Math.sin(yaw), z: -Math.cos(yaw) }
}

// cameraGroundOffset: the eye's ground-plane offset from the focus at a given yaw and planar radius
// (planarRadius = dist·cosPitch). Exactly −planarRadius·groundForward, i.e. (r·sin yaw, r·cos yaw) —
// the sign contract worldGL's `lookAt` eye position depends on (see cameraGeom.test.ts).
export function cameraGroundOffset(yaw: number, planarRadius: number): GroundVec {
  const f = groundForward(yaw)
  return { x: -planarRadius * f.x, z: -planarRadius * f.z }
}
