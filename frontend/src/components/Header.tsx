import type { ThemeTokens } from '../theme'
import type { Theme, WorldState } from '../types'

interface Props {
  t: ThemeTokens
  theme: Theme
  onToggleTheme: () => void
  world: WorldState
  onTogglePause: () => void
}

const STATUS_ICON: Record<WorldState['connectionStatus'], string> = {
  connecting:   '◌',
  live:         '●',
  reconnecting: '⚠',
}
const STATUS_COLOR: Record<WorldState['connectionStatus'], string> = {
  connecting:   '#a08050',
  live:         '#6aaa58',
  reconnecting: '#c09030',
}

export function Header({ t, theme, onToggleTheme, world, onTogglePause }: Props) {
  const starIcon = (
    <svg width="22" height="22" viewBox="0 0 22 22" fill={t.accent} style={{ flexShrink: 0 }}>
      <polygon points="11,0 13.4,7.7 21.5,7.7 15,12.5 17.4,20.2 11,15.4 4.6,20.2 7,12.5 0.5,7.7 8.6,7.7" />
    </svg>
  )

  const connStatus = world.connectionStatus
  const statusColor = STATUS_COLOR[connStatus]
  const statusIcon = STATUS_ICON[connStatus]
  const statusLabel = connStatus.toUpperCase()

  return (
    <div style={{
      background: t.headerBg,
      height: 46,
      display: 'flex',
      alignItems: 'center',
      padding: '0 20px',
      gap: 14,
      flexShrink: 0,
      borderBottom: `2px solid ${t.headerBorder}`,
    }}>
      {/* Logo */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        {starIcon}
        <span style={{ fontFamily: t.fontSerif, fontSize: 17, fontWeight: 700, color: t.accent, letterSpacing: '0.05em' }}>
          BDG — Medieval Village
        </span>
      </div>

      <div style={{ flex: 1 }} />

      {/* Tick + time */}
      <div style={{ textAlign: 'right' }}>
        <div style={{ fontFamily: t.fontMono, fontSize: 11, color: t.accent, letterSpacing: '0.08em' }}>
          TICK {String(world.tick).padStart(5, '0')}
        </div>
        <div style={{ fontFamily: t.fontMono, fontSize: 10, color: t.textDim }}>
          {world.agents.size} agents
          {world.animals.size > 0 && ` · ${world.animals.size} fauna`}
        </div>
      </div>

      <div style={{ width: 1, height: 26, background: t.headerBorder }} />

      {/* Ambient weather chip */}
      {world.climate && (
        <>
          <div style={{
            display: 'flex', alignItems: 'center', gap: 6,
            fontFamily: t.fontMono, fontSize: 10, color: t.textDim,
            background: t.panelBg, padding: '4px 10px',
            border: `1px solid ${t.panelBorder}`,
            borderRadius: theme === 'light' ? 2 : 0,
          }}>
            <span style={{ fontSize: 12 }}>{world.climate.dayNight === 'day' ? '☀' : '🌙'}</span>
            <span>{world.climate.temperature.toFixed(1)}°C</span>
            {world.climate.apparentTemp !== null && (
              <span style={{ color: t.textMuted }}>(feels {world.climate.apparentTemp.toFixed(1)}°C)</span>
            )}
            {world.climate.raining && <span style={{ color: '#3a86d0', fontSize: 12 }}>☂</span>}
            {world.climate.windMag > 0 && <span>~wind→</span>}
          </div>
          <div style={{ width: 1, height: 26, background: t.headerBorder }} />
        </>
      )}

      {/* Connection badge */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 5,
        fontFamily: t.fontMono, fontSize: 10, color: statusColor,
        border: `1px solid ${statusColor}30`,
        background: statusColor + '12',
        padding: '4px 10px',
        letterSpacing: '0.06em',
        borderRadius: theme === 'light' ? 2 : 0,
      }}>
        <span style={{ animation: connStatus === 'live' ? 'pulse-dot 1.8s ease-in-out infinite' : undefined }}>
          {statusIcon}
        </span>
        {statusLabel}
      </div>

      {/* Play/pause */}
      <button
        onClick={onTogglePause}
        title={world.paused ? 'Resume display' : 'Freeze display'}
        style={{
          background: world.paused ? t.accent : t.accentDim,
          border: `1px solid ${t.accent}`,
          color: world.paused ? t.headerBg : t.accent,
          fontFamily: t.fontMono,
          fontSize: 15,
          width: 34,
          height: 28,
          borderRadius: theme === 'light' ? 2 : 0,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}
      >
        {world.paused ? '▶' : '⏸'}
      </button>

      {/* Theme toggle */}
      <button
        onClick={onToggleTheme}
        title={`Switch to ${theme === 'dark' ? 'Parchment' : 'Dark'} theme`}
        style={{
          background: 'transparent',
          border: `1px solid ${t.textDim}`,
          color: t.textDim,
          fontFamily: t.fontMono,
          fontSize: 10,
          padding: '4px 8px',
          borderRadius: theme === 'light' ? 2 : 0,
          letterSpacing: '0.06em',
        }}
      >
        {theme === 'dark' ? '☀ PARCH' : '🌙 DARK'}
      </button>
    </div>
  )
}
