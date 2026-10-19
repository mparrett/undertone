package engine

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// Every groove is three lanes of two bars of 16ths, in the step alphabet.
func TestGroovesAreWellFormed(t *testing.T) {
	for name, g := range grooveLib {
		for d, lane := range g {
			if len(lane) != 2*rows {
				t.Errorf("%s lane %d: %d steps, want %d", name, d, len(lane), 2*rows)
			}
			if i := strings.IndexFunc(lane, func(c rune) bool { return !strings.ContainsRune("Xxo.?frR", c) }); i >= 0 {
				t.Errorf("%s lane %d: step %d is %q", name, d, i, lane[i])
			}
		}
	}
	for _, m := range moods {
		for _, g := range m.grooves {
			if _, ok := grooveLib[g]; !ok {
				t.Errorf("mood %s names groove %q, which is not in grooveLib", m.name, g)
			}
		}
	}
}

// Drums belong to the moods that ask for them; the rest stay silent. Rust's
// come only with tension: none at rest or at a quarter, a beat at a half.
func TestDrumsOnlyInRhythmicMoods(t *testing.T) {
	for _, name := range MoodNames() {
		for level := 0; level <= 5; level++ {
			p, _ := DeriveMood(name, 1, level, 0.5)
			hits := 0
			for _, lane := range buildDrums(p, 0) {
				for _, h := range lane {
					if h.on > 0 || h.off > 0 {
						hits++
					}
				}
			}
			if rhythmic := name == "village" || name == "rust"; rhythmic != (hits > 0) {
				t.Errorf("%s level %d: %d drum hits", name, level, hits)
			}
		}
	}
	for _, tension := range []float64{0, 0.25} {
		p, _ := DeriveMood("rust", 1, 3, tension)
		if p.DrumGain != 0 {
			t.Errorf("rust at tension %v: drums at %v, want none", tension, p.DrumGain)
		}
	}
}

// The same seed and pattern give the same drums; fills make patterns differ.
func TestGroovesAreDeterministicAndVary(t *testing.T) {
	p, _ := DeriveMood("village", 2, 3, 1)
	if buildDrums(p, 5) != buildDrums(p, 5) {
		t.Fatal("same params and pattern built different drums")
	}
	differ := false
	for pat := 1; pat < 8 && !differ; pat++ {
		differ = buildDrums(p, pat) != buildDrums(p, 0)
	}
	if !differ {
		t.Fatal("eight patterns at full tension all played identical drums")
	}
}

// Level fills the shaker in: more ? steps play higher up.
func TestLevelBusiesTheShaker(t *testing.T) {
	count := func(level int) int {
		p, _ := DeriveMood("village", 2, level, 0)
		n := 0
		for pat := 0; pat < 16; pat++ {
			for _, h := range buildDrums(p, pat)[2] {
				if h.on > 0 {
					n++
				}
				if h.off > 0 {
					n++
				}
			}
		}
		return n
	}
	if lo, hi := count(0), count(5); hi <= lo {
		t.Fatalf("shaker hits at level 5 (%d) not above level 0 (%d)", hi, lo)
	}
}

// soloLevel plays one drum on every beat for two seconds and returns its rms
// while sounding, in dB.
func soloLevel(d int) float64 {
	var v Voice
	var sum float64
	n := 0
	beat := SampleRate / 2
	for i := 0; i < 2*SampleRate; i++ {
		if i%beat == 0 {
			v.trigger(drumKit[d].patch, drumKit[d].note, 1, 1, uint64(i))
		}
		if i%beat == beat/4 {
			v.gate = false
		}
		if v.active {
			var l, r float64
			var dl sends
			v.run(&l, &r, &dl)
			if x := (l + r) / 2; math.Abs(x) > 1e-3 {
				sum += x * x
				n++
			}
		}
	}
	return 10 * math.Log10(sum/float64(n))
}

