package worldgen

import (
	"math"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/navmap"
)

// terrainGenParams are GenerateTerrain's dev coefficients (SPEC §GenerateTerrain). They are
// the WG1-a §2 knobs (noise octaves, sea level, river threshold, erosion …) fixed as code
// defaults until the full Generate(GenConfig) lands and promotes them to data (D10 note).
type terrainGenParams struct {
	octaves    int     // fBm octaves
	baseScale  float64 // lowest-octave wavelength as a fraction of max(world W, H)
	lacunarity float64 // per-octave frequency multiplier
	gain       float64 // per-octave amplitude multiplier

	seaQuantile      float64 // elevation quantile below which cells are sea
	mountainQuantile float64 // … above which land is mountain
	rockQuantile     float64 // … above which land is bare_rock

	riverDivisor int     // river accumulation threshold = max(6, landCells/riverDivisor)
	erosion      float64 // max elevation carved out of a river cell (scaled by accumulation)
	beachBand    float64 // land beside water within seaLevel+band elevation ⇒ sand

	moistFloor float64 // driest-inland initial moisture
	moistAmp   float64 // added moisture at distance 0 from water
	moistDecay float64 // e-folding distance (hex steps) of the water-proximity moisture
}

var defaultTerrainGen = terrainGenParams{
	octaves: 4, baseScale: 0.4, lacunarity: 2.0, gain: 0.5,
	seaQuantile: 0.16, mountainQuantile: 0.86, rockQuantile: 0.965,
	riverDivisor: 22, erosion: 0.15, beachBand: 0.06,
	moistFloor: 0.25, moistAmp: 0.7, moistDecay: 3.0,
}

// GenerateTerrain is the elevation-based terrain generator behind terrain{random:true}
// fixtures — the WG1-a TERRAIN stages (world-gen.md §1 stages 1-5) with the dev
// coefficients above (SPEC §GenerateTerrain): value-noise fBm elevation sampled at
// hex-centre world coords → quantile sea level → flow-accumulation hydrology (rivers
// erode valleys, outflow-less pits become lakes) → threshold materials (mountain/
// bare_rock/sand/soil) → water-proximity initial-moisture field (the climate stage-4
// seed). Returns (cells, elevation, moisture), each len cols*rows; elevation is
// post-erosion, normalized [0,1], and render-only downstream. Deterministic in the
// injected rng: fixed draw and iteration order (D12). Degenerate grids (cols/rows < 3)
// come back flat all-soil.
func GenerateTerrain(cols, rows int, navCfg navmap.Config, r *rng.RNG) (cells []core.Tag, elevation, moisture []float64) {
	n := cols * rows
	cells = make([]core.Tag, n)
	elevation = make([]float64, n)
	moisture = make([]float64, n)
	if n == 0 {
		return cells, elevation, moisture
	}
	p := defaultTerrainGen
	if cols < 3 || rows < 3 {
		for i := range cells {
			cells[i], elevation[i], moisture[i] = "soil", 0.5, 0.5
		}
		return cells, elevation, moisture
	}

	// 1. Elevation: fBm value noise at each hex centre's WORLD coordinates (isotropic in
	// world space — navmap.OffsetCenterOf, the hex authority), min-max normalized to [0,1].
	noise := newValueNoise(r)
	wavelen := math.Max(navCfg.MaxX-navCfg.MinX, navCfg.MaxY-navCfg.MinY) * p.baseScale
	if wavelen <= 0 {
		wavelen = 1
	}
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			c := navmap.OffsetCenterOf(navCfg, col, row)
			elevation[row*cols+col] = noise.fbm(c.X/wavelen, c.Y/wavelen, p.octaves, p.lacunarity, p.gain)
		}
	}
	normalize01(elevation)

	// 2. Sea below the sea-level quantile (quantiles ⇒ stable land/water fractions per seed).
	seaLevel := quantileOf(elevation, p.seaQuantile)
	mountainLevel := quantileOf(elevation, p.mountainQuantile)
	rockLevel := quantileOf(elevation, p.rockQuantile)
	water := make([]bool, n) // sea | lake | river
	land := 0
	for i := range cells {
		if elevation[i] < seaLevel {
			cells[i], water[i] = "sea", true
		} else {
			land++
		}
	}

	// 3. Hydrology (WG2-a flow accumulation): every land cell drains to its strictly-lowest
	// hex neighbour; rain accumulates downstream in descending-elevation order. Outflow-less
	// pits become lakes; accumulation over the threshold becomes a river, eroded into a valley.
	downhill := make([]int, n)
	for i := range downhill {
		downhill[i] = -1
	}
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			i := row*cols + col
			if water[i] {
				continue
			}
			best, bestElev := -1, elevation[i]
			for _, nb := range navmap.OffsetNeighborsOf(col, row) {
				nc, nr := nb[0], nb[1]
				if nc < 0 || nc >= cols || nr < 0 || nr >= rows {
					continue
				}
				j := nr*cols + nc
				if elevation[j] < bestElev {
					best, bestElev = j, elevation[j]
				}
			}
			downhill[i] = best
		}
	}
	order := make([]int, 0, land)
	for i := range cells {
		if !water[i] {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(a, b int) bool {
		if elevation[order[a]] != elevation[order[b]] {
			return elevation[order[a]] > elevation[order[b]]
		}
		return order[a] < order[b]
	})
	accum := make([]float64, n)
	for _, i := range order {
		accum[i]++
		if j := downhill[i]; j >= 0 {
			accum[j] += accum[i]
		}
	}
	riverThreshold := float64(max(6, land/p.riverDivisor))
	for _, i := range order {
		switch {
		case downhill[i] < 0:
			cells[i], water[i] = "lake", true // outflow-less basin
		case accum[i] >= riverThreshold:
			cells[i], water[i] = "river", true
			carve := p.erosion * math.Min(1, accum[i]/(3*riverThreshold))
			if elevation[i] = elevation[i] - carve; elevation[i] < 0 {
				elevation[i] = 0
			}
		}
	}

	// 4. Materials for the remaining land: high ⇒ mountain/bare_rock, low beside water ⇒
	// sand (beach), else soil (thresholds keep soil dominant — pos-less placement needs it).
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			i := row*cols + col
			if water[i] {
				continue
			}
			switch {
			case elevation[i] >= rockLevel:
				cells[i] = "bare_rock"
			case elevation[i] >= mountainLevel:
				cells[i] = "mountain"
			case elevation[i] <= seaLevel+p.beachBand && hasWaterNeighbor(water, cols, rows, col, row):
				cells[i] = "sand"
			default:
				cells[i] = "soil"
			}
		}
	}

	// 5. Initial-moisture field (WG1-a stage 4): hex-BFS distance to the nearest water cell,
	// exponentially decayed — riverbanks start wet, the far inland starts at the floor.
	dist := waterDistanceBFS(water, cols, rows)
	for i := range moisture {
		moisture[i] = p.moistFloor + p.moistAmp*math.Exp(-float64(dist[i])/p.moistDecay)
		if moisture[i] > 1 {
			moisture[i] = 1
		}
	}
	return cells, elevation, moisture
}

