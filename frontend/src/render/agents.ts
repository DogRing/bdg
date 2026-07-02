import type { AgentState } from '../types'
import type { ThemeTokens } from '../theme'
import { wx, wy, type Transform } from './transform'

// Role → colour index (matches SPEC: Farmer=0, Merchant=1, Guard=2, Artisan=3).
// Display heuristic only — roles/clusters stay emergent backend-side (D2).
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

// Agents stay coloured dots + cluster/role colour + aura (plan Q8): the social
// layer's primary signal. Sprite treatment is revisited after motion ships.
export function drawAgents(
  ctx: CanvasRenderingContext2D,
  agents: AgentState[],
  selectedId: string | null,
  t: ThemeTokens,
  tr: Transform,
) {
  if (agents.length === 0) return

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
