package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
)

// RenderVersion stamps what the engine renders. Bump it with any change that
// alters a take's samples by more than TestRenderVersionGolden's tolerance —
// a patch, a unit's DSP, the mix — so a page caching a take's Measurement by
// its Hash re-measures it.
//
// A hand-kept number rather than the module's version: a module version
// changes with every commit, docs included, and would throw away every cached
// measurement each time, while a build with a replace directive has no
// version at all. The golden test is what keeps the number honest.
const RenderVersion = 3

// MaxTakeSeconds bounds a take's render: a minute of stereo is 10 MB of PCM.
const MaxTakeSeconds = 60

// Take is a render of one-shots over a bed, from a fresh synth: what the sfx
// studio and board audition and measure. Its samples are a function of its
// fields, the patches its shots resolve to, and RenderVersion, and Hash
// digests exactly those. (The lead's patch is already in Params.Palette.)
type Take struct {
	Params    Params  // the bed; a silent one leaves the shots alone, though they still feed its delay and room
	MusicGain float64 // as SetMusicGain
	Shots     []Shot  // all triggered at the start; each one's DelayMS places it
	Seconds   float64 // clamped to 0..MaxTakeSeconds
}

func (t Take) frames() int {
	return int(min(max(t.Seconds, 0), MaxTakeSeconds) * SampleRate)
}

// Render renders the take as interleaved stereo 16-bit samples, from a fresh
// synth, so it never disturbs one that is playing.
func (t Take) Render() []int16 {
	s := NewSynth(t.Params)
	s.SetMusicGain(t.MusicGain)
	for _, sh := range t.Shots {
		s.TriggerShot(sh)
	}
	// In chunks through one small buffer: a minute's take is 10 MB of
	// samples, and a whole-take byte buffer beside it would double that.
	out := make([]int16, 2*t.frames())
	var buf [4096 * 4]byte
	for i := 0; i < len(out); {
		b := buf[:min(len(buf), 2*(len(out)-i))]
		_, _ = s.Read(b)
		for k := 0; k < len(b); k, i = k+2, i+1 {
			out[i] = int16(binary.LittleEndian.Uint16(b[k:]))
		}
	}
	return out
}

// Hash is a stable digest of what decides the take's samples, so a page can
// cache a take's Measurement and re-measure only when it changes: the
// RenderVersion, the bed's params, the music gain, the length, each shot and
// the patch its name resolves to now. The resolved patch rather than the
// name: a registered shot re-registered with new units keeps its name but not
// its sound.
//
// Equal hashes mean equal renders on one architecture. Across architectures a
// render can differ in its last bits (Go fuses multiply-adds on arm64), far
// below anything a Measurement shows.
func (t Take) Hash() string {
	h := sha256.New()
	// %#v writes every field, unexported ones and new ones included, with
	// floats in their shortest exact form. A pointer would write its address,
	// so the temperament's pointers are written as what they point to.
	fmt.Fprintf(h, "undertone take v%d\n", RenderVersion)
	p := t.Params
	p.Phrasing, p.Memory, p.MoodPhrasing, p.MoodMemory = nil, nil, nil, nil
	fmt.Fprintf(h, "frames %d\ngain %v\nparams %#v\n", t.frames(), math.Float64bits(t.MusicGain), p)
	for _, ph := range []*Phrasing{t.Params.Phrasing, t.Params.MoodPhrasing} {
		if ph != nil {
			fmt.Fprintf(h, "phrasing %#v\n", *ph)
		} else {
			fmt.Fprintln(h, "phrasing none")
		}
	}
	for _, m := range []*Memory{t.Params.Memory, t.Params.MoodMemory} {
		if m != nil {
			fmt.Fprintf(h, "memory %#v\n", *m)
		} else {
			fmt.Fprintln(h, "memory none")
		}
	}
	for _, sh := range t.Shots {
		fmt.Fprintf(h, "shot %#v\n", sh)
		if d, ok := lookupShot(sh.Name); ok {
			fmt.Fprintf(h, "patch %#v\n", d)
		} else {
			fmt.Fprintln(h, "patch none")
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
