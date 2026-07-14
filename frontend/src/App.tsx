import { useState, useEffect, useMemo, useCallback, useRef } from 'react'
import { Header } from './components/Header'
import { WorldCanvas } from './components/WorldCanvas'
import { WorldCanvas3D } from './components/WorldCanvas3D'
import { Sidebar } from './components/Sidebar'
import { StatusHud } from './components/StatusHud'
import { useSSE } from './hooks/useSSE'
import { useWorld } from './hooks/useWorld'
import { createSpriteCache } from './assets/sprites'
import { LIGHT, DARK } from './theme'
import { API_BASE } from './config'
import { fetchPublishedRevision, waitForRegenReady } from './utils/regenReady'
import type { Theme } from './types'

export default function App() {
  const [theme, setTheme] = useState<Theme>('light')
  const [view, setView] = useState<'2d' | '3d'>('3d')
  const t = theme === 'dark' ? DARK : LIGHT
  const isLight = theme === 'light'

  const { state, dispatchEvent, setConnection, selectAgent, togglePause, loadSnapshot } = useWorld()

  // One sprite cache for the app lifetime, injected into the render layer
  // (assets SPEC: factory + injection, no module-level singleton).
  const sprites = useMemo(() => createSpriteCache(), [])
  useEffect(() => { void sprites.preload() }, [sprites])

  // Bootstrap (SPEC §Bootstrap): snapshot FIRST — its stream_cursor is where
  // SSE replay starts, so the [capture, connect) window is replayed instead of
  // lost. Terrain follows via useWorld's availability effect (explicit env-off
  // never polls); SSE connects below once the baseline is in.
  useEffect(() => { void loadSnapshot() }, [loadSnapshot])

  // SSE (re)connects resume strictly after the last applied stream id (falls
  // back to the snapshot cursor) — lossless across the bootstrap window,
  // disconnects and server-signalled gaps (useWorld reacquires on StreamGap).
  const cursorRef = useRef('')
  useEffect(() => {
    cursorRef.current = state.lastAppliedStreamId !== '' ? state.lastAppliedStreamId : state.snapshotCursor
  }, [state.lastAppliedStreamId, state.snapshotCursor])
  useSSE(dispatchEvent, setConnection, {
    enabled: state.snapshotLoaded,
    getCursor: () => cursorRef.current,
  })

  // New map (SPEC §New-map flow): the backend rebuilds the world from its
  // fixture with a NEW seed (random terrain + placements re-rolled — POST
  // /api/regen; the same-seed POST /api/restart stays available but has no
  // button). The 202 only means the request was ACCEPTED. Readiness is the
  // PUBLISHED world_revision (data-contracts §2): read it before submitting
  // (unreadable ⇒ do not submit — completion could not be verified), poll
  // until a NEW revision is published, verify the revision-tagged baselines,
  // and only then reload — never a fixed timer, never tick heuristics.
  const regenBusy = useRef(false)
  const regenWorld = useCallback(async () => {
    if (regenBusy.current) return
    if (!window.confirm('Generate a new map? Terrain, plants and animals are re-rolled from a fresh seed.')) return
    regenBusy.current = true
    try {
      const before = await fetchPublishedRevision()
      if (before === null) {
        window.alert('Cannot verify new-map readiness: the backend has not published a world_revision yet (/api/meta). Not submitting — try again in a moment.')
        return
      }
      const res = await fetch(`${API_BASE}/api/regen`, { method: 'POST' })
      if (!res.ok) throw new Error(`regen failed: ${res.status}`)
      const ready = await waitForRegenReady(before)
      if (!ready) {
        window.alert('New map is not ready — the backend may still be generating it, or the regen was aborted (the current world keeps running in that case). This view is unchanged; try again or reload manually in a moment.')
        return
      }
      window.location.reload()
    } catch (err) {
      window.alert(`New map failed — is the backend running? (${String(err)})`)
    } finally {
      regenBusy.current = false
    }
  }, [])

  const agentList = Array.from(state.agents.values())
  const canvasProps = {
    agents: agentList, objects: state.objects, animals: state.animals, flora: state.flora,
    climate: state.climate, terrain: state.terrain, fx: state.fx, render: state.render,
    sprites, selectedId: state.selectedAgentId, t, onSelectAgent: selectAgent,
  }

  return (
    <div style={{
      width: '100vw',
      height: '100vh',
      display: 'flex',
      flexDirection: 'column',
      background: t.appBg,
      overflow: 'hidden',
    }}>
      <Header
        t={t}
        theme={theme}
        onToggleTheme={() => setTheme(th => th === 'dark' ? 'light' : 'dark')}
        world={state}
        onTogglePause={togglePause}
        onRegen={regenWorld}
      />

      <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
        <div style={{ flex: 1, position: 'relative', display: 'flex', minWidth: 0 }}>
          {view === '3d' ? <WorldCanvas3D {...canvasProps} /> : <WorldCanvas {...canvasProps} />}
          <button
            onClick={() => setView(v => (v === '3d' ? '2d' : '3d'))}
            aria-label={view === '3d' ? 'switch to 2D map' : 'switch to 3D view'}
            style={{
              position: 'absolute', top: 10, left: 12, zIndex: 5, padding: '4px 10px',
              background: t.glow ? 'rgba(20,18,16,0.88)' : 'rgba(240,227,192,0.88)',
              border: `1px solid ${t.panelBorder}`, borderRadius: t.glow ? 0 : 2,
              color: t.textMuted, fontFamily: t.fontMono, fontSize: 11, cursor: 'pointer',
            }}
          >
            {view === '3d' ? '2D map' : '3D view'}
          </button>
          <StatusHud t={t} world={state} />
        </div>

        <Sidebar
          world={state}
          t={t}
          isLight={isLight}
          onTogglePause={togglePause}
        />
      </div>
    </div>
  )
}
