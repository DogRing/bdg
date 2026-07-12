// Redis STREAM entry IDs ("<ms>-<seq>") are the SSE transport cursor
// (frontend/SPEC.md §Bootstrap; data-contracts §4): the snapshot's
// stream_cursor says where replay starts, each SSE frame carries its entry id,
// and the reducer applies an entry only when its id is strictly after the last
// applied one (duplicate + old-revision guard).

/** Numeric comparison of two entry IDs: negative ⇒ a < b, 0 ⇒ equal, positive ⇒ a > b. */
export function compareStreamIds(a: string, b: string): number {
  const [ams, aseq] = splitStreamId(a)
  const [bms, bseq] = splitStreamId(b)
  if (ams !== bms) return ams < bms ? -1 : 1
  if (aseq !== bseq) return aseq < bseq ? -1 : 1
  return 0
}

function splitStreamId(id: string): [number, number] {
  const dash = id.indexOf('-')
  if (dash < 0) return [Number(id) || 0, 0]
  return [Number(id.slice(0, dash)) || 0, Number(id.slice(dash + 1)) || 0]
}
