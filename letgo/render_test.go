package letgo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math"
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

// A render starts at the current music gain, as music-start does, so a game
// can render its shots dry.
func TestRenderWAVHonorsTheMusicGain(t *testing.T) {
	c := renderLg(t, "rendergain")
	const shot = `[[0.5 :trigger {:shot :hit}]]`
	full, err := c.Run(`(undertone/render-wav nil 2.0 ` + shot + `)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(`(undertone/set-music-gain 0.0)`); err != nil {
		t.Fatal(err)
	}
	dry, err := c.Run(`(undertone/render-wav nil 2.0 ` + shot + `)`)
	if err != nil {
		t.Fatal(err)
	}
	s := engine.NewSynth(engine.DeriveDungeon(7, 2, 0))
	s.SetMusicGain(0)
	pcm := make([]byte, 2*engine.SampleRate*4)
	s.Read(pcm[:engine.SampleRate/2*4])
	s.TriggerShot(engine.Shot{Name: "hit", Gain: 1, Pan: 0.5})
	s.Read(pcm[engine.SampleRate/2*4:])
	if dry == full || dry != vm.String(digest(pcm)) {
		t.Errorf("with the music gain at 0 the render is %v, want the dry shot %v", dry, digest(pcm))
	}
}

// So does the sfx gain: at 0 a shot leaves only the music.
func TestRenderWAVHonorsTheSfxGain(t *testing.T) {
	c := renderLg(t, "rendersfxgain")
	bed, err := c.Run(`(undertone/render-wav nil 2.0 [])`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(`(undertone/set-sfx-gain 0.0)`); err != nil {
		t.Fatal(err)
	}
	muted, err := c.Run(`(undertone/render-wav nil 2.0 [[0.5 :trigger {:shot :hit}]])`)
	if err != nil {
		t.Fatal(err)
	}
	if muted != bed {
		t.Errorf("with the sfx gain at 0 the render is %v, want the bed alone %v", muted, bed)
	}
}

// The glide natives refuse a negative or NaN glide, for either bus.
func TestGlideGainRejectsNegativeSeconds(t *testing.T) {
	c := renderLg(t, "glidebad")
	for _, bad := range []string{
		`(undertone/glide-music-gain 0.5 -1)`,
		`(undertone/glide-sfx-gain 0.5 -1)`,
		`(undertone/glide-sfx-gain 0.5 ##NaN)`,
	} {
		if _, err := c.Run(bad); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
	if _, err := c.Run(`(undertone/glide-sfx-gain 0.5 0.2)`); err != nil {
		t.Errorf("a good glide: %v", err)
	}
}

// A :cut step stops its group as Synth.Cut does: the run's waiting notes
// never play, so the render matches one without them, and a :group read from
// a keyword names the same group as one from a string.
func TestRenderWAVCutStopsItsGroup(t *testing.T) {
	c := renderLg(t, "rendercut")
	run := `[[0.5 :trigger [{:shot :hit :gain 0.8}
	                     {:shot :footstep :note 43 :delay-ms 400 :group :run}
	                     {:shot :footstep :note 43 :delay-ms 600 :group "run"}]]
	         [0.6 :cut {:group :run :fade-ms 20}]]`
	got, err := c.Run(`(undertone/render-wav nil 2.0 ` + run + `)`)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := c.Run(`(undertone/render-wav nil 2.0 [[0.5 :trigger [{:shot :hit :gain 0.8}]]])`)
	if got != want {
		t.Errorf("cut render %v, want the render without the run %v", got, want)
	}
	uncut, _ := c.Run(`(undertone/render-wav nil 2.0 [[0.5 :trigger [{:shot :hit :gain 0.8} {:shot :footstep :note 43 :delay-ms 400 :group :run}]]])`)
	if uncut == want {
		t.Error("the run's footstep changed nothing: the test proves nothing")
	}
	for _, bad := range []string{
		`(undertone/render-wav nil 2.0 [[0.5 :cut {:fade-ms 20}]])`,
		`(undertone/render-wav nil 2.0 [[0.5 :cut {:group ""}]])`,
		`(undertone/render-wav nil 2.0 [[0.5 :cut :run]])`,
		`(undertone/cut :run)`,
	} {
		if _, err := c.Run(bad); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
	if _, err := c.Run(`(undertone/cut {:group :run})`); err != nil {
		t.Errorf("undertone/cut with no synth running: %v, want a silent no-op", err)
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
		`(undertone/render-wav nil -1)`,
		`(undertone/render-wav "out.wav" -1)`,
		`(undertone/render-wav nil 1e9)`,
		`(undertone/render-wav nil 2.0 [[2.0 :set-parameters-now {:mood :rust}]])`,
		`(undertone/render-wav nil 2.0 [[1.0 :trigger {:shto :hit}]])`,
		`(undertone/render-wav nil 2.0 [[1.0 :trigger :hit]])`,
		`(undertone/render-wav nil 2.0 [[1.0 :trigger [{:shot :hit} {:gain 1}]]])`,
		`(undertone/render-wav nil 2.0 [[1.0 :trigger {:shot :hti}]])`,
	} {
		if _, err := c.Run(bad); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}

// A scripted render serves reads of any length, landing its steps on the
// same frames as aligned reads do.
func TestScriptedReadAnyLength(t *testing.T) {
	p := engine.DeriveDungeon(7, 2, 0)
	mk := func() *scripted {
		q := p
		q.Root = p.Root + 5
		return &scripted{s: engine.NewSynth(p), steps: []step{{frame: 3, apply: func(s *engine.Synth) { s.SetParametersNow(q) }}}}
	}
	want := make([]byte, 4*600)
	mk().Read(want)
	for _, size := range []int{1, 3, 5, 7} {
		r, got := mk(), make([]byte, 0, len(want))
		buf := make([]byte, size)
		for len(got) < len(want) {
			n, _ := r.Read(buf[:min(size, len(want)-len(got))])
			if n == 0 {
				t.Fatalf("size %d: no progress after %d bytes", size, len(got))
			}
			got = append(got, buf[:n]...)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("size %d: stream differs from one aligned read", size)
		}
	}
}

// :until-silent renders as long as the sound lasts: a script's trigger still
// to come keeps the render going through the silence before it (2.5 s is past
// the echo hold at this tempo, so only the script can keep it going), and the WAV
// is a prefix of a fixed render with the same script.
func TestRenderWAVUntilSilent(t *testing.T) {
	c := renderLg(t, "renderuntil")
	if _, err := c.Run(`(undertone/set-music-gain 0.0)`); err != nil {
		t.Fatal(err)
	}
	const script = `[[2.5 :trigger [{:shot :footstep :gain 0.8}]]]`
	dir := t.TempDir()
	until, fixed := filepath.Join(dir, "until.wav"), filepath.Join(dir, "fixed.wav")
	if _, err := c.Run(`(undertone/render-wav "` + until + `" :until-silent ` + script + `)`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(`(undertone/render-wav "` + fixed + `" 4.0 ` + script + `)`); err != nil {
		t.Fatal(err)
	}
	u, err := os.ReadFile(until)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.ReadFile(fixed)
	secs := float64(len(u)-44) / 4 / engine.SampleRate
	if secs < 2.5 || secs > 2.9 {
		t.Errorf("a footstep at 2.5 s rendered %.3f s", secs)
	}
	if len(u) > len(f) {
		t.Fatalf("the until-silent render (%d bytes) outran the 4 s one", len(u))
	}
	if !bytes.Equal(u[44:], f[44:len(u)]) {
		t.Error("the until-silent render is not a prefix of the fixed one")
	}
	if _, err := c.Run(`(undertone/render-wav nil :until-silen)`); err == nil {
		t.Error("a misspelt keyword rendered")
	}
}

// A theme's rest is quiet but not the end: :until-silent plays an outro with
// two silent grids through to its last note, ending where a long fixed render
// stops being audible (36 s on: the outro rings out at full gain).
func TestRenderWAVUntilSilentWaitsOutATheme(t *testing.T) {
	c := renderLg(t, "renderuntiltheme")
	const script = `[[0.0 :play-theme {:grids [{:lead [1]} {:lead [0]} {:lead [0]} {:lead [5]}] :then :silence}]]`
	dir := t.TempDir()
	until, fixed := filepath.Join(dir, "until.wav"), filepath.Join(dir, "fixed.wav")
	if _, err := c.Run(`(undertone/render-wav "` + until + `" :until-silent ` + script + `)`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(`(undertone/render-wav "` + fixed + `" 60.0 ` + script + `)`); err != nil {
		t.Fatal(err)
	}
	u, err := os.ReadFile(until)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.ReadFile(fixed)
	pcm := func(b []byte) []int16 {
		out := make([]int16, (len(b)-44)/2)
		for i := range out {
			out[i] = int16(uint16(b[44+2*i]) | uint16(b[45+2*i])<<8)
		}
		return out
	}
	thr := 32767 * math.Pow(10, engine.SilentFloorDB/20)
	end := 0
	for i, v := range pcm(f) {
		if math.Abs(float64(v)) >= thr {
			end = i/2 + 1
		}
	}
	if got, want := len(pcm(u))/2, end+engine.SampleRate/100; got != want {
		t.Errorf("the outro rendered to %.3f s; it sounds until %.3f s, plus the 10 ms margin", float64(got)/engine.SampleRate, float64(want)/engine.SampleRate)
	}
}
