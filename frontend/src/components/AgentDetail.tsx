import type { AgentState } from '../types'
import type { ThemeTokens } from '../theme'

interface Props {
  agent: AgentState | null
  t: ThemeTokens
  isLight: boolean
}

const ROLE_EMOJI: Record<string, string> = {
  satiety: '🧑‍🌾', hydration: '🧑‍🌾', food: '🧑‍🌾',
  standing: '🧑‍💼', trade: '🧑‍💼',
  safety: '🛡️', security: '🛡️',
  craft: '🔨', rest: '🛌',
}

function emojiFor(goal: string) {
  const k = goal.toLowerCase()
  for (const [kw, em] of Object.entries(ROLE_EMOJI)) {
    if (k.includes(kw)) return em
  }
  return '🧑'
}

function moodColor(mood: number, t: ThemeTokens) {
  if (mood >= 0.65) return t.positive
  if (mood < 0.35) return t.negative
  return t.textMuted
}

interface BarProps {
  label: string
  value: number    // 0–1
  color: string
  t: ThemeTokens
  isLight: boolean
}

function Bar({ label, value, color, t, isLight }: BarProps) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      <span style={{ fontSize: 9, color: t.textDim, width: 58, letterSpacing: '0.04em', fontFamily: t.fontMono }}>
        {label.toUpperCase()}
      </span>
      <div style={{ flex: 1, height: isLight ? 5 : 4, background: isLight ? '#c8b48e' : t.panelBorder, borderRadius: isLight ? 3 : 0, overflow: 'hidden', border: isLight ? undefined : `1px solid ${t.panelBorder}` }}>
        <div style={{ width: `${Math.round(value * 100)}%`, height: '100%', background: color }} />
      </div>
      <span style={{ fontFamily: t.fontMono, fontSize: 10, color, width: 28, textAlign: 'right' }}>
        {Math.round(value * 100)}
      </span>
    </div>
  )
}

export function AgentDetail({ agent, t, isLight }: Props) {
  const sectionLabel: React.CSSProperties = {
    fontFamily: t.fontSerif,
    fontSize: 9,
    color: t.textMuted,
    letterSpacing: '0.12em',
    textTransform: 'uppercase',
    marginBottom: 10,
  }

  if (!agent) {
    return (
      <div style={{ padding: '14px 16px', borderBottom: `1px solid ${t.panelBorder}`, flexShrink: 0 }}>
        <div style={sectionLabel}>{isLight ? 'Selected Agent' : '◈ Selected Agent'}</div>
        <div style={{ fontSize: 11, color: t.textDim, fontStyle: isLight ? 'italic' : undefined, fontFamily: t.fontUi }}>
          Click an agent on the map to inspect
        </div>
      </div>
    )
  }

  const emoji = emojiFor(agent.goal)
  const mood = agent.mood
  const mc = moodColor(mood, t)

  return (
    <div style={{ padding: '14px 16px', borderBottom: `1px solid ${t.panelBorder}`, flexShrink: 0 }}>
      <div style={sectionLabel}>{isLight ? 'Selected Agent' : '◈ Selected Agent'}</div>

      {/* Identity row */}
      <div style={{ display: 'flex', gap: 10, alignItems: 'flex-start', marginBottom: 10 }}>
        <div style={{
          width: 44, height: 44, flexShrink: 0,
          background: isLight ? '#7a4e2d' : t.panelBg,
          border: `${isLight ? 2 : 1}px solid ${t.accent}`,
          borderRadius: isLight ? '50%' : 0,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: 20,
          boxShadow: t.glow ? `0 0 10px ${t.accent}33` : undefined,
        }}>
          {emoji}
        </div>
        <div style={{ flex: 1 }}>
          <div style={{ fontFamily: t.fontSerif, fontSize: 15, fontWeight: 700, color: t.accent }}>
            {agent.id}
          </div>
          <div style={{ fontSize: isLight ? 11 : 10, color: t.textMuted, marginTop: 2, fontFamily: t.fontUi, fontStyle: isLight ? 'italic' : undefined, letterSpacing: isLight ? undefined : '0.04em' }}>
            {agent.goal ? agent.goal.charAt(0).toUpperCase() + agent.goal.slice(1) : 'Idle'}
          </div>
          <div style={{ fontSize: 9, color: t.textDim, marginTop: 3, fontFamily: t.fontMono }}>
            POS ({agent.pos.x.toFixed(0)}, {agent.pos.y.toFixed(0)})
          </div>
        </div>
      </div>

      {/* Goal + Action */}
      <div style={{
        background: t.panelBg,
        border: isLight ? undefined : `1px solid ${t.panelBorder}`,
        padding: '8px 10px',
        borderRadius: isLight ? 2 : 0,
        fontSize: isLight ? 11 : 10,
        marginBottom: 8,
      }}>
        <div style={{ color: t.textMuted, fontWeight: 600, marginBottom: 3 }}>
          {isLight ? '' : 'GOAL '}
          <span style={{ color: t.textPrimary, fontWeight: 400, fontStyle: isLight ? 'italic' : undefined }}>
            {agent.goal || '—'}
          </span>
        </div>
        <div style={{ color: t.textMuted }}>
          {isLight ? '' : 'ACTION '}
          <span style={{ color: t.textPrimary, fontWeight: isLight ? 400 : undefined }}>
            {agent.action || 'idle'}
          </span>
        </div>
      </div>

      {/* Mood bar */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        <Bar label="Mood" value={mood} color={mc} t={t} isLight={isLight} />
        {agent.copingMode && (
          <div style={{ fontSize: 10, color: t.negative, fontFamily: t.fontMono, marginTop: 2 }}>
            ⚠ Coping: {agent.copingMode}
          </div>
        )}
        {agent.cluster && (
          <div style={{ fontSize: 9, color: t.textDim, fontFamily: t.fontMono }}>
            Cluster: {agent.cluster}
          </div>
        )}
      </div>
    </div>
  )
}