func hasWaterNeighbor(water []bool, cols, rows, col, row int) bool {
	for _, nb := range navmap.OffsetNeighborsOf(col, row) {
		nc, nr := nb[0], nb[1]
		if nc < 0 || nc >= cols || nr < 0 || nr >= rows {
			continue
		}
		if water[nr*cols+nc] {
			return true
		}
	}
	return false
}

// waterDistanceBFS is a multi-source hex BFS from every water cell (distance in hex steps;
// water itself = 0). Deterministic: row-major seeding, FIFO queue, canonical neighbour order.
func waterDistanceBFS(water []bool, cols, rows int) []int {
	const unreached = 1 << 30
	dist := make([]int, cols*rows)
	queue := make([]int, 0, cols*rows)
	for i := range dist {
		if water[i] {
			dist[i] = 0
			queue = append(queue, i)
		} else {
			dist[i] = unreached
		}
	}
	for head := 0; head < len(queue); head++ {
		i := queue[head]
		col, row := i%cols, i/cols
		for _, nb := range navmap.OffsetNeighborsOf(col, row) {
			nc, nr := nb[0], nb[1]
			if nc < 0 || nc >= cols || nr < 0 || nr >= rows {
				continue
			}
			j := nr*cols + nc
			if dist[j] > dist[i]+1 {
				dist[j] = dist[i] + 1
				queue = append(queue, j)
			}
		}
	}
	return dist
}

// ── Seeded value noise (fBm base) ────────────────────────────────────────────────

// valueNoise is a small seeded lattice value-noise: a shuffled permutation table hashes
// integer lattice points to fixed random values; sampling is smoothstep-bilinear between
// them. All randomness is drawn from the injected rng at construction (D12).
type valueNoise struct {
	perm [512]int
	vals [256]float64
}

func newValueNoise(r *rng.RNG) *valueNoise {
	var v valueNoise
	p := make([]int, 256)
	for i := range p {
		p[i] = i
	}
	r.Shuffle(256, func(i, j int) { p[i], p[j] = p[j], p[i] })
	for i := 0; i < 256; i++ {
		v.perm[i], v.perm[256+i] = p[i], p[i]
	}
	for i := range v.vals {
		v.vals[i] = r.Float64()
	}
	return &v
}

func (v *valueNoise) lattice(ix, iy int) float64 {
	return v.vals[v.perm[v.perm[ix&255]+(iy&255)]&255]
}

func (v *valueNoise) sample(x, y float64) float64 {
	x0, y0 := math.Floor(x), math.Floor(y)
	ix, iy := int(x0), int(y0)
	tx, ty := smoothstep(x-x0), smoothstep(y-y0)
	a := v.lattice(ix, iy)
	b := v.lattice(ix+1, iy)
	c := v.lattice(ix, iy+1)
	d := v.lattice(ix+1, iy+1)
	return lerp(lerp(a, b, tx), lerp(c, d, tx), ty)
}

func (v *valueNoise) fbm(x, y float64, octaves int, lacunarity, gain float64) float64 {
	sum, norm, amp, freq := 0.0, 0.0, 1.0, 1.0
	for o := 0; o < octaves; o++ {
		sum += amp * v.sample(x*freq, y*freq)
		norm += amp
		amp *= gain
		freq *= lacunarity
	}
	return sum / norm
}

func smoothstep(t float64) float64 { return t * t * (3 - 2*t) }
func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func normalize01(vals []float64) {
	lo, hi := vals[0], vals[0]
	for _, v := range vals {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi <= lo {
		for i := range vals {
			vals[i] = 0.5
		}
		return
	}
	for i := range vals {
		vals[i] = (vals[i] - lo) / (hi - lo)
	}
}

// quantileOf returns the q-quantile of vals (sorted copy; nearest-rank). Deterministic.
func quantileOf(vals []float64, q float64) float64 {
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	idx := int(q * float64(len(sorted)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
