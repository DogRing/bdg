// Placeholder spritesheet generator — see frontend/dev/SPEC.md.
// Writes deterministic PNG sheets to frontend/public/assets/{fauna,flora}/.
// Layout contract (src/assets/SPEC.md): 32x32 frames, columns = animation frames,
// fauna rows = poses [idle, walk, run, eat, attack, dying], flora = 1 row of growth stages.
// Sprites face +x (east) at heading 0. Zero npm deps: node:zlib deflate + manual PNG chunks.

import { deflateSync } from 'node:zlib'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const OUT = join(dirname(fileURLToPath(import.meta.url)), '..', 'public', 'assets')
const F = 32 // frame size
const COLS = 4
const POSES = ['idle', 'walk', 'run', 'eat', 'attack', 'dying']

// ── minimal PNG encoder (RGBA8, filter 0) ────────────────────────────────────
const CRC_TABLE = new Int32Array(256).map((_, n) => {
  let c = n
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
  return c
})
function crc32(buf) {
  let c = -1
  for (const b of buf) c = CRC_TABLE[(c ^ b) & 0xff] ^ (c >>> 8)
  return (c ^ -1) >>> 0
}
function chunk(type, data) {
  const out = Buffer.alloc(12 + data.length)
  out.writeUInt32BE(data.length, 0)
  out.write(type, 4, 'ascii')
  data.copy(out, 8)
  out.writeUInt32BE(crc32(out.subarray(4, 8 + data.length)), 8 + data.length)
  return out
}
function encodePNG(img) {
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(img.w, 0)
  ihdr.writeUInt32BE(img.h, 4)
  ihdr[8] = 8; ihdr[9] = 6 // 8-bit RGBA
  const raw = Buffer.alloc(img.h * (1 + img.w * 4))
  for (let y = 0; y < img.h; y++)
    Buffer.from(img.data.buffer, y * img.w * 4, img.w * 4).copy(raw, y * (1 + img.w * 4) + 1)
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw, { level: 9 })),
    chunk('IEND', Buffer.alloc(0)),
  ])
}

// ── pixel canvas ─────────────────────────────────────────────────────────────
const makeImg = (w, h) => ({ w, h, data: new Uint8Array(w * h * 4) })
function px(img, x, y, [r, g, b, a = 255]) {
  x |= 0; y |= 0
  if (x < 0 || y < 0 || x >= img.w || y >= img.h) return
  const i = (y * img.w + x) * 4
  img.data[i] = r; img.data[i + 1] = g; img.data[i + 2] = b; img.data[i + 3] = a
}
function ellipse(img, cx, cy, rx, ry, col) {
  for (let y = Math.floor(cy - ry); y <= cy + ry; y++)
    for (let x = Math.floor(cx - rx); x <= cx + rx; x++)
      if ((x - cx) ** 2 / rx ** 2 + (y - cy) ** 2 / ry ** 2 <= 1) px(img, x, y, col)
}
function line(img, x0, y0, x1, y1, col) {
  const n = Math.max(Math.abs(x1 - x0), Math.abs(y1 - y0), 1)
  for (let i = 0; i <= n; i++) px(img, x0 + ((x1 - x0) * i) / n, y0 + ((y1 - y0) * i) / n, col)
}

// ── fauna sheets: draw one frame per (pose, i) into its grid cell ────────────
// Top-down quadruped facing +x: body ellipse, head at +x, 4 legs whose reach
// swings with the gait phase. `p` in [-1,1] is the leg swing for frame i.
function quadruped(img, ox, oy, c, opts) {
  const { body, head, legs, gait, headFwd, bodyRx, alpha = 255 } = opts
  const A = (col) => [col[0], col[1], col[2], alpha]
  const cx = ox + 15, cy = oy + 16
  // legs: front pair (+x) and back pair (-x), left (-y) / right (+y)
  for (const [lx, ly, phase] of [[6, -5, 0], [6, 5, 1], [-6, -5, 1], [-6, 5, 0]]) {
    const p = gait * (phase ? 1 : -1)
    line(img, cx + lx, cy + ly, cx + lx + p * legs, cy + ly + Math.sign(ly) * 2, A(c.leg))
  }
  ellipse(img, cx, cy, bodyRx, 6, A(c.body))                       // body
  line(img, cx - bodyRx, cy, cx - bodyRx - 3, cy - 1, A(c.leg))    // tail
  ellipse(img, cx + bodyRx + headFwd, cy, head, head - 1, A(c.head)) // head
  if (body === 'deer') {                                           // antlers
    const hx = cx + bodyRx + headFwd
    line(img, hx + 1, cy - 3, hx + 4, cy - 7, A(c.horn))
    line(img, hx + 1, cy + 3, hx + 4, cy + 7, A(c.horn))
  }
}
const DEER = { body: [139, 90, 43], head: [160, 82, 45], leg: [92, 64, 51], horn: [210, 180, 140] }
const WOLF = { body: [105, 105, 110], head: [70, 70, 75], leg: [50, 50, 55], horn: [0, 0, 0] }
const GAIT = [0, 1, 0, -1] // 4-frame walk cycle

