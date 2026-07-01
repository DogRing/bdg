# Codex prompts — remaining `bdg` pipeline (implement + review)

> **How to use.** One Codex **task per phase** (not per file — a module's files are interdependent;
> each task produces the module's `.go` + tests together). Run a fresh thread per task. Every prompt
> assumes Codex has read **`AGENTS.md`** (durable rules: determinism, the Open-Question gate, build/test
> commands) and, for review tasks, **`docs/code_review.md`**. Prompts use the Codex 4-element shape:
> **Goal · Context · Constraints · Done when.** Attach the listed files with `@` mentions.
>
> **Reasoning effort:** High (or Extra-High) for phases 3–6 (config/world wiring — reasoning-heavy,
> determinism-critical); Medium for 7–9.
>
> **Phases 1–2 are DONE** (`engine/env/decay`, `engine/space/pathfind` — built, reviewed, green). This
> file covers **phases 3–9**. It supersedes `docs/handoff-prompts.md` for Codex (same content, Codex
> format).

## Order & parallelism
```
3. platform/config WI-P0        (needs the env-module NewRules — all present)
        ↓
4. world WI-P1  InstallEnv       (needs config + climate/flora/decay + navmap)
        ↓
5. world WI-P2  InstallFauna      (needs fauna + scent + WI-P1)
        ↓
6. fixture/world-gen + main       (ACTIVATION: place G5 species; deliberate golden re-baseline)
        ↓
7. persist + api WI-P4            (env serialize + real WorldFrame SSE; delete the mock)
        ↓
8. frontend ecosystem rendering   (∥ can start once WorldFrame shape is fixed in 7)
        ↓
9. balance tuning + FA1–FA8 scenario tests   (verifies "the desired scenarios")
```
Each phase: **implement task → review task (paste the Review prompt, referencing `docs/code_review.md`)
→ fix loop → next phase.** Do not start a phase while its dependency is unmerged/red.

## Report format — end EVERY task with this block (verbatim, no extra prose around it)
> **Canonical copy lives in `AGENTS.md` §Reporting, which Codex auto-reads every task — so this rule
> applies WITHOUT you pasting it into a prompt.** It is duplicated here only for quick reference. Keep
> keys fixed + values terse; never hide a FAIL.

**Implement task:**
```
=== CODEX REPORT ===
TASK: <phase N — module> (implement)
STATUS: DONE | PARTIAL | BLOCKED
FILES:
  - <path> (new|edited, ~N lines)
AC_COVERAGE:
  - <SPEC acceptance criterion> -> <test func>
VERIFY:
  - go build ./...              : PASS | FAIL
  - go test ./<pkg>/ -count=2   : PASS | FAIL (<N> tests)
  - go vet ./<pkg>/             : PASS | FAIL
  - gofmt -l <pkg-dir>          : CLEAN | <files listed>
GOLDENS: none | re-baselined: <which + one-line why>
CHOICES: <SPEC-delegated functional-form/ambiguity decisions> | none
BLOCKED_ON: <OPEN question that needs a HUMAN decision> | none
DEVIATIONS: <anything not per SPEC + why> | none
NEXT: <the phase/task this unblocks>
=== END ===
```

**Review task:**
```
=== CODEX REVIEW ===
TASK: <phase N — module> (review)
VERDICT: PASS | NEEDS_FIX
VERIFY:
  - go build ./...              : PASS | FAIL
  - go test ./<pkg>/ -count=2   : PASS | FAIL (<N> tests)
  - go vet / gofmt -l           : CLEAN | <issues>
CHECKS (per docs/code_review.md): interface:? determinism:? AC:? integration:? gate:? build:?   (each PASS|FAIL)
INTEGRATION_CONTRACT: <consumer SPECs checked + do signatures match?> | n/a
FINDINGS:            (list only if NEEDS_FIX; else "none")
  - <file:line> — <violated SPEC clause / AC / invariant> — <minimal fix>
NOTES: <non-blocking observations> | none
=== END ===
```

---

## Phase 3 — `platform/config` WI-P0 (compile env/fauna Rules)

**Files:** new/edited under `backend/platform/config/` (extend `LoadContent`, `Registries`,
`ConfigHash`; add the env build + cross-checks) + tests. Driven by
`@backend/platform/config/SPEC-world.md`.

### Implement
- **Goal:** Extend `platform/config` to load the world-integration content and produce the
  already-validated, already-compiled inputs for `world.InstallEnv`/`InstallFauna`: build
  `world.EnvConfig` + the per-module geometry Configs, compile the `fauna:`/`flora:` §6 and the
  `climate.yaml` transition table into the engine `Rules` via `engine/kernel/expr`, and run the
  load-time cross-checks. Engine never parses YAML.
- **Context:** `@backend/platform/config/SPEC-world.md` (authoritative; its schema table is CURRENT —
  the `fauna:`/`climate` CA1-3/`terrain.yaml` schemas are already authored, do NOT re-author). Read the
  existing `@backend/platform/config/` loader (`LoadContent`, `ConfigHash`, the schema→build→cross-check
  pattern) and the constructors you must call: `climate.NewRules`, `flora.NewRules`, `fauna.NewRules`
  (+`DriveRule`), `decay.NewRules` (+`StateRule`/`TransformRule`) — read those modules' SPEC Public
  Interfaces for the exact shapes. Real content targets: `content/world.yaml`, `content/climate.yaml`,
  `content/terrain.yaml`, `content/objects.yaml` (`fauna:`/`flora:`/`decay:`).
- **Constraints:** Follow `AGENTS.md`. Compile each §6 via `expr.Parse` (config compiles, engine
  evaluates). Run every cross-check in the SPEC's "§6 compilation + load-time cross-checks": fauna
  `expr.ReadsAttrs() ⊆ fauna.AttrOperands() ∪ species drive ids`; Stat channel ⊆ stats registry;
  candidate actions ⊆ `actions.Registry`; flora/climate operand + terrain-id cross-checks; the
  **scent-cell ≥ max_speed·scent_spread** floor; grid-sync; bounds. A failed check is a **typed load
  error that builds NO partial registry**. New `Registries` fields are `nil` when the source file is
  absent ⇒ subsystem OFF ⇒ existing behavior unchanged (optional-file neutrality). No IO/clock/rand
  beyond reading the listed files; cross-checks iterate sorted ids (D12). Fold the new files into
  `ConfigHash`.
- **Done when:** every "Acceptance Criteria (testable)" in the SPEC has a passing test (world.yaml load +
  EnvConfig mapping; scent-cell floor rejection; grid-sync/bounds errors; fauna §6 compile + operand
  cross-check failure naming species+operand; flora/climate §6 cross-check; ConfigHash includes new
  files; optional-file neutrality; determinism/no-IO guard). Verify: `go test ./platform/config/
  -count=2`, `go vet`, `gofmt -l`, `go build ./...` all clean.

### Review
Apply `@docs/code_review.md`. Focus: (1) the cross-checks actually reject the bad cases with descriptive
errors and build no partial registry; (2) the compiled `Rules` match each engine module's `NewRules`
shape EXACTLY (grep the module SPECs); (3) optional-file neutrality (missing file ⇒ nil ⇒ existing
config goldens unchanged); (4) `ConfigHash` determinism. Verdict PASS/NEEDS_FIX per the checklist.

---

## Phase 4 — `engine/world` WI-P1 env wiring (InstallEnv)

**Files:** edited `backend/engine/world/` (add `InstallEnv` + owned env state + the Phase 4-ENV
sub-phase + the climate→navmap bridge + flora `SiteInput`/decay-env sampling + `ShadeOccluders`) +
tests. Driven by `@backend/engine/world/SPEC-world-env.md`.

### Implement
- **Goal:** Wire the pure env transforms (`climate`/`flora`/`decay`) + the navmap terrain field into the
  world tick loop as a serial env sub-phase appended to Phase 4 (apply), keeping the world the sole
  mutator and env-OFF byte-identical to today.
- **Context:** `@backend/engine/world/SPEC-world-env.md` (authoritative) + the existing world tick loop
  `@backend/engine/world/tick.go` + `@backend/engine/world/SPEC-tick.md`. Read the Public Interfaces of
  `climate`/`flora`/`decay`/`navmap` for the exact `Step`/apply signatures. **Note:** `decay.Step` takes
  `elapsedTicks int64` as its 3rd arg (pass `int64(envCfg.DecayStep)`); `flora.Step` takes an `idAlloc`
  func; `climate.Step` returns `[]Transition`. A temporary MOCK block exists in `tick.go` — leave it.
- **Constraints:** Follow `AGENTS.md`. `InstallEnv(cfg, nav, climateState/Rules, floraState/Rules,
  decayState/Rules)`; all nil/empty ⇒ env OFF ⇒ existing world goldens hold. Phase 4-ENV runs AFTER
  intent apply, serial, fixed module order **climate→flora→decay**, each on its `tick % N` cadence:
  climate.Step → map each transition `GridCell`→`[]navmap.Cell` (world-owned `climateCellToNavCells`
  bridge) → `navmap.SetTerrain`; build flora `SiteInput` per plant (sample `nav.TerrainAt` +
  `climate.CellAt` + spatial `NeighborCount`) → flora.Step → apply spawn/die/grow to `objects[]`+spatial;
  build decay `EnvInput` per lot (climate temp/moisture + StorageRateMult) → decay.Step → apply. Add
  `WorldSnapshot.ShadeOccluders` projecting `flora.ShadeOf` (empty when flora OFF). `envFork(tick,
  channel)` disjoint from agent forks; sorted everything (D12); no map-iteration for order.
- **Done when:** every SPEC AC has a passing test (env-OFF neutrality = existing world scenarios stay
  green; climate cadence + bridge; `climateCellToNavCells` correctness + D12 sort; flora step+apply;
  decay step+apply; disjoint env forks; ShadeOccluders projection + neutrality; env-phase order + a
  determinism golden). Verify: `go test ./engine/world/ -count=2` (existing scenarios MUST stay green),
  `go vet`, `gofmt -l`, `go build ./...`.

### Review
Apply `@docs/code_review.md`. Focus: (1) **env-OFF neutrality** — the existing world scenario suite is
byte-unchanged when `InstallEnv` is not called; (2) the decay/flora/climate `Step` calls match the
actual signatures (esp. `decay.Step` `elapsedTicks`); (3) serial env sub-phase runs AFTER apply, fixed
order, disjoint forks, sorted deltas — no determinism leak; (4) no map-iteration drives the transition
cell set.

---

## Phase 5 — `engine/world` WI-P2 fauna+scent wiring (InstallFauna)

**Files:** edited `backend/engine/world/` (add `InstallFauna` + animal set + world-owned `scent.Grid` +
`fauna.Step` in plan phase + combined agent+animal apply + scent deposit/spread/commit + the
`EnvSample`/`TerrainSampler` adapters) + tests. Driven by
`@backend/engine/world/SPEC-world-fauna.md` (builds on WI-P1).

### Implement
- **Goal:** Wire the reduced-reactive animal controller (`engine/fauna`) + the shared scent field into
  the tick loop: fauna.Step in the plan phase, a combined agent+animal apply in one sorted-id stream,
  and the scent deposit/spread/commit driving — keeping the world the sole mutator and fauna-OFF
  neutral.
- **Context:** `@backend/engine/world/SPEC-world-fauna.md` (authoritative) + the WI-P1 code from Phase 4
  + the Public Interfaces of `@backend/engine/fauna` and `@backend/engine/space/scent`. **Strong
  reference:** `@backend/engine/ecosim/` already implements this exact apply contract by hand (build the
  fauna.Snapshot, call fauna.Step, apply intents, drive scent) — reuse its logic as the template for the
  real wiring. The `TerrainSampler` is the 3-method `{FootprintBlocked, TerrainAt, BaseCost}` over
  navmap; `EnvSample` comes from `climate.CellAt`/`climate.Wind()`.
- **Constraints:** Follow `AGENTS.md`. `InstallFauna(cfg, faunaRules, animals)`; not called ⇒ fauna OFF
  ⇒ byte-identical. PLAN phase: build `fauna.Snapshot` (Animals sorted, committed scent buffer, spatial,
  the TerrainSampler adapter, per-animal EnvSample, Tick, Cadence, DT); call `fauna.Step` with
  `envFork(tick,"fauna")` (disjoint). COLLECT+APPLY: merge agent+animal intents into ONE
  lexicographically-sorted id stream; for an animal intent — `spatial.Move` + commit
  Drives/Stamina/Heading/ActiveUntil/CurrentAction + layer the action's drive Effect + Vital/death.
  SCENT sub-phase (after apply, serial): Deposit (predator EVERY tick at post-move cell; prey/edible-
  flora on `tick % ScentSpread`), Spread(`climate.Wind()`) on `tick % ScentSpread`, Commit every tick
  (next-tick latency). Animal ids = prefix `an:<n>` (disjoint from AgentID). D12 throughout (combined
  sort, disjoint fork, no map-iteration).
- **Done when:** every SPEC AC has a passing test (fauna-OFF neutrality; fauna.Step in plan phase +
  missing-Env panic; EnvSample from climate; TerrainSampler semantics — water traversable-at-cost, walls
  blocked; combined agent+animal apply order + conflict tie-break; animal move/commit/effect/death;
  scent deposit/spread/commit driving incl. post-move positions + next-tick latency; disjoint fork;
  scent-cell floor; determinism golden). Verify: `go test ./engine/world/ -count=2`, `go vet`,
  `gofmt -l`, `go build ./...`.
- **ALSO (small carried-over fix from phase 4, keep it isolated from the fauna wiring):** world evaluates
  `flora.Rules.PropRadius` to size the NeighborCount query WHILE `SiteInput.NeighborCount` is still 0
  (chicken-and-egg), so a `PropRadius` §6 that references `neighbor_count` would diverge from the value
  `flora.Step` later uses. Guard it: (a) in `platform/config` `buildFloraRules`, REJECT at load time if a
  species' `PropRadius` program's `ReadsAttrs()` contains `neighbor_count` (descriptive error naming the
  species), and (b) add a one-line note to `flora/SPEC.md` `PropRadius` that it must not read
  `neighbor_count`. Add a config test for the rejection. This is 3 small edits; do not entangle it with
  the fauna/scent work.

### Review
Apply `@docs/code_review.md`. Run the BROADER verify (config was touched too, and the implement report
omitted vet/gofmt): `go test ./... -count=2`, `go vet ./...`, `gofmt -l` on the changed dirs (must be
empty), `go build ./...`. Report each explicitly.

Focus: (1) fauna-OFF neutrality — with `InstallFauna` not called the existing world scenario suite +
snapshot are byte-unchanged; (2) the combined apply is ONE lexicographically-sorted id stream
(agents+animals) with conflict ties by id — shuffling collection order yields byte-identical post-apply
state; (3) scent deposits use POST-move positions + Commit gives next-tick latency (deposit at T visible
at T+1); (4) the adapters match fauna's declared `EnvSample`/`TerrainSampler` shapes; (5) disjoint
`envFork(tick,"fauna")`, independent of agent count; (6) the carried-over guard: `platform/config`
rejects a `PropRadius` §6 that reads `neighbor_count` (with a test), and `flora/SPEC.md` documents it.

ALSO scrutinize the DEVIATION the implement report flagged:
(7) **SCENT EMITTER CLASSIFICATION** — `SPEC-world-fauna.md` says the deposit channel comes from the
    emitter's `scent:<channel>` content TAG (D4/D10 — "which kind carries which channel is content, not
    engine logic"), because that lets future emitters (carrion via decay, etc.) work with no engine
    change. The implementation instead classifies predator/prey via `fauna.Rules` (IsPredator) and food
    via live flora state, because world object/animal records don't yet carry the content tags.
    Determine: (a) does it deposit the CORRECT channels for the current species+flora (predator→
    ChanPredator, prey→ChanPrey, edible flora→ChanFood), deterministically? (b) Is the hardcoded
    channel↔source mapping a D10/D2 violation, or an acceptable documented INTERIM? (c) What is the
    proper fix (world object/animal records carry their kind's `scent:<channel>` tags, or a
    kind→tags registry lookup, so classification is tag-driven)? Recommend: keep as a DOCUMENTED interim
    ONLY IF it is clearly marked + tracked and does not block the tag-driven path; otherwise NEEDS_FIX.
    Flag it either way in FINDINGS/NOTES.

---

## Phase 5-FIX — tag-driven scent emitter classification + AC regression tests

**Resolves the phase-5 review NEEDS_FIX.** Files: `backend/platform/config/` (extract per-kind
`scent:<channel>` tags into a registry on `LoadOutput`) + `backend/engine/world/fauna.go` (deposit by
that registry, not `IsPredator`/live-flora) + `backend/engine/world/fauna_test.go` (2 regression tests +
thread the registry through the install calls). Driven by `@backend/engine/world/SPEC-world-fauna.md`
(scent emitter classification: L167 "for each `scent:<channel>`-tagged emitter", L185 "emitter
classification is the world's read of content `scent:<channel>` tags — content, not engine logic",
L243/251).

### Implement
- **Goal:** Make scent DEPOSIT CHANNEL selection tag-driven, exactly as the SPEC mandates, replacing the
  interim engine-side classification (`fauna.Rules.IsPredator` for predator/prey + "all live flora =
  food"). Add the two missing determinism regression tests.
- **Context:** The content is ALREADY tagged (`content/objects.yaml`: predators `scent:predator`, prey
  `scent:prey`, edible flora `scent:food`; note `oak`/`wildflower` are flora WITHOUT `scent:food`).
  `platform/config` already parses each kind's tags (see `faunaIsPredator` reading `obj.Tags`). World
  records already carry the emitter kind — `fauna.Animal.Species` and `flora.Plant.Species` — so NO new
  record field is needed, only a kind→channels registry keyed by species/kind id. `scent.Channel`
  constants live in `@backend/engine/space/scent` (ChanPredator/ChanPrey/ChanFood; carrion is a future
  channel).
- **Constraints:** Follow `AGENTS.md` + the gate. This is CONFORMING the code to the SPEC (not inventing
  a mechanism, not weakening the SPEC).
  1. **config** — in `buildWorldContent`, build `map[core.Tag][]core.Tag` = kind id → its `scent:*` tags
     (parse every object kind's `Tags`, keep the `scent:`-prefixed ones, sorted + deduped). Expose it on
     `worldContent` and `LoadOutput` as `ScentEmitters`. Keep config a DUMB tag extractor — do NOT import
     or map to the `scent` enum in config.
  2. **world** — store the registry (set via the install path; nil/empty ⇒ NO deposits ⇒ fauna-OFF stays
     byte-identical). Add a tiny token→`scent.Channel` resolver IN world (`predator`→ChanPredator,
     `prey`→ChanPrey, `food`→ChanFood; unknown token ⇒ skip deterministically — that is exactly what lets
     a future `scent:carrion` tag no-op cleanly until P_fa3 defines the channel, with zero engine edit).
  3. `runScentEnv` / `depositFloraFoodScent` — select channels from `ScentEmitters[kind]`, NOT from
     `IsPredator` / live-flora state. Keep the SPEC's per-channel cadence (predator EVERY tick, prey/food
     on `tick % ScentSpread`): cadence is engine policy keyed by CHANNEL; the tag only decides WHICH
     channel a kind emits. Keep the existing magnitude derivation (`animalScentMagnitude`/
     `floraScentMagnitude`) UNCHANGED — only channel selection becomes tag-driven (scope guard). Iterate
     emitters in sorted id order (D12).
  4. Do NOT weaken `SPEC-world-fauna.md`; the code now conforms to it as written.
- **Behavior change (intended, not a pure refactor):** for the current G5 content the predator/prey
  channels are UNCHANGED (every animal is correctly tagged), but the FOOD channel now EXCLUDES non-edible
  flora — `oak`/`wildflower` (no `scent:food`) will stop emitting food scent, which the old "all live
  flora" path wrongly deposited. So if the determinism golden digest shifts, it MUST be only because such
  non-edible flora dropped out of the food channel — verify that is the sole cause and update the golden
  intentionally (call it out in the report).
- **Done when:** deposits are driven purely by content `scent:<channel>` tags; existing scent tests pass
  (updated to pass the registry through install); and TWO new regression tests exist —
  - **shuffle-invariance:** apply the SAME agent+animal intent set through `applyCombinedIntents` in two
    DIFFERENT collection orders (e.g. given vs reversed) on two identical fixtures → `worldDigest` +
    `animalDigest` byte-identical.
  - **plan-before-commit latency:** a scent deposited+committed at the END of tick T is NOT visible to the
    fauna plan snapshot built DURING tick T (plan runs before the scent sub-phase) and IS visible at T+1
    (F33 next-tick latency).
  Verify: `go test ./... -count=2`, `go vet ./...`, `gofmt -l` (changed dirs empty), `go build ./...`.

### Review
Apply `@docs/code_review.md`. Verify `go test ./... -count=2`, `go vet ./...`, `gofmt -l`, `go build
./...` — report each. Focus:
(1) DEPOSIT is now tag-driven — `runScentEnv`/`depositFloraFoodScent` no longer branch on `IsPredator` or
    treat all flora as food; the channel comes from `ScentEmitters[species]`. A hypothetical `scent:carrion`
    tag would route to a carrion deposit with NO engine edit (or no-op cleanly until the channel exists).
(2) Correctness of the intended behavior change — predator/prey channels UNCHANGED for G5 content; the food
    channel now excludes `scent:food`-less flora (`oak`/`wildflower`). If the golden shifted, confirm that
    is the ONLY cause (a legitimate correctness fix), not a determinism regression.
(3) fauna-OFF neutrality preserved — nil/empty registry ⇒ no deposits ⇒ the world scenario suite + snapshot
    are byte-unchanged.
(4) config stays free of the `scent` enum; `ScentEmitters` is sorted + deduped (D12); the world
    token→Channel resolver skips unknown tokens deterministically.
(5) both regression tests genuinely exercise the invariants — the shuffle test actually permutes INPUT
    order (not just the tie case), and the latency test asserts the plan at T cannot see T's committed
    deposit.
Return the `=== CODEX REVIEW ===` block, with `INTEGRATION_CONTRACT` covering scent classification vs
`SPEC-world-fauna`.

---

## Phase 6 — fixture / world-gen loader + main wiring (ACTIVATION)

**Files:** `backend/tools/worldgen` (extend `Load`) + `backend/main.go` (wire Load + InstallEnv/
InstallFauna + Spawn) + the starter fixture content + re-baselined goldens. Driven by
`@docs/world-gen.md` + `content/schema/fixture.schema.json` + the WI-P1/P2 install signatures.

### Implement
- **Goal:** Activate the ecosystem: load a fixture (terrain layout + placed plants + placed animals),
  build `navmap`/`climate`/`flora`/`decay` states, call `world.InstallEnv` + `world.InstallFauna`, and
  Spawn agents — so a live run has weather, growing flora, and moving fauna.
- **Context:** `@docs/world-gen.md`, `@content/schema/fixture.schema.json`, the WI-P1/P2
  `InstallEnv`/`InstallFauna` signatures, `@docs/activation-gate.md` (decisions are ACCEPTED). Existing
  fixtures live in `backend/engine/world/testdata/fixtures` + `content/`.
- **Constraints:** Follow `AGENTS.md` + the accepted G5/G4 decisions: place **deer×6, rabbit×8, goat×2,
  wolf×1, bear×1, fish×8** (prey near grass/`scent:food`, wolf/bear central, fish in the river, goat
  near the mountain); climate ON with the G4 band (`AnnualMid 12.5`/`AnnualAmp 17.5`); flora Rules ON
  with the starter plants; order = env first then fauna. Population maintenance = **W11 respawn-to-target
  (no birth)**. This flips env/fauna goldens OFF→ON — that is a **deliberate activation re-baseline**,
  not a regression: regenerate the affected goldens and **eyeball them for sanity**, don't just
  regenerate-to-pass. Flip the corresponding `docs/activation-gate.md` / SPEC Open-Question entries to
  RESOLVED as you go.
- **Done when:** `go build ./...` + `go test ./... -count=2` green with the re-baselined goldens stable
  across two runs; `main.go` runs a few hundred ticks headless without panic; your summary lists WHICH
  goldens were re-baselined and why. STOP and ask if a placement/balance choice is unclear beyond the
  accepted G5/G4.

### Review
Apply `@docs/code_review.md` + special attention: (1) the golden re-baseline is **deliberate + sane**
(the diffs reflect real activation, not a masked bug); (2) determinism holds post-activation
(`-count=2`, fresh process); (3) the G5 placement + G4 band match `docs/activation-gate.md`;
(4) no invented mechanism (only accepted decisions were used).

---

## Phase 7 — `platform/persist` + `platform/api` WI-P4 (env serialize + real WorldFrame)

**Files:** `backend/platform/persist/*` (serialize `animals[]`/`flora[]`/`climate` + deltas),
`backend/platform/api/*` (real `WorldFrame` SSE), and **DELETE** the mock in
`backend/engine/world/tick.go` + reconcile `events.go`. Driven by `@docs/data-contracts.md` §4/§10.

### Implement
- **Goal:** Serialize env state (periodic full + sparse deltas) and stream the REAL `WorldFrame` over
  SSE from live env state, replacing the temporary frontend mock.
- **Context:** `@docs/data-contracts.md` (§4 events, §10 env state), the live env state exposed by
  `engine/world` (WI-P1/P2), the existing `@backend/platform/persist/` + `@backend/platform/api/`, and
  the mock in `@backend/engine/world/tick.go`.
- **Constraints:** Follow `AGENTS.md`. Persist: `animals[]`/`flora[]`/`climate` in sorted id/cell order
  (scent NOT serialized — derived), periodic-full + delta channels per §10; bump `schema_version` on env
  activation; resume must be byte-identical. API: emit `WorldFrame{tick, hour_of_day, day_night,
  temperature, apparent_temp?, raining, wind, agents[], animals[], flora_delta[], terrain_delta[]}` from
  live state; **god-view (`real_stats`/`drives`/`stats`) MUST NOT appear** in WorldFrame. Now **delete
  the mock** (`world/tick.go` hardcoded deer_1/wolf_1). Env-OFF ⇒ no WorldFrame (viewer unchanged).
- **Done when:** tests pass (serialize round-trip; resume byte-identical; no god-view leak; WorldFrame
  shape per §4); `go test ./platform/... -count=2` + `go build ./...` clean; the mock is gone and no
  code still references the fake ids.

### Review
Apply `@docs/code_review.md`. Focus: (1) no god-view field in any SSE payload; (2) resume byte-identical
(env state + rng_state round-trip); (3) the mock is fully removed and the real WorldFrame carries the
same shape the frontend reducer expects (`frontend/src/types.ts` `WorldFramePayload`); (4) env-OFF
neutrality.

---

## Phase 8 — `frontend` ecosystem rendering

**Files:** `frontend/src/utils/canvasRenderer.ts` (+`drawAnimals`/`drawFlora`/`drawAmbient`),
`frontend/src/components/WorldCanvas.tsx` (accept animals/flora/climate), `frontend/src/App.tsx` (pass
the env slices), `frontend/src/components/EventLog.tsx` (an `ecosystem` filter), + Vitest tests. Driven
by `@frontend/SPEC.md` §"Ecosystem rendering".

### Implement
- **Goal:** Render the ecosystem the data layer already reduces: animals (heading-oriented, per-species,
  predator vs prey, stamina dimming), live flora (stage/width-scaled), and ambient weather (day-night
  tint, temperature cue, rain, wind arrow) — and wire the `WorldState.animals/flora/climate` slices into
  `WorldCanvas` (App.tsx passes only agents/objects today).
- **Context:** `@frontend/SPEC.md` (the §Ecosystem-rendering spec + the module structure), the existing
  `@frontend/src/hooks/useWorld.ts` (WorldFrame reducer — ALREADY done; do not rebuild it),
  `@frontend/src/utils/canvasRenderer.ts`, `@frontend/src/components/WorldCanvas.tsx`,
  `@frontend/src/types.ts`.
- **Constraints:** Follow `AGENTS.md`. All layers share the ONE `buildTransform` (anchor to
  `RenderConfig.bounds` when present). Read-only viewer; **no god-view**; env-OFF neutrality (no
  WorldFrame ⇒ animals/flora/climate empty ⇒ viewer unchanged). Keep render functions pure
  (`state → pixels`), state stays the `useWorld` reducer.
- **Done when:** Vitest unit tests for the reducer paths + pure render helpers pass per the SPEC ACs
  (drawAnimals orientation+species; drawFlora stage scale; drawAmbient day-night/rain/wind; env-off
  neutrality; one shared transform); `npm run build` + `npm test` clean.

### Review
Apply `@docs/code_review.md` (frontend-adapted). Focus: (1) env slices actually reach `WorldCanvas` and
render; (2) one shared transform (agents/animals/flora align); (3) no god-view; (4) env-OFF neutrality
(no WorldFrame ⇒ byte-same as today).

---

## Phase 9 — balance tuning + FA1–FA8 scenario tests (verify the desired scenarios)

**Files:** `backend/engine/world/scenario_*_test.go` (new FA scenario tests) + tuned `content/*.yaml`
(fauna/flora §6 coefficients, `balance.yaml`, G5 population targets) + re-baselined goldens. Driven by
`@docs/scenarios-world.md` (FA1–FA8) + `@docs/activation-gate.md`.

### Implement
- **Goal:** Tune the activated ecosystem so the headline emergent behaviors reliably occur, and lock
  them with deterministic scenario tests: deer grazes then flees on predator scent/sight; wolf
  scent-homes and the chase peters out when its fatigue rises ("predator tires first"); flora/fauna
  populations stay bounded (regen vs predation); wind-driven scent reaches downwind prey; cold climate
  slows movement (thermal→speed).
- **Context:** `@docs/scenarios-world.md` (FA1–FA8), the existing `scenario_*_test.go` style in
  `@backend/engine/world/`, `@content/objects.yaml` (`fauna:`/`flora:`), `@content/balance.yaml`,
  `@backend/engine/ecosim/` (behavior harness — a reference for what emerges). This is a **content/§6
  balance pass**, NOT engine code (D10).
- **Constraints:** Follow `AGENTS.md`. Change behavior via `content/*.yaml` §6 coefficients + balance +
  population targets, **not** engine Go. Each scenario test = fixed seed, N ticks, asserts the emergent
  behavior. Iterate coefficients until each passes; re-baseline goldens deliberately. If a scenario
  CANNOT be achieved by content tuning alone, STOP and report — that indicates an engine/SPEC gap, not a
  balance issue (do not patch the engine to force it).
- **Done when:** every FA1–FA8 scenario test passes deterministically (`-count=2`); your summary gives a
  pass/fail table + which coefficients changed. `go test ./engine/world/ -count=2` + `go build ./...`
  clean.

### Review
Apply `@docs/code_review.md`. Focus: (1) behavior changes came from **content/§6**, not engine hacks
(diff the engine — it should be untouched, or any engine change is flagged + justified); (2) scenario
tests are deterministic + assert real emergent behavior (not tautologies); (3) goldens re-baselined
sanely.

---

## Notes carried over
- **fauna wall-slide (optional):** fauna steer DEAD-STOPS at a blocked cell (SPEC-conformant "stays");
  the SPEC also allows "slides". Animals can pin at boundaries (no pathfinding by design — local steer).
  If pinning hurts Phase-9 scenarios, add a SPEC-conformant wall-slide (project the steer velocity onto
  the unblocked tangent) to `engine/fauna/step.go` + a test — as a small, separate task.
- **Decisions:** `docs/activation-gate.md` G1–G17 are ACCEPTED (recommendations). Flip each owning SPEC's
  Open-Question entry to RESOLVED as its phase lands.
