# SPEC — `engine/world` · Mine — Terrain-Driven Extraction Path (resources R1)

> Status: `DRAFT`
> Sub-spec of: `SPEC.md`  ·  Owner agent: `<filled by implementer>`
> Scope = **WI-P3** (`docs/plans/world-integration.md` §2 / `docs/plans/resources.md` R1). EXTENDS the existing
> `ore_node` Mine path (materials P_m4: `backend/engine/mind/actions/SPEC.md` + `docs/core/data-contracts.md §9`).

## Scope

Adds the **terrain-driven extraction path** to the `Mine` apply (resources R1): abundant materials
(`stone` anywhere, `clay` on soil) are mined **from the ground itself** — no finite `ore_node` object
— while the scarce metals (coal/copper/iron/gold) keep the existing **finite-node path** (P_m4 /
Xm1). Both paths are the SAME `Mine` action, the SAME `tool:digging` gate, and the SAME §6 yield
mechanism (`§6(Dexterity, tool quality)`, Xm5 / flora-yield reuse). The difference is the apply-time
**target resolution** and the yield SOURCE.

This is a **world apply** addition (the world is the sole object mutator); the `Mine` `ActionDef`
(actions SPEC) is unchanged. It depends on WI-P0 `content/terrain.yaml` (the `extract` yield table
per terrain type) + WI-P1 navmap (the terrain at a position).

## Target resolution (node OR terrain-cell, R1)

`Mine`'s apply resolves its target deterministically:
1. **Node path (existing, P_m4):** if the bound target is an `ore_node` object (or one is present at
   the target), extract from it — roll its `harvest.yields`, decrement `remaining`, on `remaining→0`
   remove the node + ONE `navmap.SetTerrain` → `source.depleted_terrain` (Xm2/Xm3). UNCHANGED here.
2. **Terrain-cell path (NEW, R1):** otherwise, extract from the **terrain cell at the actor's
   position** — read `nav.TerrainAt(nav.CellOf(actor.Pos))`, look up that terrain type's
   `extract.yields` (`content/terrain.yaml`); if the terrain has an `extract` block, roll it. If the
   terrain has **no** `extract`, the Mine fails (nothing to extract — e.g. river/sea).

The `tool:digging` action-tag gate (Xm5) applies to BOTH paths; both wear the held digging tool by
the Mine world/balance rate (FINAL durability path — no per-item `wear_per_use`).

## Terrain-cell apply (the new path)

