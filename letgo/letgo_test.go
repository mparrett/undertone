package letgo

import (
	"bytes"
	"io"
	"testing"

	"github.com/nooga/let-go/pkg/api"

	"github.com/mparrett/undertone/engine"
)

// Importing the package is the whole install for an lg runtime that links it
// as a plain Go dep: no SetMusic, no generated interop. If init stopped
// registering, lg code would fail to resolve undertone/* with nothing pointing at
// the cause.
func TestNamespaceRegisteredAtImport(t *testing.T) {
	c, err := api.NewLetGo("importtest")
	if err != nil {
		t.Fatal(err)
	}
	if CurrentMusic() == nil {
		t.Fatal("no default Music; natives would dereference nil")
	}
	if _, err := c.Run("(undertone/set-parameters {:bpm 77.0})"); err != nil {
		t.Fatalf("undertone namespace not registered at import: %v", err)
	}
}

// The namespace resolves with no source path, as it must in the browser,
// which has no filesystem, and in a cli.Main host: init registered it. A plain
// require reaches it by its qualified name, and LoadEmbedded aliases it as
// ambience, which Go hosts, lghost and descent call it by. Namespaces are
// process-global, so the second runtime finds it loaded; what it checks is
// the alias.
func TestAmbienceResolvesWithNoSourcePath(t *testing.T) {
	for _, c := range []struct {
		load func(*api.LetGo) error
		call string
	}{
		{func(c *api.LetGo) error { _, err := c.Run("(require 'undertone.ambience)"); return err }, "(undertone.ambience/score! 7 4 0.6)"},
		{LoadEmbedded, "(ambience/score! 7 4 0.6)"},
	} {
		m := NewMusic(engine.DeriveDungeon(1, 0, 0))
		SetMusic(m)
		lg, err := api.NewLetGo("embedtest")
		if err != nil {
			t.Fatal(err)
		}
		if err := c.load(lg); err != nil {
			t.Fatal(err)
		}
		if _, err := lg.Run(c.call); err != nil {
			t.Fatalf("%s: %v", c.call, err)
		}
		if got, want := m.Params().BPM, engine.DeriveDungeon(7, 4, 0.6).BPM; got != want {
			t.Errorf("%s: bpm %v, want %v", c.call, got, want)
		}
	}
}

// The seam itself: lg trigger maps must reach the engine and make sound. The
// synth is set directly (no oto, no sound card) over silent parameters, so
// any PCM that appears is the one-shot lane's.
func TestAudioTriggerReachesTheEngine(t *testing.T) {
	m := NewMusic(engine.DeriveDungeon(1, 0, 0))
	SetMusic(m)
	c, err := api.NewLetGo("triggertest")
	if err != nil {
		t.Fatal(err)
	}

	// Write-only contract: with no synth (music not started), triggers drop
	// without error.
	if _, err := c.Run(`(undertone/trigger {:shot :hit :gain 1.0})`); err != nil {
		t.Fatalf("trigger with no synth errored: %v", err)
	}

	silent := engine.Params{BPM: 120, Seed: 1, Scale: []int{0, 2, 3, 5, 7, 9, 10}}
	m.mu.Lock()
	m.synth = engine.NewSynth(silent)
	m.mu.Unlock()
	if _, err := c.Run(`(undertone/trigger [{:shot :hit :gain 1.0 :pan 0.3}
	                                    {:shot :pluck :degree 2 :octave 1 :seed "42"}])`); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, engine.SampleRate*4)
	m.synth.Read(buf)
	for _, b := range buf {
		if b != 0 {
			return
		}
	}
	t.Error("triggers sent from lg rendered pure silence; the map -> Shot plumbing dropped them")
}

// set-parameters-now merges like set-parameters and reaches the engine as
// SetParametersNow: what plays is what a synth given the merged params at once
// plays.
func TestSetParametersNowReachesTheEngineAtOnce(t *testing.T) {
	base := engine.DeriveDungeon(7, 2, 0)
	m := NewMusic(base)
	SetMusic(m)
	c, err := api.NewLetGo("nowtest")
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.synth = engine.NewSynth(base)
	m.mu.Unlock()
	ref := engine.NewSynth(base)
	lead := make([]byte, 4*3000)
	m.synth.Read(lead)
	ref.Read(lead)
	if _, err := c.Run(`(undertone/set-parameters-now {:mood :rust :bpm 80.0})`); err != nil {
		t.Fatal(err)
	}
	if got := m.Params(); got.BPM != 80 || got.Root != base.Root {
		t.Fatalf("bpm %v root %v: want 80 and the root kept, as set-parameters merges", got.BPM, got.Root)
	}
	ref.SetParametersNow(m.Params())
	got, want := make([]byte, 4*4096), make([]byte, 4*4096)
	m.synth.Read(got)
	ref.Read(want)
	if !bytes.Equal(got, want) {
		t.Error("set-parameters-now did not play as SetParametersNow with the merged params")
	}
}

// A set-parameters-now sent while music-start waits for the output, as for a
// browser's unlocking tap, is still heard at once when the music starts, not
// at the first boundary.
func TestSetParametersNowDuringStartIsHeardAtOnce(t *testing.T) {
	base := engine.DeriveDungeon(7, 2, 0)
	m := NewMusic(base)
	SetMusic(m)
	c, err := api.NewLetGo("nowstart")
	if err != nil {
		t.Fatal(err)
	}
	lead := make([]byte, 4*3000)
	m.start(func(src io.Reader) (output, error) {
		io.ReadFull(src, lead) // outputs can render before opening returns
		if _, err := c.Run(`(undertone/set-parameters-now {:mood :rust :bpm 80.0})`); err != nil {
			t.Fatal(err)
		}
		return gainTestOutput{}, nil
	})
	defer m.Stop()
	ref := engine.NewSynth(base)
	ref.Read(lead)
	ref.SetParametersNow(m.Params())
	got, want := make([]byte, 4*4096), make([]byte, 4*4096)
	m.synth.Read(got)
	ref.Read(want)
	if !bytes.Equal(got, want) {
		t.Error("a set-parameters-now during the start wait was not applied at once")
	}
}
