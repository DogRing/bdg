// WebGL curved-world hex renderer — the 3D counterpart to the pure-2D src/render layer.
// It is intentionally stateful (GL context, camera, a continuous loop) so it lives OUTSIDE
// src/render (whose SPEC mandates purity). It consumes the SAME reduced world data the 2D
// canvas does — terrain grid + live agents/animals + climate (atmosphere: day/night, weather,
// wind — gl/atmosphere.ts) — and owns two stacked canvases: a WebGL
// canvas for the terrain, and a 2D overlay for entity markers/labels (projected through the
// same view→curve→projection path as the shader). See src/gl/SPEC.md.

import type { TerrainGrid, AgentState, AnimalState, WorldObject, PlantState, RenderConfig, ClimateState } from '../types'
import { FAUNA_SHEETS, DEFAULT_FAUNA, poseFor, floraSeason, variantRow, isFloraSpecies, type FloraSeason } from '../assets/manifest'
import { frameRect, type SpriteCache } from '../assets/sprites'
import { hexCentre, pixelToOffset, neighbourOffset } from './hex'
import { createAtmosphere, drawRainOverlay, drawSnowOverlay, drawWindArrow, LEGACY, type Atmo } from './atmosphere'
import { TILE_VS, TILE_FS, SKY_VS, SKY_FS } from './shaders'

const FOV = 50 * Math.PI / 180, TANF = Math.tan(FOV / 2)
const DEG = Math.PI / 180

// terrain id → atlas tile index (0 grass,1 sand,2 stone,3 water,4 dirt) and top elevation
// (fraction of a hex radius). Water MUST map to 3 (the shader's ripple/shimmer test). Unknown
// ids fall back to grass — open content renders, never throws.
const TILE_INDEX: Record<string, number> = {
  soil: 0, plain: 0, grass: 0, forest: 0, sand: 1,
  mountain: 2, steep: 2, bare_rock: 2, stone: 2,
  river: 3, sea: 3, lake: 3, water: 3, swamp: 4,
  dry_dirt: 5,
}
const ELEV_FRAC: Record<string, number> = {
  soil: 0.55, plain: 0.55, grass: 0.55, forest: 0.72, swamp: 0.35, sand: 0.24,
  mountain: 1.6, steep: 1.4, bare_rock: 1.1, stone: 1.4,
  river: 0.0, sea: 0.0, lake: 0.0, water: 0.0,
  dry_dirt: 0.55,
}
const SKIRT_FRAC = -0.30
// Per-cell relief mapping (gl/SPEC.md): a grid carrying `elevation[]` (generated worlds,
// /api/terrain) extrudes each hex to ELEV_BASE + e·ELEV_SPAN hex-radii from ITS OWN
// e ∈ [0,1]; without it, the per-type ELEV_FRAC table keeps the pre-elevation look.
const ELEV_BASE = 0.06, ELEV_SPAN = 2.1   // real relief (taller peaks / deeper valleys)
// Curved-world bend + height-expression tuning (gl/SPEC.md "Curved / rolling world" + "Height ↔ tilt").
const CURVE_AMT = 0.06                     // rolling-world roll-off strength (uCurv = CURVE_AMT/dist)
const RELIEF_MIN = 0.85, RELIEF_GAIN = 0.5 // relief = MIN + GAIN·smoothstep(pitch) — height exaggeration
const tileIdx = (id: string) => TILE_INDEX[id] ?? 0
const elevFrac = (id: string) => ELEV_FRAC[id] ?? 0.55
const cellElevFrac = (grid: TerrainGrid, idx: number): number =>
  grid.elevation ? ELEV_BASE + grid.elevation[idx] * ELEV_SPAN : elevFrac(grid.terrain[idx])
const OBJECT_COLOR: Record<string, string> = { berry_bush: '#4a9030', water_source: '#3a86d0', shelter: '#9a6a3a', berry: '#4a9030', water: '#3a86d0' }

const clamp = (v: number, a: number, b: number) => Math.max(a, Math.min(b, v))
const smoothstep = (a: number, b: number, x: number) => { const t = clamp((x - a) / (b - a), 0, 1); return t * t * (3 - 2 * t) }

