// Package exposure is the SH1 shelter/exposure field (docs/shelter.md, SPEC.md):
// a pure, deterministic transform from `blocks_wind` blockers + a wind sector to a
// per-hex-cell exposure factor epsilon in [0,1]. It is purely combinatorial over the
// hex graph (a Topology adapter supplies neighbors + bounds; no positions, no geometry)
// and imports only engine/kernel/core, so it stays a leaf that world adapts navmap/
// climate/scent/fauna into. `local wind magnitude = global wind magnitude * epsilon(cell)`.
package exposure

import (
	"math"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
)

// Cell is the exposure grid index (axial, same convention as navmap.Cell; exposure does
// not import navmap). It is an index only, never an entity position (D11).
type Cell struct{ Q, R int }

// Wind is an injected local-forcing value. Dir is radians; Mag is normalized [0,1].
// It mirrors climate.Wind / scent.Wind without importing either package.
type Wind struct {
	Dir float64
	Mag float64
}

// Sector is the quantized wind direction (six 60-degree bins).
type Sector uint8

// NumSectors is the number of wind-direction bins (one epsilon field cached per sector, Q-W4).
const NumSectors = 6

const twoPi = 2 * math.Pi

// SectorOf maps wind.Dir to one of six deterministic 60-degree bins:
//
//	sector = floor(wrap(Dir) / (pi/3))   with Dir wrapped into [0, 2*pi)
//
// wind.Mag does not affect the sector. This 6-bin convention is the shared contract with
// Topology: Neighbors(c)[SectorOf(w)] is the downwind neighbor.
func SectorOf(w Wind) Sector {
	d := math.Mod(w.Dir, twoPi)
	if d < 0 {
		d += twoPi
	}
	// floor(d / (2π/NumSectors)); a tiny epsilon lets an exact 60°-multiple boundary resolve UP into
	// its bin despite float rounding of the bin width (e.g. d == π must land in bin 3, not 2).
	const boundaryEps = 1e-9
	s := int(d/(twoPi/NumSectors) + boundaryEps)
	if s >= NumSectors { // d at/just below 2π
		s = NumSectors - 1
	}
	return Sector(s)
}

// Topology is the minimal hex-graph adapter exposure needs. world implements it over navmap:
//   - Neighbors(c)[s] is c's neighbor in the direction of wind Sector s (array in sector order
//     matching SectorOf); out-of-bounds entries are allowed and filtered via InBounds.
//   - InBounds(c) reports whether c is a real map cell.
type Topology interface {
	Neighbors(c Cell) [6]Cell
	InBounds(c Cell) bool
}

// Blocker is one wind-shadow caster derived from content tags such as `blocks_wind`.
type Blocker struct {
	ID        core.ObjectID
	Footprint []Cell
	Height    float64 // world units; <= 0 casts no shadow
	Opacity   float64 // clamped [0,1]; 1 = strongest blocker
}

// Interior is a set of cells whose exposure is forced by an enclosing shelter (SH2 caves use
// Epsilon 0). Interiors overwrite (not multiply) blocker shadows.
type Interior struct {
	ID      core.ObjectID
	Cells   []Cell
	Epsilon float64 // clamped [0,1]
}

// Config carries balance coefficients (no geometry/rate literals hardcoded in logic, D10).
type Config struct {
	ShadowCellsPerHeight float64 // shadow length = ceil(Height * ShadowCellsPerHeight)
	ShadowFalloff        float64 // per-cell attenuation recovery; >= 0
	MinEpsilon           float64 // lower clamp, normally 0
}

// Field is an immutable exposure snapshot for one wind sector. It stores only the sparse set
// of cells whose epsilon != 1; any cell with no stored delta reads as epsilon 1.
type Field struct {
	eps map[Cell]float64
}

// Epsilon returns the local exposure factor for c (1 for any cell with no stored delta).
func (f *Field) Epsilon(c Cell) float64 {
	if f == nil || f.eps == nil {
		return 1
	}
	if v, ok := f.eps[c]; ok {
		return v
	}
	return 1
}

// LocalWind attenuates only wind magnitude by epsilon(c). Dir is preserved; Mag clamped [0,1].
func (f *Field) LocalWind(c Cell, global Wind) Wind {
	return Wind{Dir: global.Dir, Mag: clamp(global.Mag*f.Epsilon(c), 0, 1)}
}

