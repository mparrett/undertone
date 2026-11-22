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
}

// Position returns the last row the synth stepped; ok is false before the
// first. Safe to call from any goroutine.
func (s *Synth) Position() (Position, bool) { return s.PositionAt(math.MaxInt64) }

// PositionAt returns the row sounding at an output frame, for a playhead
// that follows what is heard rather than what has been rendered; ok is false
// for a frame before the first row or older than the last 64 rows. Safe to
// call from any goroutine.
func (s *Synth) PositionAt(frame int64) (Position, bool) {
	start, row, pat, ok := s.rowLog.find(frame)
	if !ok {
		return Position{}, false
	}
	c := s.themeCmd.Load() // a nil theme stops one: nothing is about to start
	return Position{
		Row: row, Pattern: pat, Frame: start,
		Pending:      s.staged() != nil,
		ThemePending: c != nil && c.t != nil,
	}, true
}