// ---- tiny mat4 (column-major) ----
type M = Float32Array
function perspective(o: M, fovy: number, asp: number, near: number, far: number) {
  const f = 1 / Math.tan(fovy / 2), nf = 1 / (near - far)
  o[0] = f / asp; o[1] = 0; o[2] = 0; o[3] = 0; o[4] = 0; o[5] = f; o[6] = 0; o[7] = 0
  o[8] = 0; o[9] = 0; o[10] = (far + near) * nf; o[11] = -1; o[12] = 0; o[13] = 0; o[14] = 2 * far * near * nf; o[15] = 0
}
function lookAt(o: M, e: number[], c: number[], up: number[]) {
  let z0 = e[0] - c[0], z1 = e[1] - c[1], z2 = e[2] - c[2]; let l = 1 / Math.hypot(z0, z1, z2); z0 *= l; z1 *= l; z2 *= l
  let x0 = up[1] * z2 - up[2] * z1, x1 = up[2] * z0 - up[0] * z2, x2 = up[0] * z1 - up[1] * z0; l = Math.hypot(x0, x1, x2)
  if (l) { l = 1 / l; x0 *= l; x1 *= l; x2 *= l }
  const y0 = z1 * x2 - z2 * x1, y1 = z2 * x0 - z0 * x2, y2 = z0 * x1 - z1 * x0
  o[0] = x0; o[1] = y0; o[2] = z0; o[3] = 0; o[4] = x1; o[5] = y1; o[6] = z1; o[7] = 0; o[8] = x2; o[9] = y2; o[10] = z2; o[11] = 0
  o[12] = -(x0 * e[0] + x1 * e[1] + x2 * e[2]); o[13] = -(y0 * e[0] + y1 * e[1] + y2 * e[2]); o[14] = -(z0 * e[0] + z1 * e[1] + z2 * e[2]); o[15] = 1
}

function animPos(a: AnimalState, clockMs: number): { x: number; y: number } {
  if (!a.prevPos || a.frameAtMs == null || a.prevFrameAtMs == null || a.frameAtMs <= a.prevFrameAtMs) return a.pos
  const t = clamp((clockMs - a.prevFrameAtMs) / (a.frameAtMs - a.prevFrameAtMs), 0, 1)
  return { x: a.prevPos.x + (a.pos.x - a.prevPos.x) * t, y: a.prevPos.y + (a.pos.y - a.prevPos.y) * t }
}
const animalColor = (species: string) => FAUNA_SHEETS[species]?.glyphColor ?? DEFAULT_FAUNA.prey.glyphColor

export interface WorldGL {
  ok: true
  setTerrain(grid: TerrainGrid | null): void
  fit(render: RenderConfig | null): void
  draw(agents: AgentState[], animals: Iterable<AnimalState>, objects: WorldObject[], flora: PlantState[], selectedId: string | null, clockMs: number, climate: ClimateState | null): void
  zoomBy(f: number): void
  tiltBy(dRad: number): void
  orbitBy(dRad: number): void
  panBy(dxPx: number, dyPx: number): void
  pick(px: number, py: number): { kind: 'agent' | 'animal'; id: string } | null
  dispose(): void
}

