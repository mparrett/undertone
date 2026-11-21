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
					m.nProg, m.chord, m.fresh, m.mem = 1, chordDraw(p, pat), true, true
					p.Memory = &Memory{}
					m.lens[secHold] = 100
					m.sec = secHold
					var drums [nDrums][rows]drumHit
					m.reshape(&p, pat, &got, &drums)
					p.Memory = nil
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
	p.Phrasing, p.Memory = &tm.Phrasing, &tm.Memory
	a, b := renderFor(p, 10), renderFor(p, 10)
	if !bytes.Equal(a, b) {
		t.Fatal("the same seed rendered two ways with a temperament")
	}
	p.Phrasing, p.Memory = nil, nil
	if bytes.Equal(a, renderFor(p, 10)) {
		t.Fatal("a temperament changed nothing")
	}
}

// The arc visits every section it planned, and a lead line stays in the
// generator's range whatever the mutations do to it.
// motifPattern is one boundary of the arc without a Synth.
func motifPattern(m *motifState, p *Params, pat int) ([nTracks][rows]trackRow, [nDrums][rows]drumHit) {
	m.advance(p, pat)
	sp := *p
	if m.phr {
		sp = m.shaded(*p)
	}
	chord := -1
	if m.mem {
		chord = m.chord
	}
	pats, drums := buildPatternsOn(sp, pat, chord), buildDrums(sp, pat)
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
		p.Phrasing, p.Memory = &tm.Phrasing, &tm.Memory
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
		p.Phrasing, p.Memory = &tm.Phrasing, &tm.Memory
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
			p.Phrasing, p.Memory = &tm.Phrasing, &tm.Memory
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
		p.Phrasing, p.Memory = &tm.Phrasing, &tm.Memory
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
	p.Phrasing, p.Memory = &tm.Phrasing, &tm.Memory
	p = p.sanitized()
	var m motifState
	b.ReportAllocs()
	for i := range b.N {
		motifPattern(&m, &p, i)
	}
}

// The identity phrasing, nil or the zero value, without memory, renders bit
// for bit as the generator alone: the motif code doesn't run.
func TestIdentityPhrasingIsBitExact(t *testing.T) {
	for _, mood := range []string{"dungeon", "rust", "village"} {
		p, _ := DeriveMood(mood, 7, 2, 0.4)
		off := renderFor(p, 30)
		p.Phrasing = &Phrasing{}
		if !bytes.Equal(off, renderFor(p, 30)) {
			t.Errorf("%s: the zero Phrasing changed the render", mood)
		}
		tm, _ := TemperamentFor(mood)
		ph := tm.Phrasing
		ph.PhraseMax = 0 // the identity, whatever else is set
		p.Phrasing = &ph
		if !bytes.Equal(off, renderFor(p, 30)) {
			t.Errorf("%s: a Phrasing with PhraseMax 0 changed the render", mood)
		}
	}
}

// The trace answers with or without phrasing: under the identity it is
// phrase 0, held, however long the music runs.
func TestIdentityTrace(t *testing.T) {
	tm, _ := TemperamentFor("rust")
	for _, mem := range []*Memory{nil, &tm.Memory} {
		p, _ := DeriveMood("rust", 7, 2, 0.3)
		p.Memory = mem
		s := NewSynth(p)
		buf := make([]byte, 4096)
		for range int(90 * SampleRate / 1024) {
			s.Read(buf)
			if m := s.MotifTrace(); m.Section != "hold" || m.Phrase != 0 {
				t.Fatalf("memory %v: trace %+v, want phrase 0 in a hold", mem != nil, m)
			}
		}
	}
}

