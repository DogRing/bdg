import { useState, useEffect, useMemo } from 'react'
import { Header } from './components/Header'
import { WorldCanvas } from './components/WorldCanvas'
import { Sidebar } from './components/Sidebar'
import { useSSE } from './hooks/useSSE'
import { useWorld } from './hooks/useWorld'
import { createSpriteCache } from './assets/sprites'
import { LIGHT, DARK } from './theme'
import type { Theme } from './types'

export default function App() {
  const [theme, setTheme] = useState<Theme>('light')
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

  const agentList = Array.from(state.agents.values())

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
      />

      <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
        <WorldCanvas
          agents={agentList}
          objects={state.objects}
          animals={state.animals}
          flora={state.flora}
          climate={state.climate}
          terrain={state.terrain}
          fx={state.fx}
          render={state.render}
          sprites={sprites}
          selectedId={state.selectedAgentId}
          t={t}
          onSelectAgent={selectAgent}
        />

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
