# SPEC — `engine/kernel/worldtime`

> Status: `READY`
> Owner agent: `implementer`
> Leaf level: `L1`  ·  Owner agent: `<filled by implementer>`

## Purpose

Pure, deterministic conversion between the engine's `Tick` counter and human-readable
game-time fields (game-minute, hour, day, season, year). Holds **no wall-clock dependency**
(no `time.Now()`, no `time` package for logic) — every value is a closed-form function of
the integer `Tick` and the injected calendar configuration. The simulation clock is just
the monotone `Tick` count advanced by `engine/world`; this module only *interprets* it.

## Public Interface

```go
package worldtime

import "github.com/dogring/bdg/engine/kernel/core"

// Config carries the calendar constants. Loaded from content/balance.yaml (world.*)
// by platform/config and injected — NEVER hardcoded here (D10).
// All durations are in game-minutes (1 Tick = TickMinutes game-minutes; default 1).
type Config struct {
    TickMinutes  int64 // game-minutes advanced per Tick           (balance.yaml world.tick_minutes)
    DayMinutes   int64 // game-minutes per day                     (balance.yaml world.day_minutes, 1440)
    DaysPerSeason int64 // days per season                         (calendar; e.g. 30)
    SeasonsPerYear int64 // seasons per year                       (calendar; e.g. 4)
}

// DefaultConfig returns the canonical 12× calendar implied by the glossary
// (24 game-h = 2 real-h; 1 Tick = 1 game-minute; 1440 min/day). It exists for
// tests and headless runs; production injects Config from content/balance.yaml.
func DefaultConfig() Config

// Validate rejects a Config with non-positive fields (would make conversions ill-defined).
func (c Config) Validate() error

// Clock interprets a Tick against a fixed Config. Immutable value type; safe to copy.
type Clock struct{ /* opaque: holds a validated Config */ }

// NewClock builds a Clock from cfg. Returns an error if cfg.Validate() fails.
func NewClock(cfg Config) (Clock, error)

// ── Tick → game-time (all pure, total functions of t) ────────────────────────

// Minutes returns the absolute game-minute count for t (t * TickMinutes).
func (c Clock) Minutes(t core.Tick) core.GameMinutes

// MinuteOfDay returns the game-minute within the current day, in [0, DayMinutes).
func (c Clock) MinuteOfDay(t core.Tick) int64

// HourOfDay returns the hour within the current day, in [0, 24).
func (c Clock) HourOfDay(t core.Tick) int

// DayOfRun returns the 0-based day index since run start.
func (c Clock) DayOfRun(t core.Tick) int64

// Season returns the 0-based season index within the year, in [0, SeasonsPerYear).
func (c Clock) Season(t core.Tick) int

// Year returns the 0-based year index since run start.
func (c Clock) Year(t core.Tick) int64

// Calendar bundles all derived fields for a Tick (one struct, for logging / events / render).
type Calendar struct {
    Minute  core.GameMinutes // absolute game-minute count
    HourOfDay int            // [0,24)
    DayOfRun  int64          // 0-based
    Season    int            // [0, SeasonsPerYear)
    Year      int64          // 0-based
}

// At returns the full Calendar for t (one call instead of several accessors).
func (c Clock) At(t core.Tick) Calendar

// ── Duration helpers (game-minute ↔ Tick) ────────────────────────────────────

// TicksForMinutes converts a game-minute duration to a Tick count (floor division).
// Used by Action.Duration and the planner's forward-sim horizon.
func (c Clock) TicksForMinutes(m core.GameMinutes) core.Tick
```

> `core.Tick` and `core.GameMinutes` are both `int64` types defined in `engine/kernel/core` (the
> `core` SPEC now declares both; see `backend/engine/kernel/core/SPEC.md`). `core` fixes only the
> types — this module owns the **ratio** between them (content-driven via `Config.TickMinutes`,
> from `content/balance.yaml world.tick_minutes`, D10).

## Dependencies

- `engine/kernel/core` — `Tick`, `GameMinutes` (the only imports; stdlib `errors`/`fmt` for `Validate`).

## Owned Data

- `Config` and `Clock` value types only. No mutable package-level state, no clock singleton.
  The authoritative monotone `Tick` is owned by `engine/world`; this module never stores it.
- The **Tick↔GameMinutes ratio** is conceptually owned here (via `Config.TickMinutes`); `core`
  only declares the two types.