export function createWorldGL(glc: HTMLCanvasElement, ovc: HTMLCanvasElement, sprites: SpriteCache | null = null): WorldGL | { ok: false; error: string } {
  // Rebind the null-checked contexts to fresh consts: control-flow narrowing of the
  // original bindings does NOT carry into the nested draw/overlay closures, but a const
  // that captured the narrowed value keeps its non-null type everywhere.
  const glCtx = glc.getContext('webgl', { antialias: true, alpha: false })
  if (!glCtx) return { ok: false, error: 'WebGL unavailable' }
  const gl = glCtx
  const extRaw = gl.getExtension('ANGLE_instanced_arrays')
  if (!extRaw) return { ok: false, error: 'ANGLE_instanced_arrays unavailable' }
  const ext = extRaw
  const octxRaw = ovc.getContext('2d')
  if (!octxRaw) return { ok: false, error: '2D overlay context unavailable' }
  const octx = octxRaw

  const compile = (type: number, src: string) => {
    const s = gl.createShader(type)!; gl.shaderSource(s, src); gl.compileShader(s)
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(s) || 'shader compile failed')
    return s
  }
  const program = (vs: string, fs: string) => {
    const p = gl.createProgram()!; gl.attachShader(p, compile(gl.VERTEX_SHADER, vs)); gl.attachShader(p, compile(gl.FRAGMENT_SHADER, fs)); gl.linkProgram(p)
    if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(p) || 'link failed')
    return p
  }
  let tileP: WebGLProgram, skyP: WebGLProgram
  try { tileP = program(TILE_VS, TILE_FS); skyP = program(SKY_VS, SKY_FS) }
  catch (e) { return { ok: false, error: String((e as Error).message || e) } }

  const AL = (p: WebGLProgram, n: string) => gl.getAttribLocation(p, n)
  const UL = (p: WebGLProgram, n: string) => gl.getUniformLocation(p, n)
  const T = {
    aLocal: AL(tileP, 'aLocal'), aTop: AL(tileP, 'aTop'), aFace: AL(tileP, 'aFace'), aNormal: AL(tileP, 'aNormal'), aEdge: AL(tileP, 'aEdge'),
    iCenter: AL(tileP, 'iCenter'), iType: AL(tileP, 'iType'), iElev: AL(tileP, 'iElev'), iNbrA: AL(tileP, 'iNbrA'), iNbrB: AL(tileP, 'iNbrB'),
    uProj: UL(tileP, 'uProj'), uView: UL(tileP, 'uView'), uCurv: UL(tileP, 'uCurv'), uRelief: UL(tileP, 'uRelief'),
    uLight: UL(tileP, 'uLight'), uTime: UL(tileP, 'uTime'), uHexR: UL(tileP, 'uHexR'), uRipple: UL(tileP, 'uRipple'),
    uCell: UL(tileP, 'uCell'), uCols: UL(tileP, 'uCols'), uTex: UL(tileP, 'uTex'), uFog: UL(tileP, 'uFog'),
    uFogNear: UL(tileP, 'uFogNear'), uFogFar: UL(tileP, 'uFogFar'),
    uAmbI: UL(tileP, 'uAmbI'), uDiffI: UL(tileP, 'uDiffI'), uTint: UL(tileP, 'uTint'),
    uWindDir: UL(tileP, 'uWindDir'), uWindSpd: UL(tileP, 'uWindSpd'),
    uCloud: UL(tileP, 'uCloud'), uCloudOff: UL(tileP, 'uCloudOff'), uCloudScale: UL(tileP, 'uCloudScale'),
  }
  const S = {
    aP: AL(skyP, 'aP'), uZen: UL(skyP, 'uZen'), uHor: UL(skyP, 'uHor'),
    uCamF: UL(skyP, 'uCamF'), uCamR: UL(skyP, 'uCamR'), uCamU: UL(skyP, 'uCamU'),
    uTanF: UL(skyP, 'uTanF'), uAspect: UL(skyP, 'uAspect'), uStar: UL(skyP, 'uStar'), uTime: UL(skyP, 'uTime'),
    uSunDir: UL(skyP, 'uSunDir'), uSunCol: UL(skyP, 'uSunCol'), uMoonDir: UL(skyP, 'uMoonDir'), uMoonCol: UL(skyP, 'uMoonCol'),
  }
  const atmo = createAtmosphere()

  // prism template: 6-tri top fan + 6 side walls. per vertex [x,z,aTop,aFace,nx,ny,nz,aEdge] (8f, stride 32)
  const tpl: number[] = []
  const CORNER = (i: number): [number, number] => [Math.cos(i * Math.PI / 3), Math.sin(i * Math.PI / 3)]
  const PV = (p: [number, number], top: number, face: number, edge: number, nx: number, ny: number, nz: number) => tpl.push(p[0], p[1], top, face, nx, ny, nz, edge)
  for (let i = 0; i < 6; i++) { const a = CORNER(i), b = CORNER((i + 1) % 6); PV([0, 0], 1, 0, 0, 0, 1, 0); PV(a, 1, 0, 0, 0, 1, 0); PV(b, 1, 0, 0, 0, 1, 0) }
  for (let i = 0; i < 6; i++) {
    const a = CORNER(i), b = CORNER((i + 1) % 6); let nx = a[0] + b[0], nz = a[1] + b[1]; const l = Math.hypot(nx, nz) || 1; nx /= l; nz /= l
    PV(a, 1, 1, i, nx, 0, nz); PV(b, 1, 1, i, nx, 0, nz); PV(a, 0, 1, i, nx, 0, nz)
    PV(b, 1, 1, i, nx, 0, nz); PV(b, 0, 1, i, nx, 0, nz); PV(a, 0, 1, i, nx, 0, nz)
  }
  const TPL_VERTS = tpl.length / 8
  const tplBuf = gl.createBuffer()!; gl.bindBuffer(gl.ARRAY_BUFFER, tplBuf); gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(tpl), gl.STATIC_DRAW)
  const skyBuf = gl.createBuffer()!; gl.bindBuffer(gl.ARRAY_BUFFER, skyBuf); gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW)
  const instBuf = gl.createBuffer()!
  let instCount = 0

  // texture atlas (3 cols × 512). 1×1 placeholder until the top-face art loads.
  const ACOLS = 3, CELL = 512
  const atlas = document.createElement('canvas'); atlas.width = 1536; atlas.height = 1024
  const actx = atlas.getContext('2d')!
  const tex = gl.createTexture()!
  const texParams = () => {
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR); gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE); gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
  }
  gl.bindTexture(gl.TEXTURE_2D, tex)
  gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, 1, 1, 0, gl.RGBA, gl.UNSIGNED_BYTE, new Uint8Array([126, 158, 120, 255])); texParams()
  const TILES = ['grass', 'sand', 'stone', 'water', 'dirt', 'dry_dirt']
  const imgs: HTMLImageElement[] = []
  let imgDone = 0, imgOk = 0
  TILES.forEach((k, i) => {
    const im = new Image()
    im.onload = () => { actx.drawImage(im, (i % ACOLS) * CELL, ((i / ACOLS) | 0) * CELL, CELL, CELL); imgOk++; onImg() }
    im.onerror = () => onImg()
    im.src = `/assets/tiles/base/${k}_top.png`
    imgs.push(im)
  })
  function onImg() {
    if (++imgDone < TILES.length) return
    if (imgOk > 0) { gl.bindTexture(gl.TEXTURE_2D, tex); gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false); gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, atlas); texParams() }
  }

  // ---- world + camera state ----
  let cellSize = 12
  let elevAt: Float32Array | null = null   // world elevation per offset cell (entity seating)
  let gCols = 0, gRows = 0
  let bMinX = 0, bMinY = 0, bMaxX = 0, bMaxY = 0
  let fx = 0, fz = 0, yaw = 0, pitch = 42 * DEG, dist = 200
  let fitDist = 200, minDist = 20, maxDist = 600
  let lastAgents: AgentState[] = [], lastAnimals: AnimalState[] = []
  const proj = new Float32Array(16), view = new Float32Array(16)
  let cssW = 800, cssH = 480, relief = 1

  function setTerrain(grid: TerrainGrid | null) {
    if (!grid) { instCount = 0; elevAt = null; return }
    cellSize = grid.cellSize; gCols = grid.cols; gRows = grid.rows
    const cols = grid.cols, rows = grid.rows, N = cols * rows
    const data = new Float32Array(N * 10); elevAt = new Float32Array(N)
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity
    for (let row = 0; row < rows; row++) for (let col = 0; col < cols; col++) {
      const idx = row * cols + col, id = grid.terrain[idx], c = hexCentre(col, row, cellSize)
      const elev = cellElevFrac(grid, idx) * cellSize; elevAt[idx] = elev
      const o = idx * 10
      data[o] = c.x; data[o + 1] = c.y; data[o + 2] = tileIdx(id); data[o + 3] = elev
      for (let k = 0; k < 6; k++) {
        const nb = neighbourOffset(col, row, k)
        const inb = nb.col >= 0 && nb.col < cols && nb.row >= 0 && nb.row < rows
        data[o + 4 + k] = (inb ? cellElevFrac(grid, nb.row * cols + nb.col) : SKIRT_FRAC) * cellSize
      }
      if (c.x < minX) minX = c.x; if (c.x > maxX) maxX = c.x; if (c.y < minY) minY = c.y; if (c.y > maxY) maxY = c.y
    }
    bMinX = minX - cellSize; bMaxX = maxX + cellSize; bMinY = minY - cellSize; bMaxY = maxY + cellSize
    instCount = N
    gl.bindBuffer(gl.ARRAY_BUFFER, instBuf); gl.bufferData(gl.ARRAY_BUFFER, data, gl.STATIC_DRAW)
  }

  function fit(render: RenderConfig | null) {
    let cx: number, cy: number, span: number
    if (render?.bounds) { const b = render.bounds; cx = (b.min.x + b.max.x) / 2; cy = (b.min.y + b.max.y) / 2; span = Math.max(b.max.x - b.min.x, b.max.y - b.min.y) }
    else if (elevAt) { cx = (bMinX + bMaxX) / 2; cy = (bMinY + bMaxY) / 2; span = Math.max(bMaxX - bMinX, bMaxY - bMinY) }
    else return
    if (!(span > 0)) span = cellSize * 12
    fx = cx; fz = cy; fitDist = span / (2 * TANF) * 1.15
    dist = fitDist; minDist = fitDist * 0.12; maxDist = fitDist * 2.6
  }

  function elevWorldAt(x: number, y: number): number {
    if (!elevAt) return 0
    const { col, row } = pixelToOffset(x, y, cellSize)
    if (col < 0 || col >= gCols || row < 0 || row >= gRows) return 0
    return elevAt[row * gCols + col]
  }

  function project3(wx: number, wy: number, wz: number): { x: number; y: number; depth: number } | null {
    const vx = view[0] * wx + view[4] * wy + view[8] * wz + view[12]
    const vy = view[1] * wx + view[5] * wy + view[9] * wz + view[13]
    const vz = view[2] * wx + view[6] * wy + view[10] * wz + view[14]
    const by = vy - (CURVE_AMT / dist) * (vz * vz)
    const cw = proj[3] * vx + proj[7] * by + proj[11] * vz + proj[15]
    if (cw <= 0.0001) return null
    const cx = proj[0] * vx + proj[4] * by + proj[8] * vz + proj[12]
    const cyy = proj[1] * vx + proj[5] * by + proj[9] * vz + proj[13]
    return { x: (cx / cw * 0.5 + 0.5) * cssW, y: (1 - (cyy / cw * 0.5 + 0.5)) * cssH, depth: -vz }
  }

  function draw(agents: AgentState[], animals: Iterable<AnimalState>, objects: WorldObject[], flora: PlantState[], selectedId: string | null, clockMs: number, climate: ClimateState | null) {
    lastAgents = agents; lastAnimals = Array.from(animals)
    cssW = glc.clientWidth || 800; cssH = glc.clientHeight || 480
    const dprPix = Math.min(window.devicePixelRatio || 1, 2)
    const bw = Math.round(cssW * dprPix), bh = Math.round(cssH * dprPix)
    if (glc.width !== bw || glc.height !== bh) { glc.width = bw; glc.height = bh }
    if (ovc.width !== bw || ovc.height !== bh) { ovc.width = bw; ovc.height = bh }
    gl.viewport(0, 0, bw, bh)
    const atm: Atmo = climate ? atmo.update(climate, clockMs) : LEGACY
    gl.clearColor(atm.hor[0], atm.hor[1], atm.hor[2], 1); gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)

    // camera (basis needed by the ray-based sky, matrices by the terrain pass)
    perspective(proj, FOV, bw / bh, Math.max(0.5, dist * 0.02), dist * 6)
    const cp = Math.cos(pitch), sp = Math.sin(pitch), cy = Math.cos(yaw), sy = Math.sin(yaw)
    lookAt(view, [fx + dist * cp * sy, dist * sp, fz + dist * cp * cy], [fx, 0, fz], [0, 1, 0])
    relief = RELIEF_MIN + RELIEF_GAIN * smoothstep(12 * DEG, 52 * DEG, pitch)

    // sky (no depth): gradient + sun/moon discs + stars, all keyed to the atmosphere
    gl.disable(gl.DEPTH_TEST); gl.depthMask(false); gl.useProgram(skyP)
    gl.bindBuffer(gl.ARRAY_BUFFER, skyBuf); gl.enableVertexAttribArray(S.aP); gl.vertexAttribPointer(S.aP, 2, gl.FLOAT, false, 0, 0)
    gl.uniform3fv(S.uZen, atm.zen); gl.uniform3fv(S.uHor, atm.hor)
    gl.uniform3f(S.uCamF, -cp * sy, -sp, -cp * cy); gl.uniform3f(S.uCamR, cy, 0, -sy); gl.uniform3f(S.uCamU, -sp * sy, cp, -sp * cy)
    gl.uniform1f(S.uTanF, TANF); gl.uniform1f(S.uAspect, bw / bh); gl.uniform1f(S.uStar, atm.star); gl.uniform1f(S.uTime, clockMs * 0.001)
    gl.uniform3fv(S.uSunDir, atm.sunDir); gl.uniform3fv(S.uSunCol, atm.sunCol)
    gl.uniform3fv(S.uMoonDir, atm.moonDir); gl.uniform3fv(S.uMoonCol, atm.moonCol)
    gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4)

    // terrain prisms
    const fogN = dist * 1.2 * atm.fogMul, fogF = dist * 3.0 * atm.fogMul
    if (instCount > 0) {
      gl.enable(gl.DEPTH_TEST); gl.depthMask(true); gl.disable(gl.BLEND); gl.disable(gl.CULL_FACE); gl.useProgram(tileP)
      gl.uniformMatrix4fv(T.uProj, false, proj); gl.uniformMatrix4fv(T.uView, false, view)
      gl.uniform1f(T.uCurv, CURVE_AMT / dist); gl.uniform1f(T.uRelief, relief)
      gl.uniform3fv(T.uLight, atm.light); gl.uniform1f(T.uTime, clockMs * 0.001)
      gl.uniform1f(T.uAmbI, atm.ambI); gl.uniform1f(T.uDiffI, atm.diffI); gl.uniform3fv(T.uTint, atm.tint)
      gl.uniform2fv(T.uWindDir, atm.rippleDir); gl.uniform1f(T.uWindSpd, atm.rippleSpd)
      gl.uniform1f(T.uCloud, atm.cloud); gl.uniform2fv(T.uCloudOff, atm.cloudOff); gl.uniform1f(T.uCloudScale, 1 / (cellSize * 6))
      gl.uniform1f(T.uHexR, cellSize); gl.uniform1f(T.uRipple, 0.05 * cellSize)
      gl.uniform2f(T.uCell, CELL / atlas.width, CELL / atlas.height); gl.uniform1f(T.uCols, ACOLS)
      gl.uniform1f(T.uFogNear, fogN); gl.uniform1f(T.uFogFar, fogF); gl.uniform3fv(T.uFog, atm.hor)
      gl.activeTexture(gl.TEXTURE0); gl.bindTexture(gl.TEXTURE_2D, tex); gl.uniform1i(T.uTex, 0)
      gl.bindBuffer(gl.ARRAY_BUFFER, tplBuf)
      va(T.aLocal, 2, 32, 0, 0); va(T.aTop, 1, 32, 8, 0); va(T.aFace, 1, 32, 12, 0); va(T.aNormal, 3, 32, 16, 0); va(T.aEdge, 1, 32, 28, 0)
      gl.bindBuffer(gl.ARRAY_BUFFER, instBuf)
      va(T.iCenter, 2, 40, 0, 1); va(T.iType, 1, 40, 8, 1); va(T.iElev, 1, 40, 12, 1); va(T.iNbrA, 3, 40, 16, 1); va(T.iNbrB, 3, 40, 28, 1)
      ext.drawArraysInstancedANGLE(gl.TRIANGLES, 0, TPL_VERTS, instCount)
    }
    drawOverlay(agents, lastAnimals, objects, flora, floraSeason(climate), selectedId, clockMs, dprPix, atm, fogN, fogF)
  }

  function va(loc: number, size: number, stride: number, offset: number, div: number) {
    if (loc < 0) return
    gl.enableVertexAttribArray(loc); gl.vertexAttribPointer(loc, size, gl.FLOAT, false, stride, offset); ext.vertexAttribDivisorANGLE(loc, div)
  }

  function drawOverlay(agents: AgentState[], animals: Iterable<AnimalState>, objects: WorldObject[], flora: PlantState[], season: FloraSeason, selectedId: string | null, clockMs: number, dprPix: number, atm: Atmo, fogN: number, fogF: number) {
    octx.setTransform(dprPix, 0, 0, dprPix, 0, 0); octx.clearRect(0, 0, cssW, cssH)
    const pscale = cssH / (2 * TANF), lift = 0.3 * cellSize
    const seat = (x: number, y: number) => elevWorldAt(x, y) * relief + lift
    // objects (static resource markers) — a plant kind carries no marker here: it is drawn as a
    // billboard below (the backend object list includes plants; a styleless plant would otherwise
    // render as a bare tan square on top of nothing, since 3D has no 2D coverage wash).
    for (const o of objects) {
      if (isFloraSpecies(o.kind) && !OBJECT_COLOR[o.kind]) continue
      const p = project3(o.pos.x, seat(o.pos.x, o.pos.y), o.pos.y); if (!p) continue
      const fade = 1 - smoothstep(fogN, fogF, p.depth); if (fade <= 0.02) continue
      const s = Math.max(3, 0.4 * cellSize * pscale / p.depth)
      octx.globalAlpha = fade; octx.fillStyle = OBJECT_COLOR[o.kind] ?? '#b09060'
      octx.fillRect(p.x - s / 2, p.y - s / 2, s, s)
      octx.lineWidth = 1; octx.strokeStyle = 'rgba(0,0,0,.4)'; octx.strokeRect(p.x - s / 2, p.y - s / 2, s, s)
    }
    // flora — sprite billboards (grass tufts / trees), bottom-anchored on the seated ground point.
    // 3D has no coverage wash: each plant draws its own stage-column / season-row frame, scaled by a
    // world height from stage + width. An undecoded sheet simply waits (no glyph) — the ground shows.
    for (const plant of flora) {
      const loaded = sprites?.flora(plant.species, season)
      if (!loaded?.ready) continue
      const p = project3(plant.pos.x, seat(plant.pos.x, plant.pos.y), plant.pos.y); if (!p) continue
      const fade = 1 - smoothstep(fogN, fogF, p.depth); if (fade <= 0.02) continue
      const def = loaded.def
      const col = clamp(Math.floor(plant.stage), 0, def.stageFrames - 1)
      const row = def.seasonRows ? (def.seasonRows[season] ?? 0) : variantRow(plant.id, def.variantRows)
      const worldH = Math.min(6, 0.8 + 0.6 * col + 0.8 * plant.width)
      const s = Math.max(6, worldH * pscale / p.depth)
      octx.globalAlpha = fade
      octx.drawImage(loaded.image, col * def.frameW, row * def.frameH, def.frameW, def.frameH, p.x - s / 2, p.y - s, s, s)
    }
    // animals — sprite billboards (FE-P6): the species' fauna sheet frame, bottom-anchored
    // at the seated ground point, mirrored when the world heading points screen-left
    // (sheets face +x; screen angle = world angle + camera yaw, same rule as the wind arrow).
    // No sheet / not decoded yet → the glyph-colour dot exactly as before.
    for (const a of animals) {
      const ap = animPos(a, clockMs); const p = project3(ap.x, seat(ap.x, ap.y), ap.y); if (!p) continue
      const fade = 1 - smoothstep(fogN, fogF, p.depth); if (fade <= 0.02) continue
      const r = Math.max(2.5, 0.45 * cellSize * pscale / p.depth)
      octx.globalAlpha = fade * Math.max(0.35, a.stamina ?? 1)
      const loaded = sprites?.fauna(a.species)
      const rect = loaded?.ready
        ? frameRect(loaded.def, poseFor(a.action), clockMs)
          ?? frameRect(loaded.def, 'walk', clockMs) ?? frameRect(loaded.def, 'idle', clockMs)
        : null
      if (loaded && rect) {
        const s = r * 4
        octx.save()
        octx.translate(p.x, p.y)
        if (Math.cos(a.heading + yaw) < 0) octx.scale(-1, 1)
        octx.drawImage(loaded.image, rect.sx, rect.sy, rect.sw, rect.sh, -s / 2, -s * 0.9, s, s)
        octx.restore()
        continue
      }
      octx.beginPath(); octx.arc(p.x, p.y, r, 0, 7); octx.fillStyle = animalColor(a.species); octx.fill()
      octx.lineWidth = Math.max(1, r * 0.22); octx.strokeStyle = 'rgba(0,0,0,.45)'; octx.stroke()
    }
    // agents (dot + selection ring)
    for (const a of agents) {
      const p = project3(a.pos.x, seat(a.pos.x, a.pos.y), a.pos.y); if (!p) continue
      const fade = 1 - smoothstep(fogN, fogF, p.depth); if (fade <= 0.02) continue
      const r = Math.max(3, 0.5 * cellSize * pscale / p.depth); const sel = a.id === selectedId
      octx.globalAlpha = fade
      octx.beginPath(); octx.arc(p.x, p.y, r, 0, 7); octx.fillStyle = sel ? '#ffd24a' : '#ecdfbf'; octx.fill()
      octx.lineWidth = Math.max(1.2, r * 0.28); octx.strokeStyle = sel ? '#8a5a10' : 'rgba(30,26,20,.7)'; octx.stroke()
      if (sel) { octx.beginPath(); octx.arc(p.x, p.y, r + 4, 0, 7); octx.lineWidth = 2; octx.strokeStyle = '#ffd24a'; octx.stroke() }
    }
    octx.globalAlpha = 1
    // atmosphere overlays: rain streaks OR snow flakes drifted downwind (screen-space, CS1) + wind
    // HUD arrow. Screen wind angle = world windDir + camera yaw (world +x → screen-right at yaw 0).
    const screenDrift = Math.cos(atm.windDir + yaw) * atm.windMag * 0.9
    drawRainOverlay(octx, cssW, cssH, atm.rain, screenDrift, clockMs)
    drawSnowOverlay(octx, cssW, cssH, atm.snow, screenDrift, clockMs)
    drawWindArrow(octx, atm.windDir + yaw, atm.windMag)
  }

  // ---- interaction ----
  function pick(px: number, py: number): { kind: 'agent' | 'animal'; id: string } | null {
    let best: { kind: 'agent' | 'animal'; id: string } | null = null, bestD = 16 * 16
    lastAgents.forEach(a => { const p = project3(a.pos.x, elevWorldAt(a.pos.x, a.pos.y) * relief + 0.3 * cellSize, a.pos.y); if (!p) return; const d = (p.x - px) ** 2 + (p.y - py) ** 2; if (d < bestD) { bestD = d; best = { kind: 'agent', id: a.id } } })
    if (best) return best
    lastAnimals.forEach(a => { const ap = a.pos; const p = project3(ap.x, elevWorldAt(ap.x, ap.y) * relief + 0.3 * cellSize, ap.y); if (!p) return; const d = (p.x - px) ** 2 + (p.y - py) ** 2; if (d < bestD) { bestD = d; best = { kind: 'animal', id: a.id } } })
    return best
  }

  function panBy(dxPx: number, dyPx: number) {
    const wpp = (2 * dist * TANF) / (cssW || 1)
    const rgt = [Math.cos(yaw), -Math.sin(yaw)], fwd = [-Math.sin(yaw), -Math.cos(yaw)]
    fx -= rgt[0] * dxPx * wpp; fz -= rgt[1] * dxPx * wpp
    fx += fwd[0] * dyPx * wpp; fz += fwd[1] * dyPx * wpp
    const m = cellSize * 4
    fx = clamp(fx, bMinX - m, bMaxX + m); fz = clamp(fz, bMinY - m, bMaxY + m)
  }

  function dispose() {
    imgs.forEach(im => { im.onload = null; im.onerror = null })
    gl.deleteBuffer(tplBuf); gl.deleteBuffer(skyBuf); gl.deleteBuffer(instBuf); gl.deleteTexture(tex)
    gl.deleteProgram(tileP); gl.deleteProgram(skyP)
  }

  return {
    ok: true, setTerrain, fit, draw, pick, panBy, dispose,
    zoomBy: (f) => { dist = clamp(dist * f, minDist, maxDist) },
    tiltBy: (d) => { pitch = clamp(pitch + d, 8 * DEG, 85 * DEG) },
    orbitBy: (d) => { yaw += d },
  }
}