// CellExposure is one sparse (cell, epsilon) entry.
type CellExposure struct {
	Cell    Cell
	Epsilon float64
}

// Active returns the sparse cells whose epsilon != 1, D12-sorted by (Q, R).
func (f *Field) Active() []CellExposure {
	if f == nil || len(f.eps) == 0 {
		return nil
	}
	out := make([]CellExposure, 0, len(f.eps))
	for c, e := range f.eps {
		out = append(out, CellExposure{Cell: c, Epsilon: e})
	}
	sort.Slice(out, func(i, j int) bool { return lessCell(out[i].Cell, out[j].Cell) })
	return out
}

// Build computes the epsilon field for a wind sector from blockers + interiors, per the Shadow
// Model in SPEC.md. Shadow contributions combine multiplicatively (order-free); the sorted
// iteration is a determinism belt-and-suspenders. Interiors are applied last and OVERWRITE.
// For SH1 world calls this with interiors == nil.
func Build(cfg Config, topo Topology, sector Sector, blockers []Blocker, interiors []Interior) *Field {
	s := int(sector)
	if s < 0 || s >= NumSectors {
		s = 0
	}

	raw := map[Cell]float64{} // touched cells only; untouched read as 1

	for _, b := range sortedBlockers(blockers) {
		if b.Height <= 0 || b.Opacity <= 0 {
			continue
		}
		length := int(math.Ceil(b.Height * cfg.ShadowCellsPerHeight))
		if length <= 0 {
			continue
		}
		for _, f := range sortedCells(b.Footprint) {
			cell := f
			for d := 1; d <= length; d++ {
				cell = topo.Neighbors(cell)[s]
				if !topo.InBounds(cell) {
					break
				}
				atten := b.Opacity * math.Max(0, 1-float64(d-1)*cfg.ShadowFalloff)
				if atten <= 0 {
					break // monotonic: all further downwind cells also have 0 attenuation
				}
				cur, ok := raw[cell]
				if !ok {
					cur = 1
				}
				raw[cell] = cur * (1 - atten)
			}
		}
	}

	eps := make(map[Cell]float64, len(raw))
	for c, v := range raw {
		if e := clamp(v, cfg.MinEpsilon, 1); e != 1 {
			eps[c] = e
		}
	}

	// Interiors overwrite blocker shadows (sorted by ID so overlaps are last-writer-deterministic).
	for _, in := range sortedInteriors(interiors) {
		e := clamp(in.Epsilon, 0, 1)
		for _, c := range in.Cells {
			if e == 1 {
				delete(eps, c)
			} else {
				eps[c] = e
			}
		}
	}

	return &Field{eps: eps}
}

// Cache stores up to NumSectors sector fields. world owns invalidation when blockers/interiors change.
type Cache struct {
	cfg   Config
	topo  Topology
	slots [NumSectors]*Field
}

// NewCache returns an empty cache bound to cfg + topo.
func NewCache(cfg Config, topo Topology) *Cache {
	return &Cache{cfg: cfg, topo: topo}
}

// Invalidate drops all cached sector fields (call after blockers/interiors change).
func (c *Cache) Invalidate() {
	c.slots = [NumSectors]*Field{}
}

// Field returns the cached field for sector, building (and caching) it on a miss.
func (c *Cache) Field(sector Sector, blockers []Blocker, interiors []Interior) *Field {
	s := int(sector)
	if s < 0 || s >= NumSectors {
		return Build(c.cfg, c.topo, sector, blockers, interiors)
	}
	if c.slots[s] == nil {
		c.slots[s] = Build(c.cfg, c.topo, sector, blockers, interiors)
	}
	return c.slots[s]
}

// ── helpers ──────────────────────────────────────────────────────────────────

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func lessCell(a, b Cell) bool {
	if a.Q != b.Q {
		return a.Q < b.Q
	}
	return a.R < b.R
}

func sortedCells(in []Cell) []Cell {
	out := append([]Cell(nil), in...)
	sort.Slice(out, func(i, j int) bool { return lessCell(out[i], out[j]) })
	return out
}

func sortedBlockers(in []Blocker) []Blocker {
	out := append([]Blocker(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedInteriors(in []Interior) []Interior {
	out := append([]Interior(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
