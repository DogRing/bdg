import type { WorldState } from '../types'
import type { ThemeTokens } from '../theme'
import { AgentDetail } from './AgentDetail'
import { EventLog } from './EventLog'

interface Props {
  world: WorldState
  t: ThemeTokens
  isLight: boolean
  onTogglePause: () => void
}

interface StatTileProps {
  label: string
  value: string | number
  delta?: string
  deltaColor?: string
  t: ThemeTokens
  isLight: boolean
}

function StatTile({ label, value, delta, deltaColor, t, isLight }: StatTileProps) {
  return (
    <div style={{
      background: t.panelBg,
      padding: '8px 10px',
      borderRadius: isLight ? 2 : 0,
      border: isLight ? undefined : `1px solid ${t.panelBorder}`,
    }}>
      <div style={{ fontSize: 9, color: t.textMuted, textTransform: 'uppercase', letterSpacing: '0.08em' }}>
        {label}
      </div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 4, marginTop: isLight ? 1 : 2 }}>
        <span style={{ fontFamily: t.fontMono, fontSize: 20, fontWeight: 700, color: isLight ? '#2c1810' : t.textPrimary }}>
          {value}
        </span>
        {delta && (
          <span style={{ fontSize: 10, color: deltaColor ?? t.textMuted }}>
            {delta}
          </span>
        )}
      </div>
    </div>
  )
}

export function Sidebar({ world, t, isLight, onTogglePause }: Props) {
  const agentCount = world.agents.size
  const avgMood = agentCount > 0
    ? Array.from(world.agents.values()).reduce((s, a) => s + a.mood, 0) / agentCount
    : 0
  const happinessPct = Math.round(avgMood * 100)
  void (happinessPct >= 60 ? t.positive : happinessPct < 35 ? t.negative : t.textMuted)

  const selectedAgent = world.selectedAgentId ? world.agents.get(world.selectedAgentId) ?? null : null

  return (
    <div style={{
      width: 358,
      flexShrink: 0,
      background: t.sidebarBg,
      borderLeft: `${isLight ? 2 : 1}px solid ${t.panelBorder}`,
      display: 'flex',
      flexDirection: 'column',
      overflow: 'hidden',
    }}>
      {/* Panel 1: Controls + Stats */}
      <div style={{ padding: '14px 16px', borderBottom: `1px solid ${t.panelBorder}`, flexShrink: 0 }}>
        {/* Playback controls */}
        <div style={{ display: 'flex', alignItems: 'center', gap: isLight ? 8 : 6, marginBottom: 14 }}>
          <button
            onClick={onTogglePause}
            style={{
              background: world.paused ? t.accent : t.panelBg,
              border: `1px solid ${t.accent}`,
              color: world.paused ? t.headerBg : t.accent,
              fontSize: 16,
              width: 36,
              height: 28,
              borderRadius: isLight ? 2 : 0,
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              fontWeight: 700, lineHeight: 1,
            }}
          >
            {world.paused ? '▶' : '⏸'}
          </button>
          <div style={{ flex: 1 }} />
          <div style={{
            fontFamily: t.fontMono,
            fontSize: isLight ? 11 : 10,
            color: world.paused ? t.accent : t.positive,
            border: `1px solid ${world.paused ? t.accent + '40' : t.positive + '40'}`,
            background: world.paused ? t.accent + '18' : t.positive + '12',
            padding: '4px 10px',
            borderRadius: isLight ? 2 : 0,
            letterSpacing: '0.06em',
          }}>
            {world.paused ? 'PAUSED' : 'RUNNING'}
          </div>
        </div>

        {/* Stats grid */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: isLight ? 7 : 6 }}>
          <StatTile label="Population" value={agentCount} t={t} isLight={isLight} />
          <StatTile
            label="Happiness"
            value={`${happinessPct}%`}
            t={t}
            isLight={isLight}
          />
          <StatTile
            label="Food"
            value={world.food ?? '—'}
            delta={world.food !== null ? undefined : undefined}
            t={t}
            isLight={isLight}
          />
          <StatTile
            label="Wood"
            value={world.wood ?? '—'}
            t={t}
            isLight={isLight}
          />
        </div>

        {/* Roles summary */}
        {world.roles.length > 0 && (
          <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 3 }}>
            {world.roles.map(r => (
              <div key={r.function} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 10, color: t.textMuted, fontFamily: t.fontMono }}>
                <span style={{ color: t.accent }}>👑</span>
                <span style={{ color: t.textPrimary }}>{r.holder}</span>
                <span style={{ color: t.textDim }}>→ {r.function} ({(r.reliance_share * 100).toFixed(0)}%)</span>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Panel 2: Agent detail */}
      <AgentDetail agent={selectedAgent} t={t} isLight={isLight} />

      {/* Panel 3: Event log */}
      <EventLog entries={world.log} t={t} isLight={isLight} />
    </div>
  )
}