// The kit's balance, set by ear and by a measured review: the clap within
// 6 dB of the kick, the shaker 6 to 18 dB under it, as hats sit.
func TestKitBalance(t *testing.T) {
	kickL, clapL, shakerL := soloLevel(0), soloLevel(1), soloLevel(2)
	if math.Abs(kickL-clapL) > 6 {
		t.Errorf("clap %.1f dB against kick %.1f dB", clapL, kickL)
	}
	if gap := kickL - shakerL; gap < 6 || gap > 18 {
		t.Errorf("shaker %.1f dB, %.1f dB under the kick", shakerL, gap)
	}
}

// Drums bring the engine's first sharp transients; the loudest village
// scenes must keep headroom under the master's saturation.
func TestVillagePeaks(t *testing.T) {
	for _, c := range []struct {
		seed    uint64
		level   int
		tension float64
	}{{7, 5, 0}, {2, 5, 1}, {11, 2, 0.8}} {
		p, _ := DeriveMood("village", c.seed, c.level, c.tension)
		s := NewSynth(p)
		buf := make([]byte, SampleRate*4*12)
		s.Read(buf)
		peak := 0.0
		for i := 0; i+1 < len(buf); i += 2 {
			peak = max(peak, math.Abs(float64(int16(uint16(buf[i])|uint16(buf[i+1])<<8))))
		}
		if db := 20 * math.Log10(peak/32768); db > -2 {
			t.Errorf("village %+v peaks at %.1f dBFS, want at most -2", c, db)
		}
	}
}

// The drums draw from their own rng streams and voices, so a groove never
// changes what the melodic tracks play: rendered with and without drums, the
// melodic voices hold the same state throughout.
func TestGrooveLeavesMelodyAlone(t *testing.T) {
	p, _ := DeriveMood("village", 7, 3, 0.6)
	quiet := p
	quiet.Groove, quiet.DrumGain, quiet.DrumBusy, quiet.Swing = "", 0, 0, 0
	a, b := NewSynth(p), NewSynth(quiet)
	buf := make([]byte, 4*a.samplesPerRow/3)
	for i := 0; i < 3*rows*4; i++ { // four patterns, in third-of-a-row chunks
		a.Read(buf)
		b.Read(buf)
		if fmt.Sprint(a.voices) != fmt.Sprint(b.voices) {
			t.Fatalf("chunk %d (pattern %d row %d): the groove changed the melodic voices", i, a.pat, a.row)
		}
	}
}

// Under a grid the drums are the grid's lanes, not the bed's: a village grid
// with empty drum lanes is silent there even with the bed on, and a grid in a
// mood without drums plays the ones it writes.
func TestGridOwnsDrums(t *testing.T) {
	sounding := func(mood string, g Grid) bool {
		p, _ := DeriveMood(mood, 2, 3, 0)
		s := NewSynth(p)
		s.SetGrid(&g)
		var any bool
		// a quarter row of frames per read (4 bytes a frame), so a short hit
		// can't start and finish between checks; 4*rows reads is one pattern
		buf := make([]byte, 4*(s.samplesPerRow/4))
		for range 4 * rows {
			s.Read(buf)
			for _, v := range s.dvoices {
				any = any || v.active
			}
		}
		return any
	}
	if sounding("village", Grid{Bed: true}) {
		t.Error("village's generated drums play under a grid with empty drum lanes")
	}
	if !sounding("aurora", Grid{Drums: [DrumLanes]string{"x...x...x...x..."}}) {
		t.Error("a kick written in an aurora grid stays silent")
	}
}

