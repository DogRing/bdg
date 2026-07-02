import type { AgentPos, FxInstance } from '../types'
import type { FxDef } from '../assets/manifest'

// Adaptive lerp between SSE frames (plan Q3). The reducer stamps the tween
// window [prevFrameAtMs, frameAtMs] on arrival (gap = measured inter-frame
// interval); these helpers evaluate it at the render clock. All pure.

export interface Interpolable {
  pos: AgentPos
  heading?: number
  prevPos?: AgentPos
  prevHeading?: number
  prevFrameAtMs?: number
  frameAtMs?: number
}

// Pose walk↔run refinement threshold: world units per second of frame-to-frame
// displacement (presentation constant — sim speeds live backend-side).
export const RUN_SPEED_THRESHOLD = 3

function tweenT(e: Interpolable, clockMs: number): number | null {
  if (e.prevPos === undefined || e.prevFrameAtMs === undefined || e.frameAtMs === undefined) return null
  const window = e.frameAtMs - e.prevFrameAtMs
  if (window <= 0) return null
  return Math.min(1, Math.max(0, (clockMs - e.prevFrameAtMs) / window))
}

export function displayPos(e: Interpolable, clockMs: number): AgentPos {
  const t = tweenT(e, clockMs)
  if (t === null || e.prevPos === undefined) return e.pos
  return {
    x: e.prevPos.x + (e.pos.x - e.prevPos.x) * t,
    y: e.prevPos.y + (e.pos.y - e.prevPos.y) * t,
  }
}

// Shortest-arc heading lerp: 350°→10° passes through 0°, never the long way.
export function displayHeading(e: Interpolable, clockMs: number): number {
  const cur = e.heading ?? 0
  const t = tweenT(e, clockMs)
  if (t === null || e.prevHeading === undefined) return cur
  const TAU = Math.PI * 2
  let delta = (cur - e.prevHeading) % TAU
  if (delta > Math.PI) delta -= TAU
  if (delta < -Math.PI) delta += TAU
  return e.prevHeading + delta * t
}

// isRunning reads the frame-to-frame displacement rate (world units/s) so the
// walk↔run pose refinement reflects actual speed, not just the ActionID.
export function isRunning(e: Interpolable): boolean {
  if (e.prevPos === undefined || e.prevFrameAtMs === undefined || e.frameAtMs === undefined) return false
  const window = (e.frameAtMs - e.prevFrameAtMs) / 1000
  if (window <= 0) return false
  const dx = e.pos.x - e.prevPos.x
  const dy = e.pos.y - e.prevPos.y
  return Math.hypot(dx, dy) / window > RUN_SPEED_THRESHOLD
}

// fxProgress: [0,1] through the fx timeline; null once expired (caller skips —
// the reducer prunes the entry on a later reduce).
export function fxProgress(fx: FxInstance, def: FxDef, clockMs: number): number | null {
  const t = (clockMs - fx.at) / def.durationMs
  if (t > 1) return null
  return Math.max(0, t)
}
