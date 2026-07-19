package world

import (
	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/actions"
)

// ── Env state digests (WI-P4, data-contracts §1/§10) ───────────────────────────
// The serializable env blocks WorldState carries (state.go) — capture in State(),
// restore in RestoreState(). Split out of state.go per the ~400-line rule.

// floraDigest is one live plant's flora-owned dynamic state (data-contracts §1/§10):
// {object_id, species, pos, length, width, death_streak}. Stage is DERIVED from
// Length (D9) — not stored. `Owner` (planted flora, inert until economy ships,
// engine/env/flora/SPEC.md 1f) is not yet part of the data-contracts shape and is
// not round-tripped — a known, currently-inconsequential gap (Owner is unused P1).
type floraDigest struct {
	ObjectID    core.ObjectID   `json:"object_id"`
	Species     flora.SpeciesID `json:"species"`
	Pos         core.Vec2       `json:"pos"`
	Length      float64         `json:"length"`
	Width       float64         `json:"width"`
	DeathStreak int             `json:"death_streak"`
}

// animalStateDigest is one live animal's fauna-owned dynamic state (data-contracts
// §1/§10 base shape: object_id, species, pos, stats, drives, stamina, vital,
// heading, current_action, active_until) EXTENDED with the Phase-6 combat/hiding
// fields (VitalCap/EngagedWith/NextExchangeTick/EngageCooldownUntil/HiddenUntil/
// Concealment) — round-tripping them is required for the D12 resume invariant (a
// snapshot captured mid-engagement or mid-hide must resume with that state
// intact, not silently reset to zero). data-contracts §1/§10 list these fields.
type animalStateDigest struct {
	ObjectID            core.ObjectID             `json:"object_id"`
	Species             fauna.SpeciesID           `json:"species"`
	Pos                 core.Vec2                 `json:"pos"`
	Stats               map[core.StatID]float64   `json:"stats,omitempty"`
	Drives              map[fauna.DriveID]float64 `json:"drives,omitempty"`
	Stamina             float64                   `json:"stamina"`
	Vital               float64                   `json:"vital"`
	VitalCap            float64                   `json:"vital_cap"`
	Heading             float64                   `json:"heading"`
	CurrentAction       string                    `json:"current_action"`
	ActiveUntil         core.Tick                 `json:"active_until"`
	EngagedWith         core.ObjectID             `json:"engaged_with,omitempty"`
	NextExchangeTick    core.Tick                 `json:"next_exchange_tick,omitempty"`
	EngageCooldownUntil core.Tick                 `json:"engage_cooldown_until,omitempty"`
	HiddenUntil         core.Tick                 `json:"hidden_until,omitempty"`
	Concealment         float64                   `json:"concealment,omitempty"`
}

// climateDigest is the coarse climate field (data-contracts §1/§10): the per-cell
// grid (periodic-full, sorted Y-major then X), the rain-process accumulator, and
// the world-uniform wind. `Terrain` per cell IS serialized: CellState.Terrain is
// climate's OWN authoritative input to Rules.Eval on every subsequent Step
// (transition source state, engine/env/climate/SPEC.md), not merely a navmap
// mirror — omitting it would silently stop terrain transitions after a resume
// (data-contracts §10).
type climateDigest struct {
	Cells []climateCellDigest `json:"cells"`
	Rain  climateRainDigest   `json:"rain"`
	Wind  climateWindDigest   `json:"wind"`
	// SnowCover is the world-uniform snowpack ∈ [0,1] (CS2b). Omitted on a snapshot from before the
	// snow feature ⇒ 0 (a snowless resume), matching the pre-feature behaviour.
	SnowCover float64 `json:"snow_cover"`
}

type climateCellDigest struct {
	Cell        climateGridCellDigest `json:"cell"`
	Moisture    float64               `json:"moisture"`
	Temperature float64               `json:"temperature"`
	Terrain     core.Tag              `json:"terrain"`
	FrozenFrom  core.Tag              `json:"frozen_from"`
}