// SeedGrid writes the mood's groove into the drum lanes, and only into them:
// the lead and bass a seed gives are the same with or without a groove.
func TestSeedGridWritesTheGroove(t *testing.T) {
	p, _ := DeriveMood("village", 7, 3, 0)
	g := SeedGrid(p)
	for d, lane := range g.Drums {
		if len(lane) != 2*Steps || strings.Trim(lane, "Xxo.") != "" {
			t.Errorf("drum lane %d = %q; want %d steps of X, x, o or .", d, lane, 2*Steps)
		}
	}
	if !strings.ContainsAny(g.Drums[0], "Xx") {
		t.Error("village's seeded kick lane has no hits")
	}
	bare := p
	bare.Groove = ""
	h := SeedGrid(bare)
	if g.Lead != h.Lead || g.Bass != h.Bass || g.Pulse != h.Pulse {
		t.Error("seeding the drums changed the lead, bass or pulse")
	}
	// fills come through too: at full tension most of them play
	fills := 0
	for seed := uint64(1); seed <= 20; seed++ {
		q, _ := DeriveMood("village", seed, 3, 1)
		sg, groove := SeedGrid(q), grooveLib[q.Groove]
		for d := range groove {
			for i := range groove[d] {
				if groove[d][i] == 'f' && sg.Drums[d][i] != '.' {
					fills++
				}
			}
		}
	}
	if fills == 0 {
		t.Error("no seeded village grid at full tension has a fill")
	}
	if h.Drums != [DrumLanes]string{} {
		t.Errorf("a mood without a groove seeded drums %q", h.Drums)
	}
}

// A drum hit while the lane still sounds starts a fresh transient: no
// envelope level or oscillator phase carried over from the last hit.
func TestDrumStrikeIsFresh(t *testing.T) {
	for d := range nDrums {
		var v Voice
		v.strike(drumKit[d], 1, 1)
		var l, r float64
		var bus sends
		for range SampleRate / 50 {
			v.run(&l, &r, &bus)
		}
		if !v.active {
			t.Fatalf("lane %d went quiet within 20 ms; the test needs it sounding", d)
		}
		v.strike(drumKit[d], 1, 2)
		for i, u := range v.units {
			if u.env != 0 || u.phase != 0 {
				t.Errorf("lane %d unit %d: re-hit kept env %.3g phase %.3g", d, i, u.env, u.phase)
			}
		}
	}
}

// SetParametersNow switches the drums at once, as it does swing,
// rather than leaving the old groove playing out its pattern.
func TestSetParametersNowSwitchesDrums(t *testing.T) {
	village, _ := DeriveMood("village", 7, 5, 0)
	aurora, _ := DeriveMood("aurora", 7, 1, 0)
	hits := func(s *Synth) bool {
		for d := range s.drums {
			for _, h := range s.drums[d] {
				if h.on > 0 || h.off > 0 {
					return true
				}
			}
		}
		return false
	}
	for _, c := range []struct {
		name     string
		from, to Params
		want     bool
	}{{"village -> aurora", village, aurora, false}, {"aurora -> village", aurora, village, true}} {
		s := NewSynth(c.from)
		s.Read(make([]byte, 4*s.samplesPerRow*3))
		s.SetParametersNow(c.to)
		s.Read(make([]byte, 4*s.samplesPerRow))
		if s.pat != 0 {
			t.Fatalf("crossed into pattern %d; the test needs the switch mid-pattern", s.pat)
		}
		if hits(s) != c.want {
			t.Errorf("%s mid-pattern: drums sounding = %v, want %v", c.name, !c.want, c.want)
		}
	}
}

