package scent

import (
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
)

// Independent re-run of the reviewer's original repro, post-fix.
func TestZZVerifyStaticDiffusion(t *testing.T) {
	g := New(1.0)
	g.DepositStatic(ChanFood, core.Vec2{X: 0.5, Y: 0.5}, 10)
	g.CommitStatic(Wind{})

	t.Logf("static  = %v", g.static)
	t.Logf("pending = %v", g.pending)

	own := g.static[cellKey{0, 0}][ChanFood]
	nb := g.static[cellKey{1, 0}][ChanFood]
	if nb <= 0 {
		t.Errorf("static neighbour still 0 (own=%v)", own)
	}
	if nb >= own {
		t.Errorf("halo not attenuated: own=%v nb=%v", own, nb)
	}
	if len(g.pending) != 0 {
		t.Errorf("pending polluted by static diffusion: %v", g.pending)
	}
	// Mass accounting: nothing created. donated=4.0, delivered=4.0*0.6=2.4, source keeps 6.0.
	var total float64
	for _, v := range g.static {
		total += v[ChanFood]
	}
	t.Logf("own=%v nb=%v total static mass=%v (deposited 10, falloff loss expected)", own, nb, total)
	if total > 10.000001 {
		t.Errorf("mass created: %v > 10", total)
	}
}
