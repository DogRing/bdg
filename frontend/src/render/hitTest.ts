import type { AgentState, AnimalState } from '../types'
import { wx, wy, type Transform } from './transform'
import type { EntityRef } from './camera'

function nearest(
  pts: Iterable<{ id: string; pos: { x: number; y: number } }>,
  px: number, py: number,
  tr: Transform,
  maxDist2: number,
): { id: string; dist2: number } | null {
  let best: { id: string; dist2: number } | null = null
  for (const p of pts) {
    const dx = px - wx(p.pos.x, tr)
    const dy = py - wy(p.pos.y, tr)
    const dist2 = dx * dx + dy * dy
    if (dist2 < maxDist2 && (!best || dist2 < best.dist2)) best = { id: p.id, dist2 }
  }
  return best
}

// hitTest resolves a canvas click to the nearest entity within `radius` px.
// Agents win ties (they carry the detail panel); an animal hit is used for
// camera-follow only. Returns null on empty space.
export function hitTest(
  agents: Iterable<AgentState>,
  animals: Iterable<AnimalState>,
  tr: Transform,
  px: number, py: number,
  radius = 15,
): EntityRef | null {
  const r2 = radius * radius
  const agent = nearest(agents, px, py, tr, r2)
  const animal = nearest(animals, px, py, tr, r2)
  if (agent && (!animal || agent.dist2 <= animal.dist2)) return { kind: 'agent', id: agent.id }
  if (animal) return { kind: 'animal', id: animal.id }
  return null
}
