package engine

import (
	"runtime"
	"testing"
)

// tune returns n grids with no bed whose lead plays one degree on every row:
// first for grid 0, first+1 for grid 1 and so on. Any row's lead note then
// names the theme and the grid it came from.
func tune(first, n int, then ThemeEnd) *Theme {
	t := &Theme{Then: then}
	for i := range n {
		var g Grid
		for r := range g.Lead {
			g.Lead[r] = first + i
		}
		t.Grids = append(t.Grids, g)
	}
	return t
}

// toRow renders a frame at a time until row has just stepped.
func toRow(t *testing.T, s *Synth, row int) {
	t.Helper()
	for range 2*rows*s.samplesPerRow + nowBlock {
		render(s, 1)
		if s.stepRow == row && s.rowLeft == s.samplesPerRow-1 {
			return
		}
	}
	t.Fatalf("row %d never stepped", row)
}

// themeAtRow starts th on a fresh synth and renders to row of its grid, with a
// few frames more so that a block boundary falls before the next row.
func themeAtRow(t *testing.T, th *Theme, grid, row int) *Synth {
	t.Helper()
	p, _ := DeriveMood("village", 3, 2, 0)
	s := NewSynth(p)
	s.PlayTheme(th)
	render(s, nowBlock)
	for g := 0; g < grid; g++ {
		toRow(t, s, rows-1)
	}
	if row > 0 || grid > 0 {
		toRow(t, s, row)
	}
	if s.themeAt != grid {
		t.Fatalf("at grid %d, want %d", s.themeAt, grid)
	}
	render(s, 3)
	return s
}

// wantLead fails unless the lead is on degree d.
func wantLead(t *testing.T, s *Synth, d int, what string) {
	t.Helper()
	if got, want := s.voices[trackLead].note, leadAt(s, d); got != want {
		t.Errorf("%s: lead plays note %d, want degree %d's %d", what, got, d, want)
	}
}

// At AtNextRow a revision is heard from the next row, and the playhead keeps
// its pattern, grid and row: nothing is cut and nothing starts over.
func TestReviseThemeLandsOnTheNextRow(t *testing.T) {
	s := themeAtRow(t, tune(1, 3, ThenLoop), 1, 5)
	pat, from := s.pat, s.runFrom
	s.ReviseTheme(tune(5, 3, ThenLoop), AtNextRow)
	render(s, nowBlock)
	if s.stepRow != 5 || s.pat != pat || s.themeAt != 1 || s.cut {
		t.Fatalf("a block after the revision: row %d of grid %d, pattern %d, cut %v; want row 5 of grid 1, pattern %d, uncut", s.stepRow, s.themeAt, s.pat, s.cut, pat)
	}
	wantLead(t, s, 2, "the row playing")
	toRow(t, s, 6)
	wantLead(t, s, 6, "the next row")
	if s.pat != pat || s.runFrom != from {
		t.Errorf("pattern %d and run from %d, want %d and %d as before", s.pat, s.runFrom, pat, from)
	}
	toRow(t, s, 0)
	wantLead(t, s, 7, "the next grid")
	if !s.ThemePlaying() {
		t.Error("a revised theme reports finished")
	}
}

// A revision with fewer grids than the playhead's wraps into itself.
func TestReviseThemeToFewerGridsWraps(t *testing.T) {
	s := themeAtRow(t, tune(1, 3, ThenLoop), 2, 5)
	s.ReviseTheme(tune(5, 2, ThenLoop), AtNextRow)
	toRow(t, s, 6)
	if s.themeAt != 0 {
		t.Errorf("at grid %d of two, want 0", s.themeAt)
	}
	wantLead(t, s, 5, "the next row")
	toRow(t, s, 0)
	wantLead(t, s, 6, "the next grid")
}

// At AtNextGrid the grid playing finishes as it was.
func TestReviseThemeAtTheNextGrid(t *testing.T) {
	s := themeAtRow(t, tune(1, 3, ThenLoop), 0, 5)
	s.ReviseTheme(tune(5, 3, ThenLoop), AtNextGrid)
	toRow(t, s, rows-1)
	wantLead(t, s, 1, "the rest of the grid")
	toRow(t, s, 0)
	wantLead(t, s, 6, "the next grid")
	if s.revise != nil {
		t.Error("the revision still waits after landing")
	}
}

