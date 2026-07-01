# Handoff Prompts — remaining ecosystem pipeline (for another agent)

> Companion to **`HANDOFF.md`** (read that first — it has the mandatory read order, determinism rules,
> and the Open-Question gate that EVERY prompt below assumes). This file gives the **ordered,
> ready-to-paste prompts** for the remaining build. Each prompt is one module/phase. Run them in the
> numbered order; items marked **∥ parallel-OK** have no dependency on each other.
>
> **Decisions are accepted** (see `docs/activation-gate.md` — G1–G17 = the recommendations). The agent
> may treat those as RESOLVED; it must still STOP if it hits any OTHER open question.

## Current state (done = do NOT rebuild)

- ✅ **Built + green** (`go test -count=2`, vet, gofmt clean): `engine/space/scent`, `engine/space/navmap`,
  `engine/env/climate`, `engine/env/flora`, `engine/fauna`, and `engine/ecosim` (an integration harness
  that already runs the activated fauna+scent+climate loop **2000 ticks deterministically** — the
  "how many ticks" smoke test).
- ✅ Pre-existing: the social engine (`agent`, `world` base tick loop, `mind/*`, `kernel/*`,
  `space/spatial`), `platform/{config,events,persist,api}` base.
- ⚠️ **Pre-existing frontend MOCK** in `engine/world/tick.go` ("MOCK ENV FOR FRONTEND TESTING" — emits
  hardcoded `deer_1`/`wolf_1` fake animals) + `events.go` `TypeWorldFrame` + frontend src. **Phase 7
  replaces the mock with the REAL `WorldFrame` from env state** — delete the mock then.
- Remaining = phases 1–9 below.

## Build order & dependencies

```
1. engine/env/decay        ∥  2. engine/space/pathfind        (both leaves; parallel-OK)
              ↓ (decay) ┐
3. platform/config WI-P0  (needs all env modules' NewRules — climate/flora/fauna/decay — now present)
              ↓
4. engine/world WI-P1  (InstallEnv: climate/flora/decay Step + navmap bridge + sampling)
              ↓
5. engine/world WI-P2  (InstallFauna: fauna.Step + combined apply + scent driving)   [needs fauna+scent+WI-P1]
              ↓
6. fixture / world-gen loader + main wiring   (ACTIVATION: place G5 species, call InstallEnv/InstallFauna)
              ↓
7. platform/persist + api WI-P4   (serialize animals/flora/climate + REAL WorldFrame SSE; delete the mock)
              ↓
8. frontend ecosystem rendering   (drawAnimals/drawFlora/drawAmbient)
              ↓
9. activation balance tuning + world scenario tests (FA1–FA8) + golden re-baselines
```

Standard loop for EACH phase: implementer builds → **reviewer verifies vs SPEC** → NEEDS_FIX loop →
only then next phase. Verify every phase from `backend/`: `gofmt -l <pkg>` (empty), `go vet ./<pkg>/`,
`go test ./<pkg>/ -count=2`, `go build ./...`.

---

## Prompt 1 — `engine/env/decay` ∥ parallel-OK

```
Implement the Go module `engine/env/decay` strictly to its SPEC. Read HANDOFF.md first (read order,
determinism D11/D12, open-question gate). The SPEC is /home/coder/workspace/bdg/backend/engine/env/decay/SPEC.md
(Status READY, Dm1–Dm5 RESOLVED — no open holes). It is a pure leaf (deps: engine/kernel/{core,expr,rng}).
The sibling engine/env/flora and engine/env/climate are already implemented — read their *.go as the
structural template (pure Step → return deltas; expr.Context adapter; the go/parser-based
forbidden-import guard test; the determinism golden). Implement the exact Public Interface (Lot, EnvInput,
State, New, Step, StepDeltas, AgeDelta/TransitionDelta/TransformOut, Rules, NewRules, KindRule, StateRule,
TransformRule, StateAt, SupplyAt, BaseRate, Accel). Honor: continuous DecayAge accumulator → derived
discrete State (never stored); multiplicative effectiveRate = baseRate·accel(temp,moisture)·StorageRateMult;
transform-on-transition (emit product, not vanish; only terminal `gone` removes mass); sorted-ObjectID
determinism; missing-EnvInput panic; empty-Rules decay-off neutrality. Write a test for EVERY Acceptance
Criterion (incl. determinism golden reproducing on a 2nd run, forbidden-import guard via go/parser).
Verify: gofmt/vet/`go test ./engine/env/decay/ -count=2`/`go build ./...`. Report files, AC→test map,
test output, any ambiguity resolved. Do not invent mechanisms.
```

