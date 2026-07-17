// Atmosphere for the 3D view (docs/plans/gl-atmosphere.md): ClimateState → sky/light/
// weather values + the overlay rain/wind drawing. A keyframed colour ramp over hourOfDay
// plus a computed sun/moon arc (Q1 hybrid); all drivers ease client-side toward their
// targets (Q5, τ≈3 s, wrap-aware for hour and wind angle) so the integer hourOfDay steps
// and raining flips never pop. climate == null ⇒ the LEGACY fixed-daylight constants.
// Streak patterns are index-hash based (no Math.random), mirroring render/ambient.ts.

import type { ClimateState } from '../types'
import { PRECIP_SNOW_BELOW_C } from '../assets/manifest'

type V3 = [number, number, number]

export interface Atmo {
  zen: V3; hor: V3               // sky zenith / horizon (== fog == clear colour)
  light: V3                       // directional light (normalized; sun↔moon blend)
  tint: V3                        // light colour × wet-ground darkening (FS multiplier)
  ambI: number; diffI: number     // lambert ambient / diffuse intensities
  sunDir: V3; sunCol: V3          // sky discs (col → 0 ⇒ invisible)
  moonDir: V3; moonCol: V3
  star: number                    // night-star brightness (hidden by cloud)
  cloud: number; cloudOff: [number, number] // cloud-shadow amount + downwind drift offset
  rippleDir: [number, number]; rippleSpd: number // water ripple travel (wind cue)
  rain: number                    // eased precip amount 0..1 as RAIN (0 when the precip falls as snow, CS1)
  snow: number                    // eased precip amount 0..1 as SNOW (0 when it falls as rain) — temp-gated form
  snowCover: number               // eased GROUND snowpack 0..1 (climate CS2b) — raw CS5b wash driver; the consumer scales it
  fogMul: number                  // fog distance multiplier (precip pulls fog closer)
  windDir: number; windMag: number // eased wind (screen arrow angle = windDir + camera yaw)
}

const TAU_MS = 3000
const clamp = (v: number, a: number, b: number) => Math.max(a, Math.min(b, v))
const smoothstep = (a: number, b: number, x: number) => { const t = clamp((x - a) / (b - a), 0, 1); return t * t * (3 - 2 * t) }
const lerp = (a: number, b: number, t: number) => a + (b - a) * t
const lerp3 = (a: V3, b: V3, t: number): V3 => [lerp(a[0], b[0], t), lerp(a[1], b[1], t), lerp(a[2], b[2], t)]
const norm3 = (x: number, y: number, z: number): V3 => { const l = Math.hypot(x, y, z) || 1; return [x / l, y / l, z / l] }
const greyed = (c: V3, k: number, dark: number): V3 => {
  const g = 0.299 * c[0] + 0.587 * c[1] + 0.114 * c[2]
  return lerp3(c, [g * 0.92, g * 0.94, g * 0.97], k).map(v => v * (1 - dark)) as V3
}
// Wrap-aware ease step: move cur toward tgt by factor k along the shortest arc mod period.
const easeWrap = (cur: number, tgt: number, k: number, period: number) => {
  let d = (tgt - cur) % period
  if (d > period / 2) d -= period
  if (d < -period / 2) d += period
  return (((cur + d * k) % period) + period) % period
}
const fract = (v: number) => v - Math.floor(v)
const hash = (i: number, salt: number) => fract(Math.sin(i * 12.9898 + salt) * 43758.5453)

