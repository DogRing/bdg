// The data-driven asset registry (frontend/src/assets/SPEC.md). ALL mapping from
// open-content ids (species / ActionID / TerrainID — D10) to render resources
// lives in these frozen tables; render modules contain zero content conditionals.
// Adding a backend species needs at worst a row here + a PNG — at minimum nothing
// (the fallback chain draws a category glyph).

// ── poses (closed render vocabulary, plan Q2) ────────────────────────────────
export type Pose = 'idle' | 'walk' | 'run' | 'eat' | 'attack' | 'dying'

export interface PoseClip { row: number; frames: number; fps: number }
export interface SheetDef {
  url: string
  frameW: number
  frameH: number
  poses: Partial<Record<Pose, PoseClip>> // only rows the sheet actually has
}
export interface SpeciesStyle {
  sheet?: SheetDef                 // absent ⇒ glyph-only species
  category: 'predator' | 'prey'    // drives the fallback glyph shape/tint
  glyphColor: string
}

// Standard placeholder sheet grid (dev/generate-spritesheets.mjs): 32×32 frames,
// 4 columns, rows in pose order idle/walk/run/eat/attack/dying.
const stdPoses = (): SheetDef['poses'] => ({
  idle:   { row: 0, frames: 4, fps: 3 },
  walk:   { row: 1, frames: 4, fps: 8 },
  run:    { row: 2, frames: 4, fps: 12 },
  eat:    { row: 3, frames: 4, fps: 4 },
  attack: { row: 4, frames: 4, fps: 10 },
  dying:  { row: 5, frames: 4, fps: 6 },
})
const sheet = (url: string): SheetDef =>
  Object.freeze({ url, frameW: 32, frameH: 32, poses: Object.freeze(stdPoses()) })

export const FAUNA_SHEETS: Record<string, SpeciesStyle> = Object.freeze({
  deer:   Object.freeze({ category: 'prey' as const,     glyphColor: '#d8b060', sheet: sheet('/assets/fauna/deer.png') }),
  wolf:   Object.freeze({ category: 'predator' as const, glyphColor: '#d44444', sheet: sheet('/assets/fauna/wolf.png') }),
  // glyph-only until art exists (content/objects.yaml species)
  rabbit: Object.freeze({ category: 'prey' as const,     glyphColor: '#c8b8a0' }),
  goat:   Object.freeze({ category: 'prey' as const,     glyphColor: '#b0a890' }),
  bear:   Object.freeze({ category: 'predator' as const, glyphColor: '#a05838' }),
  fish:   Object.freeze({ category: 'prey' as const,     glyphColor: '#78a8c8' }),
})

export const DEFAULT_FAUNA: Record<'predator' | 'prey', SpeciesStyle> = Object.freeze({
  predator: Object.freeze({ category: 'predator' as const, glyphColor: '#d44444' }),
  prey:     Object.freeze({ category: 'prey' as const,     glyphColor: '#d8b060' }),
})

// ── flora ────────────────────────────────────────────────────────────────────
export interface FloraSheetDef { url: string; frameW: number; frameH: number; stageFrames: number }

const floraSheet = (url: string): FloraSheetDef =>
  Object.freeze({ url, frameW: 32, frameH: 32, stageFrames: 4 })

export const FLORA_SHEETS: Record<string, FloraSheetDef> = Object.freeze({
  tree:        floraSheet('/assets/flora/tree.png'),
  bush:        floraSheet('/assets/flora/bush.png'),
  berry_shrub: floraSheet('/assets/flora/bush.png'), // shares the bush art
})
export const DEFAULT_FLORA_COLOR = '#4a8a2a' // glyph fallback (circle, width-scaled)

// Ground-cover flora render as a density COVERAGE wash instead of a per-plant
// sprite/dot: overlapping soft stamps accumulate into a continuous meadow, so a
// pasture reads as a painted area rather than dots pinned to the map. Membership
// + tuning are DATA here (open content, D10) — render/ branches on the looked-up
// style, never on a species string. `color` is rgb (draw applies alpha);
// `radiusUnits` is the world-unit stamp radius (≈ grass propagation clump, so
// neighbours overlap); `alpha` is the per-stamp peak (clusters build toward opaque).
// `plateau` = fraction of the radius painted at full `alpha` before the soft rim
// fades to 0. A near-solid core (plateau ~0.6) is what lets adjacent stamps MERGE
// into one filled sheet instead of reading as separate dabs when zoomed in — the
// meadow stays a painted area at every zoom, while overlap still builds opacity
// (density). Radius is world units so it tracks zoom.
export interface FloraCoverageStyle { color: string; radiusUnits: number; alpha: number; plateau: number }
export const FLORA_COVERAGE: Record<string, FloraCoverageStyle> = Object.freeze({
  grass: Object.freeze({ color: '92,150,54', radiusUnits: 4.5, alpha: 0.30, plateau: 0.6 }),
})

// ── ActionID → Pose (ordered, first match wins; ids are open content) ────────
export const ACTION_POSE_RULES: ReadonlyArray<{ pattern: RegExp; pose: Pose }> = Object.freeze([
  Object.freeze({ pattern: /hunt|attack/i,                        pose: 'attack' as const }),
  Object.freeze({ pattern: /flee|evade|escape/i,                  pose: 'run' as const }),
  Object.freeze({ pattern: /graze|eat|drink|forage/i,             pose: 'eat' as const }),
  Object.freeze({ pattern: /move|walk|wander|approach|patrol|roam/i, pose: 'walk' as const }),
])

export function poseFor(actionId: string): Pose {
  for (const rule of ACTION_POSE_RULES) {
    if (rule.pattern.test(actionId)) return rule.pose
  }
  return 'idle'
}

// ── terrain (flat per-hex colours, plan Q5 · hex; ids from content/terrain.yaml) ──
export const TERRAIN_STYLE: Record<string, string> = Object.freeze({
  // canonical content/terrain.yaml ids
  soil:     '#9a7a4a',
  sand:     '#c8b078',
  river:    '#4a8ec2', // fresh flowing water (lighter than sea)
  lake:     '#3f7eb5', // still fresh water (outflow-less basin; between river and sea)
  mountain: '#8a8078', // steep rock
  sea:      '#2f5c8a', // deep salt water
  bare_rock: '#9a938a', // depleted ore node reroute
  // legacy/example ids (older fixtures + tests)
  plain:  '#7a9a4a',
  water:  '#3a6ea5',
  steep:  '#8a8078',
  forest: '#3a6a2a',
  swamp:  '#5a6a3a',
})
export const TERRAIN_DEFAULT = '#77826e'
export const WEAR_TRAIL_COLOR = '160,130,90' // rgb; draw applies alpha ∝ wear

// ── transition FX timings (plan Q4) ──────────────────────────────────────────
export interface FxDef { durationMs: number }
export const FX_DEFS: Record<'spawn' | 'death' | 'attack' | 'grow', FxDef> = Object.freeze({
  spawn:  Object.freeze({ durationMs: 500 }),
  death:  Object.freeze({ durationMs: 1500 }), // fade + corpse mark, then removal
  attack: Object.freeze({ durationMs: 300 }),  // lunge
  grow:   Object.freeze({ durationMs: 500 }),
})
