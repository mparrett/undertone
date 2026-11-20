//go:build !js

package main

import (
	"testing"
	"time"

	"github.com/mparrett/undertone/engine"
)

// After a stall the clock moves up to the synth rather than the synth racing
// to the clock, so the missed notes are not sent in one burst.
func TestResyncAfterAStall(t *testing.T) {
	start := time.Unix(1000, 0)
	if s, now := resync(start, 5000, 4000); !s.Equal(start) || now != 5000 {
		t.Errorf("a lag under maxLag moved the clock: %v %d", s, now)
	}
	lag := int64(300 * engine.SampleRate) // five minutes stopped
	s, now := resync(start, 1000+lag, 1000)
	if now != 1000 {
		t.Errorf("now %d after a stall, want the synth's 1000", now)
	}
	if got := s.Sub(start); got != 300*time.Second {
		t.Errorf("clock moved %v, want the stall's 5m0s", got)
	}
}
