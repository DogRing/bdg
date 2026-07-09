# SPEC — `engine/world` · Shelter & Cave Orchestration

> Status: `SH1 SHIPPED` · `SH2 DESIGN-AHEAD` (SH1 SPEC-design RESOLVED in `docs/shelter.md`)
> Sub-spec of: `SPEC.md`  ·  Owner agent: `<filled by implementer>`
> Scope = **SH1–SH2** (`docs/shelter.md`): exposure local-wind injection (SH1, build now); cave
> portals + interior active-space state (SH2, design-ahead — see the marked section).

## Scope

This sub-spec wires `engine/env/exposure` into the world tick and defines the world-owned cave
interior model. It builds on the env/fauna wiring:

- SH1: world derives `blocks_wind` blockers, builds/caches exposure fields, and injects **local wind**
  into scent spread and fauna env samples.
- SH2: caves are `shelters`-tagged portals to a separately generated small **continuous interior
  region**. Entering swaps an entity's active space; exiting returns it to the entrance.

Climate remains unchanged and still produces one world-uniform `Wind`. Exposure is a multiplicative
world layer. Caves are not hardcoded terrain types; their entrances/interiors come from fixture or
worldgen data validated by config.

## Ownership & Installation

### New world-owned state

- `exposureCache *exposure.Cache` — nil until shelter is installed.
- `shelterCfg ShelterConfig` — world.yaml/content-derived coefficients.
- `blockers []exposure.Blocker` — rebuilt from content-tagged terrain/object footprints in D12 order.
- `interiors map[core.ObjectID]InteriorState` — cave/house interior spaces, sorted by ID for logic.
- `activeSpace map[core.ObjectID]SpaceRef` — entity/object current space. Missing means exterior.

```go
type SpaceKind uint8
const (
    SpaceExterior SpaceKind = iota
    SpaceInterior
)

type SpaceRef struct {
    Kind SpaceKind
    ID   core.ObjectID // valid when Kind == SpaceInterior
}

type ShelterConfig struct {
    Exposure exposure.Config
    CaveStableTemperature float64 // °C, used by SH2 interior env; SH3 may make this data/formula richer
}

// Portal connects one exterior entrance position to one interior region.
type Portal struct {
    ID          core.ObjectID
    Kind        core.Tag     // content object kind carrying `shelters`
    ExteriorPos core.Vec2
    InteriorID  core.ObjectID
    InteriorPos core.Vec2    // spawn/return point inside the interior region
}

// InteriorRegion is a separately generated continuous region. It is not a z-layer and not a tile world.
// Bounds are local coordinates for the interior render/nav context.
type InteriorRegion struct {
    ID      core.ObjectID
    Bounds  Bounds
    Portals []Portal
}

type Bounds struct{ Min, Max core.Vec2 }

type InteriorState struct {
    Region InteriorRegion
    Occupants []core.ObjectID // sorted; SH2 unlimited capacity, so this is observability/state only
}

// InstallShelter installs SH1 exposure and optional SH2 interiors/portals. If not called, shelter is OFF:
// exposure epsilon is 1 everywhere and all active spaces are exterior.
func (w *World) InstallShelter(cfg ShelterConfig, blockers []exposure.Blocker, interiors []InteriorRegion)
```

## SH1 Tick Wiring

World builds local wind from the current climate wind and the exposure field for the current sector:

```
global := climateState.Wind()
sector := exposure.SectorOf(exposure.Wind{Dir: global.Dir, Mag: global.Mag})
field  := exposureCache.Field(sector, blockers, interiorExposureCells())
local  := field.LocalWind(exposureCellAt(pos), exposure.Wind{Dir: global.Dir, Mag: global.Mag})
```

Consumers — **SH1 = per-position attenuation only** (`docs/shelter.md` SH1 SPEC-design (i); RESOLVED):

- `fauna.EnvSample.Wind = localWindAt(animal.Pos)` instead of raw climate wind — **the single SH1
  behavioural change**. `localWindAt(p)` = `field.LocalWind(exposureCellAt(p), globalWind)`.
