package engine

import "testing"

// The delay stays three rows long as the tempo changes, so its echoes land
// on the grid at any tension; a tempo no slower than one it has played
// reuses the buffer it already has.
func TestDelayFollowsTheTempo(t *testing.T) {
	p := DeriveDungeon(1, 0, 0)
	s := NewSynth(p)
	var slowest *float64
	for _, bpm := range []float64{p.BPM + 30, p.BPM - 10, p.BPM, p.BPM + 30} {
		q := p
		q.BPM = bpm
		s.SetParametersNow(q)
		render(s, s.samplesPerRow+1) // past the next row, where it applies
		if want := 3 * int(SampleRate*60/(bpm*2)); len(s.dl) != want || len(s.dr) != want {
			t.Errorf("at %v bpm the delay is %d samples, want %d", bpm, len(s.dl), want)
		}
		if bpm == p.BPM-10 {
			slowest = &s.dl[0]
		} else if slowest != nil && &s.dl[0] != slowest {
			t.Errorf("at %v bpm the delay reallocated", bpm)
		}
	}
}

// Resizing keeps the echoes due soonest in reading order, so playback does
// not jump, and a regrown line follows them with silence, not stale echoes.
func TestDelayResizeKeepsTheNextEchoes(t *testing.T) {
	s := NewSynth(DeriveDungeon(1, 0, 0))
	n := len(s.dl)
	for i := range s.dl {
		s.dl[i], s.dr[i] = float64(i+1), -float64(i+1)
	}
	s.dpos = n - 10 // ten echoes before the ring wraps
	s.fitDelay(n / 2)
	if s.dpos != 0 || s.dl[0] != float64(n-9) || s.dl[9] != float64(n) || s.dl[10] != 1 || s.dr[10] != -1 {
		t.Fatalf("after shrinking, the line reads %v %v %v from %d; want the ring's next echoes in order",
			s.dl[0], s.dl[9], s.dl[10], s.dpos)
	}
	s.fitDelay(n) // regrow into the same buffer
	if s.dl[n/2-1] != float64(n/2-10) {
		t.Errorf("regrowing changed the kept echoes: %v", s.dl[n/2-1])
	}
	for i := n / 2; i < n; i++ {
		if s.dl[i] != 0 || s.dr[i] != 0 {
			t.Fatalf("sample %d of the regrown line holds a stale echo %v", i, s.dl[i])
		}
	}
}
