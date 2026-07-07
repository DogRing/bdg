import { describe, it, expect } from 'vitest'
import {
  poseFor, ACTION_POSE_RULES, FAUNA_SHEETS, FLORA_SHEETS, TERRAIN_STYLE,
  TERRAIN_DEFAULT, FX_DEFS, type SheetDef,
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
    expect(c1.fauna('deer')).not.toBeNull()
    expect(c1.fauna('deer')).not.toBe(c2.fauna('deer'))
    expect(c1.fauna('rabbit')).toBeNull()       // glyph-only species: no sheet
    expect(c1.fauna('unknown_beast')).toBeNull() // unknown species: fallback
    expect(c1.flora('tree')).not.toBeNull()
  })

  it('not-yet-loaded sheets report ready=false; no DOM ⇒ all null', async () => {
    const c = createSpriteCache(fakeDoc())
    expect(c.fauna('deer')!.ready).toBe(false) // fake elements never load
    await c.preload() // resolves even when nothing can decode
    const noDom = createSpriteCache(null)
    expect(noDom.fauna('deer')).toBeNull()
    expect(noDom.flora('tree')).toBeNull()
  })

  it('deer/wolf sheets declare all six pose rows', () => {
    for (const species of ['deer', 'wolf']) {
      const poses = FAUNA_SHEETS[species].sheet!.poses
      for (const pose of ['idle', 'walk', 'run', 'eat', 'attack', 'dying'] as const) {
        expect(poses[pose], `${species}.${pose}`).toBeDefined()
      }
    }
    expect(FLORA_SHEETS.tree.stageFrames).toBe(4)
  })
})
