package engine

import (
	"fmt"
	"math"
	"slices"
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
	for _, m := range moods {
		name := m.name
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
			if rhythmic := len(m.grooves) > 0; rhythmic != (hits > 0) {
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

// Forge is rust's kit and groove at rest: the beat plays at tension 0, and
// tension plays it harder and busier, ratchets included, at every level.
func TestForgeDrivesTheMachineAtRest(t *testing.T) {
	hits := func(level int, tension float64) (n, ratchets int, gain float64) {
		p, _ := DeriveMood("forge", 1, level, tension)
		if p.Groove != "machine" || !samePatch(NewSynth(p).sound(0).patch, industrialKit[0].patch) {
			t.Fatalf("forge at tension %v plays groove %q off the industrial kit", tension, p.Groove)
		}
		for pat := range 8 {
			for _, lane := range buildDrums(p, pat) {
				for _, h := range lane {
					for _, g := range []float64{h.on, h.off} {
						if g > 0 {
							n++
						}
					}
					if h.onN > 1 || h.offN > 1 {
						ratchets++
					}
				}
			}
		}
		return n, ratchets, p.DrumGain
	}
	for _, level := range []int{0, 2, 15} {
		restN, restR, restGain := hits(level, 0)
		if restN == 0 || restGain == 0 {
			t.Fatalf("forge level %d at rest: %d hits at gain %v, want a beat", level, restN, restGain)
		}
		tenseN, tenseR, tenseGain := hits(level, 1)
		if tenseN <= restN || tenseR <= restR || tenseGain <= restGain {
			t.Errorf("tension did not drive forge at level %d: hits %d -> %d, ratchets %d -> %d, gain %v -> %v",
				level, restN, tenseN, restR, tenseR, restGain, tenseGain)
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

// strikes renders n frames and returns the frame and gain of each strike on
// lane d, read as a change of the lane's gain: the tests below alternate a
// grace and its main hit, which differ.
func strikes(s *Synth, d, n int) (at []int64, gain []float64) {
	last := s.dvoices[d].gain
	for range n {
		f := s.nframe
		render(s, 1)
		if g := s.dvoices[d].gain; g != last {
			at, gain, last = append(at, f), append(gain, g), g
		}
	}
	return at, gain
}

// checkFlams checks that strikes come as flams: a grace at flamGrace of its
// main hit, flamLead frames before it, with each main hit at a frame in want.
func checkFlams(t *testing.T, s *Synth, at []int64, gain []float64, want []int64) {
	t.Helper()
	if len(at) != 2*len(want) {
		t.Fatalf("struck at %v, gains %v; want %d flams", at, gain, len(want))
	}
	for i, w := range want {
		grace, main := 2*i, 2*i+1
		if at[main] != w || at[main]-at[grace] != int64(s.flamLead()) {
			t.Errorf("flam %d: grace at %d, hit at %d; want hit at %d, %d frames after its grace", i, at[grace], at[main], w, s.flamLead())
		}
		if math.Abs(gain[grace]-flamGrace*gain[main]) > 1e-12 {
			t.Errorf("flam %d: grace at gain %.3g under a hit at %.3g", i, gain[grace], gain[main])
		}
	}
}

// A flam's grace sounds before its 16th: on a grid's first row, within a
// pattern, on an off 16th, and across the boundary into the next pattern.
func TestFlamGraceLeadsItsStep(t *testing.T) {
	p, _ := DeriveMood("aurora", 1, 2, 0)
	s := NewSynth(p)
	s.SetGrid(&Grid{Drums: [DrumLanes]string{"g........g"}})
	spr, lead := int64(s.samplesPerRow), int64(s.flamLead())
	half := int64(max(1, int(float64(spr)*(0.5+s.params.Swing))))
	// the first row is held back by the lead, so its grace can go first
	at, gain := strikes(s, 0, int(lead+17*spr))
	checkFlams(t, s, at, gain, []int64{lead, lead + 4*spr + half - 1, lead + 16*spr})
}

// A groove's flam on its first 16th leads in from the last row of the
// pattern before, and a theme whose first row flams holds that row back by
// the lead rather than letting the beat land with no grace before it.
func TestFlamAcrossPatternsAndAtAThemeStart(t *testing.T) {
	grooveLib["flamtest"] = groove{"g"}
	t.Cleanup(func() { delete(grooveLib, "flamtest") })
	p, _ := DeriveMood("aurora", 1, 2, 0)
	p.Groove, p.DrumGain = "flamtest", 1
	s := NewSynth(p)
	spr, lead := int64(s.samplesPerRow), int64(s.flamLead())
	at, gain := strikes(s, 0, int(lead+32*spr+1))
	checkFlams(t, s, at, gain, []int64{lead, lead + 16*spr, lead + 32*spr})

	render(s, 5*int(spr)) // mid-pattern
	s.PlayTheme(&Theme{Grids: []Grid{{Drums: [DrumLanes]string{"g"}}}})
	start := (s.nframe/nowBlock + 1) * nowBlock // the block the theme starts in
	at, gain = strikes(s, 0, int(start-s.nframe+lead+1))
	checkFlams(t, s, at, gain, []int64{start + lead})
	if s.row != 1 || s.theme == nil {
		t.Errorf("after the flam: row %d, theme %v; want the theme's row 0 just stepped", s.row, s.theme != nil)
	}
}

// onHits steps n rows and returns the sixteenths at which lane d hit, on and
// off, counted from the first row stepped.
func onHits(s *Synth, d, n int) []int {
	var hits []int
	for i := range n {
		row := s.row
		s.step()
		if h := s.drums[d][row]; h.on > 0 {
			hits = append(hits, 2*i)
		}
		if h := s.drums[d][row]; h.off > 0 {
			hits = append(hits, 2*i+1)
		}
	}
	return hits
}

// A polymetric grid lane loops at its own length and runs on across pattern
// boundaries and edits; it restarts when a grid takes over from the
// generator and when a theme starts. A plain short lane still rests for the
// remainder of the pattern.
func TestPolymetricGridLanePhases(t *testing.T) {
	p, _ := DeriveMood("aurora", 1, 2, 0)
	s := NewSynth(p)
	g := Grid{Drums: [DrumLanes]string{"x..|", "x.."}}
	s.SetGrid(&g)
	var every3 []int
	for i := 0; i < 4*rows; i += 3 {
		every3 = append(every3, i)
	}
	if got := onHits(s, 0, 2*rows); !slices.Equal(got, every3) {
		t.Errorf("polymetric lane over two patterns hit at %v, want every third 16th %v", got, every3)
	}
	// across an edit, a hand-back and a fresh grid
	s = NewSynth(p)
	s.SetGrid(&g)
	onHits(s, 0, 5) // ten 16ths: the lane is a 16th into its fourth loop
	edit := g
	edit.Drums[2] = "x"
	s.SetGrid(&edit)
	if got := onHits(s, 0, 2); !slices.Equal(got, []int{2}) {
		t.Errorf("after an edit the lane hit at %v, want [2]: an edit keeps it running", got)
	}
	s.SetGrid(nil)
	onHits(s, 0, 1)
	s.SetGrid(&g)
	if got := onHits(s, 0, 2); !slices.Equal(got, []int{0, 3}) {
		t.Errorf("a grid taking over from the generator hit at %v, want [0 3]: a fresh run", got)
	}
	s = NewSynth(p)
	s.SetGrid(&g)
	if plain := onHits(s, 1, 2*rows); !slices.Equal(plain, []int{0, 32}) {
		t.Errorf("a plain short lane hit at %v, want [0 32]: once a pattern, resting after", plain)
	}

	// a theme starts its lanes afresh, even cutting in mid-loop
	s = NewSynth(p)
	s.SetGrid(&g)
	onHits(s, 0, 5)
	s.startTheme(&themeCmd{t: &Theme{Grids: []Grid{g, g}, Then: ThenLoop}, gen: 1})
	if got := onHits(s, 0, rows+2); !slices.Equal(got, every3[:12]) {
		t.Errorf("a theme's lane hit at %v, want %v: fresh at its start and running into its second grid", got, every3[:12])
	}
}

// A polymetric groove lane counts from the synth's first pattern, so its
// phase in any pattern follows from the seed and the pattern alone.
func TestPolymetricGrooveLaneRunsFromTheFirstPattern(t *testing.T) {
	grooveLib["polytest"] = groove{"x..|"}
	t.Cleanup(func() { delete(grooveLib, "polytest") })
	p, _ := DeriveMood("aurora", 1, 2, 0)
	p.Groove, p.DrumGain = "polytest", 1
	for pat := range 4 {
		d := buildDrums(p, pat)
		for i := range 2 * rows {
			h := d[0][i/2]
			hit := h.on > 0
			if i%2 == 1 {
				hit = h.off > 0
			}
			if want := (2*rows*pat+i)%3 == 0; hit != want {
				t.Errorf("pattern %d 16th %d: hit %v, want %v", pat, i, hit, want)
			}
		}
	}
}
