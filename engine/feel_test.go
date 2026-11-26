package engine

import (
	"bytes"
	"math"
	"runtime"
	"testing"
)

// feelGrid has a lead note on every row and a bass note on every other, so
// each eighth of the bar is struck.
func feelGrid() *Grid {
	g := &Grid{}
	for i := range g.Lead {
		g.Lead[i] = 1 + i%5
		if i%2 == 0 {
			g.Bass[i] = 1
		}
	}
	return g
}

// renderFeel plays feelGrid for two patterns in p with the given feel, and
// returns the audio and the notes.
func renderFeel(p Params, feel float64, grid *Grid) ([]byte, []NoteEvent) {
	p.Feel = feel
	s := NewSynth(p)
	s.SetGrid(grid)
	buf := make([]byte, 2*patternBytes(p))
	s.Read(buf)
	evs, _, _ := s.NoteEvents(0, nil)
	return buf, evs
}

// Feel is seeded: the same params render the same audio, and Feel changes it.
func TestFeelIsDeterministicAndHeard(t *testing.T) {
	p := DeriveOverworld(3, 2, 0)
	a, _ := renderFeel(p, 1, feelGrid())
	b, _ := renderFeel(p, 1, feelGrid())
	if !bytes.Equal(a, b) {
		t.Error("two renders with the same seed and feel differ")
	}
	plain, _ := renderFeel(p, 0, feelGrid())
	if bytes.Equal(a, plain) {
		t.Error("feel 1 renders the same audio as feel 0")
	}
}

// Generated music has no authored notes, so Feel leaves it bit for bit.
func TestFeelLeavesGeneratedMusicAlone(t *testing.T) {
	for _, mood := range MoodNames() {
		p, _ := DeriveMood(mood, 5, 2, 0.5)
		a, _ := renderFeel(p, 0, nil)
		b, _ := renderFeel(p, 1, nil)
		if !bytes.Equal(a, b) {
			t.Errorf("%s: feel changed generated music", mood)
		}
	}
}

// A note's level follows its place in the bar: at Feel 1 every downbeat is
// louder than every offbeat, in both lanes, and at Feel 0 they are all one
// level.
func TestFeelAccentsTheBar(t *testing.T) {
	p := DeriveOverworld(3, 2, 0)
	for _, feel := range []float64{0, 1} {
		_, evs := renderFeel(p, feel, feelGrid())
		for _, layer := range []int{3, 7} { // lead, bass
			lo, hi := math.Inf(1), math.Inf(-1)    // every note
			down, off := math.Inf(1), math.Inf(-1) // quietest downbeat, loudest offbeat
			n := 0
			for _, e := range evs {
				if e.Layer != layer {
					continue
				}
				n++
				lo, hi = min(lo, e.Gain), max(hi, e.Gain)
				switch {
				case e.Row%8 == 0:
					down = min(down, e.Gain)
				case e.Row%2 == 1:
					off = max(off, e.Gain)
				}
			}
			if n == 0 {
				t.Fatalf("layer %d played no notes", layer)
			}
			switch {
			case feel == 0 && lo != hi:
				t.Errorf("feel 0, layer %d: gains run %v to %v, want one level", layer, lo, hi)
			case feel == 1 && layer == 3 && down <= off:
				t.Errorf("feel 1, lead: quietest downbeat %v is not above the loudest offbeat %v", down, off)
			case feel == 1 && lo == hi:
				t.Errorf("feel 1, layer %d: every note at %v", layer, lo)
			case feel == 1 && (hi/lo > feelMax/feelMin || hi/lo < 1.3):
				t.Errorf("feel 1, layer %d: gains run %v to %v", layer, lo, hi)
			}
		}
	}
}

// The touch reads a patch by its shape. The zero touch is the patch as
// written: every factor exactly 1, which is what keeps Feel 0 bit for bit.
func TestTouchUnit(t *testing.T) {
	for _, op := range []int{opEnv, opOsc, opLpf, opOut, opSvf, opPluck} {
		for chain := range 3 {
			if f := (touch{}).unit(op, chain); f != 1 {
				t.Errorf("zero touch, op %d chain %d: factor %v", op, chain, f)
			}
		}
	}
	soft := touch{bright: -0.2, ring: -0.1} // brightness 0.8, ring 0.9
	for _, c := range []struct {
		name      string
		op, chain int
		want      float64
	}{
		{"the fundamental's envelope rings shorter", opEnv, 0, 0.9},
		{"an upper partial's dies sooner still", opEnv, 1, 0.9 * 0.8},
		{"a lowpass closes", opLpf, 0, 0.8 * 0.8},
		{"the fundamental keeps its level", opOut, 0, 1},
		{"an upper partial is quieter", opOut, 2, 0.8 * 0.8},
		{"an svf is left to Open", opSvf, 0, 1},
		{"an osc has no factor", opOsc, 1, 1},
	} {
		if got := soft.unit(c.op, c.chain); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("%s: factor %v, want %v", c.name, got, c.want)
		}
	}
}

