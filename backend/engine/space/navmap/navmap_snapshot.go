package navmap

import "maps"

// Snapshot & serialisation view — split from navmap.go to keep each file under the ~400-line rule
// (CLAUDE.md). These are the read-only plan-phase snapshot and the sparse persist/stream projections.

// Snapshot returns a frozen deep-copy of the NavMap for the plan phase.
// pathfind receives a Snapshot and must never mutate it.
// A snapshot taken before a SetTerrain call shows the old terrain.
// The immutable cfg, types, and terrainSrc are shared (not copied).
func (m *NavMap) Snapshot() *NavMap {
	wearCopy := make(map[Cell]float64, len(m.wear))
	maps.Copy(wearCopy, m.wear)
	fpCopy := make(map[Cell]struct{}, len(m.footprint))
	maps.Copy(fpCopy, m.footprint)
	overCopy := make(map[Cell]TerrainID, len(m.terrainOverrides))
	maps.Copy(overCopy, m.terrainOverrides)
	return &NavMap{
		cfg:              m.cfg,
		types:            m.types,      // shared; immutable
		terrainSrc:       m.terrainSrc, // shared; immutable
		terrainOverrides: overCopy,
		footprint:        fpCopy,
		wear:             wearCopy,
	}
}

// ActiveWear returns the sparse wear field in D12 sorted order (R-major then Q, hex).
// Only cells with wear > 0 are included. Used for persist/stream.
func (m *NavMap) ActiveWear() []WearCell {
	cells := sortedWearKeys(m.wear)
	result := make([]WearCell, 0, len(cells))
	for _, c := range cells {
		result = append(result, WearCell{Cell: c, Wear: m.wear[c]})
	}
	return result
}

// TerrainOverrides returns the sparse terrain delta (cells changed by SetTerrain away from
// the New-time base layout) in D12 sorted order. Empty before any SetTerrain is called.
// Cells reverted to their base terrain are omitted (delta-only).
func (m *NavMap) TerrainOverrides() []TerrainCell {
	cells := sortedOverrideKeys(m.terrainOverrides)
	result := make([]TerrainCell, 0, len(cells))
	for _, c := range cells {
		result = append(result, TerrainCell{Cell: c, Terrain: m.terrainOverrides[c]})
	}
	return result
}
