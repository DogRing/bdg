import type { SimEvent, LogEntry } from '../types'

let _nextId = 0

export function formatEvent(ev: SimEvent): LogEntry | null {
  const id = _nextId++
  const tick = ev.tick
  const agentId = ev.agent_id
  const p = ev.payload ?? {}
  const base = { id, tick, agentId }

  switch (ev.type) {
    case 'GoalSelected': {
      const dim = String(p.dimension ?? '')
      const target = p.target ? ` → ${p.target}` : ''
      const val = typeof p.eff_value === 'number' ? ` (val=${p.eff_value.toFixed(2)})` : ''
      return { ...base, type: ev.type, text: `🎯 ${agentId} seeks ${dim}${target}${val}`, variant: 'normal' }
    }
    case 'PlanBuilt': {
      const steps = Array.isArray(p.steps) ? p.steps.slice(0, 3).join(' → ') : ''
      const cost = typeof p.total_cost === 'number' ? ` [cost=${p.total_cost.toFixed(1)}]` : ''
      return { ...base, type: ev.type, text: `📋 ${agentId} plan: ${steps}${cost}`, variant: 'normal' }
    }
    case 'ActionStarted': {
      const action = String(p.action ?? '')
      return { ...base, type: ev.type, text: `▶ ${agentId} began ${action}`, variant: 'normal' }
    }
    case 'ActionDone': {
      const action = String(p.action ?? '')
      const result = p.result ? ` → ${p.result}` : ''
      return { ...base, type: ev.type, text: `✓ ${agentId} completed ${action}${result}`, variant: 'positive' }
    }
    case 'Interacted': {
      const with_ = p.with ? String(p.with) : '?'
      const kind = String(p.signal_kind ?? 'gossip')
      return { ...base, type: ev.type, text: `💬 ${agentId} → ${with_} (${kind})`, variant: 'normal' }
    }
    case 'BeliefUpdated': {
      const about = String(p.about ?? '?')
      const stat = String(p.stat ?? '')
      const delta = typeof p['new'] === 'number' && typeof p['old'] === 'number'
        ? (p['new'] as number) - (p['old'] as number)
        : null
      const deltaStr = delta !== null ? ` ${delta >= 0 ? '+' : ''}${delta.toFixed(2)}` : ''
      const cause = p.cause ? ` via ${p.cause}` : ''
      return {
        ...base, type: ev.type,
        text: `👁 ${agentId} ToM[${about}].${stat}${deltaStr}${cause}`,
        variant: delta !== null && delta >= 0 ? 'positive' : 'negative',
      }
    }
    case 'ReputationGossip': {
      const about = String(p.about ?? '?')
      const from = p.from ? ` from ${p.from}` : ''
      const delta = typeof p.delta === 'number' ? ` ${p.delta >= 0 ? '+' : ''}${p.delta.toFixed(2)}` : ''
      return { ...base, type: ev.type, text: `🗣 ${agentId} heard about ${about}${from}${delta}`, variant: 'normal' }
    }
    case 'RoleEmerged': {
      const fn = String(p.function ?? '')
      const holder = String(p.holder ?? '')
      const share = typeof p.reliance_share === 'number' ? ` (${(p.reliance_share * 100).toFixed(0)}%)` : ''
      return { ...base, type: ev.type, text: `👑 ${holder} is now ${fn} holder${share}`, variant: 'special' }
    }
    case 'CopingEntered': {
      const mode = String(p.mode ?? '')
      return { ...base, type: ev.type, text: `😶 ${agentId} → ${mode}`, variant: 'negative' }
    }
    case 'TickDone': {
      return null // suppress tick-done from log; update tick counter only
    }
    default: {
      return { ...base, type: ev.type, text: `[${ev.type}] ${agentId ?? ''} ${JSON.stringify(p).slice(0, 60)}`, variant: 'normal' }
    }
  }
}
