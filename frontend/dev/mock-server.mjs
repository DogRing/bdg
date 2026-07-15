// Contract-parity fake backend (frontend/dev/SPEC.md, plan Q7).
// Serves the REAL api paths/shapes (data-contracts §4) so pointing the app at
// the real backend later needs zero frontend changes. Zero npm deps.
//
//   node dev/mock-server.mjs [--port 8080] [--seed 42] [--tick-ms 500]
//   node dev/mock-server.mjs --dump 50        # print the first N ticks' events and exit
//
// Scripted scenario (deterministic per seed): goat herd grazes → a wolf roams,
// chases (walk/run by speed), strikes in range (action `hunt` → attack pose +
// lunge) → AnimalDied → eats → goat respawns (AnimalBorn); a tree grows stage
// 0→3 (grow fx) then dies and reseeds; day-night cycle, rain spells, wind
// random-walk; agents wander and wear desire paths into the terrain.

import http from 'node:http'

// ── args ─────────────────────────────────────────────────────────────────────
const arg = (name, dflt) => {
  const i = process.argv.indexOf(`--${name}`)
  return i >= 0 ? Number(process.argv[i + 1]) : dflt
}
const PORT = arg('port', 8080)
const SEED = arg('seed', 42)
const TICK_MS = arg('tick-ms', 500)
const DUMP = arg('dump', 0)

// ── deterministic PRNG (mulberry32) ──────────────────────────────────────────
function mulberry32(seed) {
  let a = seed >>> 0
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}
let seed = SEED // re-rolled by POST /api/regen (parity with the backend's new-seed rebuild)
let rng = mulberry32(seed)

// ── world (FLAT-TOP HEX offset grid, mirrors backend navmap; docs/plans/hex-grid.md) ──
const CELL = 8, WORLD = 512 // 512² world; CELL = hex circumradius
const SQRT3 = Math.sqrt(3)
// navmap-mirroring hex helpers (engine hex.go) so the mock's grid == what the frontend expects.
const hexToPixel = (q, r) => ({ x: CELL * 1.5 * q, y: CELL * (SQRT3 / 2 * q + SQRT3 * r) })
const offsetToAxial = (col, row) => ({ q: col, r: row - (col + (col & 1)) / 2 })
const axialToOffset = (q, r) => ({ col: q, row: r + (q + (q & 1)) / 2 })
function pixelToHex(x, y) {
  const fq = (2 / 3 * x) / CELL, fr = (-1 / 3 * x + SQRT3 / 3 * y) / CELL
  let rx = Math.round(fq), ry = Math.round(-fq - fr), rz = Math.round(fr)
  const xd = Math.abs(rx - fq), yd = Math.abs(ry - (-fq - fr)), zd = Math.abs(rz - fr)
  if (xd > yd && xd > zd) rx = -ry - rz
  else if (yd > zd) ry = -rx - rz
  else rz = -rx - ry
  return { q: rx, r: rz }
}
const COLS = Math.ceil(WORLD / (1.5 * CELL)) + 1
const ROWS = Math.ceil(WORLD / (SQRT3 * CELL)) + 1
const ORIENTATION = 'flat'
// Terrain is rebuilt on POST /api/regen (new seed ⇒ river/forest/fields move) —
// mock parity with the backend's GenerateTerrain materialization, including a per-cell
// `elevation[]` ∈[0,1] (smooth seeded bumps, river carved low — the 3D height wire).
// Deterministic per seed: same seed ⇒ same layout (PRNG stream separate from the
// scripted rng).
let terrain = []
let elevation = []
function buildTerrain(seedVal) {
  const rand = mulberry32(seedVal ^ 0x7e11a1)
  const riverX = 96 + Math.floor(rand() * 320)               // river N-S band
  const forest = { x: 64 + rand() * 384, y: 64 + rand() * 256 } // forest ellipse
  const field = { x: 96 + rand() * 320, y: 96 + rand() * 320 } // village fields
  // 3 seeded cosine bumps ≈ rolling hills (cheap stand-in for the backend's fBm noise)
  const bumps = [0, 1, 2].map(() => ({ x: rand() * WORLD, y: rand() * WORLD, r: 140 + rand() * 160, h: 0.35 + rand() * 0.5 }))
  terrain = []
  elevation = []
  for (let row = 0; row < ROWS; row++) {
    for (let col = 0; col < COLS; col++) {
      const { q, r } = offsetToAxial(col, row)
      const { x, y } = hexToPixel(q, r)          // world centre of this hex
      const fx = x - forest.x, fy = y - forest.y
      let e = 0.18
      for (const b of bumps) {
        const d = Math.hypot(x - b.x, y - b.y)
        if (d < b.r) e += b.h * 0.5 * (1 + Math.cos(Math.PI * d / b.r))
      }
      e = Math.min(1, e)
      if (x >= riverX && x < riverX + 16) { terrain.push('river'); e = Math.min(e, 0.10) }
      else if ((fx * fx) / 7680 + (fy * fy) / 3840 < 1) terrain.push('forest')
      else if (Math.abs(x - field.x) < 40 && Math.abs(y - field.y) < 40) terrain.push('sand')
      else terrain.push(e > 0.85 ? 'mountain' : 'soil')
      elevation.push(Math.round(e * 1000) / 1000)
    }
  }
}
buildTerrain(seed)
const wear = new Float32Array(COLS * ROWS)

