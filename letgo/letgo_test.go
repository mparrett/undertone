package letgo

import (
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
