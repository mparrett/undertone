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
		if d := prev - s.musicLevel.gain; d < 0 || d > maxStep {
			t.Fatalf("frame %d: gain moved %v (from %v to %v), want a step in [0, %v]", i, d, prev, s.musicLevel.gain, maxStep)
		}
		prev = s.musicLevel.gain
	}
	if s.musicLevel.gain != 0.2 {
		t.Fatalf("after the glide gain = %v, want exactly 0.2", s.musicLevel.gain)
	}

	// Back to 1 lands exactly, so the bus is skipped (byte-identical) again.
	s.SetMusicGain(1)
	for range int(levelGlide*SampleRate) + 1 {
		s.frame()
	}
	if s.musicLevel.gain != 1 {
		t.Fatalf("after returning gain = %v, want exactly 1", s.musicLevel.gain)
	}
}

func TestMusicGainBeforeFirstFrameIsImmediate(t *testing.T) {
	s := NewSynth(DeriveDungeon(7, 3, 0.5))
	s.SetMusicGain(0.25)
	s.frame()
	if s.musicLevel.gain != 0.25 {
		t.Fatalf("first frame gain = %v, want 0.25 at once", s.musicLevel.gain)
	}
}

func TestMusicGainRetargetsMidGlide(t *testing.T) {
	s := NewSynth(DeriveDungeon(7, 3, 0.5))
	s.frame()
	s.GlideMusicGain(0, 1)
	for range SampleRate / 2 {
		s.frame()
	}
	mid := s.musicLevel.gain
	if math.Abs(mid-0.5) > 0.01 {
		t.Fatalf("halfway gain = %v, want ~0.5", mid)
	}
	// A new call starts from where the glide had reached, not from its target.
	s.GlideMusicGain(1, 0.1)
	s.frame()
	if d := s.musicLevel.gain - mid; d <= 0 || d > 0.5/(0.1*SampleRate)+1e-12 {
		t.Fatalf("retarget jumped from %v to %v", mid, s.musicLevel.gain)
	}
}

// A set then a glide, both before the first frame, fade in from the set
// level: the title screen sets 0 at music-start and asks for the fade a
// moment later, before the output has rendered.
func TestGlideBeforeFirstFrameStartsFromTheSetLevel(t *testing.T) {
	s := NewSynth(DeriveDungeon(7, 3, 0.5))
	s.SetMusicGain(0)
	const secs = 0.1
	s.GlideMusicGain(0.5, secs)
	s.frame()
	if g := s.musicLevel.gain; g <= 0 || g > 0.5/(secs*SampleRate)+1e-12 {
		t.Fatalf("first frame gain = %v, want one step up from 0", g)
	}
	for range int(secs * SampleRate) {
		s.frame()
	}
	if s.musicLevel.gain != 0.5 {
		t.Fatalf("after the glide gain = %v, want exactly 0.5", s.musicLevel.gain)
	}
}

// A glide with no set before it runs from full level, where playback starts.
func TestGlideBeforeFirstFrameStartsFromOne(t *testing.T) {
	s := NewSynth(DeriveDungeon(7, 3, 0.5))
	s.GlideSfxGain(0.2, 0.1)
	s.frame()
	if g := s.sfxLevel.gain; g >= 1 || g < 1-0.8/(0.1*SampleRate)-1e-12 {
		t.Fatalf("first frame gain = %v, want one step down from 1", g)
	}
}
