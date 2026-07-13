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

// Real-art sheet grid (FE-P6, 2026-07-13 drop): 256×256 frames, 4 columns,
// rows in pose order idle/walk/run/eat/attack/dying (same order the placeholder
// generator used, so PoseClip rows carried over unchanged).
const stdPoses = (): SheetDef['poses'] => ({
  idle:   { row: 0, frames: 4, fps: 3 },
  walk:   { row: 1, frames: 4, fps: 8 },
  run:    { row: 2, frames: 4, fps: 12 },
  eat:    { row: 3, frames: 4, fps: 4 },
  attack: { row: 4, frames: 4, fps: 10 },
  dying:  { row: 5, frames: 4, fps: 6 },
})
const sheet = (url: string): SheetDef =>
  Object.freeze({ url, frameW: 256, frameH: 256, poses: Object.freeze(stdPoses()) })

export const FAUNA_SHEETS: Record<string, SpeciesStyle> = Object.freeze({
  goat:   Object.freeze({ category: 'prey' as const,     glyphColor: '#b0a890', sheet: sheet('/assets/fauna/goat.png') }),
  wolf:   Object.freeze({ category: 'predator' as const, glyphColor: '#d44444', sheet: sheet('/assets/fauna/wolf.png') }),
  rabbit: Object.freeze({ category: 'prey' as const,     glyphColor: '#c8b8a0', sheet: sheet('/assets/fauna/rabbit.png') }),
  bear:   Object.freeze({ category: 'predator' as const, glyphColor: '#a05838', sheet: sheet('/assets/fauna/bear.png') }),
  fish:   Object.freeze({ category: 'prey' as const,     glyphColor: '#78a8c8', sheet: sheet('/assets/fauna/fish.png') }),
  // glyph-only until real deer art exists (the sheet once named deer.png depicts a goat — FE-P6)
  deer:   Object.freeze({ category: 'prey' as const,     glyphColor: '#d8b060' }),
})

export const DEFAULT_FAUNA: Record<'predator' | 'prey', SpeciesStyle> = Object.freeze({
  predator: Object.freeze({ category: 'predator' as const, glyphColor: '#d44444' }),
  prey:     Object.freeze({ category: 'prey' as const,     glyphColor: '#d8b060' }),
})

// ── flora ────────────────────────────────────────────────────────────────────
// Sheets are stage-column grids; rows are EITHER seasonal variants inside one
// file (`seasonRows` — bush) OR per-plant shape variants (`variantRows` — trees,
// whose seasons live in separate files via `seasonUrls`). `windResponsive`
// marks one-sided billboard art whose base is anchored at the plant position:
// the renderer bends the whole tuft from that base using the climate wind
// direction/magnitude; the source art itself stays deterministic. See assets SPEC.
export type FloraSeason = 'leaf' | 'bare' | 'snow'
export interface FloraSheetDef {
  url: string                       // leaf-season sheet
  frameW: number
  frameH: number
  stageFrames: number               // columns; frame col = min(stage, stageFrames-1)
  variantRows: number               // shape-variant rows; row = variantRow(plantId, variantRows)
  seasonRows?: Partial<Record<FloraSeason, number>>                  // single-file seasons: season → row
  seasonUrls?: Partial<Record<Exclude<FloraSeason, 'leaf'>, string>> // per-season files
  windResponsive?: boolean          // bottom-anchored billboard, bent by wind in render
}

// tree1..4 real-art folders: 1254×1254 files, 4 growth cols × 4 shape-variant
// rows (313.5 px frames — fractional source rects are fine), seasons as files.
const treeSheet = (n: number): FloraSheetDef => Object.freeze({
  url: `/assets/flora/tree${n}/tree${n}.png`,
  frameW: 313.5, frameH: 313.5, stageFrames: 4, variantRows: 4,
  seasonUrls: Object.freeze({
    bare: `/assets/flora/tree${n}/tree${n}_bare.png`,
    snow: `/assets/flora/tree${n}/tree${n}_snow.png`,
  }),
})
// bush real art: 1448×1086, 4 growth cols × 3 season rows in ONE file.
const bushSheet: FloraSheetDef = Object.freeze({
  url: '/assets/flora/bush.png',
  frameW: 362, frameH: 362, stageFrames: 4, variantRows: 1,
  seasonRows: Object.freeze({ leaf: 0, bare: 1, snow: 2 }),
})
// side-view grass tuft (SVG, 128×32 = 4 growth cols, one row): drawn per plant
// as a bottom-anchored billboard so wind can visibly bend it.
const grassSheet: FloraSheetDef = Object.freeze({
  url: '/assets/flora/grass.svg',
  frameW: 32, frameH: 32, stageFrames: 4, variantRows: 1, windResponsive: true,
})

export const FLORA_SHEETS: Record<string, FloraSheetDef> = Object.freeze({
  grass:       grassSheet,
  oak:         treeSheet(1),   // P6-Q3: the one live tree species
  willow:      treeSheet(2),   // reserved — future content species render with zero code edits
  birch:       treeSheet(3),
  conifer:     treeSheet(4),
  tree:        treeSheet(1),   // legacy fixture/mock species id — shares the oak art
  bush:        bushSheet,
  berry_shrub: bushSheet,      // shares the bush art
  berry_bush:  bushSheet,      // legacy id
})
export const DEFAULT_FLORA_COLOR = '#4a8a2a' // glyph fallback (circle, width-scaled)

// Season selection (P6-Q2 RESOLVED: temperature thresholds). DATA here — render/
// only calls floraSeason. Climate unknown ⇒ leaf.
export const FLORA_SEASON_TEMP = Object.freeze({ snowBelowC: 0, bareBelowC: 5 })
export function floraSeason(climate: { temperature: number } | null | undefined): FloraSeason {
  if (!climate) return 'leaf'
  if (climate.temperature < FLORA_SEASON_TEMP.snowBelowC) return 'snow'
  if (climate.temperature < FLORA_SEASON_TEMP.bareBelowC) return 'bare'
  return 'leaf'
}

// Deterministic per-plant shape pick: FNV-1a over the plant id — same id, same
// row, every frame (render purity); distinct ids spread across rows.
export function variantRow(plantId: string, rows: number): number {
  if (rows <= 1) return 0
  let h = 0x811c9dc5
  for (let i = 0; i < plantId.length; i++) {
    h ^= plantId.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return (h >>> 0) % rows
}

// Ground-cover flora render as a density COVERAGE wash instead of a per-plant
// sprite/dot. Grass is intentionally absent: it now uses a one-sided sprite so
// the tuft can visibly bend with wind. Tall/dry ground cover keeps the cheaper
// continuous wash until matching art exists.
export interface FloraCoverageStyle { color: string; radiusUnits: number; alpha: number; plateau: number }
export const FLORA_COVERAGE: Record<string, FloraCoverageStyle> = Object.freeze({
  tall_grass: Object.freeze({ color: '60,110,40', radiusUnits: 6.0, alpha: 0.40, plateau: 0.7 }),
  dry_shrub: Object.freeze({ color: '180,160,100', radiusUnits: 3.5, alpha: 0.50, plateau: 0.5 }),
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