- `scent.Read(pos, r, wind)` inherits the attenuated wind automatically: fauna passes its
  `EnvSample.Wind` to `Read`, so no scent call-site change and no new operand (plan §0).
- `scent.Spread(wind)` is **UNCHANGED** — it keeps taking the world-uniform `scentWind()` for the
  bulk diffusion pass. SH1 attenuates only what per-position consumers *sense*, not the diffusion
  kernel; per-cell scent diffusion (a wall blocking scent drift) is a deferred increment.
- Climate-off or shelter-off uses `Wind{0,0}` / epsilon `1`, preserving existing behavior byte-for-byte.

Cache invalidation:

- On wind sector change: use/create the cached sector field.
- On blocker/interior footprint change: call `exposureCache.Invalidate()` in the serial apply phase.
- Blocker lists are rebuilt or patched only in apply/env phases, never during plan.

Topology adapter (⚠ navmap integration delta):

- World implements `exposure.Topology` over navmap. `Neighbors` and `CellCenter` are already public;
  world reorders navmap's canonical `Neighbors` into `exposure.SectorOf`'s 6 bins (using `CellCenter`
  angles) so `Neighbors[s]` is the downwind cell for sector `s`.
- **navmap must promote its private `inBounds` to a public `InBounds(Cell) bool`** (a one-line export;
  navmap SPEC + code delta at SH1 build). This is the only navmap change SH1 needs — no cell
  enumerator is required (exposure's field is sparse; untouched cells default to ε 1).
- `exposureCellAt(pos) = navmap.CellOf(pos)` maps a continuous agent position to its exposure cell.

## SH2 Cave Interior Model — ⚠ DESIGN-AHEAD, NOT the SH1 build

> Everything in this section is SH2 scope, recorded here for continuity. The SH1 implementer builds
> ONLY the exposure leaf + the per-position wind injection above; `InstallShelter` is called with
> `interiors == nil`, `activeSpace` stays empty, and no portal/enter-exit code ships in SH1. Do not
> implement the below until SH2 is scheduled (its own sub-SPECs per the ≤400-line rule).

### Active Space

Each movable entity has an active space:

- Exterior: ordinary world coordinates, navmap/climate/scent/spatial apply as today.
- Interior: coordinates are continuous local coordinates inside `InteriorRegion.Bounds`.

Entering a cave does not snap to a grid cell. It changes `(SpaceRef, Pos)` atomically in apply:

```go
func (w *World) EnterInterior(actor core.ObjectID, portalID core.ObjectID) error
func (w *World) ExitInterior(actor core.ObjectID, portalID core.ObjectID) error
func (w *World) ActiveSpace(id core.ObjectID) SpaceRef
```

Apply semantics:

1. Validate actor and portal are in the same current space and in interaction range.
2. Remove actor from the old space's spatial index.
3. Set `activeSpace[actor]` and `Pos` to the portal's destination coordinate.
4. Insert actor into the destination space's spatial index.
5. Update `InteriorState.Occupants` sorted by ObjectID.

SH2 occupancy is unlimited, so no conflict resolution is needed for capacity.

### Interior Environment

For SH2, interior env is forced:

- wind magnitude `0`
- rain blocked
- temperature buffered to `ShelterConfig.CaveStableTemperature`
- moisture unchanged unless SH3 defines a rain/moisture shelter model

Fauna/agent env samples inside an interior read these forced values. Scent/vision hiding is explicitly
deferred: interiors do not yet act as scent/vision sinks.

### Interior Navigation

SH2 interiors are small continuous regions. The first implementation may use direct continuous
movement bounded by `InteriorRegion.Bounds`; if pathfinding is needed inside, it must use a separate
interior nav context and keep D11 intact. Exterior navmap cells are never reused as interior positions.

## Content / Fixture Shape

World receives already validated portal/interior data from `tools/worldgen.Load` or scenario fixtures:

```yaml
shelters:
  interiors:
    - id: cave_1
      bounds: { min: {x: 0, y: 0}, max: {x: 30, y: 18} }
      portals:
        - id: cave_1_entrance
          kind: cave_entrance
          exterior_pos: {x: 142, y: 87}
          interior_pos: {x: 2, y: 9}
```

The object kind for a portal must carry `shelters`; wind blockers must carry `blocks_wind` with
height/opacity data. Config validates tags and dimensions; world branches on validated data, never
kind names.

## Determinism

- Exposure fields are pure and cached by sector. Invalidation happens in apply order.
- Blockers, interiors, portals, occupants, and active-space updates are sorted by ObjectID.
- Enter/exit conflict ties use actor ID if two intents target the same portal in the same tick.
- Interior generation is not done in `engine/world`; generated interiors arrive via fixture/worldgen
  from a seeded deterministic pipeline.
- No wall-clock, no global rand, no map iteration for logic.

## Acceptance Criteria

### SH1 (build now)
- [x] **Shelter-OFF neutrality** — without `InstallShelter`, `localWindAt` equals climate wind
  everywhere and existing env/fauna/scent goldens are **byte-identical**. *(`world.TestLocalWindOffNeutral`;
  full suite byte-identical.)*
- [x] **Local wind injection** — with one `blocks_wind` blocker and fixed wind, an animal downwind
  gets reduced `EnvSample.Wind.Mag` (and thus a weaker `scent.Read` gradient); upwind gets the raw
  magnitude. `scent.Spread` is verified to still receive the world-uniform wind (unchanged).
  *(`world.TestLocalWindInjectionDownwind`; `scent.Spread` uses `scentWind()` = global, untouched.)*
- [~] **Cache invalidation** — exercised at the leaf (`exposure` Cache/`Invalidate` tests). SH1 sets
  blockers once at `InstallShelter` and never mutates them per-tick, so the world apply-phase
  invalidation path is not yet triggered; it is wired when SH2/live blocker edits land.
- [~] **D12 golden (SH1)** — leaf-level determinism is covered (`exposure` order-independence +
  per-sector reproducibility) and the OFF golden is byte-identical; a *world-level* blocker-sequence
  golden is deferred until blockers become dynamic.
- [x] **No hardcoded kind names** — no `if kind == "wall"` / `"house"` logic; blockers come from the
  `blocks_wind` tag (`config.buildWindBlockerKinds` → `worldgen.buildWindBlockers`). *(SH1 uses a uniform
  per-blocker height/opacity; per-kind strength from tag data is a documented follow-up — `docs/shelter.md` (iv).)*

### SH2 (design-ahead — do NOT implement in the SH1 build)
- [ ] **Enter/exit portal** — entering moves an actor exterior→interior coordinate, updates spatial
  membership and occupants; exiting returns to the entrance position.
- [ ] **Unlimited occupancy** — multiple actors enter the same cave; `Occupants` stays sorted/deterministic.
- [ ] **Interior env defaults** — inside a cave, wind is zero and temperature equals `CaveStableTemperature`.
- [ ] **No scent/vision hiding yet** — a cave does not hide scent/vision in SH2 (deferred to fauna M3);
  a test documents the deferral so a later change is intentional.
- [ ] **No hardcoded kind names** — portals come from the `shelters` tag + validated data.

## Out of Scope

- `engine/env/exposure` internals → `backend/engine/env/exposure/SPEC.md`.
- Worldgen placement and interior generation algorithm → `backend/tools/worldgen/SPEC.md` extension.
- Fixture JSON schema details and persist wire migration.
- Rain/temperature attenuation for ordinary exposed exterior cells → SH3.
- Scent/vision hiding/refuge mechanics → later fauna cover/hiding phase.
- Frontend render-space swap → SH4/frontend SPEC.

## Open Questions

**None for SH1.** `docs/shelter.md` resolves Q-C1..Q-C5, Q-W1..Q-W7, and the SH1 SPEC-design details
(scent = per-position; shadow ε formula; SH1/SH2 split). SH1 is build-ready.

SH2 remains design-ahead; before it is scheduled it needs SPEC fan-out for worldgen interior-region
generation + fixture/schema, persist/data-contracts, and frontend render-space swap (each its own SPEC
per the ≤400-line rule).