const objects = [
  { id: 'berry_1', kind: 'berry_bush', pos: { x: 300, y: 340 } },
  { id: 'water_1', kind: 'water_source', pos: { x: 175, y: 300 } },
  { id: 'shelter_1', kind: 'shelter', pos: { x: 260, y: 262 } },
]
const waypoints = objects.map(o => o.pos)

const agents = []
const animals = new Map()
const spawnGoat = (id, x, y) =>
  animals.set(id, { id, pos: { x, y }, species: 'goat', action: 'graze', heading: 0 })
const wolf = { state: 'roam', until: 40, target: null, holdTicks: 0 }
let goatSerial = 3, respawnAt = -1

const flora = new Map()
const plant = (id, species, x, y, stage, width) => flora.set(id, { id, species, pos: { x, y }, stage, width })
let growerSerial = 1, reseedAt = -1

let tick = 0

// resetWorld (re)initialises every piece of scripted state to tick 0 — called
// once at startup and again on POST /api/restart (contract parity with the
// real backend's deterministic fixture rebuild: same seed ⇒ same replay).
function resetWorld() {
  rng = mulberry32(seed)
  tick = 0
  wear.fill(0)

  agents.length = 0
  agents.push(
    { id: 'farmer_1', pos: { x: 256, y: 256 }, goal: 'satiety', action: 'forage', mood: 0.8, wp: 0 },
    { id: 'guard_1', pos: { x: 270, y: 250 }, goal: 'safety', action: 'patrol', mood: 0.9, wp: 1 },
  )

  animals.clear()
  spawnGoat('goat_1', 150, 320); spawnGoat('goat_2', 162, 335); spawnGoat('goat_3', 140, 342)
  animals.set('wolf_1', { id: 'wolf_1', pos: { x: 400, y: 120 }, species: 'wolf', action: 'wander', heading: 0 })
  Object.assign(wolf, { state: 'roam', until: 40, target: null, holdTicks: 0 })
  goatSerial = 3; respawnAt = -1

  flora.clear()
  plant('tree_1', 'tree', 380, 130, 1, 4); plant('tree_2', 'tree', 396, 118, 3, 6)
  plant('tree_3', 'tree', 370, 150, 2, 5); plant('tree_4', 'tree', 410, 140, 3, 6)
  plant('bush_1', 'berry_shrub', 298, 338, 2, 3); plant('bush_2', 'berry_shrub', 310, 348, 1, 2)
  // grass pasture near the goats — exercises the FLORA_COVERAGE density wash (deterministic
  // layout, no rng, so the scripted stream is unchanged): a 7×4 clump ~3u apart overlaps into a meadow.
  for (let i = 0; i < 28; i++) {
    const gx = 130 + (i % 7) * 4 + ((i * 5) % 3) - 1
    const gy = 315 + Math.floor(i / 7) * 4 + ((i * 3) % 3) - 1
    plant(`grass_${i}`, 'grass', gx, gy, 2, 0.3)
  }
  growerSerial = 1; reseedAt = -1

  climateWind.dir = 0.6; climateWind.mag = 0.3
}

