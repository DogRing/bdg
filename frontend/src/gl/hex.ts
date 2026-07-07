// Flat-top odd-q hex math for the WebGL layer, MIRRORING engine/space/navmap hex.go
// (navmap is the authority — the frontend never hardcodes the convention elsewhere).
// Kept separate from render/terrain.ts (that file is the pure-2D raster path); this
// one adds the pixel→cell inverse the 3D layer needs to seat entities on the terrain.

const SQRT3 = Math.sqrt(3)

// World centre of offset(col,row) for a flat-top odd-q grid whose offset(0,0) centre
// is world (0,0). EXACTLY navmap's offsetToAxial + hexToPixel (matches render/terrain.ts).
export function hexCentre(col: number, row: number, size: number): { x: number; y: number } {
  const q = col
  const r = row - (col + (col & 1)) / 2
  return { x: size * 1.5 * q, y: size * (SQRT3 / 2 * q + SQRT3 * r) }
}

// world (x,y) → nearest offset (col,row): inverse of hexCentre (axial + cube rounding).
export function pixelToOffset(x: number, y: number, size: number): { col: number; row: number } {
  const q = (2 / 3) * (x / size)
  const r = (-1 / 3) * (x / size) + (SQRT3 / 3) * (y / size)
  // cube round (x=q, z=r, y=-x-z)
  const cy = -q - r
  let rx = Math.round(q), ry = Math.round(cy), rz = Math.round(r)
  const dx = Math.abs(rx - q), dy = Math.abs(ry - cy), dz = Math.abs(rz - r)
  if (dx > dy && dx > dz) rx = -ry - rz
  else if (dy > dz) rz = -rx - ry // (ry unused after this)
  return { col: rx, row: rz + (rx + (rx & 1)) / 2 }
}

// The 6 hex edges' axial neighbour steps (dq,dr), ordered to match the prism template's
// edge midpoints (edge i midpoint direction = (i+0.5)·60°). Edge i's wall drops toward
// this neighbour, and (i+3)%6 is its reciprocal (so shared walls collapse — no z-fight).
export const HEX_DIRS: ReadonlyArray<readonly [number, number]> = [
  [1, 0], [0, 1], [-1, 1], [-1, 0], [0, -1], [1, -1],
]

export function neighbourOffset(col: number, row: number, k: number): { col: number; row: number } {
  const ar = row - (col + (col & 1)) / 2
  const nc = col + HEX_DIRS[k][0]
  const nr = ar + HEX_DIRS[k][1]
  return { col: nc, row: nr + (nc + (nc & 1)) / 2 }
}