// Legacy fixed-daylight constants (the pre-atmosphere look; also the noon ramp key).
const L_ZEN: V3 = [0.36, 0.58, 0.78], L_HOR: V3 = [0.86, 0.90, 0.93]
const L_LIGHT = norm3(-0.45, 0.82, 0.40)
const L_RIPPLE: [number, number] = [0.8, 0.6] // legacy sin(1.7t + 0.8x/R + 0.6y/R)
export const LEGACY: Atmo = {
  zen: L_ZEN, hor: L_HOR, light: L_LIGHT, tint: [1, 1, 1], ambI: 0.42, diffI: 0.58,
  sunDir: [0, 1, 0], sunCol: [0, 0, 0], moonDir: [0, -1, 0], moonCol: [0, 0, 0],
  star: 0, cloud: 0, cloudOff: [0, 0], rippleDir: L_RIPPLE, rippleSpd: 1.7,
  rain: 0, snow: 0, snowCover: 0, fogMul: 1, windDir: 0, windMag: 0,
}

// Day-cycle keyframes (cyclic over 24 h). Noon == the legacy daylight constants.
interface Key { h: number; zen: V3; hor: V3; tint: V3; ambI: number; diffI: number; star: number }
const RAMP: Key[] = [
  { h: 0.0, zen: [0.020, 0.035, 0.10], hor: [0.10, 0.13, 0.22], tint: [0.55, 0.62, 0.82], ambI: 0.30, diffI: 0.22, star: 1.0 },
  { h: 4.5, zen: [0.030, 0.050, 0.13], hor: [0.15, 0.15, 0.25], tint: [0.62, 0.64, 0.82], ambI: 0.31, diffI: 0.26, star: 0.7 },
  { h: 6.0, zen: [0.22, 0.32, 0.55], hor: [0.95, 0.62, 0.38], tint: [1.06, 0.86, 0.68], ambI: 0.38, diffI: 0.52, star: 0.0 },
  { h: 8.0, zen: [0.33, 0.53, 0.75], hor: [0.87, 0.88, 0.90], tint: [1.02, 0.99, 0.94], ambI: 0.42, diffI: 0.58, star: 0.0 },
  { h: 13.0, zen: L_ZEN, hor: L_HOR, tint: [1.00, 1.00, 1.00], ambI: 0.42, diffI: 0.58, star: 0.0 },
  { h: 17.0, zen: [0.38, 0.54, 0.72], hor: [0.90, 0.84, 0.78], tint: [1.04, 0.96, 0.88], ambI: 0.42, diffI: 0.56, star: 0.0 },
  { h: 18.7, zen: [0.20, 0.24, 0.45], hor: [0.94, 0.52, 0.30], tint: [1.08, 0.78, 0.58], ambI: 0.38, diffI: 0.48, star: 0.15 },
  { h: 20.5, zen: [0.035, 0.055, 0.14], hor: [0.13, 0.14, 0.25], tint: [0.60, 0.65, 0.83], ambI: 0.31, diffI: 0.25, star: 0.9 },
]

function rampAt(h: number): Key {
  let i = RAMP.length - 1
  for (let k = 0; k < RAMP.length; k++) { if (RAMP[k].h <= h) i = k; else break }
  const a = RAMP[i], b = RAMP[(i + 1) % RAMP.length]
  const span = (b.h - a.h + 24) % 24 || 24
  const t = ((h - a.h + 24) % 24) / span
  return {
    h, zen: lerp3(a.zen, b.zen, t), hor: lerp3(a.hor, b.hor, t), tint: lerp3(a.tint, b.tint, t),
    ambI: lerp(a.ambI, b.ambI, t), diffI: lerp(a.diffI, b.diffI, t), star: lerp(a.star, b.star, t),
  }
}

// NIGHT_MIN legibility floor (gl-atmosphere.md §4): the ramp's darkest ambI never
// dips below this, so terrain stays readable at midnight.
const NIGHT_MIN = 0.28
const CLOUD_DRIFT = 6 // world units/s at windMag 1

