package engine

import (
	"fmt"
	"math"
	"testing"
)

// wildNotes are notes no instrument plays, as a game's arithmetic can still
// produce them: far below and far above the keyboard, out to where the
// frequency underflows to zero or overflows.
var wildNotes = []int{-100000, -500, -60, 300, 100000, math.MaxInt32}

// playedRMS renders secs of s and returns the level of what it played.
func playedRMS(s *Synth, secs float64) float64 {
	buf := make([]byte, int(secs*SampleRate)*4)
	s.Read(buf)
	sum := 0.0
	for i := 0; i+1 < len(buf); i += 2 {
		x := float64(int16(uint16(buf[i])|uint16(buf[i+1])<<8)) / 32768
		sum += x * x
	}
	return math.Sqrt(sum / float64(len(buf)/2))
}

// A one-shot with wild numbers must neither take the audio thread down nor
// leave the mix broken behind it. A plucked string sized its delay line from
// the note's period, so a note far enough down asked for a slice too long to
// make, and panicked; a note far enough up overflowed an oscillator's
// frequency, and the NaN that made stayed in the delay line, so the music
// was silent from then on. A NaN or infinite gain reaches that line the same
// way, through a shot's send.
func TestWildShotLeavesTheMusicPlaying(t *testing.T) {
	p, _ := DeriveMood("dungeon", 7, 2, 0.5)
	plain := NewSynth(p)
	playedRMS(plain, 6)
	want := playedRMS(plain, 2)
	nan, inf := math.NaN(), math.Inf(1)
	type shot struct {
		note             int
		gain, pan, delay float64
	}
	var shots []shot
	for _, note := range wildNotes {
		shots = append(shots, shot{note, 1, 0.5, 0})
	}
	for _, x := range []float64{nan, inf, -inf, 1e300} {
		shots = append(shots, shot{60, x, 0.5, 0}, shot{60, 1, x, 0}, shot{60, 1, 0.5, x})
	}
	names := ShotNames()
	if testing.Short() {
		names = []string{"pluck", "twang", "blip"} // the two strings, and a shot with a send
	}
	for _, name := range names {
		if !HasShot(name) {
			t.Fatalf("no shot named %q", name)
		}
		for _, sh := range shots {
			t.Run(fmt.Sprintf("%s/%+v", name, sh), func(t *testing.T) {
				t.Parallel()
				s := NewSynth(p)
				s.Trigger(name, sh.note, sh.gain, sh.pan, sh.delay, 1)
				playedRMS(s, 6) // the shot and its tail
				if got := playedRMS(s, 2); math.IsNaN(got) || got < want/2 || got > want*2 {
					t.Errorf("after the shot the music plays at rms %.4f, want about %.4f", got, want)
				}
			})
		}
	}
}

// The same for the music's own notes: a scale, a lead lift or a second
// drone's offset that puts a track's note out of range.
func TestWildMusicNotesDoNotPanic(t *testing.T) {
	wild := map[string]func(*Params, int){
		"scale":        func(p *Params, n int) { p.Scale = []int{0, n, 3, 5, 7} },
		"lead-lift":    func(p *Params, n int) { p.LeadLift = n },
		"fifth-offset": func(p *Params, n int) { p.FifthOffset = n },
	}
	for what, set := range wild {
		for _, lead := range LeadNames() {
			for _, note := range wildNotes {
				t.Run(fmt.Sprintf("%s/%s/%d", what, lead, note), func(t *testing.T) {
					t.Parallel()
					p, _ := DeriveMood("village", 1, 3, 0.5)
					p.LeadDensity = 1
					ApplyLead(&p, lead)
					set(&p, note)
					if rms := playedRMS(NewSynth(p), 2); !(rms > 0) {
						t.Errorf("rendered rms %v, want sound", rms)
					}
				})
			}
		}
	}
}

