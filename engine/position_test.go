package engine

import "testing"

// The position follows the rendered rows: the row and its start frame, and
// a pattern count that moves on after row 15.
func TestPositionCountsRowsAndPatterns(t *testing.T) {
	p := DeriveDungeon(1, 1, 0)
	s := NewSynth(p)
	if _, ok := s.Position(); ok {
		t.Fatal("Position before any rendering: ok, want not")
	}
	row := int64(s.samplesPerRow)
	for _, c := range []struct {
		rendered   int64 // frames rendered so far
		row, pat   int
		rowStarted int64
	}{
		{1, 0, 0, 0},
		{row + 1, 1, 0, row},
		{16 * row, 15, 0, 15 * row},
		{16*row + 1, 0, 1, 16 * row},
		{37*row + 5, 5, 2, 37 * row},
	} {
		render(s, int(c.rendered-s.Frames()))
		got, ok := s.Position()
		if !ok || got.Row != c.row || got.Pattern != c.pat || got.Frame != c.rowStarted {
			t.Errorf("after %d frames: Position = %+v, %v; want row %d pattern %d from frame %d",
				c.rendered, got, ok, c.row, c.pat, c.rowStarted)
		}
	}
}

// PositionAt answers for a frame already rendered, as RowAt does, with the
// pattern that row belonged to.
func TestPositionAtFollowsTheHeardFrame(t *testing.T) {
	s := NewSynth(DeriveDungeon(1, 1, 0))
	row := int64(s.samplesPerRow)
	render(s, int(20*row))
	for _, c := range []struct {
		frame    int64
		row, pat int
	}{{0, 0, 0}, {row - 1, 0, 0}, {15*row + 3, 15, 0}, {16 * row, 0, 1}, {19*row + 1, 3, 1}} {
		got, ok := s.PositionAt(c.frame)
		if !ok || got.Row != c.row || got.Pattern != c.pat {
			t.Errorf("PositionAt(%d) = %+v, %v; want row %d pattern %d", c.frame, got, ok, c.row, c.pat)
		}
		if r := s.RowAt(c.frame); r != got.Row {
			t.Errorf("RowAt(%d) = %d, PositionAt's row %d", c.frame, r, got.Row)
		}
	}
	// the log keeps 64 rows; older frames are gone
	render(s, int(44*row))
	if got, ok := s.PositionAt(0); !ok || got.Pattern != 0 || got.Row != 0 {
		t.Errorf("PositionAt(0) after 64 rows = %+v, %v; want row 0 of pattern 0", got, ok)
	}
	render(s, int(row))
	if _, ok := s.PositionAt(0); ok {
		t.Error("PositionAt(0) after 65 rows: ok, want gone")
	}
}

// Pending is a SetParameters waiting for the next boundary: set by the call,
// cleared by the pattern that takes it. A SetParametersNow never shows.
func TestPositionPendingUntilTheBoundary(t *testing.T) {
	p := DeriveDungeon(1, 1, 0)
	s := NewSynth(p)
	render(s, 3*s.samplesPerRow)
	if got, _ := s.Position(); got.Pending {
		t.Fatal("pending before any change was staged")
	}
	s.SetParameters(DeriveDungeon(2, 1, 0))
	render(s, 5*s.samplesPerRow)
	if got, _ := s.Position(); !got.Pending || got.Pattern != 0 {
		t.Errorf("mid-pattern after SetParameters: %+v, want pending in pattern 0", got)
	}
	render(s, 8*s.samplesPerRow+1) // past the boundary
	if got, _ := s.Position(); got.Pending || got.Pattern != 1 || got.Row != 0 {
		t.Errorf("after the boundary: %+v, want pattern 1 row 0, no longer pending", got)
	}
	s.SetParametersNow(DeriveDungeon(3, 1, 0))
	render(s, nowBlock)
	if got, _ := s.Position(); got.Pending {
		t.Error("a SetParametersNow showed as pending")
	}
}

// A theme staged with PlayTheme shows until the synth takes it; started
// mid-pattern it cuts that pattern and counts the next from row 0.
func TestPositionThemeCutsThePattern(t *testing.T) {
	s := NewSynth(DeriveDungeon(1, 1, 0))
	render(s, 5*s.samplesPerRow+1)
	s.PlayTheme(&Theme{Grids: []Grid{{Lead: [Steps]int{1}}}})
	if got, _ := s.Position(); !got.ThemePending {
		t.Error("a staged theme did not show as pending")
	}
	render(s, nowBlock+1)
	got, _ := s.Position()
	if got.ThemePending || got.Pattern != 1 || got.Row != 0 {
		t.Errorf("after the theme started: %+v, want pattern 1 row 0, no longer pending", got)
	}
	s.PlayTheme(nil) // a stop starts nothing
	if got, _ := s.Position(); got.ThemePending {
		t.Error("stopping the theme showed as a theme pending")
	}
}
