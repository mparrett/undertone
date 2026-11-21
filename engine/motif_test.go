package engine

import (
	"bytes"
	"io"
	"math"
	"runtime"
	"slices"
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
				// trims, a game's and a mood's, so the levels below are checked
				// with them: no built-in mood sets its own yet
				p.LeadDB, p.MoodLeadDB, p.PadDB, p.MoodPadDB = 2, -5, -1, 3
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
							// and at the generator's level, trims included
							if fresh[tr][i].gain != got[tr][i].gain {
								t.Fatalf("%s seed %d pattern %d track %d row %d: gain %v, read back %v",
									mood, seed, pat, tr, i, fresh[tr][i].gain, got[tr][i].gain)
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

// knobRun plays rust with full phrasing and a restless memory under the given
// knobs, and reports what its lines did. Restlessness is high so every kind
// of change turns up in a few hundred patterns.
func knobRun(t *testing.T, set func(*Memory)) (redraws, small, returnChanges, newCycles int) {
	t.Helper()
	tm, _ := TemperamentFor("rust")
	mem := NewMemory(0.9)
	set(&mem)
	p, _ := DeriveMood("rust", 7, 2, 0.3)
	p.Phrasing, p.Memory = &tm.Phrasing, &mem
	p = p.sanitized()
	var m motifState
	phrase, home := 0, [maxProg]int8{}
	for pat := range 600 {
		motifPattern(&m, &p, pat)
		if pat == 0 || m.phrase != phrase {
			if pat > 0 && m.homeProg != home {
				newCycles++
			}
			phrase, home = m.phrase, m.homeProg
		}
		for _, l := range []string{m.lastLead, m.lastPad} {
			switch l {
			case "redraw":
				redraws++
			case "shift", "drop", "add", "rotate", "invert", "octave", "revoice":
				small++
			}
			if m.sec == secReturn && l != "home" && l != "fresh" {
				returnChanges++
			}
		}
	}
	return
}

// Each memory knob does what it says, and only that: Leap splits redraws
// from small changes, ReturnDrift lets a return move off home, CycleChurn
// brings new chord cycles.
func TestMemoryKnobs(t *testing.T) {
	if r, s, _, _ := knobRun(t, func(m *Memory) { m.Leap = 0 }); r != 0 || s == 0 {
		t.Errorf("Leap 0: %d redraws, %d small changes; want none and some", r, s)
	}
	if r, s, _, _ := knobRun(t, func(m *Memory) { m.Leap = 1 }); r == 0 || s != 0 {
		t.Errorf("Leap 1: %d redraws, %d small changes; want some and none", r, s)
	}
	if _, _, rc, _ := knobRun(t, func(m *Memory) { m.ReturnDrift = 0 }); rc != 0 {
		t.Errorf("ReturnDrift 0: a return changed its lines %d times", rc)
	}
	if _, _, rc, _ := knobRun(t, func(m *Memory) { m.ReturnDrift = 1 }); rc == 0 {
		t.Error("ReturnDrift 1: no return changed its lines")
	}
	if _, _, _, nc := knobRun(t, func(m *Memory) { m.CycleChurn = 0 }); nc != 0 {
		t.Errorf("CycleChurn 0: %d new cycles", nc)
	}
	if _, _, _, nc := knobRun(t, func(m *Memory) { m.CycleChurn = 1 }); nc == 0 {
		t.Error("CycleChurn 1: no new cycle")
	}
}

// A derive hands on the mood's temperament, which plays only when the host
// opts in: off, the mood sounds exactly as with none; on, exactly as if set
// directly; and a temperament set directly wins over the mood's.
func TestMoodTemperamentOptIn(t *testing.T) {
	const n = 20 * SampleRate
	p, _ := DeriveMood("rust", 7, 2, 0.3)
	if p.MoodPhrasing == nil || p.MoodMemory == nil {
		t.Fatal("rust derives without its temperament")
	}
	tm, _ := TemperamentFor("rust")
	bare := p
	bare.MoodPhrasing, bare.MoodMemory = nil, nil
	off := render(NewSynth(bare), n)
	if !slices.Equal(render(NewSynth(p), n), off) {
		t.Fatal("a mood's temperament played without the host opting in")
	}
	on := p
	on.UsePhrasing, on.UseMemory = true, true
	direct := bare
	direct.Phrasing, direct.Memory = &tm.Phrasing, &tm.Memory
	got := render(NewSynth(on), n)
	if slices.Equal(got, off) {
		t.Fatal("opting in changed nothing")
	}
	if !slices.Equal(got, render(NewSynth(direct), n)) {
		t.Fatal("opting in plays other than the temperament set directly")
	}
	// a direct Memory (here a frozen one) wins over the mood's
	frozen := NewMemory(0)
	on.Memory = &frozen
	if s := on.sanitized(); s.Memory != &frozen || s.Phrasing != p.MoodPhrasing {
		t.Fatal("a direct Memory did not win, or the mood's phrasing went with it")
	}
}

// A host may take phrasing or memory away mid-pattern with SetParametersNow;
// the motif keeps what the last boundary saw until the next, so the rest of
// the pattern must play without what was taken, not dereference it.
func TestTakenAwayMidPattern(t *testing.T) {
	for _, off := range []string{"memory", "phrasing", "both"} {
		p, _ := DeriveMood("rust", 7, 2, 0.3)
		p.UsePhrasing, p.UseMemory = true, true
		q := p
		q.UseMemory = off == "phrasing"
		q.UsePhrasing = off == "memory"
		// at every second of two minutes, so some land in each section
		s := NewSynth(p)
		for range 120 {
			render(s, SampleRate)
			s.SetParametersNow(q)
			render(s, SampleRate/4)
			s.SetParametersNow(p)
		}
	}
}

// Presets are in range and in the order their docs give: memory from still
// to restless, phrasing from long to short.
func TestPresets(t *testing.T) {
	seen := map[string]bool{}
	prev := -1.0
	for _, mp := range MemoryPresets() {
		m := mp.Memory
		for _, x := range []float64{m.Restlessness, m.Leap, m.ReturnDrift, m.CycleChurn} {
			if x < 0 || x > 1 {
				t.Errorf("%s: a knob out of 0..1: %+v", mp.Name, m)
			}
		}
		if m.Restlessness <= prev {
			t.Errorf("%s: restlessness %v not above the one before", mp.Name, m.Restlessness)
		}
		prev = m.Restlessness
		if seen[mp.Name] {
			t.Errorf("%s twice", mp.Name)
		}
		seen[mp.Name] = true
	}
	prevMax := math.MaxInt
	for _, pp := range PhrasingPresets() {
		if pp.PhraseMin < 5 || pp.PhraseMin > pp.PhraseMax || pp.BreakChance < 0 || pp.BreakChance > 1 {
			t.Errorf("%s: lengths %d-%d, break %v", pp.Name, pp.PhraseMin, pp.PhraseMax, pp.BreakChance)
		}
		if pp.PhraseMax >= prevMax {
			t.Errorf("%s: not shorter than the one before", pp.Name)
		}
		prevMax = pp.PhraseMax
		if seen[pp.Name] {
			t.Errorf("%s twice", pp.Name)
		}
		seen[pp.Name] = true
		tm, _ := TemperamentFor("rust")
		got := pp.Apply(tm.Phrasing)
		if !slices.Equal(got.Drop, tm.Phrasing.Drop) || got.Shade != tm.Phrasing.Shade {
			t.Errorf("%s: Apply changed the mood's drops or colour", pp.Name)
		}
	}
}
