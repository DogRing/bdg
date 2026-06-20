// Package worldtime provides deterministic Tick ↔ game-time conversion.
//
// Every output is a pure integer-arithmetic function of (Config, Tick).
// No wall-clock, no floats, no mutable state (D12).
package worldtime

import (
	"errors"
	"fmt"

	"github.com/dogring/bdg/engine/core"
)

// Config carries the calendar constants. Loaded from content/balance.yaml (world.*)
// by platform/config and injected — NEVER hardcoded here (D10).
// All durations are in game-minutes (1 Tick = TickMinutes game-minutes; default 1).
type Config struct {
	TickMinutes    int64 // game-minutes advanced per Tick
	DayMinutes     int64 // game-minutes per day (default 1440 = 24 h × 60 m)
	DaysPerSeason  int64 // days per season (e.g. 30)
	SeasonsPerYear int64 // seasons per year (e.g. 4)
}

// DefaultConfig returns the canonical 12× calendar implied by the glossary
// (24 game-h = 2 real-h; 1 Tick = 1 game-minute; 1440 min/day). It exists for
// tests and headless runs; production injects Config from content/balance.yaml.
func DefaultConfig() Config {
	return Config{
		TickMinutes:    1,
		DayMinutes:     1440, // 24 h × 60 min
		DaysPerSeason:  30,
		SeasonsPerYear: 4,
	}
}

// Validate rejects a Config with non-positive fields (would make conversions ill-defined).
func (c Config) Validate() error {
	if c.TickMinutes <= 0 {
		return errors.New("worldtime: Config.TickMinutes must be positive")
	}
	if c.DayMinutes <= 0 {
		return errors.New("worldtime: Config.DayMinutes must be positive")
	}
	if c.DaysPerSeason <= 0 {
		return errors.New("worldtime: Config.DaysPerSeason must be positive")
	}
	if c.SeasonsPerYear <= 0 {
		return errors.New("worldtime: Config.SeasonsPerYear must be positive")
	}
	return nil
}

// Clock interprets a Tick against a fixed Config. Immutable value type; safe to copy.
type Clock struct {
	cfg Config
}

// NewClock builds a Clock from cfg. Returns an error if cfg.Validate() fails.
func NewClock(cfg Config) (Clock, error) {
	if err := cfg.Validate(); err != nil {
		return Clock{}, fmt.Errorf("worldtime: %w", err)
	}
	return Clock{cfg: cfg}, nil
}

// Minutes returns the absolute game-minute count for t (t * TickMinutes).
func (c Clock) Minutes(t core.Tick) core.GameMinutes {
	return core.GameMinutes(int64(t) * c.cfg.TickMinutes)
}

// MinuteOfDay returns the game-minute within the current day, in [0, DayMinutes).
func (c Clock) MinuteOfDay(t core.Tick) int64 {
	gm := int64(c.Minutes(t))
	return gm % c.cfg.DayMinutes
}

// HourOfDay returns the hour within the current day, in [0, 24).
func (c Clock) HourOfDay(t core.Tick) int {
	return int(c.MinuteOfDay(t) / 60)
}

// DayOfRun returns the 0-based day index since run start.
func (c Clock) DayOfRun(t core.Tick) int64 {
	gm := int64(c.Minutes(t))
	return gm / c.cfg.DayMinutes
}

// Season returns the 0-based season index within the year, in [0, SeasonsPerYear).
func (c Clock) Season(t core.Tick) int {
	day := c.DayOfRun(t)
	seasonLen := c.cfg.DaysPerSeason
	return int((day / seasonLen) % c.cfg.SeasonsPerYear)
}

// Year returns the 0-based year index since run start.
func (c Clock) Year(t core.Tick) int64 {
	day := c.DayOfRun(t)
	daysPerYear := c.cfg.DaysPerSeason * c.cfg.SeasonsPerYear
	return day / daysPerYear
}

// Calendar bundles all derived fields for a Tick (one struct, for logging / events / render).
type Calendar struct {
	Minute    core.GameMinutes // absolute game-minute count
	HourOfDay int              // [0,24)
	DayOfRun  int64            // 0-based
	Season    int              // [0, SeasonsPerYear)
	Year      int64            // 0-based
}

// At returns the full Calendar for t (one call instead of several accessors).
func (c Clock) At(t core.Tick) Calendar {
	return Calendar{
		Minute:    c.Minutes(t),
		HourOfDay: c.HourOfDay(t),
		DayOfRun:  c.DayOfRun(t),
		Season:    c.Season(t),
		Year:      c.Year(t),
	}
}

// TicksForMinutes converts a game-minute duration to a Tick count (floor division).
// Used by Action.Duration and the planner's forward-sim horizon.
func (c Clock) TicksForMinutes(m core.GameMinutes) core.Tick {
	return core.Tick(int64(m) / c.cfg.TickMinutes)
}