type climateGridCellDigest struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type climateRainDigest struct {
	Raining        bool    `json:"raining"`
	RainEndsAtHour int64   `json:"rain_ends_at_hour"`
	PRain          float64 `json:"p_rain"`
	HoursSinceRain int64   `json:"hours_since_rain"`
}

type climateWindDigest struct {
	Dir float64 `json:"dir"`
	Mag float64 `json:"mag"`
}

// ── Capture (called from State()) ──────────────────────────────────────────────

// captureEnvState fills ws's env blocks (WI-P4). Each block stays absent/empty
// when its subsystem is not installed — env-off snapshots are byte-unchanged.
func (w *World) captureEnvState(ws *WorldState) {
	if w.floraState != nil {
		plants := w.floraState.Plants() // sorted ObjectID (D12)
		ws.Flora = make([]floraDigest, 0, len(plants))
		for _, p := range plants {
			ws.Flora = append(ws.Flora, floraDigest{
				ObjectID: p.ID, Species: p.Species, Pos: p.Pos,
				Length: p.Length, Width: p.Width, DeathStreak: p.DeathStreak,
			})
		}
	}

	if len(w.animalIDs) > 0 {
		ws.Animals = make([]animalStateDigest, 0, len(w.animalIDs))
		for _, id := range w.animalIDs { // sorted ObjectID (D12)
			a := w.animals[id]
			if a == nil {
				continue
			}
			ws.Animals = append(ws.Animals, animalStateDigest{
				ObjectID: a.ID, Species: a.Species, Pos: a.Pos,
				Stats: cloneStatFloatMap(a.Stats), Drives: cloneFaunaDrives(a.Drives),
				Stamina: a.Stamina, Vital: a.Vital, VitalCap: a.VitalCap,
				Heading: a.Heading, CurrentAction: string(a.CurrentAction),
				ActiveUntil: a.ActiveUntil, EngagedWith: a.EngagedWith,
				NextExchangeTick: a.NextExchangeTick, EngageCooldownUntil: a.EngageCooldownUntil,
				HiddenUntil: a.HiddenUntil, Concealment: a.Concealment,
			})
		}
	}

	if w.climateState != nil {
		cells := w.climateState.Cells() // sorted Y-major then X (D12)
		cd := make([]climateCellDigest, 0, len(cells))
		for _, gcs := range cells {
			cd = append(cd, climateCellDigest{
				Cell:        climateGridCellDigest{X: gcs.Cell.X, Y: gcs.Cell.Y},
				Moisture:    gcs.State.Moisture,
				Temperature: gcs.State.Temperature,
				Terrain:     gcs.State.Terrain,
				FrozenFrom:  gcs.State.FrozenFrom,
			})
		}
		rain := w.climateState.Rain()
		wind := w.climateState.Wind()
		ws.Climate = &climateDigest{
			Cells: cd,
			Rain: climateRainDigest{
				Raining: rain.Raining, RainEndsAtHour: rain.RainEndsAtHour,
				PRain: rain.PRain, HoursSinceRain: rain.HoursSinceRain,
			},
			Wind:      climateWindDigest{Dir: wind.Dir, Mag: wind.Mag},
			SnowCover: w.climateState.SnowCover(),
		}
	}
}

// ── Restore (called from RestoreState()) ───────────────────────────────────────

// restoreFlora reconstructs w.floraState from its captured digest. A nil
// w.floraState (flora not installed on this world instance) is left untouched —
// env-off neutrality mirrors the climate/animal restores below.
func (w *World) restoreFlora(digest []floraDigest) {
	if w.floraState == nil {
		return
	}
	plants := make([]flora.Plant, 0, len(digest))
	for _, d := range digest {
		plants = append(plants, flora.Plant{
			ID: d.ObjectID, Species: d.Species, Pos: d.Pos,
			Length: d.Length, Width: d.Width, DeathStreak: d.DeathStreak,
		})
	}
	w.floraState = flora.New(plants)
}

