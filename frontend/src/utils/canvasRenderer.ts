import type { AgentState } from '../types'
import type { ThemeTokens } from '../theme'

// Fixed village terrain layout (matches the design's static map)
const FORESTS = [
  [100, 110, 78, 46], [330, 65, 88, 52], [570, 170, 72, 44], [68, 365, 76, 48],
] as const

const FIELDS = [
  [275, 285, 72, 52], [385, 382, 62, 44], [185, 432, 82, 52],
] as const

const BUILDINGS = [
  { label: 'Village Hall', rx: 0.5, ry: 0.5, w: 36, h: 28 },
  { label: 'Inn',          rx: 0.5 + 66 / 880, ry: 0.5 - 26 / 754, w: 24, h: 19 },
  { label: 'Blacksmith',   rx: 0.5 - 56 / 880, ry: 0.5 + 26 / 754, w: 27, h: 21 },
  { label: 'Granary',      rx: 0.5 + 46 / 880, ry: 0.5 + 47 / 754, w: 25, h: 19 },
] as const

// Role → colour index (matches SPEC: Farmer=0, Merchant=1, Guard=2, Artisan=3)
const ROLE_INDEX: Record<string, number> = {
  farmer: 0, food: 0, satiety: 0, hydration: 0,
  merchant: 1, trade: 1, standing: 1,
  guard: 2, safety: 2, security: 2,
  artisan: 3, blacksmith: 3, craft: 3,
}

function roleIndex(agent: AgentState): number {
  const key = (agent.goal ?? agent.role ?? '').toLowerCase()
  for (const [k, v] of Object.entries(ROLE_INDEX)) {
    if (key.includes(k)) return v
  }
  return 0
}

// World-to-canvas coordinate transform. World: 0–1000, canvas: 0–W×H.
// Auto-fit to bounding box of known agent positions.
function buildTransform(agents: AgentState[], W: number, H: number) {
  if (agents.length === 0) {
    return { sx: W / 1000, sy: H / 1000, ox: 0, oy: 0 }
  }
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity
  for (const a of agents) {
    minX = Math.min(minX, a.pos.x); maxX = Math.max(maxX, a.pos.x)
    minY = Math.min(minY, a.pos.y); maxY = Math.max(maxY, a.pos.y)
  }
  const PAD = 60
  const rangeX = Math.max(maxX - minX, 200)
  const rangeY = Math.max(maxY - minY, 200)
  const sx = (W - PAD * 2) / rangeX
  const sy = (H - PAD * 2) / rangeY
  const scale = Math.min(sx, sy)
  const ox = PAD - minX * scale + ((W - PAD * 2) - rangeX * scale) / 2
  const oy = PAD - minY * scale + ((H - PAD * 2) - rangeY * scale) / 2
  return { sx: scale, sy: scale, ox, oy }
}

function wx(worldX: number, t: { sx: number; ox: number }) { return worldX * t.sx + t.ox }
function wy(worldY: number, t: { sy: number; oy: number }) { return worldY * t.sy + t.oy }

export function drawTerrain(
  ctx: CanvasRenderingContext2D,
  W: number, H: number,
  t: ThemeTokens,
) {
  if (t.glow) {
    drawDarkTerrain(ctx, W, H, t)
  } else {
    drawLightTerrain(ctx, W, H, t)
  }
}

