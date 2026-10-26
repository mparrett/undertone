package engine

// Theme is authored music: grids played in order, one per pattern (two bars
// each), then Then. A theme is written in scale degrees, so it plays in
// whatever key and tempo the params set; set them with SetParametersNow
// alongside PlayTheme. Its degrees count the tones of the scale playing, so
// the same theme climbs about 1.6 octaves over 8 degrees in a five-note mood.
type Theme struct {
	Grids []Grid
	Then  ThemeEnd
}

// ThemeEnd is what a theme does after its last grid.
type ThemeEnd int

const (
	// ThenGenerate hands back to what played before the theme: the
	// generator, or a grid set with SetGrid. For a title theme.
	ThenGenerate ThemeEnd = iota
	// ThenSilence lets the last notes ring out and then rests, until the next
	// PlayTheme. For an outro.
	ThenSilence
	// ThenLoop plays the theme again from its first grid. For a boss phrase.
	ThenLoop
)

// themeCmd carries a PlayTheme call to the audio thread; gen is the call's
// number, which ThemePlaying compares with the last theme to end.
type themeCmd struct {
	t   *Theme
	gen int64
}

var silence = Grid{} // no bed, every lane at rest

// PlayTheme starts t within one block, from its first grid at the start of a
// pattern; the pattern that was playing is cut, with the fades a mood change
// uses. A nil or empty t stops any theme, or the silence after one, and hands
// back to what played before. Safe to call from any goroutine.
func (s *Synth) PlayTheme(t *Theme) {
	if t != nil && len(t.Grids) > 0 {
		c := Theme{Grids: append([]Grid(nil), t.Grids...), Then: t.Then} // the caller may keep editing its copy
		t = &c
	} else {
		t = nil
	}
	gen := s.themeGen.Add(1)
	if t == nil {
		s.finishTheme(gen)
	}
	s.themeCmd.Store(&themeCmd{t, gen})
}

// ThemePlaying reports whether the last theme started with PlayTheme is still
// to finish: false once its last grid has played (ThenGenerate,
// ThenSilence), never false for ThenLoop. Safe to call from any goroutine.
func (s *Synth) ThemePlaying() bool { return s.themeGen.Load() != s.themeDone.Load() }

// startTheme runs on the audio thread at a block boundary, before the row
// step, so the theme's first row steps on this frame.
func (s *Synth) startTheme(c *themeCmd) {
	s.theme, s.themeAt, s.themeOf, s.themeFresh, s.silent = c.t, 0, c.gen, true, false
	if c.t == nil {
		return
	}
	if s.row != 0 {
		s.pat++ // the cut pattern ends here; the bed draws a fresh one
	}
	s.row, s.rowLeft, s.leadIn = 0, 0, true
}

// playingGrid is the grid that owns this pattern: the theme's, the silence
// after one, or SetGrid's; nil leaves it to the generator.
func (s *Synth) playingGrid() *Grid {
	switch {
	case s.theme != nil:
		return &s.theme.Grids[s.themeAt]
	case s.silent:
		return &silence
	}
	return s.grid.Load()
}

// advanceTheme moves to the next grid as a pattern starts, so the last grid
// counts as played only once all of its rows have.
func (s *Synth) advanceTheme() {
	if s.theme == nil {
		return
	}
	at := s.themeNext()
	s.themeFresh = false
	if at >= 0 {
		s.themeAt = at
		return
	}
	if s.theme.Then == ThenSilence {
		s.silent = true
	}
	s.theme = nil
	s.finishTheme(s.themeOf)
}

// themeNext is the grid advanceTheme moves the playing theme to, or -1 when
// the theme ends there.
func (s *Synth) themeNext() int {
	switch {
	case s.themeFresh:
		return s.themeAt
	case s.themeAt+1 < len(s.theme.Grids):
		return s.themeAt + 1
	case s.theme.Then == ThenLoop:
		return 0
	}
	return -1
}

// Who owns a row's drums. A grid's polymetric lanes run on while the owner
// stays the same, through a theme's grids and SetGrid's edits alike.
const (
	ownGen = iota
	ownGrid
	ownTheme // and the silence after one
)

// nextOwner is the grid that will own the row about to step (nil for the
// generator), its owner, and the row its polymetric lanes will count from:
// that row itself when a theme starts or the owner changes there. It reads
// ahead, as step will find things once advanceTheme has run.
func (s *Synth) nextOwner() (g *Grid, own, from int) {
	theme, silent, at, fresh := s.theme != nil, s.silent, s.themeAt, false
	if theme && s.row == 0 {
		fresh = s.themeFresh
		if at = s.themeNext(); at < 0 {
			theme, silent = false, s.theme.Then == ThenSilence
		}
	}
	switch {
	case theme:
		g, own = &s.theme.Grids[at], ownTheme
	case silent:
		g, own = &silence, ownTheme
	default:
		if g = s.grid.Load(); g != nil {
			own = ownGrid
		}
	}
	from = s.runFrom
	if fresh || own != s.owner {
		from = s.pat*rows + s.row
	}
	return g, own, from
}

// finishTheme records that call gen has finished. themeDone only rises, so a
// theme ending on the audio thread cannot undo a newer call's record.
func (s *Synth) finishTheme(gen int64) {
	for {
		d := s.themeDone.Load()
		if d >= gen || s.themeDone.CompareAndSwap(d, gen) {
			return
		}
	}
}
