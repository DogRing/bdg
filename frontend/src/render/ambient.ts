import type { ClimateState } from '../types'
import { PRECIP_SNOW_BELOW_C } from '../assets/manifest'

// Weather/atmosphere overlay (parent SPEC §Ecosystem rendering): day-night
// tint, temperature vignette, animated rain, wind HUD arrow. Pure function of
// (climate, viewport, clockMs) — rain streaks derive from a deterministic
// per-index hash drifted by the clock; no Math.random, no Date.now.

const RAIN_STREAKS = 90

const fract = (v: number) => v - Math.floor(v)
const hash = (i: number, salt: number) => fract(Math.sin(i * 12.9898 + salt) * 43758.5453)

export function drawAmbient(
  ctx: CanvasRenderingContext2D,
  climate: ClimateState | null,
  viewW: number,
  viewH: number,
  clockMs: number,
) {
  if (!climate) return
  ctx.save()

  // Day-night tint: dark blue at night, warm at dawn/dusk, clear by day.
  const h = climate.hourOfDay
  if (climate.dayNight === 'night') {
    ctx.fillStyle = 'rgba(10,20,50,0.38)'
    ctx.fillRect(0, 0, viewW, viewH)
  } else if ((h >= 5 && h < 7) || (h >= 17 && h < 19)) {
    ctx.fillStyle = 'rgba(200,110,30,0.14)'
    ctx.fillRect(0, 0, viewW, viewH)
  }

  // Temperature vignette: cold (blue) ↔ hot (red) edge tint, scaled by °C.
  const temp = climate.temperature
  if (temp < 5 || temp > 25) {
    const g = ctx.createRadialGradient(
      viewW / 2, viewH / 2, Math.min(viewW, viewH) * 0.25,
      viewW / 2, viewH / 2, Math.max(viewW, viewH) * 0.72)
    g.addColorStop(0, 'rgba(0,0,0,0)')
    g.addColorStop(1, temp < 5
      ? `rgba(50,100,255,${Math.min(0.3, (5 - temp) / 25).toFixed(3)})`
      : `rgba(255,60,0,${Math.min(0.3, (temp - 25) / 25).toFixed(3)})`)
    ctx.fillStyle = g
    ctx.fillRect(0, 0, viewW, viewH)
  }

  // Precipitation: rain streaks OR snow flakes depending on temperature (CS1). Per-particle phase
  // from the index hash, position drifted by the clock so it animates — deterministic (no Math.random).
  if (climate.raining) {
    if (climate.temperature < PRECIP_SNOW_BELOW_C) {
      // Snow: slow, wind-drifted flakes (soft white dots) instead of fast vertical streaks.
      const drift = Math.cos(climate.windDir) * climate.windMag
      ctx.fillStyle = 'rgba(255,255,255,0.85)'
      for (let i = 0; i < RAIN_STREAKS; i++) {
        const speed = 0.03 + hash(i, 7) * 0.03 // px/ms — much slower than rain
        const sway = Math.sin(clockMs * 0.0012 + hash(i, 4) * 6.283) * 12
        const x = fract(hash(i, 1) + clockMs * (0.00002 + drift * 0.00008)) * viewW + sway
        const y = fract(hash(i, 2) + (clockMs * speed) / viewH) * viewH
        const r = 1.1 + hash(i, 5) * 1.4
        ctx.beginPath()
        ctx.arc(x, y, r, 0, Math.PI * 2)
        ctx.fill()
      }
    } else {
      ctx.fillStyle = 'rgba(150,200,255,0.45)'
      for (let i = 0; i < RAIN_STREAKS; i++) {
        const speed = 0.25 + hash(i, 7) * 0.2 // px/ms, per-streak
        const x = fract(hash(i, 1) + clockMs * 0.00003 * (1 + hash(i, 3))) * viewW
        const y = fract(hash(i, 2) + (clockMs * speed) / viewH) * viewH
        ctx.fillRect(x, y, 1, 9)
      }
    }
  }

  // Wind HUD arrow (top-right): direction windDir, length/opacity ∝ windMag —
  // the visible cause of scent drift / upwind homing.
  if (climate.windMag > 0) {
    const len = 12 + climate.windMag * 22
    ctx.save()
    ctx.translate(viewW - 44, 44)
    ctx.rotate(climate.windDir)
    ctx.globalAlpha = 0.4 + climate.windMag * 0.6
    ctx.strokeStyle = 'rgba(255,255,255,0.85)'
    ctx.lineWidth = 2
    ctx.beginPath()
    ctx.moveTo(-len / 2, 0)
    ctx.lineTo(len / 2, 0)
    ctx.lineTo(len / 2 - 5, -4)
    ctx.moveTo(len / 2, 0)
    ctx.lineTo(len / 2 - 5, 4)
    ctx.stroke()
    ctx.restore()
  }

  ctx.restore()
}
