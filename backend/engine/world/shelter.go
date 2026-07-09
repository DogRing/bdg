package world

import (
	"math"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/exposure"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/engine/space/scent"
)

// navTopo adapts navmap's hex geometry to exposure.Topology (docs/shelter.md SH1). exposure is a pure
// leaf that knows no geometry, so this adapter owns the alignment: it reorders navmap's canonical
// Neighbors into exposure.SectorOf's six wind-direction bins, so Neighbors(c)[s] is the downwind
// neighbor for wind sector s. The lattice is uniform, so the sector→neighbor permutation is
// cell-independent and computed once from a reference cell.
type navTopo struct {
	nav       *navmap.NavMap
	sectorIdx [exposure.NumSectors]int // wind sector → index into navmap.Neighbors()
}

func newNavTopo(nav *navmap.NavMap) *navTopo {
	t := &navTopo{nav: nav}
	origin := navmap.Cell{Q: 0, R: 0}
	oc := nav.CellCenter(origin)
	for i, nb := range nav.Neighbors(origin) {
		c := nav.CellCenter(nb)
		bearing := math.Atan2(c.Y-oc.Y, c.X-oc.X)
		t.sectorIdx[exposure.SectorOf(exposure.Wind{Dir: bearing})] = i
	}
	return t
}

func (t *navTopo) Neighbors(c exposure.Cell) [exposure.NumSectors]exposure.Cell {
	nb := t.nav.Neighbors(navmap.Cell{Q: c.Q, R: c.R})
	var out [exposure.NumSectors]exposure.Cell
	for s := 0; s < exposure.NumSectors; s++ {
		n := nb[t.sectorIdx[s]]
		out[s] = exposure.Cell{Q: n.Q, R: n.R}
	}
	return out
}

func (t *navTopo) InBounds(c exposure.Cell) bool {
	return t.nav.InBounds(navmap.Cell{Q: c.Q, R: c.R})
}

// InstallShelter enables the exposure layer: SH1 local wind = global wind × ε_wind(cell) from the
// `blocks_wind` blockers over a 6-sector cache, and SH3 overhead cover ε_cover(cell) from the `covers`
// coverers (docs/shelter.md, SPEC-world-shelter.md). Without this call the world is shelter-OFF (ε ≡ 1,
// local wind == global wind, sensed temp/moisture unchanged, byte-identical). nil coverers ⇒ SH3 OFF
// only (wind still applies). Cave interiors are SH2 and not wired here. Requires an installed navmap.
func (w *World) InstallShelter(cfg exposure.Config, blockers []exposure.Blocker, coverers []exposure.Coverer) {
	if w.nav == nil {
		return
	}
	w.exposureBlockers = blockers
	w.exposureCache = exposure.NewCache(cfg, newNavTopo(w.nav))
	if len(coverers) > 0 {
		w.exposureCover = exposure.BuildCover(cfg, coverers)
	} else {
		w.exposureCover = nil
	}
}

// localTempMoistureAt applies SH3 overhead cover to the climate values an animal SENSES at p (Q-S2
// read-time; climate state untouched). Covered cells buffer felt temperature toward the day's mean
// (Q-S3/Q-S5) and, WHILE RAINING, shed felt moisture (Q-S6). Shelter-OFF (or an uncovered cell) returns
// the raw climate values unchanged. `ε_cover∈[0,1]`: 1 = open sky, 0 = fully covered.
func (w *World) localTempMoistureAt(p core.Vec2, cellTemp, cellMoisture, dailyMean float64, raining bool) (float64, float64) {
	if w.exposureCover == nil || w.nav == nil {
		return cellTemp, cellMoisture
	}
	cell := w.nav.CellOf(p)
	eps := w.exposureCover.Epsilon(exposure.Cell{Q: cell.Q, R: cell.R})
	if eps >= 1 {
		return cellTemp, cellMoisture
	}
	feltTemp := cellTemp + (dailyMean-cellTemp)*(1-eps) // lerp(actual, dailyMean, 1−ε_cover)
	feltMoisture := cellMoisture
	if raining {
		feltMoisture = cellMoisture * eps
	}
	return feltTemp, feltMoisture
}

// localWindAt returns the world-uniform wind attenuated by the exposure field at p (SH1). It is the
// single SH1 injection point: fauna EnvSample.Wind uses it, and scent.Read inherits it via fauna
// (scent.Spread keeps the global wind — SH1 SPEC-design (a)). Shelter-OFF returns global unchanged.
func (w *World) localWindAt(p core.Vec2, global climate.Wind) scent.Wind {
	g := scent.Wind{Dir: global.Dir, Mag: global.Mag}
	if w.exposureCache == nil || w.nav == nil {
		return g
	}
	ew := exposure.Wind{Dir: global.Dir, Mag: global.Mag}
	field := w.exposureCache.Field(exposure.SectorOf(ew), w.exposureBlockers, nil)
	cell := w.nav.CellOf(p)
	lw := field.LocalWind(exposure.Cell{Q: cell.Q, R: cell.R}, ew)
	return scent.Wind{Dir: lw.Dir, Mag: lw.Mag}
}
