// Contract-parity fake backend (frontend/dev/SPEC.md, plan Q7).
// Serves the REAL api paths/shapes (data-contracts §4) so pointing the app at
// the real backend later needs zero frontend changes. Zero npm deps.
//
//   node dev/mock-server.mjs [--port 8080] [--seed 42] [--tick-ms 500]
//   node dev/mock-server.mjs --dump 50        # print the first N ticks' events and exit
//
// Scripted scenario (deterministic per seed): deer herd grazes → a wolf roams,
// chases (walk/run by speed), strikes in range (action `hunt` → attack pose +
// lunge) → AnimalDied → eats → deer respawns (AnimalBorn); a tree grows stage
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
const rng = mulberry32(SEED)

// ── world ────────────────────────────────────────────────────────────────────
const CELL = 8, GW = 64, GH = 64 // 512² world
const terrain = []
for (let y = 0; y < GH; y++) {
  for (let x = 0; x < GW; x++) {
    const fx = x - 46, fy = y - 14 // forest ellipse NE
    if (x === 20 || x === 21) terrain.push('water')                 // river N-S
    else if ((fx * fx) / 120 + (fy * fy) / 60 < 1) terrain.push('forest')
    else if (Math.abs(x - 32) < 5 && Math.abs(y - 32) < 5) terrain.push('soil') // village fields
    else terrain.push('plain')
  }
}
const wear = new Float32Array(GW * GH)

const objects = [
  { id: 'berry_1', kind: 'berry_bush', pos: { x: 300, y: 340 } },
  { id: 'water_1', kind: 'water_source', pos: { x: 175, y: 300 } },
  { id: 'shelter_1', kind: 'shelter', pos: { x: 260, y: 262 } },
]
const waypoints = objects.map(o => o.pos)

const agents = [
  { id: 'farmer_1', pos: { x: 256, y: 256 }, goal: 'satiety', action: 'forage', mood: 0.8, wp: 0 },
  { id: 'guard_1', pos: { x: 270, y: 250 }, goal: 'safety', action: 'patrol', mood: 0.9, wp: 1 },
]

const animals = new Map()
const spawnDeer = (id, x, y) =>
  animals.set(id, { id, pos: { x, y }, species: 'deer', action: 'graze', heading: 0 })
spawnDeer('deer_1', 150, 320); spawnDeer('deer_2', 162, 335); spawnDeer('deer_3', 140, 342)
animals.set('wolf_1', { id: 'wolf_1', pos: { x: 400, y: 120 }, species: 'wolf', action: 'wander', heading: 0 })
const wolf = { state: 'roam', until: 40, target: null, holdTicks: 0 }
let deerSerial = 3, respawnAt = -1

const flora = new Map()
const plant = (id, species, x, y, stage, width) => flora.set(id, { id, species, pos: { x, y }, stage, width })
plant('tree_1', 'tree', 380, 130, 1, 4); plant('tree_2', 'tree', 396, 118, 3, 6)
plant('tree_3', 'tree', 370, 150, 2, 5); plant('tree_4', 'tree', 410, 140, 3, 6)
plant('bush_1', 'berry_shrub', 298, 338, 2, 3); plant('bush_2', 'berry_shrub', 310, 348, 1, 2)
let growerSerial = 1, reseedAt = -1

