package engine

import "testing"

// stepTo renders until the row before absolute row r (pat*rows+row) has just
// stepped, so r is the next to step and EndTheme counts from it; false if
// that takes more than four patterns.
func stepTo(s *Synth, r int) bool {
	for range 4*rows*s.samplesPerRow + nowBlock {
		if s.pat*rows+s.row == r && s.rowLeft == s.samplesPerRow-1 {
			return true
		}
		render(s, 1)
	}
	return false
}

// endsAt checks that s's theme plays up to absolute row end and hands back
// there: still the theme's while row end-1 steps, gone once row end has.
func endsAt(t *testing.T, s *Synth, end int) {
	t.Helper()
	if !stepTo(s, end) {
		t.Fatalf("never reached row %d", end)
	}
	if s.theme == nil || !s.ThemePlaying() {
		t.Fatalf("theme gone before row %d stepped", end)
	}
	if !stepTo(s, end+1) {
		t.Fatalf("never reached row %d", end+1)
	}
	if s.theme != nil || s.ThemePlaying() {
		t.Fatalf("theme still playing after row %d stepped", end)
	}
}

// themeSynth starts a theme of n grids at an absolute row r0 (a pattern's
// start), and returns the synth with row r0+at next to step.
func themeSynth(t *testing.T, n int, then ThemeEnd, at int) (*Synth, int) {
	t.Helper()
	p, _ := DeriveMood("village", 3, 2, 0)
	s := NewSynth(p)
	render(s, 5*s.samplesPerRow+77)
	s.PlayTheme(themeOf(n, then))
	render(s, nowBlock)
	r0 := s.pat * rows
	if !stepTo(s, r0+at) {
		t.Fatalf("never reached row %d of the theme", at)
	}
	return s, r0
}

func TestEndThemeUnits(t *testing.T) {
	for _, c := range []struct {
		name string
		n    int
		u    ThemeUnit
		at   int // the theme's row next to step when EndTheme is called
		end  int // the theme's first row not played
	}{
		{"finish the grid", 1, ThemeGrids, 5, 16},
		{"that grid and one more", 2, ThemeGrids, 5, 32},
		{"finish the bar", 1, ThemeBars, 3, 8},
		{"finish the second bar", 1, ThemeBars, 11, 16},
		{"two bars from the first", 2, ThemeBars, 3, 16},
		{"four rows", 4, ThemeRows, 5, 9},
		// At a boundary the unit about to start is the current one.
		{"a bar at its boundary plays it whole", 1, ThemeBars, 8, 16},
		{"n below 1 is 1", 0, ThemeBars, 3, 8},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, r0 := themeSynth(t, 4, ThenGenerate, c.at)
			s.EndTheme(c.n, c.u)
			endsAt(t, s, r0+c.end)
			if s.silent {
				t.Error("ThenGenerate fell silent")
			}
		})
	}
}

// A looping theme never ends on its own; EndTheme hands it to the generator,
// even when the count runs past its last grid and wraps.
func TestEndThemeEndsALoop(t *testing.T) {
	s, r0 := themeSynth(t, 2, ThenLoop, 3)
	s.EndTheme(3, ThemeGrids)
	endsAt(t, s, r0+3*rows)
	if s.silent {
		t.Error("a looping theme ended into silence")
	}
}

// A count past a non-looping theme's end changes nothing: it ends on its own.
func TestEndThemePastTheEnd(t *testing.T) {
	s, r0 := themeSynth(t, 2, ThenGenerate, 3)
	s.EndTheme(5, ThemeGrids)
	endsAt(t, s, r0+2*rows)
}

func TestEndThemeIntoSilence(t *testing.T) {
	s, r0 := themeSynth(t, 4, ThenSilence, 3)
	s.EndTheme(1, ThemeBars)
	endsAt(t, s, r0+8)
	if !s.silent {
		t.Error("ThenSilence ended without the silence")
	}
}

// Mid-pattern, the generator takes the rest of the pattern: its own rows,
// not the theme's grid and not silence.
func TestEndThemeHandsBackMidPattern(t *testing.T) {
	s, r0 := themeSynth(t, 4, ThenGenerate, 3)
	s.EndTheme(1, ThemeBars)
	if !stepTo(s, r0+rows) {
		t.Fatal("never reached the next pattern")
	}
	if g := s.playingGrid(); g != nil {
		t.Errorf("after the hand-back a grid owns the pattern: %+v", g)
	}
}

// EndTheme is for the theme playing when it was called: a theme started
// after it plays on.
func TestEndThemeIgnoresALaterTheme(t *testing.T) {
	s, r0 := themeSynth(t, 4, ThenGenerate, 3)
	s.EndTheme(1, ThemeBars)
	s.PlayTheme(themeOf(4, ThenLoop))
	render(s, nowBlock)
	if !stepTo(s, s.pat*rows+rows+2) {
		t.Fatal("never stepped")
	}
	if s.theme == nil {
		t.Errorf("the later theme ended (started %d rows after the call's)", s.pat*rows-r0)
	}
}

// Called with no theme, or with one that has finished, it does nothing, and
// does not end the next theme.
func TestEndThemeWithNoTheme(t *testing.T) {
	p, _ := DeriveMood("village", 3, 2, 0)
	s := NewSynth(p)
	s.EndTheme(1, ThemeRows)
	render(s, nowBlock)
	s.PlayTheme(themeOf(2, ThenLoop))
	render(s, nowBlock)
	if !stepTo(s, s.pat*rows+rows+2) || s.theme == nil {
		t.Error("an EndTheme from before the theme ended it")
	}
}
