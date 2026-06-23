import { useRef, useEffect, useCallback } from 'react'
import type { AgentState } from '../types'
import type { ThemeTokens } from '../theme'
import { drawTerrain, drawAgents, hitTestAgent } from '../utils/canvasRenderer'

interface Props {
  agents: AgentState[]
  selectedId: string | null
  t: ThemeTokens
  onSelectAgent: (id: string | null) => void
}

export function WorldCanvas({ agents, selectedId, t, onSelectAgent }: Props) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const rafRef = useRef<number>(0)
  const dirtyRef = useRef(true) // terrain needs redraw on theme change
  const terrainCacheRef = useRef<ImageData | null>(null)

  // Invalidate terrain cache whenever theme changes (t reference changes)
  useEffect(() => {
    dirtyRef.current = true
    terrainCacheRef.current = null
  }, [t])

  // Mark agents dirty whenever inputs change (will draw on next RAF)
  useEffect(() => { dirtyRef.current = true }, [agents, selectedId, t])

  // RAF render loop
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    function render() {
      rafRef.current = requestAnimationFrame(render)
      if (!dirtyRef.current) return
      dirtyRef.current = false

      const c = canvasRef.current
      if (!c) return
      const ctx = c.getContext('2d', { willReadFrequently: true })
      if (!ctx) return

      const W = c.width
      const H = c.height

      // Restore cached terrain or redraw
      if (terrainCacheRef.current) {
        ctx.putImageData(terrainCacheRef.current, 0, 0)
      } else {
        drawTerrain(ctx, W, H, t)
        terrainCacheRef.current = ctx.getImageData(0, 0, W, H)
      }

      drawAgents(ctx, W, H, agents, selectedId, t)
    }

    rafRef.current = requestAnimationFrame(render)
    return () => cancelAnimationFrame(rafRef.current)
  }, [agents, selectedId, t])

  // Resize observer: keep canvas resolution in sync with display size
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    const ro = new ResizeObserver(entries => {
      for (const e of entries) {
        const { width, height } = e.contentRect
        const dpr = window.devicePixelRatio || 1
        canvas.width = Math.round(width * dpr)
        canvas.height = Math.round(height * dpr)
        const ctx = canvas.getContext('2d', { willReadFrequently: true })
        if (ctx) ctx.scale(dpr, dpr)
        // Invalidate terrain cache on resize
        terrainCacheRef.current = null
        dirtyRef.current = true
      }
    })
    ro.observe(canvas)
    return () => ro.disconnect()
  }, [])

  const handleClick = useCallback((e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current
    if (!canvas) return
    const rect = canvas.getBoundingClientRect()
    const dpr = window.devicePixelRatio || 1
    const x = (e.clientX - rect.left) * dpr
    const y = (e.clientY - rect.top) * dpr
    const hit = hitTestAgent(agents, x / dpr, y / dpr, canvas.width / dpr, canvas.height / dpr)
    onSelectAgent(hit)
  }, [agents, onSelectAgent])

  return (
    <div style={{ flex: 1, position: 'relative', overflow: 'hidden' }}>
      <canvas
        ref={canvasRef}
        onClick={handleClick}
        style={{ width: '100%', height: '100%', display: 'block', cursor: 'crosshair' }}
      />

      {/* Legend overlay */}
      <div style={{
        position: 'absolute', bottom: 14, left: 14,
        background: t.glow ? 'rgba(20,18,16,0.88)' : 'rgba(240,227,192,0.88)',
        padding: '8px 12px',
        border: `1px solid ${t.panelBorder}`,
        borderRadius: t.glow ? 0 : 2,
        fontSize: 10,
        lineHeight: 1.9,
        color: t.textMuted,
        fontFamily: t.fontUi,
      }}>
        <div style={{ fontFamily: t.fontSerif, fontSize: 9, letterSpacing: '0.1em', textTransform: 'uppercase', color: t.textMuted, marginBottom: 4 }}>Legend</div>
        {(['Farmer', 'Merchant', 'Guard', 'Artisan'] as const).map((role, i) => (
          <div key={role} style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <span style={{
              width: 7, height: 7,
              background: t.roleColours[i],
              borderRadius: '50%',
              display: 'inline-block',
              boxShadow: t.glow ? `0 0 4px ${t.roleColours[i]}` : undefined,
            }} />
            {role}
          </div>
        ))}
      </div>

      {/* Coords overlay */}
      <div style={{
        position: 'absolute', top: 10, right: 12,
        fontFamily: t.fontMono, fontSize: 10,
        color: t.textDim,
        background: t.glow ? 'rgba(20,18,16,0.7)' : 'rgba(240,227,192,0.7)',
        padding: '3px 8px',
        border: t.glow ? `1px solid ${t.panelBorder}` : undefined,
        borderRadius: t.glow ? 0 : 2,
      }}>
        {`${agents.length} agents`}
      </div>
    </div>
  )
}