export function createAtmosphere() {
  let inited = false, lastMs: number | null = null
  let hourE = 13, rainE = 0, cloudE = 0, windDirE = 0, windMagE = 0, snowCoverE = 0
  const cloudOff: [number, number] = [0, 0]

  function update(climate: ClimateState | null, clockMs: number): Atmo {
    if (!climate) { lastMs = clockMs; return LEGACY }
    const dt = lastMs == null ? 0 : clamp(clockMs - lastMs, 0, 250)
    lastMs = clockMs
    const rT = climate.raining ? 1 : 0
    const isSnow = climate.raining && climate.temperature < PRECIP_SNOW_BELOW_C
    const cT = clamp(0.35 * climate.moisture + 0.8 * rT, 0, 1)
    const snowT = climate.snowCover ?? 0
    if (!inited) {
      inited = true
      hourE = climate.hourOfDay; rainE = rT; cloudE = cT
      windDirE = climate.windDir; windMagE = climate.windMag; snowCoverE = snowT
    } else {
      const k = 1 - Math.exp(-dt / TAU_MS)
      hourE = easeWrap(hourE, climate.hourOfDay, k, 24)
      rainE += (rT - rainE) * k
      cloudE += (cT - cloudE) * k
      windDirE = easeWrap(windDirE, climate.windDir, k, Math.PI * 2)
      windMagE += (climate.windMag - windMagE) * k
      snowCoverE += (snowT - snowCoverE) * k
    }
    cloudOff[0] += Math.cos(windDirE) * windMagE * CLOUD_DRIFT * dt / 1000
    cloudOff[1] += Math.sin(windDirE) * windMagE * CLOUD_DRIFT * dt / 1000

    // ---- ramp + sun/moon arc (all pure in the eased drivers) ----
    const r = rampAt(hourE)
    const sunPhi = (hourE - 6) / 12 * Math.PI
    const sEl = Math.sin(sunPhi)
    const sunDir = norm3(Math.cos(sunPhi), sEl, 0.30)
    const moonPhi = (hourE - 18) / 12 * Math.PI
    const mEl = Math.sin(moonPhi)
    const moonDir = norm3(Math.cos(moonPhi), mEl, -0.25)
    // The lit direction clamps elevation up so relief never goes unlit (NIGHT_MIN analog).
    const sunLight = norm3(Math.cos(sunPhi), Math.max(sEl, 0.12), 0.30)
    const moonLight = norm3(Math.cos(moonPhi), Math.max(mEl, 0.35), -0.25)
    const ws = smoothstep(-0.06, 0.18, sEl)
    const light = norm3(
      lerp(moonLight[0], sunLight[0], ws), lerp(moonLight[1], sunLight[1], ws), lerp(moonLight[2], sunLight[2], ws))

    // ---- overcast / wet restyle ----
    const zen = greyed(r.zen, 0.7 * cloudE, 0.18 * cloudE)
    const hor = greyed(r.hor, 0.7 * cloudE, 0.18 * cloudE)
    const tint = greyed(r.tint, 0.5 * cloudE, 0.15 * cloudE).map(v => v * (1 - 0.25 * rainE)) as V3
    const discFade = 1 - 0.92 * cloudE
    const sunVis = smoothstep(-0.02, 0.06, sEl) * discFade
    const moonVis = smoothstep(-0.02, 0.06, mEl) * discFade * (1 - ws)
    const windUnit: [number, number] = [Math.cos(windDirE), Math.sin(windDirE)]
    const rw = clamp(windMagE * 2.5, 0, 1)
    const rd = norm3(lerp(L_RIPPLE[0], windUnit[0], rw), lerp(L_RIPPLE[1], windUnit[1], rw), 0)

    return {
      zen, hor, light, tint,
      ambI: Math.max(r.ambI * (1 + 0.08 * cloudE), NIGHT_MIN),
      diffI: r.diffI * (1 - 0.5 * cloudE),
      sunDir, sunCol: [1.00 * sunVis, 0.92 * sunVis, 0.72 * sunVis],
      moonDir, moonCol: [0.62 * moonVis, 0.66 * moonVis, 0.75 * moonVis],
      star: r.star * (1 - cloudE),
      cloud: cloudE, cloudOff: [cloudOff[0], cloudOff[1]],
      rippleDir: [rd[0], rd[1]], rippleSpd: 1.7 * (1 + 1.6 * windMagE),
      // Precipitation form (CS1): the eased density rainE is drawn as snow below the freeze-fall
      // threshold, as rain above it. The form gate uses the live temperature (not eased) — a rare,
      // acceptable hard flip at the threshold; the density itself stays eased either way.
      rain: isSnow ? 0 : rainE, snow: isSnow ? rainE : 0, snowCover: snowCoverE,
      fogMul: 1 - 0.45 * rainE,
      windDir: windDirE, windMag: windMagE,
    }
  }

  return { update }
}