// At AtNextLoop the pass playing finishes as it was, and the next is the
// revision from its first grid.
func TestReviseThemeAtTheNextLoop(t *testing.T) {
	s := themeAtRow(t, tune(1, 3, ThenLoop), 0, 5)
	s.ReviseTheme(tune(5, 2, ThenLoop), AtNextLoop)
	for g := 1; g < 3; g++ {
		toRow(t, s, 0)
		wantLead(t, s, g+1, "the pass playing")
	}
	for pass := 1; pass < 3; pass++ {
		for g := range 2 {
			toRow(t, s, 0)
			wantLead(t, s, 5+g, "the revision")
			if got, _ := s.Position(); !got.Theme || got.Grid != g || got.Pass != pass {
				t.Errorf("position %+v, want grid %d of pass %d", got, g, pass)
			}
		}
	}
}

// A revision for the next row leaves one waiting for the loop alone, and a
// later waiting one takes the place of an earlier.
func TestReviseThemeKeepsOneWaiting(t *testing.T) {
	s := themeAtRow(t, tune(1, 2, ThenLoop), 0, 5)
	s.ReviseTheme(tune(8, 2, ThenLoop), AtNextGrid)
	render(s, nowBlock)
	s.ReviseTheme(tune(3, 2, ThenLoop), AtNextLoop) // replaces the one for the grid
	render(s, nowBlock)
	s.ReviseTheme(tune(5, 2, ThenLoop), AtNextRow)
	toRow(t, s, 6)
	wantLead(t, s, 5, "the next row")
	toRow(t, s, 0)
	wantLead(t, s, 6, "the next grid")
	toRow(t, s, 0)
	wantLead(t, s, 3, "the next pass")
}

// Both kinds staged within one block both land.
func TestReviseThemeTwiceInABlock(t *testing.T) {
	s := themeAtRow(t, tune(1, 2, ThenLoop), 0, 5)
	s.ReviseTheme(tune(5, 2, ThenLoop), AtNextRow)
	s.ReviseTheme(tune(3, 2, ThenLoop), AtNextLoop)
	toRow(t, s, 6)
	wantLead(t, s, 5, "the next row")
	toRow(t, s, 0)
	toRow(t, s, 0)
	wantLead(t, s, 3, "the next pass")
}

// A revision can change how the theme ends: a loop made to stop does, after
// the grid the revision landed before, and a theme about to end loops.
func TestReviseThemeChangesThen(t *testing.T) {
	s := themeAtRow(t, tune(1, 2, ThenLoop), 1, 5)
	s.ReviseTheme(tune(5, 2, ThenGenerate), AtNextGrid)
	toRow(t, s, 0)
	if s.theme != nil || s.ThemePlaying() {
		t.Error("a loop revised to hand back is still playing after its last grid")
	}

	s = themeAtRow(t, tune(1, 2, ThenGenerate), 1, 5)
	s.ReviseTheme(tune(5, 2, ThenLoop), AtNextGrid)
	toRow(t, s, 0)
	wantLead(t, s, 5, "the pass after")
	if got, _ := s.Position(); got.Pass != 1 || got.Grid != 0 {
		t.Errorf("position %+v, want grid 0 of pass 1", got)
	}
}

// A revision waiting for a loop that never comes is dropped with the theme,
// and does not land on the next one.
func TestReviseThemeWaitingForALoopThatNeverComes(t *testing.T) {
	s := themeAtRow(t, tune(1, 2, ThenGenerate), 0, 5)
	s.ReviseTheme(tune(5, 2, ThenLoop), AtNextLoop)
	toRow(t, s, 0)
	wantLead(t, s, 2, "the last grid")
	toRow(t, s, 0)
	if s.theme != nil {
		t.Fatal("the theme did not end")
	}
	s.PlayTheme(tune(3, 2, ThenLoop))
	render(s, nowBlock)
	for range 2 {
		toRow(t, s, 0)
	}
	wantLead(t, s, 3, "the next theme's second pass")
}

