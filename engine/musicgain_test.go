package engine

import (
	"math"
	"testing"
)

func TestMusicGainGlides(t *testing.T) {
	s := NewSynth(DeriveDungeon(7, 3, 0.5))
	for range 1000 {
		s.frame()
	}
	const secs = 0.1
	frames := int(secs * SampleRate)
	s.GlideMusicGain(0.2, secs)
	prev := 1.0
	// One frame per step, so a jump of the whole 0.8 shows up as a click.
	maxStep := 0.8/float64(frames) + 1e-12
	for i := range frames {
		s.frame()
		if d := prev - s.mgain; d < 0 || d > maxStep {
			t.Fatalf("frame %d: gain moved %v (from %v to %v), want a step in [0, %v]", i, d, prev, s.mgain, maxStep)
		}
		prev = s.mgain
	}
	if s.mgain != 0.2 {
		t.Fatalf("after the glide gain = %v, want exactly 0.2", s.mgain)
	}

	// Back to 1 lands exactly, so the bus is skipped (byte-identical) again.
	s.SetMusicGain(1)
	for range int(musicGlide*SampleRate) + 1 {
		s.frame()
	}
	if s.mgain != 1 {
		t.Fatalf("after returning gain = %v, want exactly 1", s.mgain)
	}
}

func TestMusicGainBeforeFirstFrameIsImmediate(t *testing.T) {
	s := NewSynth(DeriveDungeon(7, 3, 0.5))
	s.SetMusicGain(0.25)
	s.frame()
	if s.mgain != 0.25 {
		t.Fatalf("first frame gain = %v, want 0.25 at once", s.mgain)
	}
}

func TestMusicGainRetargetsMidGlide(t *testing.T) {
	s := NewSynth(DeriveDungeon(7, 3, 0.5))
	s.frame()
	s.GlideMusicGain(0, 1)
	for range SampleRate / 2 {
		s.frame()
	}
	mid := s.mgain
	if math.Abs(mid-0.5) > 0.01 {
		t.Fatalf("halfway gain = %v, want ~0.5", mid)
	}
	// A new call starts from where the glide had reached, not from its target.
	s.GlideMusicGain(1, 0.1)
	s.frame()
	if d := s.mgain - mid; d <= 0 || d > 0.5/(0.1*SampleRate)+1e-12 {
		t.Fatalf("retarget jumped from %v to %v", mid, s.mgain)
	}
}
