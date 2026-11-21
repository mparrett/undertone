package engine

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// A trim scales its own layer's notes by its dB and nothing else: the same
// notes, gates and patches, every other track's gain untouched, generated
// and under a theme.
func TestTrimsScaleOnlyTheirLayer(t *testing.T) {
	p, _ := DeriveMood("village", 7, 4, 0.5)
	p.PadDensity, p.LeadDensity = 1, 1 // a pad and a lead on every chance, so each pattern tests them
	tp := p
	tp.LeadDB, tp.PadDB, tp.BassDB = -6, 4, -9
	scale := map[int]float64{trackLead: dbGain(-6), 2: dbGain(4)} // 2 is the pad track, which no constant names
	check := func(what string, a, b [nTracks][rows]trackRow, scale map[int]float64) {
		t.Helper()
		for tr := range nTracks {
			for r := range rows {
				x, y := a[tr][r], b[tr][r]
				f, ok := scale[tr]
				if !ok {
					f = 1
				}
				if x.note != y.note || x.gateRows != y.gateRows || fmt.Sprint(x.patch) != fmt.Sprint(y.patch) || math.Abs(y.gain-x.gain*f) > 1e-12 {
					t.Fatalf("%s: track %d row %d: %+v trimmed is %+v; want its gain x%.3f and nothing else", what, tr, r, x, y, f)
				}
			}
		}
	}
	sawLead, sawPad := false, false
	for pat := range 8 {
		a, b := buildPatterns(p, pat), buildPatterns(tp, pat)
		check(fmt.Sprintf("pattern %d", pat), a, b, scale)
		for r := range rows {
			sawLead = sawLead || a[trackLead][r].gain > 0
			sawPad = sawPad || a[2][r].gain > 0
		}
	}
	if !sawLead || !sawPad {
		t.Fatalf("no lead (%v) or pad (%v) note in 8 patterns: the test checked nothing", sawLead, sawPad)
	}

	// under a theme: the grid's lead takes LeadDB, its bass (on the fifth's
	// voice) BassDB, and the bed's pad keeps its trim under gridBedScale
	g := Grid{Bed: true}
	g.Lead[0], g.Bass[0] = 3, 1
	sa, sb := NewSynth(p), NewSynth(tp)
	sa.pats, sb.pats = buildPatterns(p, 0), buildPatterns(tp, 0)
	sa.applyGrid(&g, 0)
	sb.applyGrid(&g, 0)
	scale[trackFifth] = dbGain(-9)
	check("theme row 0", sa.pats, sb.pats, scale)
	if sa.pats[trackFifth][0].gain != gridBassGain || sa.pats[trackLead][0].gain == 0 {
		t.Fatalf("the grid's bass or lead is not on row 0: %+v %+v", sa.pats[trackFifth][0], sa.pats[trackLead][0])
	}
}

// 0 dB is a gain of exactly 1, so untrimmed notes keep the floats they had
// (the golden renders hold every untrimmed render to that), and a trim out of
// range is clamped rather than blowing up the mix.
func TestTrimRange(t *testing.T) {
	if dbGain(0) != 1 {
		t.Fatalf("dbGain(0) = %v, want exactly 1", dbGain(0))
	}
	if g := dbGain(-6); math.Abs(g-0.501187) > 1e-6 {
		t.Errorf("dbGain(-6) = %v, want 0.501187", g)
	}
	for _, c := range []struct{ in, want float64 }{{math.NaN(), 0}, {100, 12}, {-1000, -60}, {math.Inf(-1), -60}, {-3, -3}} {
		p := Params{LeadDB: c.in, PadDB: c.in, BassDB: c.in, MoodLeadDB: c.in, MoodPadDB: c.in}.sanitized()
		if p.LeadDB != c.want || p.PadDB != c.want || p.BassDB != c.want || p.MoodLeadDB != c.want || p.MoodPadDB != c.want {
			t.Errorf("trim %v sanitized to %v %v %v, mood %v %v, want %v", c.in, p.LeadDB, p.PadDB, p.BassDB, p.MoodLeadDB, p.MoodPadDB, c.want)
		}
	}
}

// A mood's trims add to a game's: lead and pad notes struck under a mood
// trim of a and a game trim of b are the notes of a single trim of a+b,
// generated and under a theme, and the sum is held to one trim's range.
func TestMoodTrimsAddToGameTrims(t *testing.T) {
	p, _ := DeriveMood("village", 7, 4, 0.5)
	p.PadDensity, p.LeadDensity = 1, 1
	both, one := p, p
	both.LeadDB, both.MoodLeadDB, both.PadDB, both.MoodPadDB = -3, -4, 2, 1.5
	one.LeadDB, one.PadDB = -7, 3.5
	same := func(what string, a, b [nTracks][rows]trackRow) {
		t.Helper()
		for tr := range nTracks {
			for r := range rows {
				x, y := a[tr][r], b[tr][r]
				if x.note != y.note || x.gateRows != y.gateRows || fmt.Sprint(x.patch) != fmt.Sprint(y.patch) || x.gain != y.gain {
					t.Fatalf("%s: track %d row %d: %+v under mood and game trims, %+v under their sum", what, tr, r, x, y)
				}
			}
		}
	}
	for pat := range 8 {
		same(fmt.Sprintf("pattern %d", pat), buildPatterns(both, pat), buildPatterns(one, pat))
	}
	g := Grid{Bed: true}
	g.Lead[0] = 3
	sa, sb := NewSynth(both), NewSynth(one)
	sa.pats, sb.pats = buildPatterns(both, 0), buildPatterns(one, 0)
	sa.applyGrid(&g, 0)
	sb.applyGrid(&g, 0)
	same("theme row 0", sa.pats, sb.pats)
	if g := trimGain(12, 12); g != dbGain(12) {
		t.Errorf("trimGain(12, 12) = %v, want +12 dB's %v", g, dbGain(12))
	}
	if g := trimGain(0, 0); g != 1 {
		t.Errorf("trimGain(0, 0) = %v, want exactly 1", g)
	}
}

// A spec's trims reach a derive as the mood's trims, never the game's, and
// its EDN names them only when set, so an untrimmed entry reads as before.
func TestMoodSpecTrims(t *testing.T) {
	s, _ := MoodByName("village")
	if plain := s.EDN(); strings.Contains(plain, "-db") {
		t.Fatalf("an untrimmed mood's EDN names a trim:\n%s", plain)
	}
	s.LeadDB, s.PadDB = -6, 2.5
	p, err := s.Derive(7, 3, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if p.MoodLeadDB != -6 || p.MoodPadDB != 2.5 || p.LeadDB != 0 || p.PadDB != 0 {
		t.Fatalf("derived mood trims %v %v, game trims %v %v; want -6 2.5, 0 0", p.MoodLeadDB, p.MoodPadDB, p.LeadDB, p.PadDB)
	}
	edn := s.EDN()
	for _, want := range []string{":lead-db   -6.0", ":pad-db    2.5"} {
		if !strings.Contains(edn, want) {
			t.Errorf("EDN lacks %q:\n%s", want, edn)
		}
	}
}
