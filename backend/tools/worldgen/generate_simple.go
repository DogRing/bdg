package worldgen

import (
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// SimpleTerrain fills a cols×rows flat-top hex offset grid (i = row*cols + col) with a
// minimal random layout: all soil, one wandering top→bottom river walk, 1-2 sand banks
// hugging it, and an optional mountain blob. This is the dev/test generator behind
// terrain{random:true} fixtures (SPEC §SimpleTerrain) — NOT the WG1-a Generate pipeline:
// no hydrology/moisture/biomes, ids fixed to the dev subset {soil,river,sand,mountain}
// ⊆ content/terrain.yaml. Deterministic in the injected rng (D12); the offset-grid river
// walk reads as a river but hex-chain connectivity is not guaranteed (navmap costs
// handle any layout). Degenerate grids (cols<3 or rows<2) stay all-soil.
func SimpleTerrain(cols, rows int, r *rng.RNG) []core.Tag {
	cells := make([]core.Tag, cols*rows)
	for i := range cells {
		cells[i] = "soil"
	}
	if cols < 3 || rows < 2 {
		return cells
	}

	// River: one cell per row, drifting ≤1 column per step, kept off the outer columns.
	riverCol := make([]int, rows)
	col := 1 + r.Intn(cols-2)
	for row := 0; row < rows; row++ {
		cells[row*cols+col] = "river"
		riverCol[row] = col
		col += r.Intn(3) - 1
		if col < 1 {
			col = 1
		}
		if col > cols-2 {
			col = cols - 2
		}
	}

	// Sand banks: 1-2 short vertical runs on one side of the river (soil cells only,
	// so the river stays unbroken).
	banks := 1 + r.Intn(2)
	for b := 0; b < banks; b++ {
		side := 1
		if r.Intn(2) == 0 {
			side = -1
		}
		row0 := r.Intn(rows)
		run := 2 + r.Intn(3)
		for k := 0; k < run; k++ {
			row := row0 + k
			if row >= rows {
				break
			}
			c := riverCol[row] + side
			if c < 0 || c >= cols {
				continue
			}
			if i := row*cols + c; cells[i] == "soil" {
				cells[i] = "sand"
			}
		}
	}

	// Mountain: an optional small blob (soil cells only).
	if r.Float64() < 0.7 {
		c0, r0 := r.Intn(cols), r.Intn(rows)
		blob := 2 + r.Intn(3)
		for k := 0; k < blob; k++ {
			c := c0 + r.Intn(3) - 1
			row := r0 + r.Intn(3) - 1
			if c < 0 || c >= cols || row < 0 || row >= rows {
				continue
			}
			if i := row*cols + c; cells[i] == "soil" {
				cells[i] = "mountain"
			}
		}
	}
	return cells
}