function faunaFrame(img, species, pose, i, ox, oy) {
  const c = species === 'deer' ? DEER : WOLF
  const base = { body: species, head: 4, legs: 3, gait: 0, headFwd: 2, bodyRx: 9 }
  const o = { ...base }
  if (pose === 'idle') o.gait = i % 2 ? 0.3 : 0 // subtle shift
  if (pose === 'walk') o.gait = GAIT[i]
  if (pose === 'run') Object.assign(o, { gait: GAIT[i] * 1.8, legs: 4, bodyRx: 10 })
  if (pose === 'eat') Object.assign(o, { headFwd: 0, head: i % 2 ? 3 : 4 }) // head down/bob
  if (pose === 'attack') {
    const lunge = [0, 2, 4, 1][i] // forward jab peaking mid-clip
    ox += lunge
    Object.assign(o, { gait: GAIT[i] * 1.5, headFwd: 3, legs: 4 })
    px(img, ox + 15 + o.bodyRx + o.headFwd + 4, oy + 16, [200, 40, 40]) // bite mark
  }
  if (pose === 'dying') {
    o.alpha = 255 - i * 60 // fade across frames
    o.gait = 0
    quadruped(img, ox, oy, c, o)
    line(img, ox + 12, oy + 13, ox + 18, oy + 19, [120, 20, 20, o.alpha]) // X mark
    line(img, ox + 18, oy + 13, ox + 12, oy + 19, [120, 20, 20, o.alpha])
    return
  }
  quadruped(img, ox, oy, c, o)
}

// ── flora sheets: 1 row, frames = growth stages 0..3 ────────────────────────
function floraFrame(img, species, stage, ox, oy) {
  const cx = ox + 16, cy = oy + 16
  if (species === 'tree') {
    const trunk = [101, 67, 33], leaf = [46, 106, 26], hi = [76, 140, 46]
    const r = [2, 4, 7, 10][stage]
    if (stage > 0) line(img, cx, cy + r, cx, cy + r + 4, trunk)
    ellipse(img, cx, cy, r, r, leaf)
    ellipse(img, cx - r / 3, cy - r / 3, r / 2.5, r / 2.5, hi)
    if (stage === 0) line(img, cx, cy + 2, cx, cy + 5, trunk) // sprout stem
  } else {
    const leaf = [90, 154, 42], hi = [120, 180, 70]
    const r = [1.5, 3, 5, 7][stage]
    ellipse(img, cx, cy + 2, r + 1, r, leaf)
    ellipse(img, cx - 1, cy, r / 1.8, r / 2, hi)
  }
}

// ── assemble + write ─────────────────────────────────────────────────────────
// Never overwrite an existing file: these paths carry real art since FE-P6 —
// placeholders only fill gaps. Delete a file first to regenerate it.
function writeSheet(rel, img) {
  const path = join(OUT, rel)
  if (existsSync(path)) {
    console.log(`skip ${rel}  (exists — real art is never overwritten)`)
    return
  }
  mkdirSync(dirname(path), { recursive: true })
  writeFileSync(path, encodePNG(img))
  console.log(`wrote ${rel}  ${img.w}x${img.h}`)
}
for (const species of ['deer', 'wolf']) {
  const img = makeImg(F * COLS, F * POSES.length)
  POSES.forEach((pose, row) => {
    for (let i = 0; i < COLS; i++) faunaFrame(img, species, pose, i, i * F, row * F)
  })
  writeSheet(`fauna/${species}.png`, img)
}
for (const species of ['tree', 'bush']) {
  const img = makeImg(F * COLS, F)
  for (let s = 0; s < COLS; s++) floraFrame(img, species, s, s * F, 0)
  writeSheet(`flora/${species}.png`, img)
}
