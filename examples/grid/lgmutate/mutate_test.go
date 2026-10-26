package lgmutate

import (
	"testing"

	"github.com/mparrett/undertone/engine"
)

func TestMutateVariesWithinTheGrid(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	g := engine.SeedGrid(engine.DeriveOverworld(1, 2, 0))
	a, err := m.Mutate(g, 7)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.Mutate(g, 7)
	if a != b {
		t.Fatal("same grid and seed gave different variations")
	}
	changed := 0
	for i := 0; i < engine.Steps; i++ {
		for _, d := range []int{a.Lead[i], a.Bass[i]} {
			if d < 0 || d > engine.Degrees {
				t.Fatalf("step %d: degree %d out of range", i, d)
			}
		}
		if a.Lead[i] != g.Lead[i] || a.Bass[i] != g.Bass[i] || a.Pulse[i] != g.Pulse[i] {
			changed++
		}
	}
	if changed == 0 || changed > 10 {
		t.Errorf("%d of 16 steps changed; a variation should keep most of the pattern but not all", changed)
	}
	if !a.Pulse[0] {
		t.Error("the downbeat pulse moved")
	}
	c, _ := m.Mutate(g, 8)
	if c == a {
		t.Error("a different seed gave the same variation")
	}
}

func TestRepeatedMutationKeepsAPhrase(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	g := engine.SeedGrid(engine.DeriveOverworld(1, 2, 0))
	for seed := 1; seed <= 40; seed++ {
		if g, err = m.Mutate(g, seed); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, d := range g.Lead {
			if d > 0 {
				n++
			}
		}
		if n < 4 || n > 12 {
			t.Fatalf("after %d mutations the lead has %d notes; it should stay a phrase", seed, n)
		}
	}
}
