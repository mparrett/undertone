package letgo

import (
	"testing"
	"time"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// webaudioTestOutput reports a queue and underruns, as webaudio does.
type webaudioTestOutput struct {
	queuedTestOutput
	under int64
}

func (o webaudioTestOutput) Underruns() int64 { return o.under }

func TestUnderrunsOnlyWhereTheOutputCounts(t *testing.T) {
	p := engine.DeriveDungeon(1, 1, 0)
	m := NewMusic(p)
	if _, ok := m.Underruns(); ok {
		t.Error("Underruns before music-start: ok, want not")
	}
	startRendering(t, m, gainTestOutput{}, 512)
	if _, ok := m.Underruns(); ok {
		t.Error("Underruns from an output that doesn't count them: ok, want not")
	}
	m.Stop()
	startRendering(t, m, webaudioTestOutput{under: 4}, 512)
	if n, ok := m.Underruns(); !ok || n != 4 {
		t.Errorf("Underruns = %d, %v; want 4, ok", n, ok)
	}
	if st, ok := m.RenderStats(); !ok || st.Reads != 1 || st.Frames != 512 {
		t.Errorf("RenderStats = %+v, %v; want one Read of 512 frames", st, ok)
	}
}

func TestAudioStatsFromLg(t *testing.T) {
	p := engine.DeriveDungeon(1, 1, 0)
	m := NewMusic(p)
	SetMusic(m)
	c, err := api.NewLetGo("audiostatstest")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := c.Run(`(undertone/audio-stats)`); err != nil || v != vm.NIL {
		t.Fatalf("audio-stats before music-start = %v, %v; want nil", v, err)
	}
	startRendering(t, m, gainTestOutput{}, 512)
	v, err := c.Run(`(let [s (undertone/audio-stats)]
	                   [(:reads s) (:over s) (contains? s :underruns) (contains? s :queued-ms) (>= (:render-ms-max s) (:render-ms-last s) 0)])`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), "[1 0 false false true]"; got != want {
		t.Errorf("without a counting output: %s, want %s", got, want)
	}
	m.Stop()
	startRendering(t, m, webaudioTestOutput{queuedTestOutput{queued: 60 * time.Millisecond}, 2}, 512)
	v, err = c.Run(`(let [s (undertone/audio-stats)] [(:underruns s) (:queued-ms s)])`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), "[2 60.0]"; got != want {
		t.Errorf("with webaudio's counts: %s, want %s", got, want)
	}
}
