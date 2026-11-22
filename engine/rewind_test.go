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