In the serial apply phase, for a Mine intent resolving to the terrain-cell path (world = sole mutator,
fixed agent-ID order, D12):
```
cell    = nav.CellOf(actor.Pos)
terrain = nav.TerrainAt(cell)
ex      = terrainTypes[terrain].Extract        // from content/terrain.yaml (WI-P0 join); nil ⇒ Mine fails
for each yield y in ex.Yields (authored order, D12):
    chance = expr.EvalNumber(y.Chance, ctx{ Stat: actor.RealStats, Attr: tool quality })  // §6(Dexterity, tool quality)
    if fork.Float() < chance:                  // per-agent fork (SPEC-tick.md), D12
        qty = rollQty(y.QtyMin, y.QtyMax, fork) // base [min,max]
        place qty × y.Item into the actor's inventory (a fresh decay lot if y.Item is perishable, §8)
wear the held tool:digging instance by the Mine wear rate (durability ↓, break@0 → ToolBroke, §9)
emit Mined{ ... terrain path: no ore_node id, no remaining/depleted } (data-contracts §4)
```
- **NO finite counter, NO `SetTerrain` for the abundant path** — `stone` is effectively infinite
  (고갈≈0, R1); `clay` is bound to `soil` presence (it is only mined where the terrain is soil, because
  only soil's `extract` lists clay). There is no per-cell remaining count and no depletion transform in
  P1 (finite extraction = the ore_node path; terrain depletion-to-`SetTerrain` is a parked frontier,
  Q4 — Open Questions).
- **§6 yield reuse (Xm5):** `chance` is the terrain `extract.yields[].chance` §6 over the actor's
  Dexterity + the held tool quality (`tool:<family>.quality`, Cm3) — capability composition, not a
  stored skill (D7). `qty` is the base `[min,max]` (no length scaling — that is flora's).
- **Determinism (D12):** the roll uses the acting agent's per-tick fork (SPEC-tick.md `forkRNG`),
  yields evaluated in authored order; same `(seed, tick, actor)` ⇒ identical extraction. Mine apply
  runs in the combined sorted-ObjectID apply order with every other intent.

## Acceptance Criteria (testable)
- [ ] **Node path unchanged** — a Mine targeting an `ore_node` behaves exactly as P_m4 (rolls
  `harvest.yields`, decrements `remaining`, `remaining→0` → remove + `SetTerrain` to
  `depleted_terrain`); existing P_m4 goldens hold (regression guard).
- [ ] **Terrain-cell path yields from terrain `extract`** — a Mine with no ore_node target, standing
  on `soil`, rolls soil's `extract.yields` (clay + stone) via `§6(Dexterity, tool quality)` and the
  per-agent fork; on `mountain`/`sand`/`bare_rock` it yields stone; placed into the actor's inventory.
  Table-driven over terrains.
- [ ] **No-extract terrain fails** — a Mine on `river`/`sea` (no `extract` block) produces nothing
  (the action fails/zero-yield), never panics.
- [ ] **`tool:digging` gate + wear on both paths** — Mine requires the `tool:digging` tag (Xm5) and
  wears the held digging tool by the Mine rate on a successful terrain-cell extraction too; at 0
  durability the tool breaks (`ToolBroke`, §9).
- [ ] **Abundant = no depletion** — repeated terrain-cell Mine on the same `soil`/`mountain` cell keeps
  yielding (no `remaining`, no `SetTerrain`); the cell's terrain type is unchanged (contrast the node
  path). `stone` never exhausts.
- [ ] **Determinism** — same `(seed, tick, actor, terrain)` ⇒ byte-identical extracted items; the roll
  draws ONLY from the per-agent fork; yields evaluated in authored order (D12).
- [ ] **Combined apply order** — a terrain Mine applies in the one sorted-ObjectID intent stream
  (SPEC-tick.md); two agents mining adjacent cells do not interfere (independent cells, no conflict).

## Out of Scope
- **The `ore_node` finite-node path** (yield/`remaining`/`SetTerrain` on depletion) — existing P_m4
  (`docs/core/data-contracts.md §9`, `backend/engine/mind/actions/SPEC.md`); this sub-spec only ADDS the
  terrain-cell branch + references the node path.
- **The `Mine` `ActionDef`** (tags, `tool:digging`, target kind) → `engine/mind/actions` +
  `content/actions.yaml` (unchanged; the terrain path is an apply-time resolution, not a new action).
- **`content/terrain.yaml` `extract` blocks + `terrain.schema.json` + the §6 chance compile + the
  TerrainID→TerrainType join** → `content` + `platform/config` (WI-P0; this sub-spec consumes the
  built `terrainTypes[terrain].Extract`).
- **The held-tool quality operand** (`tool:<family>.quality`, Cm3) — the world Context operand
  (materials FINAL); this sub-spec uses it in the chance §6, it does not define it.
- **Terrain depletion-to-`SetTerrain` for the abundant path** (Q4 finite terrain extraction) — PARKED
  frontier (R1 rec: stone 고갈≈0, no depletion in P1). Finite extraction = the ore_node path only.
- **Smelting / tool chains from the mined materials** → `content/recipes.yaml` (resources R6).

## Open Questions
> `docs/plans/resources.md` R1 + `docs/plans/world-integration.md` W1-W9 RESOLVED. Two plumbing seams:
- **Planner binding for the terrain path (planner/actions seam) — ✅RESOLVED (a), 2026-06-28: world apply falls back to actor cell; planner offers Mine where the terrain has `extract`.** The node path binds an `ore_node`
  target; the terrain path extracts at the actor's standing cell (no object target). For the planner to
  select Mine to satisfy `has_materials` WHERE no node exists (stone anywhere), Mine must be selectable
  without an `ore_node` target — i.e. the planner treats Mine as available when the actor's (reachable)
  terrain has an `extract` table. Options: **(a)** world-apply fallback to the actor cell when Mine has
  no node target + the planner offers Mine generically (it produces `has_materials` anywhere mineable);
  **(b)** a distinct `MineGround` action for the terrain path. **rec: (a)** — one `Mine`, two apply
  resolutions (R1 "same Mine"); the planner availability over terrain `extract` is the binding nuance.
  **Flag to the planner/actions owner.**
- **Tool-quality operand presence (Cm3).** The chance §6 references the held tool quality
  (`tool:<family>.quality`); confirm the world Context exposes it for the Mine apply (materials FINAL
  Cm3) so `content/terrain.yaml extract.chance` can use it. Non-blocking; a config cross-check.

## Notes
- The two Mine paths are deliberately one action: scarcity is a CONTENT property (a scarce metal is a
  finite `ore_node` placed on mountain by world-gen; an abundant material is a terrain `extract` table),
  not a separate mechanism — tier emerges from recipe-input availability (resources §0-8, D2/D3).
- Terrain-driven Mine + the §6 yield is the same shape as flora `Yield` and the node `harvest.yields`
  (Xm5 reuse) — one seeded-roll yield mechanism across flora/ground/node, keeping goldens legible.
- Reference paths: `docs/plans/resources.md` R1/R7 (the resolution), `docs/plans/materials.md` P_m4/Xm1-6 (the node
  path), `content/terrain.yaml` + `content/schema/terrain.schema.json` (the `extract` table),
  `docs/core/data-contracts.md §9` (node `remaining`/`SetTerrain`) + §4 (`Mined` event),
  `backend/engine/mind/actions/SPEC.md` (`Mine` def), `backend/engine/space/navmap/SPEC.md`
  (`TerrainAt`/`CellOf`), `SPEC-tick.md` (per-agent fork + combined apply order).
