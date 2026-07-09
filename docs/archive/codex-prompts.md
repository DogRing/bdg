# Codex prompts — remaining `bdg` pipeline (implement + review)

> **How to use.** One Codex **task per phase** (not per file — a module's files are interdependent;
> each task produces the module's `.go` + tests together). Run a fresh thread per task. Every prompt
> assumes Codex has read **`AGENTS.md`** (durable rules: determinism, the Open-Question gate, build/test
> commands) and, for review tasks, **`docs/process/code-review.md`**. Prompts use the Codex 4-element shape:
> **Goal · Context · Constraints · Done when.** Attach the listed files with `@` mentions.
>
> **Reasoning effort:** High (or Extra-High) for phases 3–6 (config/world wiring — reasoning-heavy,
> determinism-critical); Medium for 7–9.
>
> **Phases 1–2 are DONE** (`engine/env/decay`, `engine/space/pathfind` — built, reviewed, green). This
> file covers **phases 3–9**. It supersedes `docs/archive/handoff-prompts.md` for Codex (same content, Codex
> format).

## Order & parallelism
```
3. platform/config WI-P0        (needs the env-module NewRules — all present)      [DONE]
        ↓
4. world WI-P1  InstallEnv       (needs config + climate/flora/decay + navmap)     [DONE]
        ↓
5. world WI-P2  InstallFauna     (needs fauna + scent + WI-P1)                      [DONE +FIX]
        ↓
6. COMBAT module mods  (leaf-first, OFF-neutral — goldens hold; docs/plans/fauna.md 클러스터 9 / FC1–FC13)
     6a scent +ChanCarrion · 6b decay +WithLot · 6c fauna Attack/Feed+engaged+operands
     6d config combat fields · 6e world damage/carcass/feed apply · 6f content (species STILL unplaced)
        ↓
7. fixture/world-gen + main       (ACTIVATION: place G5 + combat content; ONE golden re-baseline;
                                   end-to-end predation verified HERE)
        ↓
8. persist + api WI-P4            (env serialize + real WorldFrame SSE; delete the mock)
        ↓
9. frontend ecosystem rendering   (∥ can start once WorldFrame shape is fixed in 8)
        ↓
10. balance tuning + FA1–FA8 scenario tests   (verifies "the desired scenarios" incl. predation)
```
Each phase: **implement task → review task (paste the Review prompt, referencing `docs/process/code-review.md`)
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
CHECKS (per docs/process/code-review.md): interface:? determinism:? AC:? integration:? gate:? build:?   (each PASS|FAIL)
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
Apply `@docs/process/code-review.md`. Focus: (1) the cross-checks actually reject the bad cases with descriptive
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
Apply `@docs/process/code-review.md`. Focus: (1) **env-OFF neutrality** — the existing world scenario suite is
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
Apply `@docs/process/code-review.md`. Run the BROADER verify (config was touched too, and the implement report
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
Apply `@docs/process/code-review.md`. Verify `go test ./... -count=2`, `go vet ./...`, `gofmt -l`, `go build
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

## Phase 6 — Combat module mods (leaf-first, OFF-neutral; `docs/plans/fauna.md` 클러스터 9 / FC1–FC13)

**The predation/combat feature — built BEFORE activation so activation turns on a COMPLETE ecosystem once.**
Engage → exchange → kill → feed, across the built modules. **CRITICAL LEVER:** every sub-step ships with
species STILL UNPLACED (no combat animals installed) ⇒ existing **world scenario goldens stay
byte-identical**; only each module's OWN unit tests + the ecosim/scent goldens re-baseline where the mechanic
is exercised (deliberate). Do the sub-steps **leaf-first** (deps below). End-to-end predation ("wolf kills
deer → feeds → carcass rots → carrion") is verified at **Phase 7 activation**, not here. The 5 SPECs are
already reworked (each sub-step names its authoritative SPEC).

### 6a — scent: +ChanCarrion   `@backend/engine/space/scent/SPEC.md`
Goal: APPEND `ChanCarrion` after `ChanPredator` (NumChannels 3→4) + `Reading.Carrion` + Sense mapping.
Constraints: APPEND only — the existing 3 channels keep their index (no food/prey/predator churn); D12.
Done when: scent unit tests cover the 4th channel (deposit/spread/read); `go test ./engine/space/scent/ -count=2`.

### 6b — decay: +`State.WithLot`   `@backend/engine/env/decay/SPEC.md`
Goal: add `(s *State) WithLot(lot Lot) *State` — PURE runtime lot injection (carcass on death), keeps the
ObjectID-sorted invariant, panics on dup id. `New` stays init-only; `Step` signature unchanged.
Done when: unit test — WithLot inserts sorted + is pure (prev unchanged), then `Step` decays it like any lot;
`go test ./engine/env/decay/ -count=2`.

### 6c — fauna: combat core   `@backend/engine/fauna/SPEC.md` §Combat & Predation (+ 클러스터 9)
Goal: FC1–FC13 fauna side — SHARED actions `Attack`+`Feed` (utility-scored, no FSM, D3); `Animal` fields
`EngagedWith`/`NextExchangeTick`/`EngageCooldownUntil`/`VitalCap` (+serialize); operands `target.threat` +
`scent.carrion` in `AttrOperands()`; per-species `attack_power`/`hit` §6 (stat compositions, D4/D7, no stored
skill); engage(50–100)/exchange(10–20) cooldowns via seeded fork; disengage on predator stamina-drop OR
target beyond `disengage_range` (~2·cell); locomotion suppressed while engaged. Fauna only PROPOSES the
damage intent + engage state (world owns death, F3).
Constraints: no species content yet ⇒ module stays outcome-neutral. Predator↔predator ONLY via utility cost
(`target.threat`) — NO special-case rule (gate). import guard (no world/agent). Determinism throughout.
Done when: unit tests — utility picks Attack when a diet target is in range + hungry; engage/exchange/disengage
timing (seeded); `VitalCap` regen cap; operand cross-check; `go test ./engine/fauna/ -count=2`.

### 6d — config: combat content parse   `@backend/platform/config/SPEC-world.md` §Fauna combat content
Goal: parse combat content in THREE places — (a) per-species fauna combat §6 (`attack_power`/`hit`/`feed`)
into `fauna.Rules` (cross-check `ReadsAttrs ⊆ AttrOperands()∪drives`, incl. `target.threat`/`scent.carrion`);
(b) the scalar `CombatParams` (exchange/engage cooldown min/max, `disengage_range_factor`,
`stamina_drop_threshold`, `vital_regen_per_tick`, `vital_cap_damage_fraction`) into `EnvConfig.FaunaCombat`
— MIRROR how `FaunaCadence` is already parsed into `EnvConfig` (6c-fix added this surface); (c) the `carcass`
item's `decay:` states into `decay.Rules` (generic decay parse). `ScentEmitters` already handles
`scent:carrion`. ABSENT combat fields ⇒ neutral (current content loads, combat §6 = nil, FaunaCombat = zero).
Done when: config tests — combat §6 compile + cross-check, unknown operand rejected, `CombatParams` parsed
into `EnvConfig.FaunaCombat` (mirror the FaunaCadence test), carcass decay states loaded, current content
still loads neutral; `go test ./platform/config/ ... -count=2`.

### 6e — world: combat apply   `@backend/engine/world/SPEC-world-fauna.md` §Combat, death & carcass apply
Goal: `applyAnimalIntent` cross-animal damage (Attack → target Vital↓; engage state on BOTH, id-sorted);
death→carcass (spawn object + `decay.State.WithLot` lot, track in `decayLotPos`, feed `decayEnvInputs`);
`Feed`→hunger from carcass supply; `scentChannelFromTag` +`carrion` case; slow Vital regen toward `VitalCap`.
Constraints: all OFF-neutral when no combat animals installed. Determinism (combined sorted apply, seeded fork).
Done when: world unit tests — targeted damage, death→carcass+lot, Feed→hunger, carrion deposit, regen-cap;
`go test ./engine/world/ ./engine/ecosim/ -count=2` (ecosim/scent golden re-baseline ONLY from the new channel
width / carrion — verify it is that + call it out).

### 6f — content: combat data (species STILL unplaced)   `content/{actions,objects,balance}.yaml`
Goal: `actions.yaml` Attack+Feed (tags/uses/effect/duration); `objects.yaml` per-predator combat §6 + a
`carcass` item (`scent:carrion` + decay states + Butcher yields); `balance.yaml` vital_regen, injury/`VitalCap`
penalty, `disengage_range` (~2·cell), cooldown ranges, fear-on-witness. **Do NOT place species / install them
here** → world scenario goldens byte-unchanged.
Done when: `config.Load` parses it all; `go build ./... && go test ./... -count=2` green; world scenario
goldens byte-unchanged.

### Review (all 6a–6f)
Apply `@docs/process/code-review.md`. Per sub-step: (1) SPEC-faithful interface; (2) **OFF-neutrality** — existing
world scenario goldens byte-unchanged (only the sub-step's own + ecosim/scent goldens shift, deliberately);
(3) determinism `-count=2`; (4) **no invented mechanism** beyond 클러스터 9 (predator↔predator via cost only;
world owns death; APPEND-only channel); (5) tag-driven carrion (`scentChannelFromTag` single case + content
tag — no bespoke path). Return the `=== CODEX REVIEW ===` block.

---

## Phase 7 — activation: `worldgen.Load` + main wiring (env+fauna+combat go LIVE)

**SCOPE = ACTIVATION (with combat).** Turn the WI-P0/P1/P2 + Phase-6 combat wiring ON in a live run. The
combat mechanic (predator engages/kills prey → feeds → carcass rots → carrion) is now BUILT (Phase 6) and IS
exercised here — this is where end-to-end predation is verified. **Population respawn is still OUT of scope**
(a later phase); with predation + no respawn the population declines over a long run — that is EXPECTED, do
NOT "fix" it or add respawn (gate).

**Files:** `backend/tools/worldgen` (implement the run-time `Load` path per its SPEC) + `backend/main.go`
(call `worldgen.Load` instead of the ad-hoc `placeObjects` starter) + a starter fixture (reuse/extend
`backend/engine/world/testdata/fixtures/*.yaml` + `content/schema/fixture.schema.json`) + a headless
smoke test. Driven by `@backend/tools/worldgen/SPEC.md` + the WI-P1/P2 install signatures +
`@docs/plans/activation-gate.md` (G5/G4 ACCEPTED).

### Implement
- **Goal:** Make a live `main.go` run actually have weather, growing/dying flora, and moving/sensing
  fauna — by implementing `worldgen.Load(fixture, *config.LoadOutput) → constructed *world.World` and
  calling it from `main.go`. This closes **gap-A**: TODAY `InstallEnv`/`InstallFauna` have ZERO non-test
  callers and `main.go` builds no env states, so a live run silently takes the dormant/mock path.
- **Context:** the activation contract is `@backend/tools/worldgen/SPEC.md` ("Fixture → env states →
  `world.InstallEnv`/`InstallFauna` + Spawn/PlaceObject"). `config.Load` already returns the compiled
  Rules + cfgs: `WorldEnv (*world.EnvConfig)`, `NavCfg`, `TerrainTypes`, `ClimateCfg`, `ClimateRules`,
  `FloraRules`, `DecayRules`, `FaunaRules`, `ScentEmitters`. Constructors:
    - `navmap.New(cfg navmap.Config, terrainAt func(core.Vec2) navmap.TerrainID, types map[TerrainID]TerrainType)`
    - `climate.New(cfg climate.Config, terrainAt func(core.Vec2) navTerrainID)`
    - `flora.New(plants []flora.Plant)` ; `decay.New(lots []decay.Lot)` (start EMPTY)
    - the scent grid is created INSIDE `InstallFauna` — do NOT build it in worldgen.
  Install signatures: `w.InstallEnv(envCfg, nav, climate, ClimateRules, flora, FloraRules, decay,
  DecayRules)` then `w.InstallFauna(envCfg, FaunaRules, ScentEmitters, animals)`. `@docs/plans/activation-gate.md`
  placements (ACCEPTED): **deer×6, rabbit×8, goat×2, wolf×1, bear×1, fish×8**; climate band G4
  (`AnnualMid 12.5`/`AnnualAmp 17.5`).
- **Constraints:** Follow `AGENTS.md` + the gate.
  1. **worldgen.Load** — parse+validate the fixture; build the terrain layout into a `terrainAt` closure
     (a MINIMAL layout is fine: mostly grassland + a river strip + a mountain patch — just enough to
     exercise water/mountain terrain cost and the fish-in-river / goat-near-mountain placements);
     construct nav / climate / flora(initial plants from the fixture) / decay(empty). `envCfg` is simply
     `*LoadOutput.WorldEnv`. Call **InstallEnv FIRST, then InstallFauna** (accepted order). Also
     `PlaceObject` the initial flora as world objects (SPEC-world-env: flora plants are world objects, so
     agents/perception see them) and `Spawn` the agents.
  2. **main.go** — replace the ad-hoc `placeObjects(berry_bush/water_source/shelter)` starter with a
     `worldgen.Load` call over the starter fixture; keep the existing agent spawn if the fixture doesn't
     own agents. **Runtime generation = 0 (D12): main only LOADS a fixture, never generates** — do NOT
     implement the procedural WG1-a generator here (Load path only; a hand-authored starter fixture is
     fine).
  3. Do NOT touch the WorldFrame mock in `tick.go` (phase 8/9). Predation is ALREADY built (Phase 6) —
     place the combat-capable G5 so it happens; do NOT add RESPAWN (a later phase). Determinism: injected
     seeded rng throughout; fixture load is pure; no map-iteration for logic.
- **Done when:** a HEADLESS SMOKE TEST constructs the world via `worldgen.Load` and runs a few hundred
  ticks with NO panic, and ASSERTS the ecosystem is genuinely LIVE (not the dormant path): at least one
  animal moved, the flora world-object count changed (growth/death/propagation), climate temperature varied
  across a day, AND predation fired end-to-end (a predator engaged a prey → prey Vital dropped/died → a
  `carcass` appeared → `ChanCarrion` deposited). `go build ./...` + `go test ./... -count=2` green and stable
  across two runs. The report states that population decline (predation + no respawn yet) is expected. STOP
  and ask if terrain-layout or a placement choice is unclear beyond accepted G5/G4.

### Review
Apply `@docs/process/code-review.md` + focus:
(1) **gap-A closed** — `InstallEnv` AND `InstallFauna` now have a real non-test caller
    (`worldgen.Load` ← `main.go`); a live run takes the `faunaInstalled()==true` path, not the
    mock/dormant path.
(2) env-first-then-fauna order; `envCfg` = `*WorldEnv`; `ScentEmitters` threaded into `InstallFauna`;
    initial flora both in `flora.State` AND as world objects.
(3) the smoke test genuinely proves LIVE (asserts movement + flora-count change + temp variation + a
    predation event: kill → carcass → carrion), not just "no panic".
(4) predation FIRES end-to-end from the placed G5; **respawn NOT added** (later phase); WorldFrame mock
    untouched; no procedural generator (Load only). Gate respected.
(5) determinism across two fresh processes; runtime generation = 0.
Return the `=== CODEX REVIEW ===` block.

---

## Phase 8 — `platform/persist` + `platform/api` WI-P4 (env serialize + real WorldFrame)

**Files:** `backend/platform/persist/*` (serialize `animals[]`/`flora[]`/`climate` + deltas),
`backend/platform/api/*` (real `WorldFrame` SSE), and **DELETE** the mock in
`backend/engine/world/tick.go` + reconcile `events.go`. Driven by `@docs/core/data-contracts.md` §4/§10.

### Implement
- **Goal:** Serialize env state (periodic full + sparse deltas) and stream the REAL `WorldFrame` over
  SSE from live env state, replacing the temporary frontend mock.
- **Context:** `@docs/core/data-contracts.md` (§4 events, §10 env state), the live env state exposed by
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
Apply `@docs/process/code-review.md`. Focus: (1) no god-view field in any SSE payload; (2) resume byte-identical
(env state + rng_state round-trip); (3) the mock is fully removed and the real WorldFrame carries the
same shape the frontend reducer expects (`frontend/src/types.ts` `WorldFramePayload`); (4) env-OFF
neutrality.

---

## Phase 9 — `frontend` ecosystem rendering

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
Apply `@docs/process/code-review.md` (frontend-adapted). Focus: (1) env slices actually reach `WorldCanvas` and
render; (2) one shared transform (agents/animals/flora align); (3) no god-view; (4) env-OFF neutrality
(no WorldFrame ⇒ byte-same as today).

---

## Phase 10 — balance tuning + FA1–FA8 scenario tests (verify the desired scenarios)

**Files:** `backend/engine/world/scenario_*_test.go` (new FA scenario tests) + tuned `content/*.yaml`
(fauna/flora §6 coefficients, `balance.yaml`, G5 population targets) + re-baselined goldens. Driven by
`@docs/plans/scenarios-world.md` (FA1–FA8) + `@docs/plans/activation-gate.md`.

### Implement
- **Goal:** Tune the activated ecosystem so the headline emergent behaviors reliably occur, and lock
  them with deterministic scenario tests: deer grazes then flees on predator scent/sight; wolf
  scent-homes and the chase peters out when its fatigue rises ("predator tires first"); flora/fauna
  populations stay bounded (regen vs predation); wind-driven scent reaches downwind prey; cold climate
  slows movement (thermal→speed).
- **Context:** `@docs/plans/scenarios-world.md` (FA1–FA8), the existing `scenario_*_test.go` style in
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
Apply `@docs/process/code-review.md`. Focus: (1) behavior changes came from **content/§6**, not engine hacks
(diff the engine — it should be untouched, or any engine change is flagged + justified); (2) scenario
tests are deterministic + assert real emergent behavior (not tautologies); (3) goldens re-baselined
sanely.

---

## Notes carried over
- **fauna wall-slide (optional):** fauna steer DEAD-STOPS at a blocked cell (SPEC-conformant "stays");
  the SPEC also allows "slides". Animals can pin at boundaries (no pathfinding by design — local steer).
  If pinning hurts Phase-9 scenarios, add a SPEC-conformant wall-slide (project the steer velocity onto
  the unblocked tangent) to `engine/fauna/step.go` + a test — as a small, separate task.
- **Decisions:** `docs/plans/activation-gate.md` G1–G17 are ACCEPTED (recommendations). Flip each owning SPEC's
  Open-Question entry to RESOLVED as its phase lands.
