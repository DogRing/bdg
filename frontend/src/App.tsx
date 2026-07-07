import { useState, useEffect, useMemo, useCallback } from 'react'
import { Header } from './components/Header'
import { WorldCanvas } from './components/WorldCanvas'
import { WorldCanvas3D } from './components/WorldCanvas3D'
import { Sidebar } from './components/Sidebar'
import { useSSE } from './hooks/useSSE'
import { useWorld } from './hooks/useWorld'
import { createSpriteCache } from './assets/sprites'
import { LIGHT, DARK } from './theme'
import { API_BASE } from './config'
import type { Theme } from './types'

export default function App() {
  const [theme, setTheme] = useState<Theme>('light')
  const [view, setView] = useState<'2d' | '3d'>('3d')
  const t = theme === 'dark' ? DARK : LIGHT
  const isLight = theme === 'light'

  const { state, dispatchEvent, setConnection, selectAgent, togglePause, loadSnapshot, loadTerrain } = useWorld()

  // One sprite cache for the app lifetime, injected into the render layer
  // (assets SPEC: factory + injection, no module-level singleton).
  const sprites = useMemo(() => createSpriteCache(), [])
  useEffect(() => { void sprites.preload() }, [sprites])

  // Bootstrap the agent roster + terrain grid once on mount; after that, live
  // state streams over SSE (TickDone/WorldFrame + terrain_delta).
  useEffect(() => {
    void loadSnapshot()
    void loadTerrain()
  }, [loadSnapshot, loadTerrain])

  // SSE connection
  useSSE(dispatchEvent, setConnection)

  // New map: the backend rebuilds the world from its fixture with a NEW seed
  // (random terrain + placements re-rolled — POST /api/regen; the same-seed
  // POST /api/restart stays available but has no button), then we reload the
  // page so client-side state (entity maps, fx, event log) starts fresh too.
  const regenWorld = useCallback(async () => {
    if (!window.confirm('Generate a new map? Terrain, plants and animals are re-rolled from a fresh seed.')) return
    try {
      const res = await fetch(`${API_BASE}/api/regen`, { method: 'POST' })
      if (!res.ok) throw new Error(`regen failed: ${res.status}`)
      // Give the sim a moment to rebuild + flush the fresh snapshot/terrain.
      setTimeout(() => window.location.reload(), 1500)
    } catch (err) {
      window.alert(`New map failed — is the backend running? (${String(err)})`)
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
