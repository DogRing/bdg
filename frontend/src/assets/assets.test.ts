import { describe, it, expect } from 'vitest'
import {
  poseFor, ACTION_POSE_RULES, FAUNA_SHEETS, FLORA_SHEETS, TERRAIN_STYLE,
  TERRAIN_DEFAULT, FX_DEFS, floraSeason, variantRow, type SheetDef,
} from './manifest'
import { createSpriteCache, frameRect } from './sprites'

describe('poseFor (ActionID → closed pose set, Q2)', () => {
  it.each([
    ['hunt_deer', 'attack'],
    ['attack', 'attack'],
    ['flee', 'run'],
    ['evade_predator', 'run'],
    ['graze', 'eat'],
    ['drink', 'eat'],
    ['forage_berry', 'eat'],
    ['move_to', 'walk'],
    ['wander', 'walk'],
    ['patrol', 'walk'],
    ['sleep', 'idle'],
    ['???unknown-action', 'idle'],
    ['', 'idle'],
  ])('%s → %s', (action, pose) => {
    expect(poseFor(action)).toBe(pose)
  })
})

describe('frameRect', () => {
  const def: SheetDef = {
    url: '/x.png', frameW: 32, frameH: 32,
    poses: { walk: { row: 1, frames: 4, fps: 8 } },
  }

  it('cycles frames at the clip fps and offsets sy by row', () => {
    expect(frameRect(def, 'walk', 0)).toEqual({ sx: 0, sy: 32, sw: 32, sh: 32 })
    expect(frameRect(def, 'walk', 125)).toEqual({ sx: 32, sy: 32, sw: 32, sh: 32 })
    expect(frameRect(def, 'walk', 500)).toEqual({ sx: 0, sy: 32, sw: 32, sh: 32 }) // 4 frames loop
  })

  it('returns null for an absent pose row (caller falls back)', () => {
    expect(frameRect(def, 'attack', 0)).toBeNull()
  })
})

describe('terrain style totality', () => {
  it('covers the content/terrain.yaml ids (incl. hex fixture river/mountain/sea); unknown → default', () => {
    for (const id of ['soil', 'sand', 'river', 'mountain', 'sea', 'bare_rock']) {
      expect(TERRAIN_STYLE[id], id).toBeTruthy()
    }
    expect(TERRAIN_STYLE['???'] ?? TERRAIN_DEFAULT).toBe(TERRAIN_DEFAULT)
  })
})

describe('manifest tables are frozen data', () => {
  it('mutation throws in strict mode', () => {
    expect(() => { (FAUNA_SHEETS as Record<string, unknown>).hacked = {} }).toThrow()
    expect(() => { (ACTION_POSE_RULES as unknown as unknown[]).push({}) }).toThrow()
    expect(() => { (TERRAIN_STYLE as Record<string, string>).plain = '#000' }).toThrow()
    expect(() => { (FX_DEFS as Record<string, unknown>).death = {} }).toThrow()
  })
})