## Prompt 2 — `engine/space/pathfind` ∥ parallel-OK

```
Implement the Go module `engine/space/pathfind` strictly to its SPEC. Read HANDOFF.md first. SPEC:
/home/coder/workspace/bdg/backend/engine/space/pathfind/SPEC.md. Deps: engine/kernel/core +
engine/space/navmap (ALREADY built — read backend/engine/space/navmap/*.go for the exact snapshot API:
CellOf, Passable, StepCost, RequiredTags). This is for AGENT movement over the cost field (animals do
NOT use it — they local-steer). Implement Path(m, start, goal, caps) → (waypoints, cost, ok) and
EstimateCost + Caps. Per the SPEC Open Questions, default to **A* + funnel/string-pull, admissible
octile heuristic** (these defaults are pre-approved — not open). Honor D12: priority-queue ties broken by
a FIXED cell order (Y,X) not insertion; fixed neighbor compass order; no map-iteration affecting results;
maxExpansions budget → ok=false on exhaustion (never freeze); diagonal length = √2 agreeing with
navmap.StepCost. Write a test for EVERY Acceptance Criterion (straight line on uniform cost / routes
around a wall / through a door / prefers worn trail / capability gate / unreachable / determinism golden).
Verify gofmt/vet/`go test -count=2`/build. Report as usual.
```

## Prompt 3 — `platform/config` WI-P0 (compile env/fauna Rules)

```
Implement the WI-P0 world/env content loading in `platform/config` strictly to its sub-SPEC. Read
HANDOFF.md first. SPEC: /home/coder/workspace/bdg/backend/platform/config/SPEC-world.md (its status table
is current: the world.yaml/climate(CA1-3)/objects fauna+flora+decay/terrain SCHEMAS are ALL authored —
do NOT re-author schemas; build the LOADER). Read the existing platform/config/*.go (LoadContent,
ConfigHash, the schema-validate→build→cross-check pattern) and the engine NewRules signatures you must
call (all built): climate.NewRules([]TransitionRule), flora.NewRules(map[SpeciesID]SpeciesRule),
fauna.NewRules(map[SpeciesID]SpeciesRule)+DriveRule, decay.NewRules(map[KindID]KindRule). Implement:
parse content/world.yaml→world.EnvConfig + climate.Config geometry + navmap.Config; compile each
fauna/flora §6 + climate `when:` via engine/kernel/expr.Parse; build the 4 Rules; run the load-time
cross-checks (§ "§6 compilation + load-time cross-checks" — esp. fauna ReadsAttrs ⊆ AttrOperands() ∪
species drive ids; flora/climate operand + terrain-id cross-checks; the scent-cell ≥ max_speed·scent_spread
floor; grid-sync; bounds); add the new Registries fields (WorldEnv/ClimateCfg/ClimateRules/NavCfg/
TerrainTypes/FloraRules/FaunaRules/DecayRules, nil when source absent = subsystem OFF); fold the new files
into ConfigHash. Use the REAL content (content/objects.yaml fauna:/flora:/decay:, climate.yaml, world.yaml,
terrain.yaml) as the load target. Honor: optional-file neutrality (missing file ⇒ nil ⇒ OFF ⇒ existing
goldens hold); no IO/clock/rand beyond reading; typed load error builds NO partial registry. Write tests
for EVERY Acceptance Criterion. Verify gofmt/vet/`go test ./platform/config/ -count=2`/build. Report.
```

## Prompt 4 — `engine/world` WI-P1 env wiring (InstallEnv)

