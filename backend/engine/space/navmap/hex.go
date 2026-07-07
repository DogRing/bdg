package navmap

import "math"

// Flat-top hexagonal grid geometry (docs/hex-grid.md, phase H2). Pure functions over axial (q,r) hex
// coordinates, an offset(col,row) rectangular layout, and continuous world points measured relative to
// a grid origin. navmap OWNS the hex convention: H3 wires CellOf/CellCenter/Neighbors onto these
// helpers; pathfind + the frontend consume it and never redefine it. Orientation is FLAT-TOP.
//
// Conventions (size = circumradius = hex centre→vertex distance, world units):
//   - Axial neighbour topology is orientation-independent; flat-top only changes the pixel mapping.
//   - The offset layout is "odd-q": odd columns are shifted DOWN half a row (flat-top). This is the
//     bijection between the fixture/wire/frontend rectangular array and internal axial coords.
//   - No RNG, no wall-clock — deterministic pure math (D12). Negative-safe (floor-free; cube rounding).

// sqrt3 is √3, computed once (Go's math package has Sqrt2 but not Sqrt3).
var sqrt3 = math.Sqrt(3)

// hexDirs are the 6 axial neighbour directions in FIXED canonical order — the single neighbour
// authority (navmap.Neighbors returns these; pathfind must not hardcode its own set).
var hexDirs = [6][2]int{
	{+1, 0}, {+1, -1}, {0, -1}, {-1, 0}, {-1, +1}, {0, +1},
}

// hexToPixel maps axial (q,r) to the continuous centre of that hex, relative to the grid origin
// (the caller adds the world origin). Flat-top.
func hexToPixel(q, r int, size float64) (x, y float64) {
	fq, fr := float64(q), float64(r)
	x = size * (1.5 * fq)
	y = size * (sqrt3/2*fq + sqrt3*fr)
	return
}

// pixelToHex maps a continuous point (relative to the grid origin) to the axial (q,r) of the hex
// containing it: flat-top pixel→hex + cube rounding. Deterministic; negative-safe.
func pixelToHex(x, y, size float64) (q, r int) {
	fq := (2.0 / 3.0 * x) / size
	fr := (-1.0/3.0*x + sqrt3/3.0*y) / size
	return cubeRound(fq, fr)
}

// cubeRound rounds a fractional axial (qf,rf) to the nearest hex via the cube constraint x+y+z=0,
// re-deriving the component with the LARGEST rounding error (the standard robust hex rounding — doing
// this in axial directly is the classic bug source, hence the cube detour + this dedicated test).
func cubeRound(qf, rf float64) (q, r int) {
	xf, zf := qf, rf
	yf := -qf - rf
	rx := math.Round(xf)
	ry := math.Round(yf)
	rz := math.Round(zf)
	xd, yd, zd := math.Abs(rx-xf), math.Abs(ry-yf), math.Abs(rz-zf)
	switch {
	case xd > yd && xd > zd:
		rx = -ry - rz
	case yd > zd:
		ry = -rx - rz
	default:
		rz = -rx - ry
	}
	return int(rx), int(rz) // q = cube.x, r = cube.z
}

// hexDistance is the number of single-step moves between two axial hexes (the hex metric).
func hexDistance(q1, r1, q2, r2 int) int {
	return (hexAbs(q1-q2) + hexAbs(q1+r1-q2-r2) + hexAbs(r1-r2)) / 2
}

// axialToOffset / offsetToAxial convert between axial (q,r) and the offset(col,row) rectangular layout
// (flat-top odd-q). Bijection; numerator (q + (q&1)) is always even so the /2 is exact for any sign.
func axialToOffset(q, r int) (col, row int) {
	return q, r + (q+(q&1))/2
}

func offsetToAxial(col, row int) (q, r int) {
	return col, row - (col+(col&1))/2
}

func hexAbs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