// ---- overlay drawing (2D-context ports of render/ambient.ts, wind-aware) ----

const RAIN_STREAKS = 110

// Screen-space rain streaks slanted downwind. slant = screen-x drift per screen-y unit
// of fall (the wind's screen-right component); per-streak phase from the index hash.
export function drawRainOverlay(octx: CanvasRenderingContext2D, w: number, h: number, rain: number, slant: number, clockMs: number) {
  if (rain <= 0.02) return
  const n = Math.ceil(RAIN_STREAKS * rain)
  const len = 10
  octx.save()
  octx.strokeStyle = `rgba(160,200,240,${(0.42 * rain).toFixed(3)})`
  octx.lineWidth = 1
  octx.beginPath()
  for (let i = 0; i < n; i++) {
    const speed = 0.25 + hash(i, 7) * 0.2 // px/ms, per-streak
    const x = fract(hash(i, 1) + clockMs * 0.00003 * (1 + hash(i, 3)) + slant * (clockMs * speed) / h / (w / h)) * w
    const y = fract(hash(i, 2) + (clockMs * speed) / h) * h
    octx.moveTo(x, y)
    octx.lineTo(x + slant * len, y + len)
  }
  octx.stroke()
  octx.restore()
}

// Screen-space snow flakes (CS1): slow, wind-drifted soft dots instead of slanted streaks.
// drift = the wind's screen-right component; per-flake phase + size from the index hash.
export function drawSnowOverlay(octx: CanvasRenderingContext2D, w: number, h: number, snow: number, drift: number, clockMs: number) {
  if (snow <= 0.02) return
  const n = Math.ceil(RAIN_STREAKS * snow)
  octx.save()
  octx.fillStyle = `rgba(255,255,255,${(0.8 * snow).toFixed(3)})`
  for (let i = 0; i < n; i++) {
    const speed = 0.03 + hash(i, 7) * 0.03 // px/ms — much slower than rain
    const sway = Math.sin(clockMs * 0.0012 + hash(i, 4) * 6.283) * 12
    const x = fract(hash(i, 1) + clockMs * 0.00002 + drift * (clockMs * speed) / h / (w / h)) * w + sway
    const y = fract(hash(i, 2) + (clockMs * speed) / h) * h
    const r = 1.1 + hash(i, 5) * 1.4
    octx.beginPath()
    octx.arc(x, y, r, 0, Math.PI * 2)
    octx.fill()
  }
  octx.restore()
}

// HUD compass arrow (top-left; top-right holds the counts box). angle is the wind's
// SCREEN direction (world windDir + camera yaw); length/alpha ∝ mag.
export function drawWindArrow(octx: CanvasRenderingContext2D, angle: number, mag: number) {
  if (mag <= 0.02) return
  const len = 12 + mag * 22
  octx.save()
  octx.translate(44, 44)
  octx.rotate(angle)
  octx.globalAlpha = 0.4 + mag * 0.6
  octx.strokeStyle = 'rgba(255,255,255,0.85)'
  octx.lineWidth = 2
  octx.beginPath()
  octx.moveTo(-len / 2, 0)
  octx.lineTo(len / 2, 0)
  octx.lineTo(len / 2 - 5, -4)
  octx.moveTo(len / 2, 0)
  octx.lineTo(len / 2 - 5, 4)
  octx.stroke()
  octx.restore()
}
