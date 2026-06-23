import { useState, useRef, useEffect } from 'react'
import type { LogEntry } from '../types'
import type { ThemeTokens } from '../theme'

type FilterKey = 'all' | 'social' | 'goals' | 'roles' | 'actions'

const FILTER_TYPES: Record<FilterKey, string[]> = {
  all: [],
  social: ['Interacted', 'ReputationGossip', 'BeliefUpdated'],
  goals: ['GoalSelected', 'PlanBuilt'],
  roles: ['RoleEmerged', 'CopingEntered'],
  actions: ['ActionStarted', 'ActionDone'],
}

const VARIANT_COLOR = {
  normal: undefined as string | undefined,
  positive: undefined as string | undefined,
  negative: undefined as string | undefined,
  special: undefined as string | undefined,
}

function entryColor(e: LogEntry, t: ThemeTokens): string {
  void VARIANT_COLOR
  switch (e.variant) {
    case 'positive': return t.positive
    case 'negative': return t.negative
    case 'special': return t.accent
    default: return t.textPrimary
  }
}

interface Props {
  entries: LogEntry[]
  t: ThemeTokens
  isLight: boolean
}

export function EventLog({ entries, t, isLight }: Props) {
  const [filter, setFilter] = useState<FilterKey>('all')
  const listRef = useRef<HTMLDivElement>(null)
  const pinnedRef = useRef(true) // auto-scroll when pinned to top

  const visible = filter === 'all'
    ? entries
    : entries.filter(e => FILTER_TYPES[filter].includes(e.type))

  // Detect if user has scrolled away from top (newest-first, so top = newest)
  const handleScroll = () => {
    const el = listRef.current
    if (!el) return
    pinnedRef.current = el.scrollTop < 8
  }

  // Auto-scroll to top when new entries arrive and pinned
  useEffect(() => {
    if (pinnedRef.current && listRef.current) {
      listRef.current.scrollTop = 0
    }
  }, [entries.length])

  const selectStyle: React.CSSProperties = {
    background: t.panelBg,
    border: `1px solid ${t.panelBorder}`,
    color: t.textMuted,
    fontFamily: t.fontMono,
    fontSize: 9,
    padding: '3px 6px',
    borderRadius: isLight ? 2 : 0,
    outline: 'none',
    cursor: 'pointer',
    letterSpacing: '0.06em',
  }

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden', padding: '12px 16px' }}>
      {/* Header row */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 9, flexShrink: 0 }}>
        <span style={{
          width: 6, height: 6, background: t.positive, borderRadius: '50%',
          display: 'inline-block', flexShrink: 0,
          animation: 'pulse-dot 1.8s ease-in-out infinite',
          boxShadow: t.glow ? `0 0 6px ${t.positive}` : undefined,
        }} />
        <span style={{ fontFamily: t.fontSerif, fontSize: 9, color: t.textMuted, letterSpacing: '0.12em', textTransform: 'uppercase' }}>
          Live Event Log
        </span>
        <div style={{ flex: 1 }} />
        <select value={filter} onChange={e => setFilter(e.target.value as FilterKey)} style={selectStyle}>
          <option value="all">all</option>
          <option value="social">social</option>
          <option value="goals">goals</option>
          <option value="roles">roles</option>
          <option value="actions">actions</option>
        </select>
      </div>

      {/* Entries */}
      <div
        ref={listRef}
        className="event-log-scroll"
        onScroll={handleScroll}
        style={{ flex: 1, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: isLight ? 5 : 4 }}
      >
        {visible.length === 0 && (
          <div style={{ fontSize: 10, color: t.textDim, fontFamily: t.fontMono, fontStyle: 'italic' }}>
            Waiting for events…
          </div>
        )}
        {visible.map((e, i) => (
          <div
            key={e.id}
            className={i === 0 ? 'entry-new' : undefined}
            style={{
              fontSize: isLight ? 11 : 10,
              color: entryColor(e, t),
              lineHeight: isLight ? 1.5 : 1.55,
              fontFamily: t.fontUi,
            }}
          >
            <span style={{ color: t.textDim, fontFamily: t.fontMono, fontSize: isLight ? 9 : 9 }}>
              T{e.tick}{' '}
            </span>
            {e.text}
          </div>
        ))}
      </div>
    </div>
  )
}
