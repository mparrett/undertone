package engine

import (
	"sync/atomic"
	"time"
)

// RenderStats is how long the synth has taken to render, for telling a slow
// render apart from an output that starved for other reasons (a busy page, a
// stalled worker). Each field is current, but a Read can land between two of
// them, so they may disagree by one Read.
type RenderStats struct {
	Reads            int64
	Last, Max, Total time.Duration
	// Over counts Reads that took longer than the audio they rendered lasts:
	// a synth doing that falls behind whatever its output buffers.
	Over int64
}

// renderClock times each Read. In wasm time.Now is performance.now, which
// browsers coarsen to 100 µs to 1 ms on a page that isn't cross-origin
// isolated: Max and Over stay meaningful against a slice's 11.6 ms, Last is
// noisy.
type renderClock struct {
	now                     func() time.Time // a test's fake, or time.Now
	reads, last, max, total atomic.Int64
	over                    atomic.Int64
}

// record notes a Read of frames that started at t0. Only the audio thread
// writes, so the loads here race with nothing.
func (c *renderClock) record(t0 time.Time, frames int) {
	d := int64(c.now().Sub(t0))
	c.reads.Store(c.reads.Load() + 1)
	c.last.Store(d)
	c.total.Store(c.total.Load() + d)
	if d > c.max.Load() {
		c.max.Store(d)
	}
	if d > int64(frames)*int64(time.Second)/SampleRate {
		c.over.Store(c.over.Load() + 1)
	}
}

// RenderStats returns the render timings so far. Safe to call from any
// goroutine.
func (s *Synth) RenderStats() RenderStats {
	c := &s.clock
	return RenderStats{
		Reads: c.reads.Load(),
		Last:  time.Duration(c.last.Load()),
		Max:   time.Duration(c.max.Load()),
		Total: time.Duration(c.total.Load()),
		Over:  c.over.Load(),
	}
}