// Phrasing without memory arranges and colours the generator's own pattern:
// every pitched note it plays is the one the generator draws on the shaded
// params, or silence where the arc took a layer out; only the pad's second
// strike may be added. The chord is the generator's.
func TestPhrasingKeepsChordsAndLines(t *testing.T) {
	for _, mood := range []string{"dungeon", "rust", "village"} {
		tm, _ := TemperamentFor(mood)
		p, _ := DeriveMood(mood, 7, 2, 0.3)
		p.Phrasing = &tm.Phrasing
		p = p.sanitized()
		var m motifState
		kept, dropped := 0, 0
		for pat := range 200 {
			got, _ := motifPattern(&m, &p, pat)
			if m.chord != chordDraw(p, pat) {
				t.Fatalf("%s pattern %d: chord %d, the generator draws %d", mood, pat, m.chord, chordDraw(p, pat))
			}
			want := buildPatterns(m.shaded(p), pat)
			for tr := range nTracks {
				for i := range rows {
					g, w := got[tr][i].note, want[tr][i].note
					switch {
					case g == w && g != 0:
						kept++
					case g == 0 && w != 0:
						dropped++
					case g == w:
					case tr == 2 && i == 8 && w == 0 && m.to.Pad > 0:
					default:
						t.Fatalf("%s pattern %d (%s) track %d row %d: note %d, the generator's %d", mood, pat, m.sec, tr, i, g, w)
					}
				}
			}
		}
		if kept == 0 || dropped == 0 {
			t.Errorf("%s: %d notes kept, %d dropped: want both", mood, kept, dropped)
		}
	}
}

// Memory under the identity phrasing develops its lines in one endless hold:
// lines change, but nothing of the arc runs. The drums, which only the arc
// touches, play as without memory, and the chords keep to the cycle.
func TestMemoryUnderIdentity(t *testing.T) {
	for _, mood := range []string{"dungeon", "rust"} {
		tm, _ := TemperamentFor(mood)
		p, _ := DeriveMood(mood, 7, 2, 0.3)
		p.Memory = &tm.Memory
		p = p.sanitized()
		var m motifState
		changed := 0
		for pat := range 200 {
			_, drums := motifPattern(&m, &p, pat)
			if m.sec != secHold || m.phrase != 0 || m.fill || m.to != (Shade{}) {
				t.Fatalf("%s pattern %d: section %s phrase %d fill %v colour %+v", mood, pat, m.sec, m.phrase, m.fill, m.to)
			}
			if drums != buildDrums(p, pat) {
				t.Fatalf("%s pattern %d: memory alone changed the drums", mood, pat)
			}
			if m.chord != int(m.prog[m.slot]) {
				t.Fatalf("%s pattern %d: chord %d off the cycle", mood, pat, m.chord)
			}
			if m.lastLead != "repeat" && m.lastLead != "fresh" {
				changed++
			}
		}
		if changed == 0 {
			t.Errorf("%s: no line changed in 200 patterns", mood)
		}
	}
}

// Phrasing without memory sizes its sections from the phrase's length alone:
// the chord cycle it draws is never played, so it must not stretch a return
// or a hold. Every phrase's thin, hold and return add up to its length, with
// the return the plain minimum.
func TestPhrasingIgnoresTheCycle(t *testing.T) {
	longer := 0
	for _, mood := range MoodNames() {
		tm, ok := TemperamentFor(mood)
		if !ok {
			continue
		}
		for seed := range uint64(40) {
			p, _ := DeriveMood(mood, seed, 2, 0.3)
			p.Phrasing = &tm.Phrasing
			p = p.sanitized()
			m := motifState{phr: true}
			for ph := range 4 {
				m.phrase = ph
				m.plan(&p, ph*20, ph == 0)
				n := m.lens[secHold] + m.lens[secThin] + m.lens[secReturn]
				if m.lens[secReturn] != max(2, n/5) || m.lens[secThin] != max(1, n/5) {
					t.Fatalf("%s seed %d phrase %d: return %d, thin %d in %d patterns; want %d and %d",
						mood, seed, ph, m.lens[secReturn], m.lens[secThin], n, max(2, n/5), max(1, n/5))
				}
				if m.homeNProg > max(2, n/5) {
					longer++ // a case the cycle would have stretched
				}
			}
		}
	}
	if longer == 0 {
		t.Error("no phrase drew a cycle longer than its return: the test proves nothing")
	}
}
