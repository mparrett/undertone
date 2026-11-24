package letgo

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// step is one call in a render script, applied once frame frames have been
// rendered.
type step struct {
	frame int
	apply func(*engine.Synth)
}

// renderWAV is the offline lane: it makes "same parameters and calls ->
// byte-identical audio" checkable in a test rather than by ear. It renders
// seconds from the current parameters and music gain, applying script on
// the way, writes a WAV to path unless path is nil, and returns the SHA-256
// of the PCM as hex, so an lg test can pin a render without reading the
// file back. A seconds of 0 renders until the sound is silent
// (engine.ReadUntilSilent), with the script's steps still to come counting
// as sound, up to engine.MaxTakeSeconds.
func (m *Music) renderWAV(path vm.Value, seconds float64, script vm.Value) (vm.Value, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	until := seconds == 0
	if until {
		seconds = engine.MaxTakeSeconds
	}
	steps, err := parseScript(script, m.params, seconds)
	if err != nil {
		return vm.NIL, fmt.Errorf("undertone/render-wav: %w", err)
	}
	src := &scripted{s: engine.NewSynth(m.params), steps: steps}
	m.applyGains(src.s)
	sum := sha256.New()
	if until {
		pcm := engine.ReadUntilSilent(src.s, src, seconds, engine.SilentFloorDB,
			func() bool { return len(src.steps) > 0 })
		if p, ok := path.(vm.String); ok {
			if err := engine.WritePCMWAV(string(p), pcm); err != nil {
				return vm.NIL, fmt.Errorf("undertone/render-wav: %w", err)
			}
		}
		_ = binary.Write(sum, binary.LittleEndian, pcm)
		return vm.String(hex.EncodeToString(sum.Sum(nil))), nil
	}
	switch p := path.(type) {
	case vm.String:
		err = engine.WriteWAV(string(p), io.TeeReader(src, sum), seconds)
	default:
		_, err = io.CopyN(sum, src, int64(seconds*engine.SampleRate)*4)
	}
	if err != nil {
		return vm.NIL, fmt.Errorf("undertone/render-wav: %w", err)
	}
	return vm.String(hex.EncodeToString(sum.Sum(nil))), nil
}

// scripted reads s, stopping at each step's frame to apply it, so a call
// lands on the same frame on every render.
type scripted struct {
	s     *engine.Synth
	steps []step
	at    int // bytes read so far; a step applies only between frames
}

// Read serves any length, as Synth.Read does: a reader that returns (0, nil)
// for a short tail stalls io.ReadFull and the copies WriteWAV makes.
func (r *scripted) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if r.at%4 == 0 {
			for len(r.steps) > 0 && r.steps[0].frame <= r.at/4 {
				r.steps[0].apply(r.s)
				r.steps = r.steps[1:]
			}
		}
		end := len(p)
		if len(r.steps) > 0 {
			end = min(end, n+4*r.steps[0].frame-r.at)
		}
		m, _ := r.s.Read(p[n:end])
		r.at += m
		n += m
	}
	return n, nil
}

// parseScript reads [[seconds op arg] ...], where op is :set-parameters,
// :set-parameters-now, :play-theme, :trigger or :cut and arg is what that native
// takes. Steps run in time order, within the render.
func parseScript(v vm.Value, p engine.Params, seconds float64) ([]step, error) {
	if v == vm.NIL {
		return nil, nil
	}
	vec, ok := asVec(v)
	if !ok {
		return nil, fmt.Errorf("script must be a vector of [seconds op arg]")
	}
	var steps []step
	last := 0.0
	for i, e := range vec {
		call, ok := asVec(e)
		if !ok || len(call) != 3 {
			return nil, fmt.Errorf("step %d must be [seconds op arg]", i)
		}
		var t float64
		switch x := call[0].(type) {
		case vm.Float:
			t = float64(x)
		case vm.Int:
			t = float64(x)
		default:
			return nil, fmt.Errorf("step %d: seconds must be a number", i)
		}
		if t < last || t >= seconds {
			return nil, fmt.Errorf("step %d at %vs: steps must be in time order, before the end of the %vs render", i, t, seconds)
		}
		last = t
		op, _ := call[1].(vm.Keyword)
		arg := call[2]
		var apply func(*engine.Synth)
		switch op {
		case "set-parameters", "set-parameters-now":
			lgm, ok := arg.(*vm.PersistentMap)
			if !ok {
				return nil, fmt.Errorf("step %d: %s takes a map", i, op)
			}
			if err := mergeParams(&p, lgm); err != nil {
				return nil, fmt.Errorf("step %d: %w", i, err)
			}
			q := p
			if op == "set-parameters" {
				apply = func(s *engine.Synth) { s.SetParameters(q) }
			} else {
				apply = func(s *engine.Synth) { s.SetParametersNow(q) }
			}
		case "play-theme":
			th, err := theme(arg)
			if err != nil {
				return nil, fmt.Errorf("step %d: %w", i, err)
			}
			apply = func(s *engine.Synth) { s.PlayTheme(th) }
		case "trigger":
			shs := shots(arg)
			if err := checkShots(arg, shs); err != nil {
				return nil, fmt.Errorf("step %d: %w", i, err)
			}
			apply = func(s *engine.Synth) {
				for _, sh := range shs {
					s.TriggerShot(sh)
				}
			}
		case "cut":
			group, fadeMS, err := cutArgs(arg)
			if err != nil {
				return nil, fmt.Errorf("step %d: %w", i, err)
			}
			apply = func(s *engine.Synth) { s.Cut(group, fadeMS) }
		default:
			return nil, fmt.Errorf("step %d: op must be :set-parameters, :set-parameters-now, :play-theme, :trigger or :cut, got %v", i, call[1])
		}
		steps = append(steps, step{int(t * engine.SampleRate), apply})
	}
	return steps, nil
}

// checkShots holds a script's trigger to more than a live one, which drops
// what it cannot play: a hash pinned on a dropped shot would pin silence.
func checkShots(arg vm.Value, shs []engine.Shot) error {
	want := 1
	if ix, ok := indexed(arg); ok {
		want = ix.RawCount()
	}
	if len(shs) != want || want == 0 {
		return fmt.Errorf(":trigger takes a map with :shot, or a vector of them")
	}
	for _, sh := range shs {
		if !engine.HasShot(sh.Name) {
			return fmt.Errorf(":trigger: no shot named %q", sh.Name)
		}
	}
	return nil
}