function drawLightTerrain(ctx: CanvasRenderingContext2D, W: number, H: number, t: ThemeTokens) {
  // Parchment gradient
  const grad = ctx.createLinearGradient(0, 0, W, H)
  grad.addColorStop(0, '#dfc898')
  grad.addColorStop(0.5, '#e8d5a3')
  grad.addColorStop(1, '#d8c08a')
  ctx.fillStyle = grad; ctx.fillRect(0, 0, W, H)

  // Grid
  ctx.strokeStyle = t.gridColor; ctx.lineWidth = 0.6
  for (let x = 0; x < W; x += 64) { ctx.beginPath(); ctx.moveTo(x, 0); ctx.lineTo(x, H); ctx.stroke() }
  for (let y = 0; y < H; y += 64) { ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(W, y); ctx.stroke() }

  // River
  ctx.save()
  ctx.strokeStyle = '#7aace080'; ctx.lineWidth = 12; ctx.lineCap = 'round'
  ctx.beginPath(); ctx.moveTo(W * 0.28, 0)
  ctx.quadraticCurveTo(W * 0.32, H * 0.38, W * 0.38, H * 0.58)
  ctx.quadraticCurveTo(W * 0.44, H * 0.78, W * 0.42, H); ctx.stroke()
  ctx.strokeStyle = '#9ec8f040'; ctx.lineWidth = 6; ctx.stroke()
  ctx.restore()

  // Forests
  for (const [fx, fy, rx, ry] of FORESTS) {
    ctx.fillStyle = 'rgba(70,110,42,0.55)'
    ctx.beginPath(); ctx.ellipse(fx, fy, rx, ry, 0, 0, Math.PI * 2); ctx.fill()
    ctx.fillStyle = '#3a5828'
    for (let i = 0; i < 8; i++) {
      const tx = fx + Math.cos(i * 0.8) * rx * 0.62
      const ty = fy + Math.sin(i * 0.8) * ry * 0.62
      ctx.beginPath(); ctx.arc(tx, ty, 5, 0, Math.PI * 2); ctx.fill()
    }
  }

  // Fields
  for (const [fx, fy, fw, fh] of FIELDS) {
    ctx.fillStyle = t.fieldColor; ctx.fillRect(fx - fw / 2, fy - fh / 2, fw, fh)
    ctx.strokeStyle = '#8a7028'; ctx.lineWidth = 1.5; ctx.strokeRect(fx - fw / 2, fy - fh / 2, fw, fh)
    ctx.strokeStyle = '#9a8030'; ctx.lineWidth = 0.6
    for (let row = fy - fh / 2 + 7; row < fy + fh / 2; row += 7) {
      ctx.beginPath(); ctx.moveTo(fx - fw / 2, row); ctx.lineTo(fx + fw / 2, row); ctx.stroke()
    }
  }

  // Roads
  ctx.strokeStyle = t.roadColor; ctx.lineWidth = 8; ctx.lineCap = 'round'
  ctx.beginPath(); ctx.moveTo(W * 0.5, H); ctx.lineTo(W * 0.5, H / 2); ctx.stroke()
  ctx.beginPath(); ctx.moveTo(0, H / 2 + 12); ctx.lineTo(W * 0.54, H / 2); ctx.stroke()

  // Buildings
  for (const b of BUILDINGS) {
    const bx = b.rx * W, by = b.ry * H
    ctx.fillStyle = 'rgba(0,0,0,0.13)'
    ctx.fillRect(bx - b.w / 2 + 3, by - b.h / 2 + 4, b.w, b.h)
    ctx.fillStyle = t.buildingColor
    ctx.fillRect(bx - b.w / 2, by - b.h / 2, b.w, b.h)
    ctx.strokeStyle = '#4a2810'; ctx.lineWidth = 1.5
    ctx.strokeRect(bx - b.w / 2, by - b.h / 2, b.w, b.h)
    ctx.fillStyle = '#4a2010'; ctx.beginPath()
    ctx.moveTo(bx - b.w / 2 - 3, by - b.h / 2)
    ctx.lineTo(bx, by - b.h / 2 - 14)
    ctx.lineTo(bx + b.w / 2 + 3, by - b.h / 2)
    ctx.closePath(); ctx.fill()
    ctx.fillStyle = '#3a2010'; ctx.font = '9px serif'; ctx.textAlign = 'center'
    ctx.fillText(b.label, bx, by + b.h / 2 + 12)
  }

  // Vignette
  const vig = ctx.createRadialGradient(W / 2, H / 2, Math.min(W, H) * 0.28, W / 2, H / 2, Math.max(W, H) * 0.78)
  vig.addColorStop(0, 'transparent')
  vig.addColorStop(1, 'rgba(80,40,10,0.38)')
  ctx.fillStyle = vig; ctx.fillRect(0, 0, W, H)
}