// ── movement helpers ─────────────────────────────────────────────────────────
const dist = (a, b) => Math.hypot(a.x - b.x, a.y - b.y)
function stepToward(e, target, speed) {
  const d = dist(e.pos, target)
  if (d < 1e-6) return
  const k = Math.min(1, speed / d)
  e.heading = Math.atan2(target.y - e.pos.y, target.x - e.pos.x)
  e.pos = { x: e.pos.x + (target.x - e.pos.x) * k, y: e.pos.y + (target.y - e.pos.y) * k }
}
const jitter = (p, r) => ({ x: p.x + (rng() - 0.5) * r, y: p.y + (rng() - 0.5) * r })
const round = (p) => ({ x: Math.round(p.x * 10) / 10, y: Math.round(p.y * 10) / 10 })

// ── one simulation step → the tick's events (pure given PRNG state) ─────────
function step() {
  tick++
  const events = []
  let seq = 0
  const emit = (type, agent_id, payload) =>
    events.push({ schema_version: 1, tick, seq: seq++, agent_id, type, payload })
  const floraDelta = []
  const terrainDelta = []
  const wearOn = (pos) => {
    const { q, r } = pixelToHex(pos.x, pos.y)
    const { col, row } = axialToOffset(q, r)
    if (col < 0 || row < 0 || col >= COLS || row >= ROWS) return
    const i = row * COLS + col
    if (wear[i] >= 1) return
    wear[i] = Math.min(1, wear[i] + 0.03)
    terrainDelta.push({ cell: i, wear: Math.round(wear[i] * 100) / 100 }) // offset index i=row·cols+col
  }

  // agents: waypoint wander (and desire paths)
  for (const a of agents) {
    const target = waypoints[a.wp]
    if (dist(a.pos, target) < 4) { a.wp = Math.floor(rng() * waypoints.length); a.action = a.id === 'farmer_1' ? 'forage' : 'patrol' }
    else { stepToward(a, target, 2.2); a.action = 'move_to'; wearOn(a.pos) }
  }

  // goat: graze-wander; flee the wolf inside 40 units
  const w = animals.get('wolf_1')
  for (const d of animals.values()) {
    if (d.species !== 'goat') continue
    if (w && dist(d.pos, w.pos) < 40) {
      d.action = 'flee'
      const away = { x: d.pos.x + (d.pos.x - w.pos.x), y: d.pos.y + (d.pos.y - w.pos.y) }
      stepToward(d, away, 4.5)
    } else {
      d.action = 'graze'
      stepToward(d, jitter(d.pos, 8), 0.5)
    }
    d.pos.x = Math.min(508, Math.max(4, d.pos.x)); d.pos.y = Math.min(508, Math.max(4, d.pos.y))
  }

  // wolf: roam → chase → strike (hunt) → kill → eat → roam; prey respawns later
  if (w) {
    wolf.until--
    const prey = wolf.target ? animals.get(wolf.target) : null
    if (wolf.state === 'roam') {
      w.action = 'wander'
      stepToward(w, jitter(w.pos, 24), 1.6)
      if (wolf.until <= 0) {
        let best = null
        for (const d of animals.values()) if (d.species === 'goat' && (!best || dist(w.pos, d.pos) < dist(w.pos, best.pos))) best = d
        if (best) { wolf.state = 'chase'; wolf.target = best.id }
        else wolf.until = 20
      }
    } else if (wolf.state === 'chase' && prey) {
      if (dist(w.pos, prey.pos) <= 6) {
        w.action = 'hunt' // pose→attack: lunge fx on entry (plan Q2/Q4)
        wolf.holdTicks++
        if (wolf.holdTicks >= 4) {
          animals.delete(prey.id)
          emit('AnimalDied', null, { object_id: prey.id, species: 'goat', cause: 'hunted' })
          respawnAt = tick + 40
          wolf.state = 'eat'; wolf.until = 10; wolf.holdTicks = 0; wolf.target = null
        }
      } else {
        w.action = 'move_to' // fast chase reads as `run` via displacement rate
        stepToward(w, prey.pos, 5)
        wolf.holdTicks = 0
        wearOn(w.pos)
      }
    } else if (wolf.state === 'eat') {
      w.action = 'eat'
      if (wolf.until <= 0) { wolf.state = 'roam'; wolf.until = 30 + Math.floor(rng() * 20) }
    } else { wolf.state = 'roam'; wolf.until = 20 }
    w.pos.x = Math.min(508, Math.max(4, w.pos.x)); w.pos.y = Math.min(508, Math.max(4, w.pos.y))
  }
  if (respawnAt === tick) {
    const id = `goat_${++goatSerial}`
    const pos = { x: 60 + rng() * 40, y: 420 + rng() * 60 } // wilds, out of the action
    spawnGoat(id, pos.x, pos.y)
    emit('AnimalBorn', null, { object_id: id, species: 'goat', pos: round(pos) })
  }

  // flora: one tree grows a stage every 30 ticks; past 3 it dies + reseeds
  if (tick % 30 === 0) {
    const g = flora.get(`tree_${growerSerial > 1 ? 'g' + growerSerial : '1'}`) ?? flora.get('tree_1')
    if (g) {
      if (g.stage >= 3) {
        flora.delete(g.id)
        emit('PlantDied', null, { object_id: g.id, species: g.species, pos: round(g.pos) })
        reseedAt = tick + 10
      } else {
        g.stage++; g.width += 1
        // Full render row per entry (data-contracts §4): the reducer upserts it.
        floraDelta.push({ id: g.id, species: g.species, pos: round(g.pos), stage: g.stage, width: g.width })
      }
    }
  }
  if (reseedAt === tick) {
    const id = `tree_g${++growerSerial}`
    plant(id, 'tree', 380 + rng() * 12, 128 + rng() * 10, 0, 2)
    const p = flora.get(id)
    // PlantSpawned is FX-only on the client; the plant's render STATE arrives via
    // the paired flora_delta full row (same tick), matching the backend.
    emit('PlantSpawned', null, { object_id: id, species: 'tree', pos: round(p.pos) })
    floraDelta.push({ id, species: p.species, pos: round(p.pos), stage: p.stage, width: p.width })
  }

  // climate: 0.25 game-hour per tick (48 s day at 500 ms); rain spell each ~120 ticks
  const hour = (tick * 0.25) % 24
  const dayOfRun = Math.floor((tick * 0.25) / 24) // 0-based day index (worldtime.DayOfRun)
  const minuteOfDay = Math.round(hour * 60)       // [0,1440) game-minute within the day
  const dayNight = hour >= 6 && hour < 18 ? 'day' : 'night'
  const temperature = Math.round((12 + 10 * Math.sin(((hour - 9) / 24) * 2 * Math.PI)) * 10) / 10
  const raining = tick % 120 < 20
  climateWind.dir = (climateWind.dir + (rng() - 0.5) * 0.15) % (Math.PI * 2)
  climateWind.mag = Math.min(1, Math.max(0.05, climateWind.mag + (rng() - 0.5) * 0.08))

  // periodic-full flora (§10) every 20 ticks so late joiners converge
  const fullFlora = tick % 20 === 1
  const floraOut = fullFlora
    ? [...flora.values()].map(f => ({ id: f.id, pos: round(f.pos), species: f.species, stage: f.stage, width: f.width }))
    : floraDelta

  // AgentFrame (data-contracts §4): sparse per-agent delta; the mock keeps it
  // simple and re-sends every agent's full fields each tick (a valid delta —
  // every field counts as changed).
  emit('AgentFrame', null, {
    tick,
    agents: agents.map(a => ({ id: a.id, pos: round(a.pos), goal: a.goal, action: a.action, mood: a.mood })),
    removed: [],
  })
  emit('WorldFrame', null, {
    tick,
    day_of_run: dayOfRun,
    hour_of_day: Math.round(hour * 100) / 100,
    minute_of_day: minuteOfDay,
    day_night: dayNight,
    temperature,
    raining,
    wind: { dir: Math.round(climateWind.dir * 1000) / 1000, mag: Math.round(climateWind.mag * 100) / 100 },
    animals: [...animals.values()].map(a => ({
      id: a.id, pos: round(a.pos), species: a.species, action: a.action,
      heading: Math.round(a.heading * 1000) / 1000,
    })),
    flora_delta: floraOut,
    terrain_delta: terrainDelta,
  })
  return events
}
const climateWind = { dir: 0.6, mag: 0.3 }
resetWorld()