// A voice carries its touch into its units: a soft note on a chime has
// quieter upper partials, and a detuned one a different pitch.
func TestVoiceTakesItsTouch(t *testing.T) {
	var v Voice
	v.touch = touch{bright: -0.3, detune: 0.05}
	v.trigger(chime, 72, 1, 1, 1)
	outs := 0
	for i, u := range chime {
		if u.op != opOut {
			continue
		}
		want := 1.0
		if outs > 0 {
			want = 0.7 * 0.7
		}
		if got := v.units[i].tf; math.Abs(got-want) > 1e-12 {
			t.Errorf("out %d: factor %v, want %v", outs, got, want)
		}
		outs++
	}
	if v.detune != 0.05 {
		t.Errorf("detune %v", v.detune)
	}
	// the next note, with no touch, is the patch again
	v.touch = touch{}
	v.trigger(chime, 72, 1, 1, 2)
	for i := range chime {
		// an out glides there from the touched note's level; the rest are there at once
		if u := v.units[i]; u.tfTo != 1 || (chime[i].op != opOut && u.tf != 1) {
			t.Fatalf("unit %d keeps factor %v (to %v) after a plain note", i, u.tf, u.tfTo)
		}
	}
	if v.detune != 0 {
		t.Errorf("a plain note is detuned %v", v.detune)
	}
}

func TestFeelIsClamped(t *testing.T) {
	for in, want := range map[float64]float64{-1: 0, 0.4: 0.4, 3: 1, math.NaN(): 0} {
		if got := (Params{Feel: in}).sanitized().Feel; got != want {
			t.Errorf("feel %v sanitized to %v, want %v", in, got, want)
		}
	}
}

// Every lead voice still sounds through the whole range of touches, with no
// NaN from a factor at its extreme.
func TestFeelEveryLeadSounds(t *testing.T) {
	for name := range leadLib {
		p := DeriveOverworld(9, 2, 0)
		ApplyLead(&p, name)
		ApplyBass(&p, name)
		buf, evs := renderFeel(p, 1, feelGrid())
		if v := rms(buf); !(v > 1e-3 && v < 1) {
			t.Errorf("%s: rms %v", name, v)
		}
		if len(evs) == 0 {
			t.Errorf("%s: no notes", name)
		}
	}
}

// Striking authored notes with Feel allocates nothing on the audio thread.
func TestFeelDoesNotAllocate(t *testing.T) {
	old := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = old }()

	p := DeriveOverworld(3, 2, 0)
	p.Feel = 1
	s := NewSynth(p)
	s.SetGrid(feelGrid())
	buf := make([]byte, benchSlice*4)
	s.Read(buf) // the grid lands
	before := readAllocs()
	for range 2 * rows * s.samplesPerRow / benchSlice {
		s.Read(buf)
	}
	if n := readAllocs() - before; n != 0 {
		t.Errorf("%d allocations inside Synth.Read with feel, want 0", n)
	}
}

// A touched note struck over one still ringing glides to its level: the
// gain and an upper partial's level both arrive within a few milliseconds,
// with no step in one sample. An untouched note steps, as it always has.
func TestTouchedNoteGlidesOverARingingOne(t *testing.T) {
	var v Voice
	v.touch = touch{bright: -0.2}
	v.trigger(chime, 72, 4, 0.8, 1)
	var l, r float64
	var b sends
	for range 2000 {
		v.run(&l, &r, &b)
	}
	v.touch = touch{bright: 0.2}
	v.trigger(chime, 74, 4, 1.4, 2)
	if v.gain != 0.8 || v.gainTo != 1.4 {
		t.Fatalf("gain %v gliding to %v, want 0.8 to 1.4", v.gain, v.gainTo)
	}
	out := -1
	for i, u := range chime {
		if u.op == opOut {
			out = i // the last out: an upper partial
		}
	}
	if u := v.units[out]; math.Abs(u.tf-0.64) > 1e-12 || math.Abs(u.tfTo-1.44) > 1e-12 {
		t.Fatalf("upper partial at %v gliding to %v, want 0.64 to 1.44", u.tf, u.tfTo)
	}
	for range 2 * fadeFrames {
		v.run(&l, &r, &b)
	}
	if u := v.units[out]; v.gain != 1.4 || u.tf != u.tfTo {
		t.Errorf("after the glide: gain %v, upper partial %v of %v", v.gain, u.tf, u.tfTo)
	}

	var plain Voice
	plain.trigger(chime, 72, 4, 0.8, 1)
	plain.run(&l, &r, &b)
	plain.trigger(chime, 74, 4, 1.4, 2)
	if plain.gain != 1.4 {
		t.Errorf("an untouched note's gain is %v at once, want 1.4", plain.gain)
	}
}

// A SetParametersNow with nothing changed, landing in a pattern's last row,
// leaves the note sounding there at its level: the redraw draws that note's
// touch as the step did, by the pattern it is in, not the one coming.
func TestFeelRedrawKeepsTheLastRowsNote(t *testing.T) {
	p := DeriveOverworld(3, 2, 0)
	p.Feel = 1
	g := &Grid{}
	g.Lead[rows-1] = 3
	s := NewSynth(p)
	s.SetGrid(g)
	buf := make([]byte, 4)
	for s.stepRow != rows-1 || s.row != 0 { // into the last row: the sequencer is on the next pattern
		s.Read(buf)
	}
	for s.late[laneLead].left != 0 { // the note starts a little after its row
		s.Read(buf)
	}
	for range fadeFrames {
		s.Read(buf)
	}
	v := &s.voices[trackLead]
	want, touched := v.gain, s.touch[laneLead][rows-1]
	s.applyNow(&paramCmd{p: s.params})
	if v.gainTo != want {
		t.Errorf("the sounding note glides from %v to %v after a redraw with nothing changed", want, v.gainTo)
	}
	if got := s.touch[laneLead][rows-1]; got != touched {
		t.Errorf("the note's touch was redrawn as %+v, was %+v", got, touched)
	}
}
