package engine

import (
	"fmt"
	"slices"
	"testing"
)

// fiveNoteSpec is village with pentatonic scales: no built-in mood uses one
// yet, so the test brings its own. Village's drone always plays, which marks
// each pattern's chord, and its lead lift is an octave.
func fiveNoteSpec() MoodSpec {
	s, _ := MoodByName("village")
	s.Name = "pentavillage"
	s.Scales = [][]int{majorPentatonic, minorPentatonic}
	return s
}

// A degree counts the scale's own tones and wraps by octave (ADR-001).
func TestDegreeNoteWrapsByOctave(t *testing.T) {
	for _, c := range []struct {
		scale []int
		want  []int // degrees 1..8 above base 60
	}{
		{scalesByDarkness[0], []int{60, 62, 63, 65, 67, 69, 70, 72}},
		{majorPentatonic, []int{60, 62, 64, 67, 69, 72, 74, 76}},
		{minorPentatonic, []int{60, 63, 65, 67, 70, 72, 75, 77}},
	} {
		p := Params{Scale: c.scale}
		for d := 1; d <= Degrees; d++ {
			if got := degreeNote(p, 60, d); got != c.want[d-1] {
				t.Errorf("%v degree %d: %d, want %d", c.scale, d, got, c.want[d-1])
			}
		}
	}
	// Below the root, as the generator's chords reach: tone -2 is the
	// second-to-last tone an octave down.
	for _, c := range []struct {
		scale    []int
		i, wantT int
	}{
		{scalesByDarkness[0], -2, 9 - 12},
		{scalesByDarkness[0], -4, 5 - 12},
		{scalesByDarkness[0], -7, -12},
		{majorPentatonic, -2, 7 - 12},
		{majorPentatonic, -4, 2 - 12},
		{majorPentatonic, -5, -12},
		{majorPentatonic, -6, 9 - 24},
	} {
		if got := scaleTone(c.scale, c.i); got != c.wantT {
			t.Errorf("%v tone %d: %d, want %d", c.scale, c.i, got, c.wantT)
		}
	}
}

func TestMoodSpecScaleLengths(t *testing.T) {
	for _, c := range []struct {
		scale []int
		ok    bool
	}{
		{[]int{0, 2, 4, 7}, false},
		{majorPentatonic, true},
		{[]int{0, 3, 5, 6, 7, 10}, true},
		{scalesByDarkness[0], true},
		{[]int{0, 2, 3, 5, 6, 8, 9, 11}, false},
	} {
		s := fiveNoteSpec()
		s.Scales = [][]int{c.scale}
		if _, err := s.Derive(1, 0, 0); (err == nil) != c.ok {
			t.Errorf("scale %v: err %v, want accepted %v", c.scale, err, c.ok)
		}
	}
}

// A five-note mood derives and plays at every level and tension, and every
// pad and lead note the generator writes is a tone of the scale above the
// pattern's chord.
func TestFiveNoteMoodRenders(t *testing.T) {
	spec := fiveNoteSpec()
	for _, level := range []int{0, 1, 2, 3, 5, 8} {
		for _, tension := range []float64{0, 0.3, 0.6, 1} {
			name := fmt.Sprintf("level %d tension %.1f", level, tension)
			p, err := spec.Derive(7, level, tension)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if len(p.Scale) != 5 {
				t.Fatalf("%s: scale %v", name, p.Scale)
			}
			if !nonSilent(render(NewSynth(p), 2*patternBytes(p)/4)) {
				t.Errorf("%s: rendered silence", name)
			}
			for pat := range 40 {
				out := buildPatterns(p.sanitized(), pat)
				chord := out[0][0].note + 12
				for _, tr := range []int{2, trackLead} {
					for _, r := range out[tr] {
						if r.gain == 0 {
							continue
						}
						if pc := ((r.note-chord)%12 + 12) % 12; !slices.Contains(p.Scale, pc) {
							t.Fatalf("%s pattern %d track %d: note %d is %d above the chord, outside %v", name, pat, tr, r.note, pc, p.Scale)
						}
					}
				}
			}
		}
	}
}

// A theme is written in degrees, so in a five-note mood degree 6 is the
// octave and degree 8 the third tone above it.
func TestThemePlaysInAFiveNoteScale(t *testing.T) {
	p, err := fiveNoteSpec().Derive(3, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSynth(p)
	render(s, 5*s.samplesPerRow+77)
	s.PlayTheme(themeOf(Degrees, ThenLoop))
	render(s, nowBlock)
	base, _ := GridBases(s.params)
	for d := 1; d <= Degrees; d++ {
		if d > 1 && !untilRow0(s) {
			t.Fatalf("grid %d never started", d)
		}
		want := base + 12*((d-1)/5) + p.Scale[(d-1)%5]
		if got := s.voices[trackLead].note; got != want {
			t.Errorf("degree %d plays %d, want %d", d, got, want)
		}
	}
}

// One-shots pitched by degree land in the five-note scale, and a five-note
// scale survives sanitizing where a shorter one does not.
func TestShotsInAFiveNoteScale(t *testing.T) {
	p := silentParams
	p.Scale = majorPentatonic
	if got := p.sanitized().Scale; !slices.Equal(got, majorPentatonic) {
		t.Fatalf("sanitized replaced a five-note scale with %v", got)
	}
	p.Root = 60
	s := NewSynth(p)
	for _, c := range []struct{ degree, octave, want int }{
		{0, 0, 60}, {3, 0, 67}, {5, 0, 72}, {7, -1, 64},
	} {
		if got := s.resolveDegree(c.degree, c.octave); got != c.want {
			t.Errorf("degree %d octave %d: %d, want %d", c.degree, c.octave, got, c.want)
		}
	}
	if !s.TriggerShot(Shot{Name: "pluck", Gain: 1, Pan: 0.5, Degree: 4, HasDegree: true}) {
		t.Fatal("trigger refused")
	}
	if !nonSilent(render(s, SampleRate/2)) {
		t.Error("a degree-pitched one-shot rendered silence")
	}
	short := p
	short.Scale = []int{0, 2, 4, 7}
	if got := short.sanitized().Scale; len(got) < 5 {
		t.Errorf("sanitized kept a four-note scale %v", got)
	}
}
