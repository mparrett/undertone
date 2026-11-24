package engine

import "testing"

// Rewind starts a grid over within one block: row 0 steps again, in a new
// pattern, and the rows before the rewind stay in the log.
func TestRewindStartsAGridOverWithinABlock(t *testing.T) {
	p, _ := DeriveMood("village", 3, 2, 0)
	s := NewSynth(p)
	var g Grid
	g.Lead[0], g.Lead[5] = 3, 6
	s.SetGrid(&g)
	render(s, 5*s.samplesPerRow+77) // row 5 sounding
	before, _ := s.Position()
	if before.Row != 5 || s.voices[trackLead].note != leadAt(s, 6) {
		t.Fatalf("before Rewind: row %d, lead %d; want row 5 playing degree 6", before.Row, s.voices[trackLead].note)
	}
	s.Rewind()
	render(s, nowBlock)
	got, ok := s.Position()
	if !ok || got.Row != 0 || got.Pattern != before.Pattern+1 {
		t.Fatalf("a block after Rewind: Position = %+v, %v; want row 0 of pattern %d", got, ok, before.Pattern+1)
	}
	if got.Frame <= before.Frame || got.Frame > before.Frame+int64(s.samplesPerRow) {
		t.Errorf("row 0 restarted at frame %d; want within a row of %d", got.Frame, before.Frame)
	}
	if want := leadAt(s, 3); s.voices[trackLead].note != want {
		t.Errorf("after Rewind the lead plays %d, want row 0's %d", s.voices[trackLead].note, want)
	}
	if r := s.RowAt(before.Frame); r != 5 {
		t.Errorf("RowAt(the frame before the rewind) = %d, want 5", r)
	}
	// one Rewind is one restart: the pattern then runs its 16 rows
	render(s, 15*s.samplesPerRow)
	if got, _ := s.Position(); got.Row != 15 || got.Pattern != before.Pattern+1 {
		t.Errorf("15 rows after Rewind: Position = %+v; want row 15 of the same pattern", got)
	}
}

// Rewind at the top of a pattern, before its row 0 has stepped, starts no
// extra pattern.
func TestRewindBeforeAnyRowKeepsThePatternCount(t *testing.T) {
	s := NewSynth(DeriveDungeon(1, 1, 0))
	s.Rewind()
	render(s, nowBlock)
	if got, ok := s.Position(); !ok || got.Row != 0 || got.Pattern != 0 {
		t.Errorf("Rewind before any row: Position = %+v, %v; want row 0 of pattern 0", got, ok)
	}
}

// Rewind restarts a theme from its first grid, and the theme then plays its
// grids in order as before.
func TestRewindRestartsAThemeFromItsFirstGrid(t *testing.T) {
	p, _ := DeriveMood("village", 3, 2, 0)
	s := NewSynth(p)
	s.PlayTheme(themeOf(3, ThenGenerate))
	render(s, nowBlock)
	if !untilRow0(s) || s.themeAt != 1 {
		t.Fatalf("the theme's second grid never started (grid %d)", s.themeAt)
	}
	render(s, 3*s.samplesPerRow)
	s.Rewind()
	render(s, nowBlock)
	if s.row != 1 || s.themeAt != 0 {
		t.Fatalf("a block after Rewind: row %d of grid %d; want row 0 of grid 0 just stepped", s.row-1, s.themeAt)
	}
	for i := range 3 {
		if i > 0 && !untilRow0(s) {
			t.Fatalf("grid %d never started", i)
		}
		if want := leadAt(s, i+1); s.voices[trackLead].note != want {
			t.Errorf("pattern %d after Rewind: lead plays %d, want grid %d's %d", i, s.voices[trackLead].note, i, want)
		}
	}
	if !s.ThemePlaying() {
		t.Error("the theme reports finished before its last grid ends")
	}
	if !untilRow0(s) || s.ThemePlaying() {
		t.Error("the rewound theme never ended")
	}
}

// Rewind restarts a grid's polymetric lanes with it: a lane that loops at
// its own length plays from its first step again, as on the first play.
func TestRewindRestartsPolymetricLanes(t *testing.T) {
	p, _ := DeriveMood("village", 3, 2, 0)
	var g Grid
	g.Drums[0] = "X.....|" // six sixteenths: three rows
	s := NewSynth(p)
	s.SetGrid(&g)
	render(s, 20*s.samplesPerRow+77) // row 4 of the second pattern
	s.Rewind()
	render(s, nowBlock)
	if s.row != 1 {
		t.Fatalf("a block after Rewind: row %d about to step, want 1", s.row)
	}
	if s.runFrom != s.pat*rows {
		t.Errorf("the lanes count from row %d, want this pattern's first, %d", s.runFrom, s.pat*rows)
	}
	for r := range rows {
		if got, want := gridHit(g.Drums[0], 0, r, s.pat*rows+r-s.runFrom, &s.params), gridHit(g.Drums[0], 0, r, r, &s.params); got != want {
			t.Errorf("row %d after Rewind: the lane plays %+v, the first play's %+v", r, got, want)
		}
	}
}

// A parameter change landing while a rewound row 0 is still held back for
// its flam does not revoice from the pattern that was cut: every track keeps
// what it was playing until row 0 steps.
func TestParamsNowDuringARewindsFlamLeadDoesNotRevoiceTheCutPattern(t *testing.T) {
	p, _ := DeriveMood("village", 3, 2, 0)
	s := NewSynth(p)
	var g Grid
	g.Lead[0], g.Lead[5], g.Lead[14] = 3, 6, 7
	g.Drums[0] = "g" // a flam on the first sixteenth holds row 0 back
	g.Bed = true     // so the generator's tracks have notes to move
	s.SetGrid(&g)
	render(s, 5*s.samplesPerRow+77)
	for s.nframe%nowBlock != 0 {
		render(s, 1)
	}
	type held struct {
		note int
		gate bool
	}
	voices := func() (h [nTracks]held) {
		for i, v := range s.voices {
			h[i] = held{v.note, v.gate}
		}
		return h
	}
	was := voices()
	faster := p
	faster.BPM = p.BPM + 20
	s.Rewind()
	s.SetParametersNow(faster)
	render(s, 1)
	if s.row != 0 || !s.cut {
		t.Fatalf("a frame after Rewind: row %d about to step, cut %v; want row 0 held back by its flam", s.row, s.cut)
	}
	if s.params.BPM != faster.BPM {
		t.Errorf("the tempo change waited: %v bpm, want %v", s.params.BPM, faster.BPM)
	}
	if got := voices(); got != was {
		t.Errorf("voices moved before row 0 stepped:\n got %v\nwant %v", got, was)
	}
	render(s, s.samplesPerRow)
	if got, want := s.voices[trackLead].note, leadAt(s, 3); got != want || s.cut {
		t.Errorf("after the held row stepped: lead %d, cut %v; want row 0's %d", got, s.cut, want)
	}
}
