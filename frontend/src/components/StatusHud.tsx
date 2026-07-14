import type { ThemeTokens } from '../theme'
import type { WorldState } from '../types'

interface Props {
  t: ThemeTokens
  world: WorldState
}

// Top-right canvas overlay: the current in-world date (Day N), clock (HH:MM) and
// ambient temperature. `day_of_run`/`minute_of_day` arrive live on the WorldFrame
// SSE stream (data-contracts §4, WI-P4); `climate === null` (env OFF / before the
// first frame) hides the panel entirely. Display-only — `pointerEvents: 'none'`
// lets camera drag/wheel pass straight through to the canvas beneath it.
export function StatusHud({ t, world }: Props) {
  const c = world.climate
  if (!c) return null

  const day = c.dayOfRun + 1 // dayOfRun is the 0-based worldtime.DayOfRun index
  const hh = String(Math.floor(c.minuteOfDay / 60)).padStart(2, '0')
  const mm = String(Math.floor(c.minuteOfDay % 60)).padStart(2, '0')

  return (
    <div style={{
      position: 'absolute', top: 10, right: 12, zIndex: 5,
      display: 'flex', flexDirection: 'column', gap: 3, alignItems: 'flex-end',
      padding: '6px 12px',
      background: t.glow ? 'rgba(20,18,16,0.88)' : 'rgba(240,227,192,0.88)',
      border: `1px solid ${t.panelBorder}`, borderRadius: t.glow ? 0 : 2,
      fontFamily: t.fontMono, pointerEvents: 'none', userSelect: 'none',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 7, fontSize: 12, color: t.accent, letterSpacing: '0.04em' }}>
        <span style={{ fontSize: 13 }}>{c.dayNight === 'day' ? '☀' : '🌙'}</span>
        <span style={{ fontWeight: 700 }}>Day {day}</span>
        <span style={{ color: t.textMuted }}>·</span>
        <span>{hh}:{mm}</span>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11, color: t.textDim }}>
        <span>{c.temperature.toFixed(1)}°C</span>
        {c.raining && <span style={{ color: '#3a86d0', fontSize: 12 }}>☂</span>}
        {c.snowCover > 0 && <span style={{ fontSize: 11 }}>❄</span>}
      </div>
    </div>
  )
}
