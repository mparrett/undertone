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

// ThemeUnit is what EndTheme counts in.
type ThemeUnit int

const (
	// ThemeRows counts sequencer rows: an eighth note each.
	ThemeRows ThemeUnit = iota
	// ThemeBars counts bars: 8 rows, half a grid.
	ThemeBars
	// ThemeGrids counts grids: one pattern, 16 rows.
	ThemeGrids
)

// unitRows is how many rows one u spans.
func unitRows(u ThemeUnit) int {
	switch u {
	case ThemeBars:
		return rows / 2
	case ThemeGrids:
		return rows
	}
	return 1
}

// endCmd carries an EndTheme call to the audio thread; gen is the theme it
// was meant for, the last PlayTheme before it.
type endCmd struct {
	n   int
	u   ThemeUnit
	gen int64
}

// themeCmd carries a PlayTheme call to the audio thread; gen is the call's
// number, which ThemePlaying compares with the last theme to end.
type themeCmd struct {
	t   *Theme
	gen int64
}

// ThemeAt is when a ReviseTheme lands.
type ThemeAt int

const (
	// AtNextRow lands on the next row to step, as a SetGrid does: for an
	// editor, where a change is heard the next time the playhead passes it.
	AtNextRow ThemeAt = iota
	// AtNextGrid lands as the next grid starts.
	AtNextGrid
	// AtNextLoop lands as a ThenLoop theme next starts over: for a variation
	// made ready during one pass and played on the next.
	AtNextLoop
)

