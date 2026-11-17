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
					on := buildPatternsOn(p, pat, chordDraw(p, pat))
					for tr := range nTracks {
						for i := range rows {
							if on[tr][i].note != fresh[tr][i].note || on[tr][i].gain != fresh[tr][i].gain {
								t.Fatalf("%s seed %d pattern %d: buildPatternsOn its own chord differs", mood, seed, pat)
							}
						}
					}
					got := fresh
					// a fresh slot: the lines are read back from the pattern and
					// written again
					var m motifState
					m.nProg, m.chord, m.fresh = 1, chordDraw(p, pat), true
					tm := Temperament{}
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
// motifPattern is one boundary of the arc without a Synth.
func motifPattern(m *motifState, p *Params, pat int) ([nTracks][rows]trackRow, [nDrums][rows]drumHit) {
	m.advance(p, pat)
	sp := m.shaded(*p)
	pats, drums := buildPatternsOn(sp, pat, m.chord), buildDrums(sp, pat)
	m.reshape(&sp, pat, &pats, &drums)
	return pats, drums
}

// The arc visits every section it planned, a lead line stays in the
// generator's range whatever the mutations do to it, and a return plays the
// home cycle's chords and lines in order.
func TestMotifArcAndBounds(t *testing.T) {
	for _, mood := range []string{"dungeon", "rust"} {
		tm, _ := TemperamentFor(mood)
		p, _ := DeriveMood(mood, 11, 2, 0.3)
		p.Temperament = &tm
		p = p.sanitized()
		var m motifState
		seen := map[string]bool{}
		n := len(p.Scale)
		returns := 0
		for pat := range 300 {
			motifPattern(&m, &p, pat)
			seen[m.sec.String()] = true
			l := m.leads[m.slot]
			for i := range rows {
				if d := int(l.deg[i]); l.on[i] && (d < 3 || d > 2*n+3) {
					t.Fatalf("%s: lead degree %d out of range", mood, d)
				}
			}
			if m.sec == secReturn && m.secPos < m.homeNProg {
				returns++
				if m.slot != m.secPos || m.prog != m.homeProg || m.chord != int(m.homeProg[m.slot]) {
					t.Fatalf("%s pattern %d: return slot %d chord %d, want slot %d of %v", mood, pat, m.slot, m.chord, m.secPos, m.homeProg)
				}
				if m.homeHave[m.slot] && m.leads[m.slot] != m.homeLeads[m.slot] {
					t.Fatalf("%s pattern %d: the return changed the home line", mood, pat)
				}
			}
		}
		for _, sec := range []string{"build", "hold", "thin", "break", "return"} {
			if !seen[sec] {
				t.Errorf("%s: the arc never reached %s", mood, sec)
			}
		}
		if returns == 0 {
			t.Errorf("%s: no return", mood)
		}
	}
}

// High tension holds off the break and keeps the drums in; a rising one holds
// off the break too; and phrases shorten as tension rises.
func TestMotifTension(t *testing.T) {
	tm, _ := TemperamentFor("rust")
	phraseLen := func(tension float64) float64 {
		p, _ := DeriveMood("rust", 3, 2, tension)
		p.Temperament = &tm
		p = p.sanitized()
		var m motifState
		for pat := range 400 {
			_, drums := motifPattern(&m, &p, pat)
			if tension > 0.6 && m.sec == secBreak {
				t.Fatalf("tension %v: a break at pattern %d", tension, pat)
			}
			if tension > 0.6 {
				want := buildDrums(m.shaded(p), pat)
				for d := range drums {
					for i := range rows - 4 { // a fill may rewrite the last rows
						if drums[d][i].on != want[d][i].on {
							t.Fatalf("tension %v pattern %d (%s): drum %d row %d at %v, want %v",
								tension, pat, m.sec, d, i, drums[d][i].on, want[d][i].on)
						}
					}
				}
			}
		}
		return 400 / float64(m.phrase+1)
	}
	if lo, hi := phraseLen(0.1), phraseLen(0.9); hi >= lo*0.8 {
		t.Errorf("phrases at tension 0.9 average %.1f patterns, at 0.1 %.1f: want shorter", hi, lo)
	}

	// a ramp: tension rising every pattern never breaks; the same tension
	// held does
	breaks := func(slope float64) int {
		var m motifState
		n := 0
		for pat := range 70 {
			p, _ := DeriveMood("rust", 3, 2, 0.02+slope*float64(pat))
			p.Temperament = &tm
			p = p.sanitized()
			motifPattern(&m, &p, pat)
			if m.sec == secBreak {
				n++
			}
		}
		return n
	}
	if n := breaks(0.008); n != 0 {
		t.Errorf("%d break patterns while tension rises", n)
	}
	if breaks(0) == 0 {
		t.Error("no break at a held tension: the ramp check proves nothing")
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
	b.ReportAllocs()
	for i := range b.N {
		motifPattern(&m, &p, i)
	}
}
