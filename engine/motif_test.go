package engine

import (
	"bytes"
	"io"
	"runtime"
	"testing"
)

// patternChord repeats buildPatterns' first draw; the lines read back from a
// fresh pattern and written again must give the same notes.
func TestPatternChordMatches(t *testing.T) {
	for _, mood := range MoodNames() {
		for _, seed := range []uint64{0, 1, 7, 42, 1<<63 + 5} {
			for _, tension := range []float64{0, 0.5, 1} {
				p, _ := DeriveMood(mood, seed, 3, tension)
				p = p.sanitized()
				for pat := range 40 {
					fresh := buildPatterns(p, pat)
					got := fresh
					var m motifState
					m.lead, m.pad = freshLead(&p, pat, &fresh), freshPad(&p, pat, &fresh)
					m.prevLead, m.prevPad = m.lead, m.pad
					tm := Temperament{} // no restlessness: every line repeats
					p.Temperament = &tm
					m.lens[secHold] = 100
					m.sec = secHold
					var drums [nDrums][rows]drumHit
					m.reshape(&p, pat, &got, &drums)
					p.Temperament = nil
					for _, tr := range []int{2, 3} {
						for i := range rows {
							if fresh[tr][i].note != got[tr][i].note {
								t.Fatalf("%s seed %d tension %v pattern %d track %d row %d: fresh %d, read back %d",
									mood, seed, tension, pat, tr, i, fresh[tr][i].note, got[tr][i].note)
							}
						}
					}
				}
			}
		}
	}
}

func renderFor(p Params, seconds float64) []byte {
	s := NewSynth(p)
	var b bytes.Buffer
	io.CopyN(&b, s, int64(seconds*SampleRate)*4)
	return b.Bytes()
}

func TestMotifSeeded(t *testing.T) {
	tm, _ := TemperamentFor("rust")
	p, _ := DeriveMood("rust", 7, 3, 0.5)
	p.Temperament = &tm
	a, b := renderFor(p, 10), renderFor(p, 10)
	if !bytes.Equal(a, b) {
		t.Fatal("the same seed rendered two ways with a temperament")
	}
	p.Temperament = nil
	if bytes.Equal(a, renderFor(p, 10)) {
		t.Fatal("a temperament changed nothing")
	}
}

// The arc visits every section it planned, and a lead line stays in the
// generator's range whatever the mutations do to it.
func TestMotifArcAndBounds(t *testing.T) {
	for _, mood := range []string{"dungeon", "rust"} {
		tm, _ := TemperamentFor(mood)
		p, _ := DeriveMood(mood, 11, 2, 0.4)
		p.Temperament = &tm
		p = p.sanitized()
		var m motifState
		seen := map[string]bool{}
		n := len(p.Scale)
		for pat := range 200 {
			pats, drums := buildPatterns(p, pat), buildDrums(p, pat)
			m.boundary(&p, pat, &pats, &drums)
			seen[m.sec.String()] = true
			for i := range rows {
				if d := int(m.lead.deg[i]); m.lead.on[i] && (d < 3 || d > 2*n+3) {
					t.Fatalf("%s: lead degree %d out of range", mood, d)
				}
			}
		}
		for _, sec := range []string{"build", "hold", "thin", "break", "return"} {
			if !seen[sec] {
				t.Errorf("%s: the arc never reached %s", mood, sec)
			}
		}
	}
}

func TestMotifDoesNotAllocate(t *testing.T) {
	old := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = old }()
	var synths []*Synth
	for _, mood := range []string{"dungeon", "rust"} {
		tm, _ := TemperamentFor(mood)
		p, _ := DeriveMood(mood, 7, 4, 0.6)
		p.Temperament = &tm
		synths = append(synths, NewSynth(p))
	}
	buf := make([]byte, benchSlice*4)
	before := readAllocs()
	for _, s := range synths {
		for range 5 * rows * s.samplesPerRow / benchSlice {
			s.Read(buf)
		}
	}
	if n := readAllocs() - before; n != 0 {
		t.Errorf("%d allocations inside Synth.Read with a temperament, want 0", n)
	}
}

// The boundary's own cost, beside what a pattern costs to render.
func BenchmarkMotifBoundary(b *testing.B) {
	tm, _ := TemperamentFor("rust")
	p, _ := DeriveMood("rust", 7, 3, 0.5)
	p.Temperament = &tm
	p = p.sanitized()
	var m motifState
	pats, drums := buildPatterns(p, 0), buildDrums(p, 0)
	b.ResetTimer()
	for i := range b.N {
		m.boundary(&p, i, &pats, &drums)
	}
}
