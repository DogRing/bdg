import { useRef, useEffect, useMemo, type MutableRefObject } from 'react'
import type { AgentState, AnimalState, TerrainGrid, RenderConfig } from '../types'
import type { ThemeTokens } from '../theme'
import type { CameraFocus } from '../gl/worldGL'
import {
  buildTransform, wx, wy, initialCamera, makeTerrainRaster, drawTerrain,
  type TerrainRaster,
} from '../render'
import { minimapSize, cameraCone } from './minimapGeom'

// Minimap — the retained 2D renderer, repurposed as a small bottom-right overview of the whole
// world (docs/plans/frontend.md §11: capability = camera-marker/display-only, detail = simplified).
// It reuses the pure render library (whole-world-fit transform + terrain raster) and draws entities
// as dots plus a "where the 3D camera looks" cone read from the GL renderer's focus (focusRef).
// Display-only: no input handlers (the main 3D view owns camera + selection). Geometry (size, cone
// direction/radius) lives in ./minimapGeom (unit-tested).

interface Props {
  agents: AgentState[]
  animals: Map<string, AnimalState>
  terrain: TerrainGrid | null
  render: RenderConfig | null
  selectedId: string | null
  t: ThemeTokens
  focusRef: MutableRefObject<CameraFocus | null>
}

// The world w×h the minimap frames — RenderConfig.bounds when known (derived from the terrain grid,
// frontend/SPEC.md), else a neutral square until it arrives. Drives the minimap's aspect ratio.
function worldExtent(render: RenderConfig | null): { w: number; h: number } {
  if (render?.bounds) {
    const { min, max } = render.bounds
    return { w: Math.max(max.x - min.x, 1), h: Math.max(max.y - min.y, 1) }
  }
  return { w: 1, h: 1 }
}

export function Minimap({ agents, animals, terrain, render, selectedId, t, focusRef }: Props) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const rafRef = useRef<number>(0)
  const rasterRef = useRef<TerrainRaster | null>(null)
  const propsRef = useRef({ agents, animals, terrain, render, selectedId, t, focusRef })
  propsRef.current = { agents, animals, terrain, render, selectedId, t, focusRef }

  // CSS size follows the world aspect ratio (minimapGeom, unit-tested).
  const { w: worldW, h: worldH } = worldExtent(render)
  const { mmW, mmH } = useMemo(() => {
    const { w, h } = minimapSize(worldW, worldH)
    return { mmW: w, mmH: h }
  }, [worldW, worldH])

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const dpr = window.devicePixelRatio || 1

    const frame = () => {
      rafRef.current = requestAnimationFrame(frame)
      const p = propsRef.current
      const ctx = canvas.getContext('2d')
      if (!ctx) return

      // Rebuild the terrain raster only on grid identity change (reducer replaces it per delta).
      if (p.terrain) { if (rasterRef.current?.grid !== p.terrain) rasterRef.current = makeTerrainRaster(p.terrain) }
      else rasterRef.current = null

      // Backing store follows CSS size × DPR; draw in CSS px.
      const bw = Math.round(mmW * dpr), bh = Math.round(mmH * dpr)
      if (canvas.width !== bw || canvas.height !== bh) { canvas.width = bw; canvas.height = bh }
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      ctx.clearRect(0, 0, mmW, mmH)

      // Whole-world fit: reuse the render layer's bounds-fit camera + shared transform. `pts` is the
      // entity-bbox fallback used until RenderConfig.bounds arrives.
      const pts = [...p.agents, ...p.animals.values()]
      const cam = initialCamera(p.render, pts, mmW, mmH)
      if (!cam) return
      const tr = buildTransform(cam, mmW, mmH)

      drawTerrain(ctx, p.terrain, tr, rasterRef.current)

      // Animals: small neutral dots (under agents so agent dots read on top).
      ctx.fillStyle = p.t.positive
      for (const a of p.animals.values()) {
        ctx.beginPath(); ctx.arc(wx(a.pos.x, tr), wy(a.pos.y, tr), 1.8, 0, Math.PI * 2); ctx.fill()
      }

      // Agents: accent dots; the selected one gets a highlight ring.
      for (const a of p.agents) {
        const cx = wx(a.pos.x, tr), cy = wy(a.pos.y, tr)
        if (a.id === p.selectedId) {
          ctx.strokeStyle = p.t.textPrimary; ctx.lineWidth = 1.5
          ctx.beginPath(); ctx.arc(cx, cy, 4, 0, Math.PI * 2); ctx.stroke()
        }
        ctx.fillStyle = p.t.accent
        ctx.beginPath(); ctx.arc(cx, cy, 2.4, 0, Math.PI * 2); ctx.fill()
      }

      // Camera-focus marker: a translucent cone from the 3D camera's ground focus, opening in the
      // view-forward direction (== -sin/-cos yaw on the ground) and sized by dist/fitDist so it
      // grows as the camera zooms out toward framing the whole world, plus a focus reticle.
      const f = p.focusRef.current
      if (f) {
        const fx = wx(f.x, tr), fy = wy(f.z, tr) // focus.z == world y (GL ground plane is x,z)
        const { angle: ang, radius: r, halfAngle: half } = cameraCone(f, mmW, mmH)
        const cone = p.t.glow ? '230,220,190' : '60,30,12'
        ctx.beginPath()
        ctx.moveTo(fx, fy)
        ctx.arc(fx, fy, r, ang - half, ang + half)
        ctx.closePath()
        ctx.fillStyle = `rgba(${cone},0.14)`; ctx.fill()
        ctx.strokeStyle = `rgba(${cone},0.5)`; ctx.lineWidth = 1; ctx.stroke()
        ctx.fillStyle = p.t.accent
        ctx.beginPath(); ctx.arc(fx, fy, 2, 0, Math.PI * 2); ctx.fill()
      }
    }
    rafRef.current = requestAnimationFrame(frame)
    return () => cancelAnimationFrame(rafRef.current)
  }, [mmW, mmH])

  return (
    <div
      aria-label="minimap"
      style={{
        position: 'absolute', bottom: 12, right: 12, zIndex: 4,
        width: mmW, height: mmH, lineHeight: 0,
        background: t.glow ? 'rgba(20,18,16,0.9)' : 'rgba(240,227,192,0.9)',
        border: `1px solid ${t.panelBorder}`, borderRadius: t.glow ? 0 : 2,
        boxShadow: t.glow ? 'none' : '0 1px 4px rgba(0,0,0,0.25)',
      }}
    >
      <canvas ref={canvasRef} style={{ width: mmW, height: mmH, display: 'block' }} />
    </div>
  )
}