```
Implement the WI-P1 env orchestration in `engine/world` strictly to its sub-SPEC. Read HANDOFF.md first.
SPEC: /home/coder/workspace/bdg/backend/engine/world/SPEC-world-env.md. Read the existing world tick loop
(backend/engine/world/tick.go + SPEC-tick.md) — you EXTEND Phase 4 with a serial env sub-phase; do NOT
disturb the existing read→plan→collect→apply determinism. NOTE: there is a temporary "MOCK ENV FOR
FRONTEND TESTING" block in tick.go (hardcoded deer_1/wolf_1) — leave it for now (Phase 7 removes it).
Add InstallEnv(cfg, nav, climateState/Rules, floraState/Rules, decayState/Rules) + the new owned state
(all nil/empty ⇒ env OFF ⇒ byte-identical to today). Implement the Phase 4-ENV sub-phase in fixed order
climate→flora→decay (each on its tick%N cadence): climate.Step→map transitions GridCell→[]navmap.Cell via
the world-owned climateCellToNavCells bridge→navmap.SetTerrain; build flora SiteInput per plant (sample
nav.TerrainAt + climate.CellAt + spatial NeighborCount) → flora.Step → apply Spawned/Died/Grown to
objects[]+spatial; build decay EnvInput per lot (climate temp/moisture + StorageRateMult) → decay.Step →
apply. Add WorldSnapshot.ShadeOccluders projecting flora.ShadeOf (empty when flora OFF). Use
envFork(tick,channel) disjoint from agent forks; sorted everything (D12). Honor env-OFF neutrality (the
WI-P1 lever — existing world goldens MUST still pass). Write tests for EVERY Acceptance Criterion. Verify
gofmt/vet/`go test ./engine/world/ -count=2`/build (the existing world scenario tests must stay green).
Report. STOP and surface if any SPEC seam is ambiguous (e.g. the TerrainAttrs accessor G13 — accepted
rec (a): world reads map[TerrainID]Attrs from terrain.yaml via config).
```

## Prompt 5 — `engine/world` WI-P2 fauna+scent wiring (InstallFauna)

```
Implement the WI-P2 fauna & scent wiring in `engine/world` strictly to its sub-SPEC. Read HANDOFF.md
first. SPEC: /home/coder/workspace/bdg/backend/engine/world/SPEC-world-fauna.md (builds on WI-P1). Read
the just-built WI-P1 code + engine/fauna + engine/space/scent APIs + engine/ecosim/* (the integration
harness already implements this exact apply contract by hand — REUSE its logic as the reference for the
real wiring). Add InstallFauna(cfg, faunaRules, animals) + the animal set (map + sorted-ObjectID slice) +
the world-owned scent.Grid. In the PLAN phase build the fauna.Snapshot (Animals sorted, committed scent,
spatial, the TerrainSampler adapter = {FootprintBlocked, TerrainAt, BaseCost} over navmap, per-animal
EnvSample from climate.CellAt/Wind, Tick, Cadence, DT) and call fauna.Step with envFork(tick,"fauna"). In
COLLECT+APPLY, merge agent+animal intents into ONE lexicographically-sorted id stream; for an animal
intent: spatial.Move + commit Drives/Stamina/Heading/ActiveUntil/CurrentAction + layer the action drive
Effect + Vital/death. Then the SCENT sub-phase (after apply, serial): Deposit (predator EVERY tick at
post-move cell; prey/edible-flora on tick%ScentSpread), Spread(climate.Wind()) on tick%ScentSpread, Commit
every tick. Animal ids = prefix `an:<n>` (G12). Honor fauna-OFF neutrality + D12 (combined sort, disjoint
fork, no map-iteration). Write tests for EVERY Acceptance Criterion. Verify gofmt/vet/`go test
./engine/world/ -count=2`/build. Report.
```

## Prompt 6 — fixture / world-gen loader + main wiring (ACTIVATION)

```
Implement the fixture loader + main wiring that ACTIVATES the ecosystem, per docs/world-gen.md +
content/schema/fixture.schema.json + the InstallEnv/InstallFauna signatures (WI-P1/P2). Read HANDOFF.md +
docs/activation-gate.md (decisions accepted). Build/extend tools/worldgen Load: fixture → terrain layout →
navmap.New + climate.New + initial flora.State + decay.State + placed fauna.Animal set, then call
world.InstallEnv + world.InstallFauna, and Spawn agents. ACTIVATION per G5: place **deer×6, rabbit×8,
goat×2, wolf×1, bear×1, fish×8** (prey near grass/scent:food sources, wolf/bear central, fish in river,
goat near mountain), climate ON (annual band G4: AnnualMid 12.5/AnnualAmp 17.5), flora Rules ON with the
starter plants. Wire backend/main.go to Load the fixture + InstallEnv/InstallFauna. EXPECT a deliberate
golden re-baseline (env/fauna goldens flip from OFF→ON — this is the planned activation re-baseline, not a
regression; regenerate + eyeball them). Population maintenance = W11 respawn-to-target (no birth). Verify
build + `go test ./... -count=2` (re-baselined goldens stable). Report which goldens were re-baselined +
why. STOP and ask if any placement/balance choice is unclear beyond the accepted G5/G4.
```

## Prompt 7 — `platform/persist` + `platform/api` WI-P4 (env serialize + real WorldFrame)

