import { describe, it, expect } from 'vitest'
import { groundForward, cameraGroundOffset } from './cameraGeom'

// Coordinate convention under test (see cameraGeom.ts):
//   • GL ground plane is (x, z); world +y maps to GL +z, so `z` here == world y.
//   • yaw orbits the camera about its focus; groundForward is the direction the camera looks along
//     the ground; cameraGroundOffset is the eye's ground offset from the focus.
// These are the SAME functions worldGL.ts feeds into `lookAt` (eye) and the ray-sky camera basis, and
// components/minimapGeom.ts feeds into the minimap cone — so this file is the regression guard that
// keeps the 3D camera and the 2D minimap marker on ONE heading-sign convention. Do not weaken.

describe('groundForward(yaw) — unit ground-plane look direction', () => {
  it('yaw 0 → (0, -1): camera south of focus, looking toward -z (== -world y, "north"/up-screen)', () => {
    const f = groundForward(0)
    expect(f.x).toBeCloseTo(0, 12)
    expect(f.z).toBeCloseTo(-1, 12)
  })
  it('yaw +π/2 → (-1, 0)', () => {
    const f = groundForward(Math.PI / 2)
    expect(f.x).toBeCloseTo(-1, 12)
    expect(f.z).toBeCloseTo(0, 12)
  })
  it('yaw π → (0, +1)', () => {
    const f = groundForward(Math.PI)
    expect(f.x).toBeCloseTo(0, 12)
    expect(f.z).toBeCloseTo(1, 12)
  })
  it('yaw -π/2 → (+1, 0)', () => {
    const f = groundForward(-Math.PI / 2)
    expect(f.x).toBeCloseTo(1, 12)
    expect(f.z).toBeCloseTo(0, 12)
  })
  it('is a unit vector at arbitrary yaw', () => {
    for (const yaw of [0.3, 1.1, 2.7, -0.9, 5.5]) {
      const f = groundForward(yaw)
      expect(Math.hypot(f.x, f.z)).toBeCloseTo(1, 12)
    }
  })
})

describe('cameraGroundOffset(yaw, r) — eye ground offset from focus', () => {
  // The CONTRACT: worldGL's lookAt eye is [fx + r·sin yaw, dist·sp, fz + r·cos yaw] with r = dist·cosPitch.
  // cameraGroundOffset MUST reproduce that ground offset exactly (this is what lets worldGL delegate to
  // the shared helper without moving the camera). z == world y.
  const cases: Array<[number, string]> = [
    [0, 'yaw 0'],
    [Math.PI / 2, 'yaw +π/2'],
    [Math.PI, 'yaw π'],
    [-Math.PI / 2, 'yaw -π/2'],
  ]
  const R = 137 // arbitrary planar radius (= dist·cosPitch)

  for (const [yaw, name] of cases) {
    it(`${name}: equals (r·sin yaw, r·cos yaw) — the worldGL eye offset sign`, () => {
      const o = cameraGroundOffset(yaw, R)
      expect(o.x).toBeCloseTo(R * Math.sin(yaw), 10)
      expect(o.z).toBeCloseTo(R * Math.cos(yaw), 10)
    })
    it(`${name}: is exactly -r·groundForward (eye sits opposite the look direction)`, () => {
      const o = cameraGroundOffset(yaw, R)
      const f = groundForward(yaw)
      expect(o.x).toBeCloseTo(-R * f.x, 10)
      expect(o.z).toBeCloseTo(-R * f.z, 10)
    })
  }
})
