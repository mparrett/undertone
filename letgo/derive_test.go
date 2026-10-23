package letgo

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/mparrett/undertone/engine"
	"github.com/nooga/let-go/pkg/api"
)

// lg derives through Go (#49), so what can go wrong is the trip between them:
// the derived Params become an lg map, and set-parameters reads it back. A
// field the map drops or mistypes shows up only for the moods and branches
// that set it, so the trip is checked over a matrix of moods, seeds, levels
// and tensions.

func lgRuntime(t *testing.T) (*api.LetGo, *Music) {
	t.Helper()
	m := NewMusic(engine.DeriveDungeon(1, 0, 0))
	SetMusic(m)
	c, err := api.NewLetGo("derive")
	if err != nil {
		t.Fatal(err)
	}
	// no load path: init registered the namespace
	if _, err := c.Run("(require '[undertone.ambience :as ambience])"); err != nil {
		t.Fatal(err)
	}
	return c, m
}

// fingerprint renders half a second and hashes the PCM. It covers everything
// the parameters reach — including the patch palette, which the parameter map
// selects by mood rather than carrying.
func fingerprint(p engine.Params) string {
	s := engine.NewSynth(p)
	buf := make([]byte, engine.SampleRate/2*4)
	s.Read(buf)
	return fmt.Sprintf("%x", sha256.Sum256(buf))[:16]
}

// checkParams holds parameters to the derivation's, bit for bit, and
// renders both, since the palette is reached only through the audio.
func checkParams(t *testing.T, name string, got, want engine.Params) {
	t.Helper()
	if got.BPM != want.BPM || got.Root != want.Root {
		t.Errorf("%s: bpm/root = %v/%d, want %v/%d", name, got.BPM, got.Root, want.BPM, want.Root)
	}
	if fmt.Sprint(got.Scale) != fmt.Sprint(want.Scale) {
		t.Errorf("%s: scale = %v, want %v", name, got.Scale, want.Scale)
	}
	if got.LeadName != want.LeadName || got.LeadGate != want.LeadGate {
		t.Errorf("%s: lead = %s/%d, want %s/%d", name, got.LeadName, got.LeadGate, want.LeadName, want.LeadGate)
	}
	if got.FifthOffset != want.FifthOffset || got.BellLift != want.BellLift {
		t.Errorf("%s: offset/lift = %d/%d, want %d/%d", name, got.FifthOffset, got.BellLift, want.FifthOffset, want.BellLift)
	}
	if got.Groove != want.Groove {
		t.Errorf("%s: groove = %q, want %q", name, got.Groove, want.Groove)
	}
	if got.Seed != want.Seed {
		t.Errorf("%s: seed = %d, want %d", name, got.Seed, want.Seed)
	}
	// The gains must land bit-identical, or the audio will not.
	for _, f := range []struct {
		what      string
		got, want float64
	}{
		{"drone-gain", got.DroneGain, want.DroneGain},
		{"fifth-gain", got.FifthGain, want.FifthGain},
		{"pad-density", got.PadDensity, want.PadDensity},
		{"bell-density", got.BellDensity, want.BellDensity},
		{"air-gain", got.AirGain, want.AirGain},
		{"pulse-gain", got.PulseGain, want.PulseGain},
		{"grit", got.Grit, want.Grit},
		{"open", got.Open, want.Open},
		{"drum-gain", got.DrumGain, want.DrumGain},
		{"drum-busy", got.DrumBusy, want.DrumBusy},
		{"swing", got.Swing, want.Swing},
	} {
		if f.got != f.want {
			t.Errorf("%s: %s = %v, want %v", name, f.what, f.got, f.want)
		}
	}
	if g, w := fingerprint(got), fingerprint(want); g != w {
		t.Errorf("%s: rendered audio %s, want %s", name, g, w)
	}
}

func TestScoreMoodReachesTheEngineAsDerived(t *testing.T) {
	c, m := lgRuntime(t)

	for _, seed := range []uint64{1, 7, 42} {
		for _, depth := range []int{-5, -2, 0, 1, 2, 3, 5, 8} {
			for _, tension := range []float64{0, 0.3, 0.6, 1} {
				for _, mood := range engine.MoodNames() {
					// Name the mood explicitly rather than going through the
					// signed-depth sugar, which cannot express overworld at
					// elevation 0 (-0 is 0, so it routes to dungeon).
					want, _ := engine.DeriveMood(mood, seed, depth, tension)
					name := fmt.Sprintf("%s/seed%d/depth%d/tension%.1f", mood, seed, depth, tension)

					// A fresh base per case, so a field the lg map forgets to
					// set shows up as a mismatch instead of leaking the
					// previous case's value.
					m.params = engine.DeriveDungeon(1, 0, 0)
					expr := fmt.Sprintf("(ambience/score-mood! :%s %d %d %v)", mood, seed, depth, tension)
					if _, err := c.Run(expr); err != nil {
						t.Fatalf("%s: %v", name, err)
					}
					got := m.params

					checkParams(t, name, got, want)
				}
			}
		}
	}
}

// A partial map must merge over current state rather than reset it: that is
// the contract that makes live tweaking from the console one call.
func TestSetParametersMergesPartialMap(t *testing.T) {
	c, m := lgRuntime(t)
	if _, err := c.Run("(ambience/score! 7 4 0.6)"); err != nil {
		t.Fatal(err)
	}
	before := m.params

	if _, err := c.Run("(undertone/set-parameters {:bpm 42.0 :unknown-key :ignored})"); err != nil {
		t.Fatal(err)
	}
	if m.params.BPM != 42 {
		t.Errorf("bpm = %v, want 42", m.params.BPM)
	}
	if m.params.Root != before.Root || m.params.LeadName != before.LeadName ||
		m.params.BellDensity != before.BellDensity {
		t.Errorf("partial map disturbed untouched fields: %+v", m.params)
	}
}

// An int where the engine wants a float is the likeliest thing a hand-written
// derivation or a console call gets wrong; accepting both is deliberate.
func TestSetParametersAcceptsIntForFloat(t *testing.T) {
	c, m := lgRuntime(t)
	if _, err := c.Run("(undertone/set-parameters {:bpm 60})"); err != nil {
		t.Fatal(err)
	}
	if m.params.BPM != 60 {
		t.Errorf("bpm = %v, want 60", m.params.BPM)
	}
}

// The write-only rule: the surface has no reader, so game
// logic cannot branch on audio state and console noodling cannot taint replay.
func TestSurfaceIsWriteOnly(t *testing.T) {
	for _, name := range []string{"undertone/get-parameters", "undertone/parameters", "undertone/playing?"} {
		c, _ := lgRuntime(t)
		if _, err := c.Run("(" + name + ")"); err == nil {
			t.Errorf("%s resolved; the surface must stay write-only", name)
		}
	}
}
