package letgo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

func renderLg(t *testing.T, name string) *api.LetGo {
	t.Helper()
	SetMusic(NewMusic(engine.DeriveDungeon(7, 2, 0)))
	c, err := api.NewLetGo(name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func digest(pcm []byte) string {
	sum := sha256.Sum256(pcm)
	return hex.EncodeToString(sum[:])
}

// With no script, render-wav renders the current parameters as before, writes
// them as the WAV's data, and returns their hash.
func TestRenderWAVReturnsTheHashOfWhatItWrote(t *testing.T) {
	c := renderLg(t, "renderplain")
	path := filepath.Join(t.TempDir(), "out.wav")
	v, err := c.Run(`(undertone/render-wav "` + path + `" 2.0)`)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, 2*engine.SampleRate*4)
	engine.NewSynth(engine.DeriveDungeon(7, 2, 0)).Read(want)
	if v != vm.String(digest(want)) {
		t.Errorf("hash %v, want the hash of a plain render", v)
	}
	wav, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wav[44:], want) {
		t.Error("the WAV's data is not the plain render")
	}
	if nilPath, _ := c.Run(`(undertone/render-wav nil 2.0)`); nilPath != v {
		t.Errorf("with a nil path the hash is %v, want %v", nilPath, v)
	}
}

// A script's calls land on their frames: the render matches a synth given the
// same calls by hand at those frames, and renders the same every time.
func TestRenderWAVScriptLandsOnItsFrames(t *testing.T) {
	c := renderLg(t, "renderscript")
	const script = `[[0.5 :play-theme {:grids [{:lead [1 0 3 0 5] :drums ["X...x..."]}] :then :silence}]
	                 [1.25 :trigger [{:shot :hit :gain 0.8 :pan 0.3}]]
	                 [2.0 :set-parameters-now {:mood :rust}]]`
	got, err := c.Run(`(undertone/render-wav nil 3.0 ` + script + `)`)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := c.Run(`(undertone/render-wav nil 3.0 ` + script + `)`); again != got {
		t.Errorf("the same script rendered %v, then %v", got, again)
	}

	p := engine.DeriveDungeon(7, 2, 0)
	s := engine.NewSynth(p)
	pcm := make([]byte, 3*engine.SampleRate*4)
	at := func(sec float64) int { return int(sec*engine.SampleRate) * 4 }
	s.Read(pcm[:at(0.5)])
	var g engine.Grid
	g.Lead[0], g.Lead[2], g.Lead[4] = 1, 3, 5
	g.Drums[0] = "X...x..."
	s.PlayTheme(&engine.Theme{Grids: []engine.Grid{g}, Then: engine.ThenSilence})
	s.Read(pcm[at(0.5):at(1.25)])
	s.TriggerShot(engine.Shot{Name: "hit", Gain: 0.8, Pan: 0.3})
	s.Read(pcm[at(1.25):at(2.0)])
	rust := p
	if d, ok := engine.DeriveMood("rust", 1, 0, 0); ok {
		rust.Palette, rust.LeadName, rust.LeadGate = d.Palette, d.LeadName, d.LeadGate
	}
	s.SetParametersNow(rust)
	s.Read(pcm[at(2.0):])
	if want := vm.String(digest(pcm)); got != want {
		t.Errorf("scripted render %v, want the by-hand render %v", got, want)
	}
	if plain, _ := c.Run(`(undertone/render-wav nil 3.0)`); plain == got {
		t.Error("the script changed nothing")
	}
}

// A bad script is an error, since it is a bug in the test that wrote it.
func TestRenderWAVRejectsBadScripts(t *testing.T) {
	c := renderLg(t, "renderbad")
	for _, bad := range []string{
		`(undertone/render-wav nil 2.0 {:at 0})`,
		`(undertone/render-wav nil 2.0 [[0.0 :play-theme]])`,
		`(undertone/render-wav nil 2.0 [[1.0 :trigger {:shot :hit}] [0.5 :trigger {:shot :hit}]])`,
		`(undertone/render-wav nil 2.0 [[3.0 :trigger {:shot :hit}]])`,
		`(undertone/render-wav nil 2.0 [[0.0 :music-start nil]])`,
		`(undertone/render-wav nil 2.0 [[0.0 :play-theme {:grids [{:lead [9]}]}]])`,
		`(undertone/render-wav nil 2.0 [[0.0 :set-parameters {:palette :nope}]])`,
		`(undertone/render-wav 7 2.0)`,
	} {
		if _, err := c.Run(bad); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}