function drawDarkTerrain(ctx: CanvasRenderingContext2D, W: number, H: number, t: ThemeTokens) {
  ctx.fillStyle = t.canvasBg; ctx.fillRect(0, 0, W, H)

  // Grid
  ctx.strokeStyle = t.gridColor; ctx.lineWidth = 1
  for (let x = 0; x < W; x += 32) { ctx.beginPath(); ctx.moveTo(x, 0); ctx.lineTo(x, H); ctx.stroke() }
  for (let y = 0; y < H; y += 32) { ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(W, y); ctx.stroke() }

  // Forests
  for (const [fx, fy, rx, ry] of FORESTS) {
    ctx.fillStyle = '#0c1a0d'
    ctx.beginPath(); ctx.ellipse(fx, fy, rx, ry, 0, 0, Math.PI * 2); ctx.fill()
    ctx.fillStyle = '#183016'
    for (let i = 0; i < 10; i++) {
      const tx = fx + Math.cos(i * 0.65) * rx * 0.68
      const ty = fy + Math.sin(i * 0.65) * ry * 0.68
      ctx.beginPath(); ctx.arc(tx, ty, 4, 0, Math.PI * 2); ctx.fill()
    }
  }

  // River (glowing)
  ctx.save()
  ctx.strokeStyle = t.riverColor; ctx.lineWidth = 7; ctx.lineCap = 'round'
  ctx.shadowColor = '#2060c8'; ctx.shadowBlur = 12
  ctx.beginPath(); ctx.moveTo(W * 0.28, 0)
  ctx.quadraticCurveTo(W * 0.32, H * 0.38, W * 0.38, H * 0.58)
  ctx.quadraticCurveTo(W * 0.44, H * 0.78, W * 0.42, H); ctx.stroke()
  ctx.restore()

  // Fields
  for (const [fx, fy, fw, fh] of FIELDS) {
    ctx.fillStyle = t.fieldColor; ctx.fillRect(fx - fw / 2, fy - fh / 2, fw, fh)
    ctx.strokeStyle = '#1e3016'; ctx.lineWidth = 1
    ctx.strokeRect(fx - fw / 2, fy - fh / 2, fw, fh)
  }

  // Roads
  ctx.strokeStyle = t.roadColor; ctx.lineWidth = 7; ctx.lineCap = 'round'
  ctx.beginPath(); ctx.moveTo(W * 0.5, H); ctx.lineTo(W * 0.5, H / 2); ctx.stroke()
  ctx.beginPath(); ctx.moveTo(0, H / 2 + 12); ctx.lineTo(W * 0.54, H / 2); ctx.stroke()

  // Buildings
  for (const b of BUILDINGS) {
    const bx = b.rx * W, by = b.ry * H
    ctx.fillStyle = t.buildingColor
    ctx.fillRect(bx - b.w / 2, by - b.h / 2, b.w, b.h)
    ctx.strokeStyle = 'rgba(212,168,83,0.5)'; ctx.lineWidth = 1
    ctx.strokeRect(bx - b.w / 2, by - b.h / 2, b.w, b.h)
    const bg = ctx.createRadialGradient(bx, by, 0, bx, by, 24)
    bg.addColorStop(0, 'rgba(212,168,83,0.12)')
    bg.addColorStop(1, 'transparent')
    ctx.fillStyle = bg; ctx.beginPath(); ctx.arc(bx, by, 24, 0, Math.PI * 2); ctx.fill()
  }

  // Corner labels
  ctx.fillStyle = '#3a3020'; ctx.font = '10px monospace'
  ctx.textAlign = 'left';  ctx.fillText('(0, 0)', 8, 16)
  ctx.textAlign = 'right'; ctx.fillText('(1000, 0)', W - 8, 16)
  ctx.textAlign = 'left';  ctx.fillText('(0, 1000)', 8, H - 6)
  ctx.textAlign = 'right'; ctx.fillText('(1000, 1000)', W - 8, H - 6)
}

export function drawAgents(
  ctx: CanvasRenderingContext2D,
  W: number, H: number,
  agents: AgentState[],
  selectedId: string | null,
  t: ThemeTokens,
) {
  if (agents.length === 0) return
  const tr = buildTransform(agents, W, H)

  for (const agent of agents) {
    const cx = wx(agent.pos.x, tr)
    const cy = wy(agent.pos.y, tr)
    const col = t.roleColours[roleIndex(agent)]
    const isSel = agent.id === selectedId

    if (t.glow) {
      // Dark theme: glow aura
      ctx.fillStyle = col + '22'
      ctx.beginPath(); ctx.arc(cx, cy, 15, 0, Math.PI * 2); ctx.fill()
      ctx.save()
      ctx.fillStyle = col; ctx.shadowColor = col; ctx.shadowBlur = 10
      ctx.beginPath(); ctx.arc(cx, cy, 4.5, 0, Math.PI * 2); ctx.fill()
      ctx.restore()
    } else {
      // Light theme: radial gradient aura
      const g = ctx.createRadialGradient(cx, cy, 0, cx, cy, 13)
      g.addColorStop(0, col + '55'); g.addColorStop(1, 'transparent')
      ctx.fillStyle = g; ctx.beginPath(); ctx.arc(cx, cy, 13, 0, Math.PI * 2); ctx.fill()
      ctx.fillStyle = col
      ctx.beginPath(); ctx.arc(cx, cy, 5.5, 0, Math.PI * 2); ctx.fill()
      ctx.strokeStyle = 'rgba(255,255,255,0.8)'; ctx.lineWidth = 1.5; ctx.stroke()
    }

    // Selected: dashed ring + name
    if (isSel) {
      ctx.strokeStyle = t.accent; ctx.lineWidth = t.glow ? 1.5 : 2
      ctx.setLineDash([4, 3])
      ctx.beginPath(); ctx.arc(cx, cy, t.glow ? 11 : 12, 0, Math.PI * 2); ctx.stroke()
      ctx.setLineDash([])
      ctx.fillStyle = t.accent
      ctx.font = `bold 10px ${t.fontMono}`
      ctx.textAlign = 'center'
      ctx.fillText(agent.id.toUpperCase(), cx, cy - (t.glow ? 18 : 17))
    }
  }
}

export function hitTestAgent(
  agents: AgentState[],
  canvasX: number, canvasY: number,
  W: number, H: number,
  radius = 15,
): string | null {
  if (agents.length === 0) return null
  const tr = buildTransform(agents, W, H)
  let best: string | null = null
  let bestDist = radius * radius
  for (const a of agents) {
    const cx = wx(a.pos.x, tr)
    const cy = wy(a.pos.y, tr)
    const dx = canvasX - cx, dy = canvasY - cy
    const dist2 = dx * dx + dy * dy
    if (dist2 < bestDist) { bestDist = dist2; best = a.id }
  }
  return best
}
