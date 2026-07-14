import { useRef, useEffect, useCallback } from 'react'
import type {
  AgentState, WorldObject, AnimalState, PlantState, ClimateState, TerrainGrid,
  FxInstance, RenderConfig,
} from '../types'
import type { ThemeTokens } from '../theme'
import type { SpriteCache } from '../assets/sprites'
import {
  buildTransform, drawObjects, drawAgents, drawFlora, drawFauna, drawFx,
  drawTerrain, makeTerrainRaster, drawAmbient, hitTest, wx, wy,
  initialCamera, cameraZoom, cameraPan, cameraFollow, cameraTick,
  type CameraState, type TerrainRaster,
} from '../render'

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
}

export function WorldCanvas({ agents, objects, animals, flora, climate, terrain, fx, render, sprites, selectedId, t, onSelectAgent }: Props) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const rafRef = useRef<number>(0)
  const dirtyRef = useRef(true)
  // Camera is the only view state (render SPEC): initialized lazily on the first
  // frame that knows either RenderConfig or some entity positions.
  const camRef = useRef<CameraState | null>(null)
  // Whether the camera has been anchored to RenderConfig.bounds. An entity-bbox
  // fit is provisional ("auto-fit until fetched", render SPEC §camera): when the
  // config arrives after init — REST world geometry can lose the race against
  // the first SSE entities — the camera re-anchors ONCE to the world bounds.
  const anchoredRef = useRef(false)
  const dragRef = useRef<{ x: number; y: number; moved: boolean } | null>(null)
  // Derived terrain raster, rebuilt only when the grid object identity changes.
  const rasterRef = useRef<TerrainRaster | null>(null)
  // Latest props for the (mounted-once) RAF loop + native wheel listener.
  const propsRef = useRef({ agents, objects, animals, flora, climate, terrain, fx, render, sprites, selectedId, t, onSelectAgent })
  propsRef.current = { agents, objects, animals, flora, climate, terrain, fx, render, sprites, selectedId, t, onSelectAgent }

  // Redraw on next RAF whenever any input changes.
  useEffect(() => { dirtyRef.current = true }, [agents, objects, animals, flora, climate, terrain, fx, render, selectedId, t])

  const viewSize = useCallback(() => {
    const c = canvasRef.current
    const dpr = window.devicePixelRatio || 1
    return c ? { W: c.width / dpr, H: c.height / dpr } : { W: 0, H: 0 }
  }, [])

  // RAF render loop: background → world bounds → objects → agents, all through
  // ONE camera-driven transform (render SPEC: one buildTransform per frame).
  useEffect(() => {
    function frame() {
      rafRef.current = requestAnimationFrame(frame)
      const cam = camRef.current
      // Keep drawing while uninitialized (waiting for data), following, or
      // animating (fauna sprite frames + interpolation + live fx + rain).
      if (!dirtyRef.current && cam && !cam.follow &&
          propsRef.current.animals.size === 0 && propsRef.current.fx.length === 0 &&
          !propsRef.current.climate?.raining) return
      dirtyRef.current = false
      const clockMs = performance.now() // sampled ONCE per frame at the loop boundary

      const c = canvasRef.current
      const ctx = c?.getContext('2d')
      if (!c || !ctx) return

      const dpr = window.devicePixelRatio || 1
      const W = c.width / dpr   // logical (CSS) px
      const H = c.height / dpr
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0) // map logical → device each frame

      const p = propsRef.current
      ctx.fillStyle = p.t.canvasBg
      ctx.fillRect(0, 0, W, H)

      if (!camRef.current) {
        camRef.current = initialCamera(p.render, [...p.objects, ...p.agents], W, H)
        if (!camRef.current) return // nothing known yet — background only
        anchoredRef.current = p.render !== null
      } else if (!anchoredRef.current && p.render && !dragRef.current) {
        // RenderConfig arrived after a provisional entity-bbox fit: re-anchor
        // once to the world bounds (skip mid-drag; never repeats afterwards).
        camRef.current = initialCamera(p.render, [], W, H) ?? camRef.current
        anchoredRef.current = true
      }
      camRef.current = cameraTick(camRef.current, p.agents, p.animals.values(), clockMs)
      const tr = buildTransform(camRef.current, W, H)

      // Terrain raster: re-rasterize only when the grid object changed
      // (reducer replaces it on /api/terrain load + every terrain_delta merge).
      if (p.terrain && rasterRef.current?.grid !== p.terrain) {
        rasterRef.current = makeTerrainRaster(p.terrain)
      }
      drawTerrain(ctx, p.terrain, tr, rasterRef.current)

      // World bounds outline (the real world edge — replaces the decorative map).
      if (p.render) {
        const { min, max } = p.render.bounds
        ctx.strokeStyle = p.t.gridColor
        ctx.lineWidth = 1
        ctx.strokeRect(wx(min.x, tr), wy(min.y, tr), (max.x - min.x) * tr.sx, (max.y - min.y) * tr.sy)
      }

      // Layer order (render SPEC): terrain → flora → objects → animals →
      // agents → fx → ambient.
      drawFlora(ctx, p.flora, tr, p.sprites, clockMs, p.fx, p.climate, p.animals.values())
      drawObjects(ctx, p.objects, tr, p.t)
      drawFauna(ctx, p.animals.values(), tr, p.sprites, clockMs, p.fx)
      drawAgents(ctx, p.agents, p.selectedId, p.t, tr)
      drawFx(ctx, p.fx, tr, p.sprites, clockMs)
      drawAmbient(ctx, p.climate, W, H, clockMs)
    }

    rafRef.current = requestAnimationFrame(frame)
    return () => cancelAnimationFrame(rafRef.current)
  }, [])

  // Resize observer: keep the backing store in sync with display size (DPR-aware).
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

  // Wheel zoom, cursor-anchored. Native listener: React's onWheel is passive,
  // and we must preventDefault to keep the page from scrolling.
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const cam = camRef.current
      if (!cam) return
      const rect = canvas.getBoundingClientRect()
      const { W, H } = viewSize()
      const cursor = { x: e.clientX - rect.left, y: e.clientY - rect.top }
      camRef.current = cameraZoom(cam, cursor, Math.exp(-e.deltaY * 0.0015), W, H)
      dirtyRef.current = true
    }
    canvas.addEventListener('wheel', onWheel, { passive: false })
    return () => canvas.removeEventListener('wheel', onWheel)
  }, [viewSize])

  // On-screen +/− buttons: zoom about the viewport centre (same pure reducer
  // as wheel zoom, cursor pinned to the middle of the view).
  const zoomBy = useCallback((factor: number) => {
    const cam = camRef.current
    if (!cam) return
    const { W, H } = viewSize()
    camRef.current = cameraZoom(cam, { x: W / 2, y: H / 2 }, factor, W, H)
    dirtyRef.current = true
  }, [viewSize])

  // Drag = pan (breaks follow); a click (no drag) selects + follows the hit
  // entity: agents get the detail panel, animals follow-only (render SPEC).
  const onPointerDown = useCallback((e: React.PointerEvent<HTMLCanvasElement>) => {
    e.currentTarget.setPointerCapture(e.pointerId)
    dragRef.current = { x: e.clientX, y: e.clientY, moved: false }
  }, [])

  const onPointerMove = useCallback((e: React.PointerEvent<HTMLCanvasElement>) => {
    const drag = dragRef.current
    const cam = camRef.current
    if (!drag || !cam) return
    const dx = e.clientX - drag.x
    const dy = e.clientY - drag.y
    if (!drag.moved && dx * dx + dy * dy < 9) return // 3px slop before it becomes a pan
    drag.moved = true
    camRef.current = cameraPan(cam, dx, dy)
    drag.x = e.clientX
    drag.y = e.clientY
    dirtyRef.current = true
  }, [])

  const onPointerUp = useCallback((e: React.PointerEvent<HTMLCanvasElement>) => {
    const drag = dragRef.current
    dragRef.current = null
    if (!drag || drag.moved) return // pan end — not a click
    const canvas = canvasRef.current
    const cam = camRef.current
    if (!canvas || !cam) return
    const rect = canvas.getBoundingClientRect()
    const { W, H } = viewSize()
    const tr = buildTransform(cam, W, H)
    const hit = hitTest(propsRef.current.agents, propsRef.current.animals.values(),
      tr, e.clientX - rect.left, e.clientY - rect.top)
    camRef.current = cameraFollow(cam, hit)
    propsRef.current.onSelectAgent(hit?.kind === 'agent' ? hit.id : null)
    dirtyRef.current = true
  }, [viewSize])

  return (
    <div style={{ flex: 1, position: 'relative', overflow: 'hidden' }}>
      <canvas
        ref={canvasRef}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        style={{ width: '100%', height: '100%', display: 'block', cursor: 'crosshair', touchAction: 'none' }}
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

      {/* Zoom controls */}
      <div style={{ position: 'absolute', bottom: 14, right: 12, display: 'flex', flexDirection: 'column', gap: 4 }}>
        {([['+', 1.4], ['−', 1 / 1.4]] as const).map(([label, factor]) => (
          <button
            key={label}
            onClick={() => zoomBy(factor)}
            aria-label={label === '+' ? 'zoom in' : 'zoom out'}
            style={{
              width: 30, height: 30,
              background: t.glow ? 'rgba(20,18,16,0.88)' : 'rgba(240,227,192,0.88)',
              border: `1px solid ${t.panelBorder}`,
              borderRadius: t.glow ? 0 : 2,
              color: t.textMuted,
              fontFamily: t.fontMono,
              fontSize: 16,
              lineHeight: 1,
              cursor: 'pointer',
            }}
          >
            {label}
          </button>
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
        {`${agents.length} agents · ${objects.length} resources · scroll zoom / drag pan`}
      </div>
    </div>
  )
}