// restoreAnimals reconstructs w.animals/w.animalIDs from the captured digest.
// Gated on w.scent (InstallFauna's unconditional marker — see faunaInstalled)
// rather than w.animals itself, since an empty pre-restore animal set is a
// valid installed-but-currently-empty state, not "fauna not installed".
func (w *World) restoreAnimals(digest []animalStateDigest) {
	if w.scent == nil {
		return
	}
	w.animals = make(map[core.ObjectID]*fauna.Animal, len(digest))
	w.animalIDs = make([]core.ObjectID, 0, len(digest))
	for _, d := range digest {
		a := fauna.Animal{
			ID: d.ObjectID, Species: d.Species, Pos: d.Pos,
			Stats: cloneStatFloatMap(d.Stats), Drives: cloneFaunaDrives(d.Drives),
			Stamina: d.Stamina, Vital: d.Vital, VitalCap: d.VitalCap,
			Heading: d.Heading, CurrentAction: actions.ActionID(d.CurrentAction),
			ActiveUntil: d.ActiveUntil, EngagedWith: d.EngagedWith,
			NextExchangeTick: d.NextExchangeTick, EngageCooldownUntil: d.EngageCooldownUntil,
			HiddenUntil: d.HiddenUntil, Concealment: d.Concealment,
		}
		w.animals[a.ID] = &a
		w.animalIDs = append(w.animalIDs, a.ID)
	}
	sortObjectIDs(w.animalIDs) // digest is already sorted; explicit for D12 clarity
}

// restoreClimate reconstructs w.climateState via climate.Restore, reusing the
// ALREADY-installed w.climateState's Config (geometry + rates — not itself part
// of the serialized snapshot; the caller is assumed to have called InstallEnv
// with the same content-derived Config/Rules as the captured run).
func (w *World) restoreClimate(digest *climateDigest) {
	if w.climateState == nil || digest == nil {
		return
	}
	cells := make([]climate.GridCellState, 0, len(digest.Cells))
	for _, c := range digest.Cells {
		cells = append(cells, climate.GridCellState{
			Cell: climate.GridCell{X: c.Cell.X, Y: c.Cell.Y},
			State: climate.CellState{
				Moisture: c.Moisture, Temperature: c.Temperature, Terrain: c.Terrain,
				FrozenFrom: c.FrozenFrom,
			},
		})
	}
	rain := climate.RainProcess{
		Raining: digest.Rain.Raining, RainEndsAtHour: digest.Rain.RainEndsAtHour,
		PRain: digest.Rain.PRain, HoursSinceRain: digest.Rain.HoursSinceRain,
	}
	wind := climate.Wind{Dir: digest.Wind.Dir, Mag: digest.Wind.Mag}
	w.climateState = climate.Restore(w.climateState, cells, rain, wind, digest.SnowCover)
}

// rebuildScent replays ONE deposit(+conditional spread)+commit cycle against the
// just-restored animal/flora/object positions, reproducing exactly what the
// ORIGINAL run's LAST tick (finalTick-1, the pre-increment value runScentEnv saw
// during that tick's body — see tick.go) would have committed. The scent grid is
// derived, never serialized (data-contracts §10): its committed buffer at any
// tick is a pure function of that tick's emitter positions + wind (runScentEnv
// rebuilds pending from scratch every cadence — see engine/world/fauna.go), so
// this reproduces it exactly, not merely approximately. w.tick is temporarily
// set to the pre-increment tick so HiddenUntil-vs-tick checks inside the reused
// deposit helpers match the original run, then restored.
func (w *World) rebuildScent(finalTick core.Tick) {
	if w.scent == nil || finalTick <= 0 {
		return
	}
	saved := w.tick
	w.tick = finalTick - 1
	for _, id := range w.animalIDs {
		if a := w.animals[id]; a != nil {
			w.depositAnimalScent(a)
		}
	}
	if w.envCfg.ScentSpread > 0 && int64(w.tick)%int64(w.envCfg.ScentSpread) == 0 {
		w.depositFloraScent()
		w.depositObjectScent()
		w.scent.Spread(w.scentWind())
	}
	w.scent.Commit()
	w.tick = saved
}
