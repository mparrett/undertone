package engine

import (
	"fmt"
	"math"
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
		p := Params{LeadDB: c.in, PadDB: c.in, BassDB: c.in}.sanitized()
		if p.LeadDB != c.want || p.PadDB != c.want || p.BassDB != c.want {
			t.Errorf("trim %v sanitized to %v %v %v, want %v", c.in, p.LeadDB, p.PadDB, p.BassDB, c.want)
		}
	}
}
