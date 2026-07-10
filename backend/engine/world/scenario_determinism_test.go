package world

import "testing"

func assertScenarioDeterministic(t *testing.T, name string, run func() *World) {
	t.Helper()
	assertWorldDigestsEqual(t, name, run(), run())
}

func assertWorldDigestsEqual(t *testing.T, name string, a, b *World) {
	t.Helper()
	dA, dB := worldDigest(a), worldDigest(b)
	if dA != dB {
		t.Fatalf("DETERMINISM FAILED (%s)\nA:\n%s\nB:\n%s", name, dA, dB)
	}
}