// reviseCmd carries a ReviseTheme call to the audio thread; gen is the theme
// it was meant for, the last PlayTheme before it.
type reviseCmd struct {
	t   *Theme
	at  ThemeAt
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

// ReviseTheme replaces what the playing theme holds, its grids and its Then,
// and leaves where it is playing alone: the playhead keeps its grid and row,
// where PlayTheme would start over from the first. It is what SetGrid is to
// one grid. t is always the whole theme.
//
// At AtNextRow the change is heard from the next row on. At AtNextGrid or
// AtNextLoop it waits for that boundary; only one revision waits, a later one
// taking its place, and an AtNextRow meanwhile does not disturb it. A waiting
// revision also lands at a Rewind, and is dropped if the theme ends first:
// an AtNextLoop one always is unless the theme playing loops.
//
// The playhead's grid number carries over. If t has fewer grids than that,
// it wraps into t; with as many, the grid playing is replaced by t's at the
// same place. An EndTheme already counting still ends the theme where it was
// going to.
//
// ReviseTheme applies to the theme started by the last PlayTheme before it,
// and is ignored if that one has finished, or if t is nil or has no grids.
// Safe to call from any goroutine.
func (s *Synth) ReviseTheme(t *Theme, at ThemeAt) {
	if t == nil || len(t.Grids) == 0 {
		return
	}
	c := &reviseCmd{
		&Theme{Grids: append([]Grid(nil), t.Grids...), Then: t.Then}, // the caller may keep editing its copy
		at, s.themeGen.Load(),
	}
	if at == AtNextRow {
		s.reviseNow.Store(c)
	} else {
		s.reviseLater.Store(c)
	}
}

// takeRevisions runs on the audio thread at a block boundary, after any
// PlayTheme staged in the same block, so a revision of the theme just
// started is not taken for one of the theme before.
func (s *Synth) takeRevisions() {
	now, later := s.reviseNow.Swap(nil), s.reviseLater.Swap(nil)
	if s.theme == nil {
		return
	}
	if now != nil && now.gen == s.themeOf {
		s.theme = now.t
		s.themeAt %= len(now.t.Grids)
	}
	if later != nil && later.gen == s.themeOf {
		s.revise = later
	}
}

// EndTheme ends the playing theme after n units, counted from the unit the
// next row belongs to: (1, ThemeGrids) finishes the grid playing now, (1,
// ThemeBars) the bar, and (4, ThemeRows) plays four more rows. At a unit's
// boundary the unit about to start is the current one, so it plays whole.
// Units count from the theme's start, which is always a pattern's, so a bar
// or a grid is one of the theme's own. The theme then ends as its Then says,
// except that a looping theme hands back to the generator, as ThenGenerate
// does; one that would end sooner on its own still does. n below 1 counts as
// 1. EndTheme applies to the theme started by the last PlayTheme before it,
// so it is ignored if that one has finished or another has started since; a
// second call replaces the first, and a Rewind drops it. Safe to call from
// any goroutine.
func (s *Synth) EndTheme(n int, u ThemeUnit) {
	s.endCmd.Store(&endCmd{max(n, 1), u, s.themeGen.Load()})
}

// ThemePlaying reports whether the last theme started with PlayTheme is still
// to finish: false once its last grid has played (ThenGenerate,
// ThenSilence), or once EndTheme has ended it, the only way a ThenLoop theme
// ends short of another PlayTheme. Safe to call from any goroutine.
func (s *Synth) ThemePlaying() bool { return s.themeGen.Load() != s.themeDone.Load() }

// startTheme runs on the audio thread at a block boundary, before the row
// step, so the theme's first row steps on this frame.
func (s *Synth) startTheme(c *themeCmd) {
	s.theme, s.themeAt, s.themeOf, s.themeFresh, s.silent = c.t, 0, c.gen, true, false
	s.themeEnds, s.themePass, s.revise = false, 0, nil
	if c.t == nil {
		return
	}
	if s.row != 0 {
		s.pat++ // the cut pattern ends here; the bed draws a fresh one
	}
	s.row, s.rowLeft, s.leadIn, s.cut = 0, 0, true, true
	s.late = [2]lateNote{} // a note of the cut pattern, not yet started, never does
}

// markThemeEnd sets the row the playing theme ends at, for EndTheme's call c.
// It runs on the audio thread after any PlayTheme staged in the same block,
// so a theme started just before c is the one it ends.
func (s *Synth) markThemeEnd(c *endCmd) {
	if s.theme == nil || c.gen != s.themeOf {
		return
	}
	size, next := unitRows(c.u), s.pat*rows+s.row
	s.themeEndAt, s.themeEnds = next-next%size+c.n*size, true
}

// endThemeAt ends the theme before the row about to step when EndTheme's
// count has run out there. The generator's pattern for the rest of this one
// was built at its row 0 and only the rows already played were overwritten
// by the theme's grid, so a mid-pattern hand-back plays on from its rows.
func (s *Synth) endThemeAt() {
	if s.theme == nil || !s.themeEnds || s.pat*rows+s.row < s.themeEndAt {
		return
	}
	s.themeEnds = false
	g := s.theme.Grids[s.themeAt]
	if s.theme.Then == ThenSilence {
		s.silent = true
	}
	s.theme = nil
	s.finishTheme(s.themeOf)
	if s.row != 0 && !g.Bed && !s.silent {
		s.resumeBed()
	}
}

// bedTracks are the generator's tracks a grid without a bed silences.
var bedTracks = []int{0, trackFifth, 2, 4}

// resumeBed re-sounds, from the row about to step, the generator's bed notes
// that would still be held there. The drone and fifth trigger only at row 0
// and the pad at rows 0 and 8, and a grid without a bed blanked those rows,
// so without this a mid-pattern hand-back leaves them out until the next
// pattern: a dropout right where the music changes hands.
func (s *Synth) resumeBed() {
	gen := buildPatterns(s.params, s.pat)
	for _, t := range bedTracks {
		if s.pats[t][s.row].note != 0 {
			continue // the generator starts a note here anyway
		}
		for r := s.row - 1; r >= 0; r-- {
			if n := gen[t][r]; n.note != 0 {
				if left := n.gateRows - (s.row - r); left > 0 {
					n.gateRows = left
					s.pats[t][s.row] = n
				}
				break
			}
		}
	}
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
	t, at, wraps := s.themeAhead()
	if t != s.theme {
		s.theme, s.revise = t, nil // the waiting revision lands here
	}
	s.themeFresh = false
	if wraps {
		s.themePass++
	}
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

// themeAhead is what the pattern about to start plays: the theme, which is
// the waiting revision's if that lands there, and the grid of it, or -1 when
// the theme ends there. wraps is whether the theme starts over there.
func (s *Synth) themeAhead() (t *Theme, at int, wraps bool) {
	t = s.theme
	if s.themeFresh {
		// the theme's first grid, or a Rewind's: a boundary of every kind
		if s.revise != nil {
			t = s.revise.t
		}
		return t, 0, false
	}
	if s.revise != nil && s.revise.at == AtNextGrid {
		t = s.revise.t
	}
	switch {
	case s.themeAt+1 < len(t.Grids):
		return t, s.themeAt + 1, false
	case t.Then != ThenLoop:
		return t, -1, false
	}
	if s.revise != nil {
		t = s.revise.t
	}
	return t, 0, true
}

// themeWord is where the theme is as the row log keeps it: the pass above
// the grid plus one, and 0 for no theme.
func (s *Synth) themeWord() int64 {
	if s.theme == nil {
		return 0
	}
	return int64(s.themePass)<<32 | int64(s.themeAt+1)
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
	theme, silent, at, fresh := s.theme, s.silent, s.themeAt, false
	if theme != nil && s.row == 0 {
		fresh = s.themeFresh
		if theme, at, _ = s.themeAhead(); at < 0 {
			theme, silent = nil, theme.Then == ThenSilence
		}
	}
	switch {
	case theme != nil:
		g, own = &theme.Grids[at], ownTheme
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
