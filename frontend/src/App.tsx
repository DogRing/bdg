import { useState, useEffect } from 'react'
import { Header } from './components/Header'
import { WorldCanvas } from './components/WorldCanvas'
import { Sidebar } from './components/Sidebar'
import { useSSE } from './hooks/useSSE'
import { useWorld } from './hooks/useWorld'
import { LIGHT, DARK } from './theme'
import type { Theme } from './types'

export default function App() {
  const [theme, setTheme] = useState<Theme>('dark')
  const t = theme === 'dark' ? DARK : LIGHT
  const isLight = theme === 'light'

  const { state, dispatchEvent, setConnection, selectAgent, togglePause, loadSnapshot } = useWorld()

  // Load initial snapshot on mount
  useEffect(() => {
    void loadSnapshot()
  }, [loadSnapshot])

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