// A grid's degrees are a host's numbers too.
func TestWildGridDegreeLeavesTheMusicPlaying(t *testing.T) {
	p, _ := DeriveMood("village", 1, 3, 0.5)
	for _, d := range []int{1 << 20, 1 << 40, math.MaxInt} {
		s := NewSynth(p)
		g := SeedGrid(p)
		g.Lead[0], g.Bass[1] = d, d
		s.SetGrid(&g)
		playedRMS(s, 4)
		s.SetGrid(nil)
		playedRMS(s, 6)
		if got := playedRMS(s, 2); !(got > 0.001) {
			t.Errorf("degree %d: afterwards the music plays at rms %.5f", d, got)
		}
	}
}

// The bound on a string's line leaves every string in range as it was, and
// holds one far below the keyboard to a line the audio thread can afford.
func TestPluckLineIsBoundedNotRetuned(t *testing.T) {
	line := func(note int) int {
		v := Voice{}
		v.trigger(pluck, note, 1, 1, 1)
		var l, r float64
		v.run(&l, &r, &sends{})
		return len(v.units[1].ks)
	}
	for _, note := range []int{12, 24, 43, 60} {
		want := int(SampleRate / (440 * math.Pow(2, float64(note-69)/12)))
		if got := line(note); got != want {
			t.Errorf("note %d: a line of %d samples, want %d", note, got, want)
		}
	}
	for _, note := range wildNotes {
		// 4 is pluckTuning's floor: shorter, and the allpass coefficient
		// nears -1 and the string stops decaying
		if got := line(note); got < 4 || got > maxPluckLine {
			t.Errorf("note %d: a line of %d samples, want 4 to %d", note, got, maxPluckLine)
		}
	}
}

// A held note above the range sounds at the range's top, and a mood change
// that leaves it alone must see it as the same note: comparing the voice's
// bounded note with the pattern's unbounded one restarted it every time.
func TestOutOfRangeHeldNoteIsNotRestarted(t *testing.T) {
	p, _ := bareMood("dungeon", 7, 5, 0.5)
	p.FifthOffset = 300
	s := NewSynth(p)
	playedRMS(s, 1)
	s.SetParametersNow(p)
	playedRMS(s, 2*nowBlock/float64(SampleRate))
	if v := &s.voices[trackFifth]; v.pend.note != 0 || v.gainTo == 0 {
		t.Errorf("the held fifth is being faded out for a restart (pending note %d, gain to %v)", v.pend.note, v.gainTo)
	}
}

// Two notes past the range sound the same, so the repeat gap treats them as
// one shot rather than stacking a voice for each.
func TestOutOfRangeNotesShareTheRepeatGap(t *testing.T) {
	p, _ := DeriveMood("dungeon", 7, 2, 0)
	s := NewSynth(p)
	for _, note := range []int{200, 260, 320, 100000} {
		s.Trigger("pluck", note, 1, 0.5, 0, 1)
	}
	playedRMS(s, 0.01)
	n := 0
	for i := range s.fx.shots {
		n += b2i(s.fx.shots[i].active)
	}
	if n != 1 {
		t.Errorf("%d voices sounding, want 1", n)
	}
}

// A delay out of range is dropped, not held to the bound, where a batch of
// them would sound together a minute on.
func TestTriggerDropsADelayOutOfRange(t *testing.T) {
	p, _ := DeriveMood("dungeon", 7, 2, 0)
	s := NewSynth(p)
	for delay, want := range map[float64]bool{
		0: true, -5: true, maxDelayMS: true,
		maxDelayMS + 1: false, 1e300: false, math.Inf(1): false,
	} {
		if got := s.Trigger("hit", 60, 1, 0.5, delay, 1); got != want {
			t.Errorf("delay %v ms: staged %v, want %v", delay, got, want)
		}
	}
	if s.Trigger("hit", 60, 1, 0.5, math.NaN(), 1) {
		t.Errorf("a NaN delay was staged")
	}
}
