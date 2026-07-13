import {
  FAUNA_SHEETS, FLORA_SHEETS,
  type SheetDef, type FloraSheetDef, type FloraSeason, type Pose,
} from './manifest'

// Spritesheet loader/cache. A factory — the instance is created in App and
// injected into render fns (never a module-level singleton). Without a DOM
// (tests, SSR) every lookup returns null and callers draw glyph fallbacks.

export interface LoadedSheet<D = SheetDef | FloraSheetDef> {
  image: HTMLImageElement
  def: D
  ready: boolean
}

export interface SpriteCache {
  fauna(species: string): LoadedSheet<SheetDef> | null   // null ⇒ glyph fallback
  flora(species: string, season?: FloraSeason): LoadedSheet<FloraSheetDef> | null
    // seasonal FILE not loaded/ready → the leaf sheet; seasonRows species use one
    // file for every season (the draw layer applies the season as a row)
  preload(): Promise<void>
}

type DocLike = Pick<Document, 'createElement'>

export function createSpriteCache(
  doc: DocLike | null = typeof document !== 'undefined' ? document : null,
): SpriteCache {
  const faunaSheets = new Map<string, LoadedSheet<SheetDef>>()
  const floraSheets = new Map<string, LoadedSheet<FloraSheetDef>>()

  const load = <D>(url: string, def: D): LoadedSheet<D> | null => {
    if (!doc) return null
    const image = doc.createElement('img') as HTMLImageElement
    image.src = url
    return {
      image,
      def,
      // `ready` re-checks the element each read — no onload mutation needed.
      get ready() { return Boolean(image.complete && image.naturalWidth > 0) },
    }
  }

  const floraKey = (species: string, season: FloraSeason) => `${species}\u0000${season}`

  if (doc) {
    for (const [species, style] of Object.entries(FAUNA_SHEETS)) {
      if (style.sheet) {
        const s = load(style.sheet.url, style.sheet)
        if (s) faunaSheets.set(species, s)
      }
    }
    for (const [species, def] of Object.entries(FLORA_SHEETS)) {
      const leaf = load(def.url, def)
      if (leaf) floraSheets.set(floraKey(species, 'leaf'), leaf)
      for (const [season, url] of Object.entries(def.seasonUrls ?? {})) {
        const s = load(url, def)
        if (s) floraSheets.set(floraKey(species, season as FloraSeason), s)
      }
    }
  }

  return {
    fauna: (species) => faunaSheets.get(species) ?? null,
    flora: (species, season = 'leaf') => {
      // Seasonal file when it exists AND decoded; otherwise the leaf sheet (the
      // season degrades before the glyph — assets SPEC fallback chain step 4).
      const seasonal = season !== 'leaf' ? floraSheets.get(floraKey(species, season)) : undefined
      if (seasonal?.ready) return seasonal
      return floraSheets.get(floraKey(species, 'leaf')) ?? seasonal ?? null
    },
    preload: async () => {
      const decodes: Promise<unknown>[] = []
      for (const s of [...faunaSheets.values(), ...floraSheets.values()]) {
        if (typeof s.image.decode === 'function') decodes.push(s.image.decode().catch(() => {}))
      }
      await Promise.allSettled(decodes)
    },
  }
}

// frameRect picks the source rect for a pose at clockMs (pure; loops at the
// clip's fps). null ⇒ the sheet has no row for this pose (caller falls back).
export function frameRect(
  def: SheetDef,
  pose: Pose,
  clockMs: number,
): { sx: number; sy: number; sw: number; sh: number } | null {
  const clip = def.poses[pose]
  if (!clip) return null
  const frame = Math.floor((clockMs * clip.fps) / 1000) % clip.frames
  return { sx: frame * def.frameW, sy: clip.row * def.frameH, sw: def.frameW, sh: def.frameH }
}
