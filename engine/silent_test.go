package engine

import (
	"math"
	"slices"
	"testing"
)

// lastAudible is the frame after the last one at or above floorDB in pcm.
func lastAudible(pcm []int16, floorDB float64) int {
	thr := 32767 * math.Pow(10, floorDB/20)
	for i := len(pcm) - 1; i >= 0; i-- {
		if math.Abs(float64(pcm[i])) >= thr {
			return i/2 + 1
		}
	}
	return 0
}

func shotTake(name string, delayMS float64) Take {
	return Take{Params: DeriveDungeon(7, 3, 0.5), Shots: []Shot{
		{Name: name, Gain: 1, Pan: 0.5, DelayMS: delayMS, Seed: 3, Note: 60, HasNote: true}}}
}

// A take rendered until silent ends where a render long enough to hold all of
// it stops being audible, plus the margin: no echo past a gap in the delay is
// lost, and no silence is kept. Dry shots end in a fraction of a second;
// shots that send to the delay ring for seconds at full gain, an echo
// falling 6 dB each period.
func TestUntilSilentEndsWhereTheSoundDoes(t *testing.T) {
	margin := silentMarginMS * SampleRate / 1000
	for _, name := range []string{"footstep", "snap", "blip", "hit", "cry", "twang", "fall"} {
		tk := shotTake(name, 0)
		got := tk.RenderUntilSilent(SilentFloorDB)
		tk.Seconds = 20
		want := lastAudible(tk.Render(), SilentFloorDB) + margin
		if len(got)/2 != want {
			t.Errorf("%s: until silent %.3f s, audible to %.3f s", name,
				float64(len(got)/2)/SampleRate, float64(want)/SampleRate)
		}
	}
	if n := len(shotTake("footstep", 0).RenderUntilSilent(SilentFloorDB)) / 2; n > SampleRate*3/10 {
		t.Errorf("a footstep rendered %.2f s", float64(n)/SampleRate)
	}
}

// It is a prefix of the fixed-length render, so a page rendering the WAV's
// frame count plays exactly what was measured.
func TestUntilSilentIsAPrefix(t *testing.T) {
	tk := boardTake
	got := tk.RenderUntilSilent(SilentFloorDB)
	tk.Seconds = (float64(len(got)/2) + 0.5) / SampleRate
	if !slices.Equal(got, tk.Render()) {
		t.Fatal("until silent differs from the fixed render of its length")
	}
}

// A shot still waiting for its delay keeps the take going through the
// silence before it, and a bed above the floor runs to the cap.
func TestUntilSilentWaitsAndCaps(t *testing.T) {
	if n := len(shotTake("footstep", 1500).RenderUntilSilent(SilentFloorDB)) / 2; n < 3*SampleRate/2 {
		t.Errorf("a footstep 1.5 s in ended at %.2f s", float64(n)/SampleRate)
	}
	bed := Take{Params: DeriveDungeon(7, 3, 0.5), MusicGain: 1, Seconds: 3}
	if n := len(bed.RenderUntilSilent(SilentFloorDB)) / 2; n != 3*SampleRate {
		t.Errorf("a playing bed rendered %d frames, want the 3 s cap", n)
	}
}

func TestUntilSilentHash(t *testing.T) {
	if boardTake.UntilSilentHash(-60) == boardTake.Hash() ||
		boardTake.UntilSilentHash(-60) == boardTake.UntilSilentHash(-80) {
		t.Error("the until-silent hash does not tell its floor or form apart")
	}
}
