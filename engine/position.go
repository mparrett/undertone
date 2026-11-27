package engine

import "math"

// Position is where the sequencer is, for a host drawing a playhead.
type Position struct {
	Row int // 0 to 15
	// Pattern counts patterns since the synth started. A theme starting
	// mid-pattern, or a Rewind there, cuts that pattern short and starts
	// the next; a change made with SetParametersNow keeps the count.
	Pattern int
	Frame   int64 // the frame the row started on
	// Pending is a SetParameters staged for the next pattern boundary, and
	// ThemePending a PlayTheme staged to start within a block. Both are the
	// synth's state now, not at Frame: a host following the heard row with
	// PositionAt sees Pending clear up to one output queue before the change
	// is heard.
	Pending, ThemePending bool
	// Theme is whether a theme played this row. Grid is then which of its
	// grids, from 0, and Pass how many times the theme had played through
	// before: 0 until a ThenLoop theme first starts over. A Rewind counts
	// from 0 again; a ReviseTheme does not.
	Theme      bool
	Grid, Pass int
	// Run is how many rows into its run the row was: the count a grid's or a
	// theme's polymetric lanes (those ending in |) and chance steps go by,
	// from 0 where the run began. A lane of n sixteenths plays its step
	// (2*Run+half)%n, so a host can draw such a lane for the row heard, or
	// for any row ahead, without waiting for its hits. The run starts over
	// at a Rewind and when a grid or theme takes over from what played
	// before; it carries on through a theme's grids, SetGrid and ReviseTheme.
	Run int
}

// Position returns the last row the synth stepped; ok is false before the
// first. Safe to call from any goroutine.
func (s *Synth) Position() (Position, bool) { return s.PositionAt(math.MaxInt64) }

// RunStart is the row the grid or theme now playing counts its run from, as
// of the last row rendered; Position.Run is the same count for a row heard.
// It is
// Pattern*16+Row: a polymetric lane (one ending in |) plays its step
// (Pattern*16+Row-RunStart)*2 sixteenths in, wrapping at its length, for a
// host drawing where such a lane is. It moves when the run starts over, at a
// Rewind or when the grid or theme takes over from what played before. Safe
// to call from any goroutine.
func (s *Synth) RunStart() int { return int(s.runStart.Load()) }

// PositionAt returns the row sounding at an output frame, for a playhead
// that follows what is heard rather than what has been rendered; ok is false
// for a frame before the first row or older than the last 64 rows. Safe to
// call from any goroutine.
func (s *Synth) PositionAt(frame int64) (Position, bool) {
	start, row, pat, th, run, ok := s.rowLog.find(frame)
	if !ok {
		return Position{}, false
	}
	c := s.themeCmd.Load() // a nil theme stops one: nothing is about to start
	return Position{
		Row: row, Pattern: pat, Frame: start,
		Pending:      s.staged() != nil,
		ThemePending: c != nil && c.t != nil,
		Theme:        th != 0,
		Grid:         max(int(th&0xffffffff)-1, 0),
		Pass:         int(th >> 32),
		Run:          run,
	}, true
}