## Invariants

- **No wall-clock**: the `time` package is never imported for logic. No `time.Now()`,
  no `time.Since`, no OS clock. Every output is a deterministic function of `(Config, Tick)`
  (D12). A `grep` static check guards the absence of `time` import.
- **Closed-form & total**: every accessor is pure integer arithmetic on `t`; no allocation,
  no `map` iteration, no goroutines, no panics for any `core.Tick` value `t ≥ 0`.
- **Monotone**: for `t1 < t2`, `Minutes(t1) ≤ Minutes(t2)`; the clock never runs backward.
- **Determinism of conversion**: same `(Config, t)` → byte-identical `Calendar` on every call
  and every platform (no float for time math — integer division only).
- **Config is content-driven**: calendar constants come from `content/balance.yaml` via
  `platform/config`; nothing in this package hardcodes 1440, 12, season counts, etc. for
  *logic* (DefaultConfig holds them only as a test/headless convenience, D10).
- **Bounds**: `MinuteOfDay ∈ [0,DayMinutes)`, `HourOfDay ∈ [0,24)`, `Season ∈ [0,SeasonsPerYear)`
  for all valid `t` — enforced by modular arithmetic.

## Acceptance Criteria (testable)

- [ ] `DefaultConfig()` satisfies `Validate()` and yields `TickMinutes=1, DayMinutes=1440`
  (matches `content/balance.yaml` world.tick_minutes / world.day_minutes).
- [ ] `Validate()` rejects every field ≤ 0 (table-driven: zero and negative for each field).
- [ ] `Minutes/MinuteOfDay/HourOfDay/DayOfRun/Season/Year` table test against hand-computed
  values across day boundaries, season boundaries, and a year rollover (e.g. `t=0`, end-of-day,
  start-of-day, last tick of a season, first tick of next year).
- [ ] `HourOfDay(t) ∈ [0,24)` and `MinuteOfDay(t) ∈ [0,DayMinutes)` for a sweep of `t`
  (0 … 3×year) — property test.
- [ ] `At(t)` returns fields equal to the individual accessors for the same `t` (cross-check).
- [ ] Monotonicity: `Minutes` is non-decreasing over an increasing `t` sweep.
- [ ] `TicksForMinutes(c.Minutes(t)) == t` when `TickMinutes` divides evenly (round-trip);
  documented floor behaviour otherwise.
- [ ] Static check: no `time` package import (grep in test file) — guards D12.
- [ ] Determinism: 10 000 repeated `At(t)` calls for fixed `t` are byte-identical.

## Out of Scope

- Advancing the `Tick` counter and the tick loop ordering → `engine/world`.
- Seasonal effects on resources / needs (e.g. regrowth, scarcity) → `content/balance.yaml`
  + the consuming module (`engine/mind/needs`, resource regen in `engine/world`).
- Real-time pacing / sleep-between-ticks for live playback → `platform` (never the engine).
- Persisting the current tick → `platform/persist` + Redis `sim:{run}:tick` (data-contracts §2).

## Open Questions

- **Calendar granularity (season/year)**: the design docs fix the 12× day scale but do not
  specify `DaysPerSeason` / `SeasonsPerYear`. Proposed defaults: 30 days/season, 4 seasons/year.
  These belong in `content/balance.yaml world.*` (D10) — needs a human/architect decision and a
  matching `balance.schema.json` addition. Does **not** block P1 (P0 only needs `Minute/Hour/Day`).

> Resolved: `core.GameMinutes` (and the `Tick` ownership question) — `engine/kernel/core` now declares
> both `Tick` and `GameMinutes`; the ratio is worldtime-owned (`Config.TickMinutes`). No longer a
> blocker.

## Notes

- Keep all time math in **integer** game-minutes; never use float for calendar fields
  (float drift would break byte-determinism across platforms).
- The 12× "real-time scale" (`balance.yaml world.real_scale`) is a *playback* concern for
  `platform`, not the engine; it is intentionally **not** part of this module's interface.
- Units convention: glossary §"World & time" — `GameMinutes / Tick`, "24 game-h = 2 real-h
  (12×). Default tick = 1 game-minute." All durations in `content/balance.yaml` are game-minutes.
- The snapshot stores the raw integer `tick` (data-contracts §1 `tick`); the `Calendar` is
  always re-derivable from it, so it is never serialized.