// Tension drives the drums of a mood that has them, on top of the level's
// build: the shaker fills in and the hits land harder, up to the gain's own
// ceiling, and the busy chance stops at certainty. A mood without drums, or
// with its drums at zero gain, gets none from tension.
func TestTensionDrivesTheDrums(t *testing.T) {
	for _, level := range []int{0, 2, 6} {
		calm, _ := DeriveMood("village", 1, level, 0)
		hot, _ := DeriveMood("village", 1, level, 1)
		louder := hot.DrumGain > calm.DrumGain || calm.DrumGain == 0.8
		if !louder || hot.DrumGain > 0.8 || hot.DrumBusy <= calm.DrumBusy || hot.DrumBusy > 1 {
			t.Errorf("village level %d: tension 1 gives gain %v busy %v, against %v %v at rest",
				level, hot.DrumGain, hot.DrumBusy, calm.DrumGain, calm.DrumBusy)
		}
		hits := func(p Params) (n int) {
			for pat := 0; pat < 8; pat++ {
				for _, lane := range buildDrums(p, pat) {
					for _, h := range lane {
						if h.on > 0 {
							n++
						}
						if h.off > 0 {
							n++
						}
					}
				}
			}
			return n
		}
		if hits(hot) <= hits(calm) {
			t.Errorf("village level %d: tension did not add hits (%d against %d)", level, hits(hot), hits(calm))
		}
	}
	for _, name := range []string{"dungeon", "overworld", "meadow", "aurora"} {
		if p, _ := DeriveMood(name, 1, 3, 1); p.DrumGain != 0 || p.DrumBusy != 0 {
			t.Errorf("%s at tension 1 has drum gain %v busy %v", name, p.DrumGain, p.DrumBusy)
		}
	}
	quiet, _ := MoodByName("village")
	quiet.Drums = Line{0, 0, math.Inf(-1), math.Inf(1)}
	if p, _ := quiet.Derive(1, 3, 1); p.DrumGain != 0 {
		t.Errorf("drums set to zero gain play at %v under tension", p.DrumGain)
	}
}

// A ratchet strikes its lane three times within its 16th, each quieter than
// the one before, and is done before the next 16th lands.
func TestRatchetStrikesThreeTimesInItsSixteenth(t *testing.T) {
	p, _ := DeriveMood("rust", 1, 0, 1)
	s := NewSynth(p)
	s.drums = [nDrums][rows]drumHit{}
	s.drums[1][0] = drumHit{on: 0.7, onN: 3}
	s.row = 0
	s.stepDrums()
	gains := []float64{s.dvoices[1].gain}
	var at []int // frames after the hit's own frame
	half := s.samplesPerRow / 2
	for f := 0; f <= half; f++ { // the hit's frame, then up to the off 16th
		before := s.dvoices[1].gain
		var l, r float64
		s.runDrums(&l, &r)
		if g := s.dvoices[1].gain; g != before {
			gains = append(gains, g)
			at = append(at, f)
		}
	}
	if len(gains) != 3 || !(gains[0] > gains[1] && gains[1] > gains[2]) {
		t.Fatalf("struck at gains %v, want three, falling", gains)
	}
	// evenly spaced: the re-strikes a third and two thirds of the way in
	if gap := half / 3; at[0] != gap || at[1] != 2*gap {
		t.Errorf("re-struck %v frames in, want %d and %d", at, gap, 2*gap)
	}
}

// Rust's drums are its own kit, not the village's.
func TestRustPlaysTheIndustrialKit(t *testing.T) {
	p, _ := DeriveMood("rust", 1, 0, 1)
	s := NewSynth(p)
	for d := range nDrums {
		if !samePatch(s.sound(d).patch, industrialKit[d].patch) {
			t.Errorf("lane %d plays another kit", d)
		}
	}
	v, _ := DeriveMood("village", 1, 0, 1)
	s = NewSynth(v)
	if !samePatch(s.sound(0).patch, kick) {
		t.Error("the village lost its kick")
	}
}

// Open reaches the bed, not the drums: a kit's filters are its tuning.
func TestOpenLeavesTheDrumsAlone(t *testing.T) {
	p, _ := DeriveMood("rust", 1, 5, 1)
	if p.Open >= 0 {
		t.Fatalf("rust at level 5 derives open %v; the test needs it closing", p.Open)
	}
	s := NewSynth(p)
	if s.voices[0].openTo != p.Open {
		t.Errorf("the drone's open is %v, want %v", s.voices[0].openTo, p.Open)
	}
	for d, v := range s.dvoices {
		if v.open != 0 || v.openTo != 0 {
			t.Errorf("drum lane %d has open %v toward %v", d, v.open, v.openTo)
		}
	}
}