let tick = 0

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
    const cx = Math.floor(pos.x / CELL), cy = Math.floor(pos.y / CELL)
    if (cx < 0 || cy < 0 || cx >= GW || cy >= GH) return
    const i = cy * GW + cx
    if (wear[i] >= 1) return
    wear[i] = Math.min(1, wear[i] + 0.03)
    terrainDelta.push({ cell: { x: cx, y: cy }, wear: Math.round(wear[i] * 100) / 100 })
  }

  // agents: waypoint wander (and desire paths)
  for (const a of agents) {
    const target = waypoints[a.wp]
    if (dist(a.pos, target) < 4) { a.wp = Math.floor(rng() * waypoints.length); a.action = a.id === 'farmer_1' ? 'forage' : 'patrol' }
    else { stepToward(a, target, 2.2); a.action = 'move_to'; wearOn(a.pos) }
  }

  // deer: graze-wander; flee the wolf inside 40 units
  const w = animals.get('wolf_1')
  for (const d of animals.values()) {
    if (d.species !== 'deer') continue
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
        for (const d of animals.values()) if (d.species === 'deer' && (!best || dist(w.pos, d.pos) < dist(w.pos, best.pos))) best = d
        if (best) { wolf.state = 'chase'; wolf.target = best.id }
        else wolf.until = 20
      }
    } else if (wolf.state === 'chase' && prey) {
      if (dist(w.pos, prey.pos) <= 6) {
        w.action = 'hunt' // pose→attack: lunge fx on entry (plan Q2/Q4)
        wolf.holdTicks++
        if (wolf.holdTicks >= 4) {
          animals.delete(prey.id)
          emit('AnimalDied', null, { object_id: prey.id, species: 'deer', cause: 'hunted' })
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
    const id = `deer_${++deerSerial}`
    const pos = { x: 60 + rng() * 40, y: 420 + rng() * 60 } // wilds, out of the action
    spawnDeer(id, pos.x, pos.y)
    emit('AnimalBorn', null, { object_id: id, species: 'deer', pos: round(pos) })
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
        floraDelta.push({ id: g.id, pos: round(g.pos), stage: g.stage })
      }
    }
  }
  if (reseedAt === tick) {
    const id = `tree_g${++growerSerial}`
    plant(id, 'tree', 380 + rng() * 12, 128 + rng() * 10, 0, 2)
    const p = flora.get(id)
    emit('PlantSpawned', null, { object_id: id, species: 'tree', pos: round(p.pos) })
  }

  // climate: 0.25 game-hour per tick (48 s day at 500 ms); rain spell each ~120 ticks
  const hour = (tick * 0.25) % 24
  const dayNight = hour >= 6 && hour < 18 ? 'day' : 'night'
  const temperature = Math.round((12 + 10 * Math.sin(((hour - 9) / 24) * 2 * Math.PI)) * 10) / 10
  const raining = tick % 120 < 20
  climateWind.dir = (climateWind.dir + (rng() - 0.5) * 0.15) % (Math.PI * 2)
  climateWind.mag = Math.min(1, Math.max(0.05, climateWind.mag + (rng() - 0.5) * 0.08))

  // periodic-full flora (§10) every 20 ticks so late joiners converge
  const fullFlora = tick % 20 === 1
  const floraOut = fullFlora
    ? [...flora.values()].map(f => ({ id: f.id, pos: round(f.pos), stage: f.stage }))
    : floraDelta

  emit('TickDone', null, {
    tick,
    agents: agents.map(a => ({ id: a.id, pos: round(a.pos), goal: a.goal, action: a.action, mood: a.mood })),
  })
  emit('WorldFrame', null, {
    tick,
    hour_of_day: Math.round(hour * 100) / 100,
    day_night: dayNight,
    temperature,
    raining,
    wind: { dir: Math.round(climateWind.dir * 1000) / 1000, mag: Math.round(climateWind.mag * 100) / 100 },
    agents: agents.map(a => ({ id: a.id, pos: round(a.pos), action: a.action })),
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

// ── dump mode: deterministic event listing, no server ────────────────────────
if (DUMP > 0) {
  for (let i = 0; i < DUMP; i++) for (const ev of step()) console.log(JSON.stringify(ev))
  process.exit(0)
}

// ── http server ──────────────────────────────────────────────────────────────
const clients = new Set()
const server = http.createServer((req, res) => {
  res.setHeader('Access-Control-Allow-Origin', '*')
  const url = req.url ?? ''
  if (url.startsWith('/api/snapshot')) {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({
      tick,
      agents: agents.map(a => ({ id: a.id, pos: round(a.pos), goal: a.goal, action: a.action, mood: a.mood })),
      objects,
    }))
  } else if (url.startsWith('/api/terrain')) {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({
      cell_size: CELL,
      size: { w: GW, h: GH },
      terrain,
      wear: [...wear].map(v => Math.round(v * 100) / 100),
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
  console.log('[mock] GET /api/snapshot · /api/terrain · /sse')
})
