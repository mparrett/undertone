package engine

import (
	"testing"
	"time"
)

// fakeClock advances by step on every reading, so each Read takes exactly
// step.
func fakeClock(step time.Duration) func() time.Time {
	t := time.Unix(0, 0)
	return func() time.Time {
		t = t.Add(step)
		return t
	}
}

func TestRenderStatsCountsReadsAndTime(t *testing.T) {
	s := NewSynth(DeriveDungeon(1, 1, 0))
	s.clock.now = fakeClock(2 * time.Millisecond)
	buf := make([]byte, 4*512) // 11.6 ms of audio
	for range 3 {
		s.Read(buf)
	}
	s.Read(buf[:0]) // an empty Read is not a render
	got := s.RenderStats()
	if f := s.Frames(); f != 3*512 {
		t.Errorf("Frames = %d, want %d", f, 3*512)
	}
	want := RenderStats{Reads: 3, Last: 2 * time.Millisecond, Max: 2 * time.Millisecond, Total: 6 * time.Millisecond}
	if got != want {
		t.Errorf("RenderStats = %+v, want %+v", got, want)
	}
}

// Over counts the Reads that took longer than the audio they made.
func TestRenderStatsCountsReadsOverBudget(t *testing.T) {
	s := NewSynth(DeriveDungeon(1, 1, 0))
	buf := make([]byte, 4*512) // 11.6 ms of audio
	s.clock.now = fakeClock(11 * time.Millisecond)
	s.Read(buf)
	s.clock.now = fakeClock(12 * time.Millisecond)
	s.Read(buf)
	s.Read(buf)
	s.clock.now = fakeClock(time.Millisecond)
	s.Read(buf)
	got := s.RenderStats()
	if got.Over != 2 || got.Max != 12*time.Millisecond || got.Last != time.Millisecond {
		t.Errorf("RenderStats = %+v, want 2 over, max 12ms, last 1ms", got)
	}
}
