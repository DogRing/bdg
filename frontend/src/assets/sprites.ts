import {
  FAUNA_SHEETS, FLORA_SHEETS,
  type SheetDef, type FloraSheetDef, type Pose,
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
  flora(species: string): LoadedSheet<FloraSheetDef> | null
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

  if (doc) {
    for (const [species, style] of Object.entries(FAUNA_SHEETS)) {
      if (style.sheet) {
        const s = load(style.sheet.url, style.sheet)
        if (s) faunaSheets.set(species, s)
      }
    }
    for (const [species, def] of Object.entries(FLORA_SHEETS)) {
      const s = load(def.url, def)
      if (s) floraSheets.set(species, s)
    }
  }

  return {
    fauna: (species) => faunaSheets.get(species) ?? null,
    flora: (species) => floraSheets.get(species) ?? null,
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