```
Implement env-state serialization + the REAL WorldFrame SSE projection per docs/data-contracts.md §4/§10.
Read HANDOFF.md. Add to platform/persist: serialize animals[]/flora[]/climate (periodic full + sparse
deltas, sorted ids; scent NOT serialized — derived) per §10; bump schema_version on env activation. Add to
platform/api/world: emit the real WorldFrame{tick, hour_of_day, day_night, temperature, apparent_temp?,
raining, wind, agents[], animals[], flora_delta[], terrain_delta[]} from live env state — and DELETE the
temporary "MOCK ENV FOR FRONTEND TESTING" block in engine/world/tick.go (the hardcoded deer_1/wolf_1) now
that real data exists. God-view (real_stats/drives/stats) MUST NOT appear in WorldFrame. Honor env-OFF
neutrality (no WorldFrame when env not installed). Write tests (serialize round-trip / resume byte-identical
/ no god-view leak / WorldFrame shape). Verify gofmt/vet/`go test ./platform/... -count=2`/build. Report.
```

## Prompt 8 — `frontend` ecosystem rendering

```
Implement the ecosystem rendering in the frontend per frontend/SPEC.md §"Ecosystem rendering". Read
HANDOFF.md + frontend/SPEC.md. The data layer (useWorld.ts WorldFrame reducer → animals/flora/climate) is
ALREADY done — your job is the RENDER layer + wiring those WorldState slices into WorldCanvas (App.tsx
currently passes only agents/objects). Add to src/utils/canvasRenderer.ts: drawAnimals(ctx, animals, tr, t)
(per-species sprite oriented by heading, predator vs prey distinct, stamina dimming), drawFlora(ctx, flora,
tr, t) (stage/width-scaled sprite), drawAmbient(ctx, climate, W, H, t) (day-night tint, temperature cue,
rain overlay, wind arrow). All layers share the ONE buildTransform (anchor to RenderConfig.bounds when
present). Add an `ecosystem` event-log filter. Keep read-only / no god-view / env-off neutrality (no
WorldFrame ⇒ unchanged viewer). Write Vitest unit tests for the reducer paths + pure render helpers per
the SPEC Acceptance Criteria. Verify `npm run build` + `npm test`. Report.
```

## Prompt 9 — activation balance tuning + world scenario tests (FA1–FA8)

```
Tune the activated ecosystem to the desired emergent scenarios and lock them with tests. Read HANDOFF.md +
docs/scenarios-world.md (FA1–FA8) + docs/activation-gate.md. The engine + wiring are built; this is the
BALANCE + SCENARIO-TEST pass (content §6 coefficients in content/objects.yaml fauna:/flora: + balance.yaml,
NOT engine code — D10). Write engine/world scenario tests (mirroring the existing scenario_*_test.go style)
that assert the headline emergent behaviors over N ticks with a fixed seed: deer grazes then flees on
predator scent/sight; wolf scent-homes + the chase peters out when wolf fatigue rises ("predator tires
first"); flora/fauna populations stay bounded (regen vs predation); wind-driven scent reaches downwind prey;
cold climate slows movement (thermal→speed). Iterate the §6 coefficients (e.g. fear bands, hunger/fatigue
rates, thermal speed coeffs, the G5 population targets) until each scenario passes, re-baselining goldens
deliberately. Report which coefficients changed + the final scenario pass/fail table. This is where
"원하는 시나리오대로 진행" is actually verified. STOP and surface any scenario that cannot be achieved by
content tuning alone (it would indicate an engine/SPEC gap, not a balance issue).
```

---

## Notes for the human

- **Each phase flips the matching `docs/activation-gate.md` Open-Question entries to RESOLVED** as it is
  built (the recs are accepted; the SPEC text stays authoritative).
- **The `engine/ecosim` harness** (already green, 2000 ticks deterministic) can stay as a fast integration
  smoke test, or be retired once the real `engine/world` WI-P2 scenario tests (Prompt 9) cover the same
  ground. It also has an unfinished behavior-scenario refinement (a `TestEcosimPredatorPreyScenario` was
  requested but the agent was cut off before writing it) — Prompt 9's real world scenario tests supersede it.
- **Wall-slide (optional fauna polish):** fauna steer currently DEAD-STOPS at a blocked cell (SPEC-conformant
  "stays"); the SPEC also allows "slides". Animals can pin at boundaries/obstacles (they have no pathfinding
  by design — D11/F35 local steering). If pinning hurts the scenarios in Prompt 9, add a SPEC-conformant
  wall-slide (project the steer velocity onto the unblocked tangent) to engine/fauna step.go + a test. This
  is the one identified behavior-quality improvement; decide based on Prompt 9 results.
```