describe('createSpriteCache (factory, no singleton)', () => {
  const fakeDoc = () => {
    const created: Array<Record<string, unknown>> = []
    return {
      created,
      createElement: () => {
        const el: Record<string, unknown> = {}
        created.push(el)
        return el as unknown as HTMLElement
      },
    }
  }

  it('two caches are independent; entries follow the manifest', () => {
    const d1 = fakeDoc(), d2 = fakeDoc()
    const c1 = createSpriteCache(d1), c2 = createSpriteCache(d2)
    expect(d1.created.length).toBeGreaterThan(0)
    expect(d1.created.length).toBe(d2.created.length) // same manifest, separate elements
    expect(c1.fauna('goat')).not.toBeNull()
    expect(c1.fauna('goat')).not.toBe(c2.fauna('goat'))
    expect(c1.fauna('deer')).toBeNull()          // glyph-only species: no sheet (FE-P6)
    expect(c1.fauna('unknown_beast')).toBeNull() // unknown species: fallback
    expect(c1.flora('tree')).not.toBeNull()
  })

  it('not-yet-loaded sheets report ready=false; no DOM ⇒ all null', async () => {
    const c = createSpriteCache(fakeDoc())
    expect(c.fauna('goat')!.ready).toBe(false) // fake elements never load
    await c.preload() // resolves even when nothing can decode
    const noDom = createSpriteCache(null)
    expect(noDom.fauna('goat')).toBeNull()
    expect(noDom.flora('tree')).toBeNull()
  })

  it('every sheeted species declares all six pose rows', () => {
    for (const species of ['goat', 'wolf', 'bear', 'fish', 'rabbit']) {
      const poses = FAUNA_SHEETS[species].sheet!.poses
      for (const pose of ['idle', 'walk', 'run', 'eat', 'attack', 'dying'] as const) {
        expect(poses[pose], `${species}.${pose}`).toBeDefined()
      }
    }
    expect(FLORA_SHEETS.tree.stageFrames).toBe(4)
  })

  it('seasonal flora files fall back to the leaf sheet until decoded', () => {
    const d = fakeDoc()
    const c = createSpriteCache(d)
    // nothing decoded yet → the snow lookup serves the leaf sheet
    expect(c.flora('oak', 'snow')).toBe(c.flora('oak', 'leaf'))
    // mark every element decoded → the snow lookup serves its own file
    for (const el of d.created) { el.complete = true; el.naturalWidth = 1 }
    expect(c.flora('oak', 'snow')).not.toBe(c.flora('oak', 'leaf'))
    // single-file season sheets (bush seasonRows) use one file for every season
    expect(c.flora('bush', 'snow')).toBe(c.flora('bush', 'leaf'))
  })

  it('grass declares normal/snow rows while bush owns the 3-second concealment rustle', () => {
    const grass = FLORA_SHEETS.grass
    expect(grass.url).toBe('/assets/flora/grass.png')
    expect(grass.frameW).toBe(313.5)
    expect(grass.frameH).toBe(313.5)
    expect(grass.seasonRows).toEqual({ leaf: 0, bare: 0, snow: 1 })
    expect(grass.windResponsive).toBe(true)
	const bush = FLORA_SHEETS.berry_shrub
	expect(bush.rustle).toEqual({ periodMs: 3000, durationMs: 300, maxBend: 0.08 })
  })
})

describe('floraSeason (temperature fallback when snowCover absent — P6-Q2)', () => {
  it.each([
    [null, 'leaf'],
    [{ temperature: -3 }, 'snow'],
    [{ temperature: 0 }, 'bare'],   // snow strictly below 0
    [{ temperature: 3 }, 'bare'],
    [{ temperature: 5 }, 'leaf'],   // bare strictly below 5
    [{ temperature: 12 }, 'leaf'],
  ] as const)('%o → %s', (climate, season) => {
    expect(floraSeason(climate)).toBe(season)
  })
})

describe('floraSeason (CS4: accumulated snowCover drives the snow variant, not instantaneous temp)', () => {
  it.each([
    // Cold but no accumulated pack → NOT snow (fixes the daily-swing flicker): it is bare, not snow,
    // even at −5 °C, until snow actually builds up.
    [{ temperature: -5, snowCover: 0 }, 'bare'],
    [{ temperature: -5, snowCover: 0.05 }, 'bare'],  // below the 0.1 accumulation threshold
    [{ temperature: -5, snowCover: 0.1 }, 'snow'],   // pack crossed the threshold
    // Warm air but snow still lying on the ground → STILL snow (melts gradually, sprite follows the pack).
    [{ temperature: 4, snowCover: 0.5 }, 'snow'],
    // Pack melted away while warm → leaf.
    [{ temperature: 8, snowCover: 0 }, 'leaf'],
  ] as const)('%o → %s', (climate, season) => {
    expect(floraSeason(climate)).toBe(season)
  })
})

describe('variantRow (deterministic per-plant shape pick)', () => {
  it('is stable, in range, spreads ids, and degenerates to 0 for one row', () => {
    expect(variantRow('plant_1', 4)).toBe(variantRow('plant_1', 4))
    const rows = new Set<number>()
    for (let i = 0; i < 40; i++) {
      const r = variantRow(`plant_${i}`, 4)
      expect(r).toBeGreaterThanOrEqual(0)
      expect(r).toBeLessThan(4)
      rows.add(r)
    }
    expect(rows.size).toBeGreaterThan(1) // distinct ids do land on different rows
    expect(variantRow('anything', 1)).toBe(0)
    expect(variantRow('anything', 0)).toBe(0)
  })
})