// A revision is for the theme playing when it was made: none, or one that
// another PlayTheme has replaced since, and it is ignored.
func TestReviseThemeIgnoredWithoutItsTheme(t *testing.T) {
	p, _ := DeriveMood("village", 3, 2, 0)
	s := NewSynth(p)
	s.ReviseTheme(tune(5, 2, ThenLoop), AtNextRow)
	render(s, 2*nowBlock)
	if s.theme != nil || s.ThemePlaying() {
		t.Error("a revision with no theme playing started one")
	}

	s = themeAtRow(t, tune(1, 2, ThenLoop), 0, 5)
	s.ReviseTheme(tune(5, 2, ThenLoop), AtNextRow)
	s.ReviseTheme(tune(6, 2, ThenLoop), AtNextGrid)
	s.PlayTheme(tune(3, 2, ThenLoop))
	render(s, nowBlock)
	wantLead(t, s, 3, "the new theme")
	toRow(t, s, 0)
	wantLead(t, s, 4, "the new theme's second grid")

	s.ReviseTheme(nil, AtNextRow)
	s.ReviseTheme(&Theme{}, AtNextRow)
	toRow(t, s, 3)
	wantLead(t, s, 4, "after an empty revision")
}

// A Rewind starts the theme over, which is a boundary for a waiting revision
// of either kind, and counts its passes from 0 again.
func TestRewindLandsAWaitingRevision(t *testing.T) {
	for _, at := range []ThemeAt{AtNextGrid, AtNextLoop} {
		s := themeAtRow(t, tune(1, 2, ThenLoop), 1, 5)
		toRow(t, s, 0) // into the second pass
		s.ReviseTheme(tune(5, 2, ThenLoop), at)
		render(s, nowBlock)
		s.Rewind()
		render(s, nowBlock)
		wantLead(t, s, 5, "after the Rewind")
		if got, _ := s.Position(); got.Grid != 0 || got.Pass != 0 || s.revise != nil {
			t.Errorf("at %v: position %+v, waiting %v; want grid 0 of pass 0 and nothing waiting", at, got, s.revise != nil)
		}
	}
}

// A revision of a theme staged in the same block lands on its first row.
func TestReviseThemeBeforeItsFirstRow(t *testing.T) {
	for _, at := range []ThemeAt{AtNextRow, AtNextGrid, AtNextLoop} {
		p, _ := DeriveMood("village", 3, 2, 0)
		s := NewSynth(p)
		render(s, 5*s.samplesPerRow+77)
		s.PlayTheme(tune(1, 2, ThenLoop))
		s.ReviseTheme(tune(5, 2, ThenLoop), at)
		render(s, nowBlock)
		if s.stepRow != 0 {
			t.Fatalf("at %v: row %d, want the theme's row 0", at, s.stepRow)
		}
		if got, want := s.voices[trackLead].note, leadAt(s, 5); got != want {
			t.Errorf("at %v: the first row plays %d, want the revision's %d", at, got, want)
		}
	}
}

// An EndTheme counting when a revision lands still ends the theme where it
// was going to.
func TestReviseThemeKeepsAnEndThemeCounting(t *testing.T) {
	s := themeAtRow(t, tune(1, 3, ThenLoop), 0, 5)
	s.EndTheme(2, ThemeGrids)
	render(s, nowBlock)
	s.ReviseTheme(tune(5, 3, ThenLoop), AtNextRow)
	toRow(t, s, 0)
	wantLead(t, s, 6, "the second grid")
	if !s.ThemePlaying() {
		t.Fatal("the theme ended a grid early")
	}
	toRow(t, s, 0)
	if s.theme != nil || s.ThemePlaying() {
		t.Error("the theme plays on past the EndTheme")
	}
}

// ReviseTheme takes a copy, as PlayTheme does.
func TestReviseThemeCopiesTheTheme(t *testing.T) {
	s := themeAtRow(t, tune(1, 2, ThenLoop), 0, 5)
	th := tune(5, 2, ThenLoop)
	s.ReviseTheme(th, AtNextRow)
	th.Grids[0].Lead[6] = 8
	toRow(t, s, 6)
	wantLead(t, s, 5, "the next row")
}

