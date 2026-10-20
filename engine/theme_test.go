package engine

import (
	"testing"
)

// themeOf returns n grids whose lead plays degree i+1 on row 0 of grid i,
// with no bed, so each pattern's first lead note names its grid.
func themeOf(n int, then ThemeEnd) *Theme {
	t := &Theme{Then: then}
	for i := range n {
		var g Grid
		g.Lead[0] = i + 1
		t.Grids = append(t.Grids, g)
	}
	return t
}

// leadAt is the note grid degree d plays in the lead lane under s's params.
func leadAt(s *Synth, d int) int {
	base, _ := GridBases(s.params)
	return degreeNote(s.params, base, d)
}

// untilRow renders a frame at a time until row 0 of a pattern has just
// stepped, and returns false if that takes more than a pattern.
func untilRow0(s *Synth) bool {
	for range rows*s.samplesPerRow + nowBlock + 1 {
		render(s, 1)
		if s.row == 1 && s.rowLeft == s.samplesPerRow-1 {
			return true
		}
	}
	return false
}

// A theme starts within one block, at row 0 of its first grid, cutting the
// pattern that was playing; then it plays its grids in order.
func TestThemeStartsWithinABlockAndPlaysInOrder(t *testing.T) {
	p, _ := DeriveMood("village", 3, 2, 0)
	s := NewSynth(p)
	render(s, 5*s.samplesPerRow+77) // mid-pattern
	s.PlayTheme(themeOf(3, ThenLoop))
	render(s, nowBlock)
	if s.row != 1 || s.theme == nil || s.themeAt != 0 {
		t.Fatalf("a block after PlayTheme: row %d, theme grid %d; want row 0 of grid 0 just stepped", s.row, s.themeAt)
	}
	for i := range 4 { // the fourth wraps to the first: ThenLoop
		if i > 0 && !untilRow0(s) {
			t.Fatalf("grid %d never started", i)
		}
		if want := leadAt(s, i%3+1); s.voices[trackLead].note != want {
			t.Errorf("pattern %d: lead plays %d, want grid %d's %d", i, s.voices[trackLead].note, i%3, want)
		}
	}
	if !s.ThemePlaying() {
		t.Error("a looping theme reports finished")
	}
}

// ThenGenerate hands back to the generator after the last grid, and
// ThemePlaying turns false.
func TestThemeThenGenerateHandsBack(t *testing.T) {
	p, _ := DeriveMood("village", 3, 2, 0)
	s := NewSynth(p)
	s.PlayTheme(themeOf(2, ThenGenerate))
	if !s.ThemePlaying() {
		t.Fatal("ThemePlaying is false straight after PlayTheme")
	}
	render(s, nowBlock)
	untilRow0(s)
	render(s, (rows-1)*s.samplesPerRow) // into the last grid's last row
	if s.row != 0 || !s.ThemePlaying() {
		t.Fatalf("on the last grid's last row (row %d): playing %v; want true until it has played", s.row, s.ThemePlaying())
	}
	untilRow0(s)
	if s.ThemePlaying() || s.playingGrid() != nil {
		t.Errorf("after the last grid: playing %v, grid %v; want the generator", s.ThemePlaying(), s.playingGrid())
	}
}

// ThenSilence rests after the last grid until the next PlayTheme; a nil
// PlayTheme brings the generator back.
func TestThemeThenSilenceRestsUntilStopped(t *testing.T) {
	p, _ := DeriveMood("dungeon", 3, 2, 0)
	s := NewSynth(p)
	s.PlayTheme(themeOf(1, ThenSilence))
	render(s, nowBlock)
	untilRow0(s)
	if s.ThemePlaying() {
		t.Error("an outro still reports playing after its last grid")
	}
	render(s, 13*SampleRate) // the tails, the delay and the long room ring out
	if m := maxAbs(render(s, SampleRate)); m > 1 {
		t.Errorf("after an outro the output reaches %d, want silence", m)
	}
	s.PlayTheme(nil)
	render(s, nowBlock)
	untilRow0(s)
	if m := maxAbs(render(s, SampleRate)); m < 1000 {
		t.Errorf("after PlayTheme(nil) the output reaches only %d: the generator did not come back", m)
	}
}

// PlayTheme copies the theme, so a caller editing its own afterwards does not
// change what plays.
func TestPlayThemeCopiesTheTheme(t *testing.T) {
	p, _ := DeriveMood("village", 3, 2, 0)
	s := NewSynth(p)
	th := themeOf(1, ThenLoop)
	s.PlayTheme(th)
	th.Grids[0].Lead[0] = 7
	render(s, nowBlock)
	if want := leadAt(s, 1); s.voices[trackLead].note != want {
		t.Errorf("lead plays %d, want the copied grid's %d", s.voices[trackLead].note, want)
	}
}

func maxAbs(pcm []byte) int {
	best := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		best = max(best, abs(int(int16(uint16(pcm[i])|uint16(pcm[i+1])<<8))))
	}
	return best
}
