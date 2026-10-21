package letgo

import (
	"crypto/sha256"
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
// seconds from the current parameters and music gain, applying script on the way, writes
// a WAV to path unless path is nil, and returns the SHA-256 of the PCM as
// hex, so an lg test can pin a render without reading the file back.
func (m *Music) renderWAV(path vm.Value, seconds float64, script vm.Value) (vm.Value, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	steps, err := parseScript(script, m.params, seconds)
	if err != nil {
		return vm.NIL, fmt.Errorf("undertone/render-wav: %w", err)
	}
	src := &scripted{s: engine.NewSynth(m.params), steps: steps}
	if m.gainSet {
		src.s.SetMusicGain(m.musicGain)
	}
	sum := sha256.New()
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
	frame int
}

func (r *scripted) Read(p []byte) (int, error) {
	n := 0
	for n+4 <= len(p) {
		for len(r.steps) > 0 && r.steps[0].frame <= r.frame {
			r.steps[0].apply(r.s)
			r.steps = r.steps[1:]
		}
		end := n + (len(p)-n)/4*4
		if len(r.steps) > 0 {
			end = min(end, n+4*(r.steps[0].frame-r.frame))
		}
		r.s.Read(p[n:end])
		r.frame += (end - n) / 4
		n = end
	}
	return n, nil
}

// parseScript reads [[seconds op arg] ...], where op is :set-parameters,
// :set-parameters-now, :play-theme or :trigger and arg is what that native
// takes. Steps run in time order, within the render.
func parseScript(v vm.Value, p engine.Params, seconds float64) ([]step, error) {
	if v == vm.NIL {
		return nil, nil
	}
	vec, ok := v.(vm.ArrayVector)
	if !ok {
		return nil, fmt.Errorf("script must be a vector of [seconds op arg]")
	}
	var steps []step
	last := 0.0
	for i, e := range vec {
		call, ok := e.(vm.ArrayVector)
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
		default:
			return nil, fmt.Errorf("step %d: op must be :set-parameters, :set-parameters-now, :play-theme or :trigger, got %v", i, call[1])
		}
		steps = append(steps, step{int(t * engine.SampleRate), apply})
	}
	return steps, nil
}

// checkShots holds a script's trigger to more than a live one, which drops
// what it cannot play: a hash pinned on a dropped shot would pin silence.
func checkShots(arg vm.Value, shs []engine.Shot) error {
	want := 1
	if vec, ok := arg.(vm.ArrayVector); ok {
		want = len(vec)
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