// ── dump mode: deterministic event listing, no server ────────────────────────
if (DUMP > 0) {
  for (let i = 0; i < DUMP; i++) for (const ev of step()) console.log(JSON.stringify(ev))
  process.exit(0)
}

// ── http server ──────────────────────────────────────────────────────────────
// world_revision (data-contracts §2): the mock publishes revision 1 at boot and
// bumps it on POST /api/regen — parity with the backend's publish-last marker
// (the mock's rebuild is synchronous, so "published" and "servable" coincide).
// The mock streams live-tail only (no id: lines / ?cursor replay) — the
// frontend treats an id-less transport as legacy and applies frames
// unconditionally (frontend/SPEC.md §Bootstrap).
let worldRevision = 1
const clients = new Set()
const server = http.createServer((req, res) => {
  res.setHeader('Access-Control-Allow-Origin', '*')
  const url = req.url ?? ''
  if (url.startsWith('/api/meta')) {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({
      tick: String(tick),
      schema_version: '1',
      started_at: '1970-01-01T00:00:00Z',
      status: 'running',
      world_revision: String(worldRevision),
      terrain: 'on',
      flora: 'on',
    }))
  } else if (url.startsWith('/api/snapshot')) {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({
      tick,
      world_revision: worldRevision,
      terrain: 'on',
      flora: 'on',
      agents: agents.map(a => ({ id: a.id, pos: round(a.pos), goal: a.goal, action: a.action, mood: a.mood })),
      objects,
    }))
  } else if (url.startsWith('/api/terrain')) {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({
      cell_size: CELL,
      orientation: ORIENTATION,
      size: { cols: COLS, rows: ROWS },
      terrain,
      wear: [...wear].map(v => Math.round(v * 100) / 100),
      elevation,
      world_revision: worldRevision,
    }))
  } else if (url.startsWith('/api/flora')) {
    // Flora baseline (persist.FloraDoc; data-contracts §2): the full live plant
    // render set for this revision, so a late joiner sees fixtures + already-
    // propagated plants that no SSE event replays. SSE flora_delta keeps it current.
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({
      world_revision: worldRevision,
      flora: [...flora.values()].map(f => ({
        object_id: f.id, species: f.species, pos: round(f.pos), stage: f.stage, width: f.width,
      })),
    }))
  } else if (url.startsWith('/sse')) {
    res.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      Connection: 'keep-alive',
      'X-Accel-Buffering': 'no',
    })
    clients.add(res)
    req.on('close', () => clients.delete(res))
  } else if (url.startsWith('/api/restart') && req.method === 'POST') {
    resetWorld()
    console.log('[mock] world reset to tick 0 (POST /api/restart)')
    res.writeHead(202, { 'Content-Type': 'application/json' })
    res.end('{"status":"restarting"}')
  } else if (url.startsWith('/api/regen') && req.method === 'POST') {
    const m = /[?&]seed=(-?\d+)/.exec(url)
    seed = m ? Number(m[1]) : Math.floor(Math.random() * 2 ** 31) || 1
    buildTerrain(seed)
    resetWorld()
    worldRevision++ // publish the new revision (rebuild is synchronous here)
    console.log(`[mock] world regenerated with seed ${seed} (POST /api/regen) → world_revision ${worldRevision}`)
    res.writeHead(202, { 'Content-Type': 'application/json' })
    res.end('{"status":"regenerating"}')
  } else if (url.startsWith('/healthz') || url.startsWith('/readyz')) {
    res.writeHead(200); res.end('ok')
  } else {
    res.writeHead(404, { 'Content-Type': 'application/json' })
    res.end('{"error":"not found"}')
  }
})

setInterval(() => {
  for (const ev of step()) {
    const line = `data: ${JSON.stringify(ev)}\n\n`
    for (const c of clients) c.write(line)
  }
}, TICK_MS)

server.listen(PORT, () => {
  console.log(`[mock] contract-parity backend on :${PORT} (seed ${SEED}, tick ${TICK_MS}ms)`)
  console.log('[mock] GET /api/snapshot · /api/terrain · /api/flora · /sse · POST /api/restart · /api/regen')
})