// Position says which grid of a theme a row was, for the row heard as well
// as the last stepped, and nothing of one once the theme has ended.
func TestPositionNamesTheThemesGrid(t *testing.T) {
	p, _ := DeriveMood("village", 3, 2, 0)
	s := NewSynth(p)
	render(s, 3*s.samplesPerRow)
	if got, _ := s.Position(); got.Theme || got.Grid != 0 || got.Pass != 0 {
		t.Errorf("with no theme: %+v", got)
	}
	s.PlayTheme(tune(1, 2, ThenGenerate))
	render(s, nowBlock)
	first := s.nframe
	toRow(t, s, 0)
	if got, _ := s.Position(); !got.Theme || got.Grid != 1 || got.Pass != 0 {
		t.Errorf("in the second grid: %+v", got)
	}
	if got, ok := s.PositionAt(first); !ok || !got.Theme || got.Grid != 0 {
		t.Errorf("back in the first grid: %+v, %v", got, ok)
	}
	toRow(t, s, 0)
	if got, _ := s.Position(); got.Theme {
		t.Errorf("after the theme: %+v", got)
	}
}

// Revising the playing theme allocates nothing on the audio thread.
func TestReviseThemeDoesNotAllocateInRead(t *testing.T) {
	old := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = old }()

	s := themeAtRow(t, tune(1, 3, ThenLoop), 0, 5)
	buf := make([]byte, benchSlice*4)
	// the calls allocate their copies; Read, which applies them, must not
	s.ReviseTheme(tune(5, 3, ThenLoop), AtNextRow)
	s.ReviseTheme(tune(3, 2, ThenLoop), AtNextLoop)
	before := readAllocs()
	for range 4 * rows * s.samplesPerRow / benchSlice {
		s.Read(buf)
	}
	if n := readAllocs() - before; n != 0 {
		t.Errorf("%d allocations inside Synth.Read while revisions landed, want 0", n)
	}
	if s.themePass == 0 || s.revise != nil {
		t.Fatalf("the revisions did not both land: pass %d, waiting %v", s.themePass, s.revise != nil)
	}
}

// A revision with fewer grids landing at a grid mid-pass wraps into itself
// as one landing on a row does: the theme does not end there, and no pass is
// counted.
func TestReviseThemeAtTheNextGridToFewerGridsWraps(t *testing.T) {
	for _, then := range []ThemeEnd{ThenGenerate, ThenLoop} {
		s := themeAtRow(t, tune(1, 4, then), 1, 5)
		s.ReviseTheme(tune(5, 2, then), AtNextGrid)
		toRow(t, s, 0)
		wantLead(t, s, 5, "the next grid")
		if got, _ := s.Position(); !got.Theme || got.Grid != 0 || got.Pass != 0 {
			t.Errorf("then %v: position %+v, want grid 0 of pass 0", then, got)
		}
		toRow(t, s, 0)
		wantLead(t, s, 6, "the grid after")
	}
}

// A revision with more grids landing at what was the last grid plays on into
// them, as one landing on a row does.
func TestReviseThemeAtTheNextGridToMoreGrids(t *testing.T) {
	s := themeAtRow(t, tune(1, 2, ThenGenerate), 1, 5)
	s.ReviseTheme(tune(3, 4, ThenGenerate), AtNextGrid)
	toRow(t, s, 0)
	wantLead(t, s, 5, "the third grid")
	if got, _ := s.Position(); got.Grid != 2 || got.Pass != 0 {
		t.Errorf("position %+v, want grid 2 of pass 0", got)
	}
}

// A PlayTheme and its revision can both arrive after the audio thread has
// looked for a theme in a block and before it looks for revisions. The
// revision then waits in its slot for the theme to start, and lands.
func TestReviseThemeArrivingBeforeItsThemeStarts(t *testing.T) {
	for _, at := range []ThemeAt{AtNextRow, AtNextLoop} {
		p, _ := DeriveMood("village", 3, 2, 0)
		s := NewSynth(p)
		render(s, 5*s.samplesPerRow+77)
		s.PlayTheme(tune(1, 2, ThenLoop))
		s.ReviseTheme(tune(5, 2, ThenLoop), at)
		s.takeRevisions() // as if the block's look for the theme had come before both
		if s.reviseNow.Load() == nil && s.reviseLater.Load() == nil {
			t.Fatalf("at %v: the revision of a theme not yet started was dropped", at)
		}
		for s.theme == nil {
			render(s, 1)
		}
		if got, want := s.voices[trackLead].note, leadAt(s, 5); got != want {
			t.Errorf("at %v: the first row plays %d, want the revision's %d", at, got, want)
		}
		if s.reviseNow.Load() != nil || s.reviseLater.Load() != nil {
			t.Errorf("at %v: the revision is still in its slot after landing", at)
		}
	}
}
