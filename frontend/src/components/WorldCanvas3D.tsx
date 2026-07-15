import { useRef, useEffect, useState, useCallback } from 'react'
import type {
  AgentState, WorldObject, AnimalState, PlantState, ClimateState, TerrainGrid,
  FxInstance, RenderConfig,
} from '../types'
import type { ThemeTokens } from '../theme'
import type { SpriteCache } from '../assets/sprites'
import { createWorldGL, type WorldGL, type CameraFocus } from '../gl/worldGL'

// The primary viewer (frontend/SPEC.md §Purpose · Viewer note). Draws terrain + agents + animal &
// flora sprite billboards + objects + climate atmosphere (day/night, weather, wind — gl/atmosphere.ts);
// fx arrive in a later phase. Shares the canvasProps shape with the retained render library.
interface Props {
  agents: AgentState[]
  objects: WorldObject[]
  animals: Map<string, AnimalState>
  flora: PlantState[]
  climate: ClimateState | null
  terrain: TerrainGrid | null
  fx: FxInstance[]
  render: RenderConfig | null
  sprites: SpriteCache
  selectedId: string | null
  t: ThemeTokens
  onSelectAgent: (id: string | null) => void
  // Written each frame with the GL camera's ground focus so the Minimap overlay can mark it.
  focusOut?: React.MutableRefObject<CameraFocus | null>
}

