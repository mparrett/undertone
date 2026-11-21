package letgo

import (
	"testing"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

func TestLevelsFromLg(t *testing.T) {
	p := engine.DeriveDungeon(1, 2, 0)
	m := NewMusic(p)
	SetMusic(m)
	c, err := api.NewLetGo("levelstest")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"layer-levels", "levels"} {
		if v, err := c.Run(`(undertone/` + f + `)`); err != nil || v != vm.NIL {
			t.Fatalf("%s before music-start = %v, %v; want nil", f, v, err)
		}
	}
	if _, ok := m.LayerLevels(); ok {
		t.Error("LayerLevels before music-start: ok, want not")
	}
	startRendering(t, m, gainTestOutput{}, engine.SampleRate)
	v, err := c.Run(`(let [l (undertone/layer-levels) b (undertone/levels)]
	                   [(sort (map name (keys l))) (pos? (:drone l)) (sort (map name (keys b))) (pos? (:sub b))])`)
	if err != nil {
		t.Fatal(err)
	}
	want := `[("air" "bass" "drone" "drums" "fifth" "lead" "pad" "pulse") true ("high" "low-mid" "mid" "sub") true]`
	if got := v.String(); got != want {
		t.Errorf("levels = %s, want %s", got, want)
	}
	got, _ := m.LayerLevels()
	if got != m.synth.LayerLevels() {
		t.Error("Music.LayerLevels differs from the synth's")
	}
}

// A paused output reads nothing, so the meters hold their last values; the
// level accessors report silence until it resumes.
func TestLevelsSilentWhilePaused(t *testing.T) {
	p := engine.DeriveDungeon(1, 2, 0)
	m := NewMusic(p)
	startRendering(t, m, gainTestOutput{}, engine.SampleRate)
	m.Pause()
	l, ok := m.LayerLevels()
	b, bok := m.Levels()
	if !ok || !bok || l != [engine.NumLayers]float64{} || b != [4]float64{} {
		t.Errorf("paused: layers %v ok %v, bands %v ok %v; want silence, ok", l, ok, b, bok)
	}
	m.Resume()
	if l, _ := m.LayerLevels(); l[0] == 0 {
		t.Error("resumed: the drone reads silent")
	}
}
