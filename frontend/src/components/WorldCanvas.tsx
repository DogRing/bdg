import { useRef, useEffect, useCallback } from 'react'
import type { AgentState, WorldObject } from '../types'
import type { ThemeTokens } from '../theme'
import { drawTerrain, drawObjects, drawAgents, hitTestAgent, buildTransform } from '../utils/canvasRenderer'

interface Props {
  agents: AgentState[]
  objects: WorldObject[]
  selectedId: string | null
  t: ThemeTokens
  onSelectAgent: (id: string | null) => void
}

export function WorldCanvas({ agents, objects, selectedId, t, onSelectAgent }: Props) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const rafRef = useRef<number>(0)
  const dirtyRef = useRef(true)

  // Redraw on next RAF whenever any input changes.
  useEffect(() => { dirtyRef.current = true }, [agents, objects, selectedId, t])

  // RAF render loop: background terrain → real resource objects → agents, all on
  // ONE world transform (anchored on objects + agents so they align).
  useEffect(() => {
    function render() {
      rafRef.current = requestAnimationFrame(render)
      if (!dirtyRef.current) return
      dirtyRef.current = false

      const c = canvasRef.current
      if (!c) return
      const ctx = c.getContext('2d')
      if (!ctx) return

      const dpr = window.devicePixelRatio || 1
      const W = c.width / dpr   // logical (CSS) px
      const H = c.height / dpr
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0) // map logical → device each frame

      drawTerrain(ctx, W, H, t)
      const tr = buildTransform([...objects, ...agents], W, H)
      drawObjects(ctx, objects, tr, t)
      drawAgents(ctx, agents, selectedId, t, tr)
    }

    rafRef.current = requestAnimationFrame(render)
    return () => cancelAnimationFrame(rafRef.current)
  }, [agents, objects, selectedId, t])

  // Resize observer: keep the backing store in sync with display size (DPR-aware).
  // The render loop applies the DPR transform itself, so we only size here.
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ro = new ResizeObserver(entries => {
      for (const e of entries) {
        const { width, height } = e.contentRect
        const dpr = window.devicePixelRatio || 1
        canvas.width = Math.round(width * dpr)
        canvas.height = Math.round(height * dpr)
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
    const x = e.clientX - rect.left  // CSS px == logical px used by the transform
    const y = e.clientY - rect.top
    const tr = buildTransform([...objects, ...agents], canvas.width / dpr, canvas.height / dpr)
    onSelectAgent(hitTestAgent(agents, x, y, tr))
  }, [agents, objects, onSelectAgent])

  return (
    <div style={{ flex: 1, position: 'relative', overflow: 'hidden' }}>
      <canvas
        ref={canvasRef}
        onClick={handleClick}
        style={{ width: '100%', height: '100%', display: 'block', cursor: 'crosshair' }}
      />

      {/* Resource legend */}
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
        <div style={{ fontFamily: t.fontSerif, fontSize: 9, letterSpacing: '0.1em', textTransform: 'uppercase', color: t.textMuted, marginBottom: 4 }}>Resources</div>
        {([['berry', '#4a9030'], ['water', '#3a86d0'], ['shelter', '#9a6a3a']] as const).map(([label, col]) => (
          <div key={label} style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <span style={{
              width: 7, height: 7,
              background: col,
              borderRadius: label === 'shelter' ? 1 : '50%',
              display: 'inline-block',
              boxShadow: t.glow ? `0 0 4px ${col}` : undefined,
            }} />
            {label}
          </div>
        ))}
      </div>

      {/* Counts overlay */}
      <div style={{
        position: 'absolute', top: 10, right: 12,
        fontFamily: t.fontMono, fontSize: 10,
        color: t.textDim,
        background: t.glow ? 'rgba(20,18,16,0.7)' : 'rgba(240,227,192,0.7)',
        padding: '3px 8px',
        border: t.glow ? `1px solid ${t.panelBorder}` : undefined,
        borderRadius: t.glow ? 0 : 2,
      }}>
        {`${agents.length} agents · ${objects.length} resources`}
      </div>
    </div>
  )
}