export function WorldCanvas3D(props: Props) {
  const { agents, terrain, render, t } = props
  const glRef = useRef<HTMLCanvasElement>(null)
  const ovRef = useRef<HTMLCanvasElement>(null)
  const handleRef = useRef<WorldGL | null>(null)
  const rafRef = useRef<number>(0)
  const fittedRef = useRef(false)
  const dragRef = useRef<{ x: number; y: number; moved: boolean } | null>(null)
  const [error, setError] = useState<string | null>(null)

  const propsRef = useRef(props)
  propsRef.current = props

  // Create the GL renderer once and run a continuous RAF loop (water ripple +
  // entity interpolation need every-frame redraws).
  useEffect(() => {
    const glc = glRef.current, ovc = ovRef.current
    if (!glc || !ovc) return
    const h = createWorldGL(glc, ovc, propsRef.current.sprites)
    if (!h.ok) { setError(h.error); return }
    handleRef.current = h
    // seed with whatever we already have
    h.setTerrain(propsRef.current.terrain)
    if (propsRef.current.render || propsRef.current.terrain) { h.fit(propsRef.current.render); fittedRef.current = true }

    const frame = () => {
      rafRef.current = requestAnimationFrame(frame)
      const p = propsRef.current
      h.draw(p.agents, p.animals.values(), p.objects, p.flora, p.selectedId, performance.now(), p.climate)
      if (p.focusOut) p.focusOut.current = h.getFocus()
    }
    rafRef.current = requestAnimationFrame(frame)
    return () => { cancelAnimationFrame(rafRef.current); h.dispose(); handleRef.current = null }
  }, [])

  // Terrain grid rebuild on identity change (reducer replaces it on load + every delta).
  useEffect(() => {
    const h = handleRef.current
    if (!h) return
    h.setTerrain(terrain)
    if (!fittedRef.current && (render || terrain)) { h.fit(render); fittedRef.current = true }
  }, [terrain, render])

  // Wheel: zoom (plain) · tilt (Alt) · rotate (Shift). Native listener to preventDefault.
  useEffect(() => {
    const glc = glRef.current
    if (!glc) return
    const onWheel = (e: WheelEvent) => {
      const h = handleRef.current
      if (!h) return
      e.preventDefault() // always: plain wheel is intentionally inert (no accidental page scroll/zoom)
      // Browsers remap Alt/Shift + vertical wheel to horizontal scroll (deltaX), so read
      // whichever axis carries the notch — otherwise the modifier gestures silently do nothing.
      const dv = e.deltaY || e.deltaX
      if (e.ctrlKey || e.metaKey) h.zoomBy(Math.exp(dv * 0.0015)) // scroll up = in, down = out
      else if (e.altKey) h.tiltBy(-dv * 0.0016)
      else if (e.shiftKey) h.orbitBy(dv * 0.004)
      // plain wheel: locked
    }
    glc.addEventListener('wheel', onWheel, { passive: false })
    return () => glc.removeEventListener('wheel', onWheel)
  }, [])

  const onPointerDown = useCallback((e: React.PointerEvent<HTMLCanvasElement>) => {
    e.currentTarget.setPointerCapture(e.pointerId)
    dragRef.current = { x: e.clientX, y: e.clientY, moved: false }
  }, [])

  const onPointerMove = useCallback((e: React.PointerEvent<HTMLCanvasElement>) => {
    const drag = dragRef.current, h = handleRef.current
    if (!drag || !h) return
    const dx = e.clientX - drag.x, dy = e.clientY - drag.y
    if (!drag.moved && dx * dx + dy * dy < 9) return
    drag.moved = true
    h.panBy(dx, dy)
    drag.x = e.clientX; drag.y = e.clientY
  }, [])

  const onPointerUp = useCallback((e: React.PointerEvent<HTMLCanvasElement>) => {
    const drag = dragRef.current
    dragRef.current = null
    const h = handleRef.current
    if (!drag || drag.moved || !h) return // pan end — not a click
    const rect = e.currentTarget.getBoundingClientRect()
    const hit = h.pick(e.clientX - rect.left, e.clientY - rect.top)
    propsRef.current.onSelectAgent(hit?.kind === 'agent' ? hit.id : null)
  }, [])

  const zoomBy = useCallback((f: number) => handleRef.current?.zoomBy(f), [])

  return (
    <div style={{ flex: 1, position: 'relative', overflow: 'hidden' }}>
      <canvas
        ref={glRef}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', display: 'block', cursor: 'grab', touchAction: 'none' }}
      />
      <canvas
        ref={ovRef}
        style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', display: 'block', pointerEvents: 'none' }}
      />

      {error && (
        <div style={{
          position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center',
          padding: 24, textAlign: 'center', color: t.textMuted, fontFamily: t.fontMono, fontSize: 13,
        }}>
          {`3D view unavailable (${error}). A WebGL-capable browser is required.`}
        </div>
      )}

      {/* Zoom controls — bottom-LEFT so the bottom-right minimap overlay (App) has the corner. */}
      <div style={{ position: 'absolute', bottom: 14, left: 12, display: 'flex', flexDirection: 'column', gap: 4 }}>
        {([['+', 1.25], ['−', 1 / 1.25]] as const).map(([label, factor]) => (
          <button
            key={label}
            onClick={() => zoomBy(factor)}
            aria-label={label === '+' ? 'zoom in' : 'zoom out'}
            style={{
              width: 30, height: 30,
              background: t.glow ? 'rgba(20,18,16,0.88)' : 'rgba(240,227,192,0.88)',
              border: `1px solid ${t.panelBorder}`, borderRadius: t.glow ? 0 : 2,
              color: t.textMuted, fontFamily: t.fontMono, fontSize: 16, lineHeight: 1, cursor: 'pointer',
            }}
          >
            {label}
          </button>
        ))}
      </div>

      {/* Counts + controls hint */}
      <div style={{
        position: 'absolute', top: 10, right: 12,
        fontFamily: t.fontMono, fontSize: 10, color: t.textDim,
        background: t.glow ? 'rgba(20,18,16,0.7)' : 'rgba(240,227,192,0.7)',
        padding: '3px 8px', border: t.glow ? `1px solid ${t.panelBorder}` : undefined, borderRadius: t.glow ? 0 : 2,
      }}>
        {`${agents.length} agents · Ctrl zoom · Alt tilt · Shift rotate · drag pan`}
      </div>
    </div>
  )
}
