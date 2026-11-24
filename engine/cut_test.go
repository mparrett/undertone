package engine

import (
	"bytes"
	"math"
	"testing"
)

// rms16 is the RMS of little-endian stereo int16 PCM.
func rms16(buf []byte) float64 {
	var sum float64
	n := len(buf) / 2
	for i := 0; i < n; i++ {
		v := float64(int16(uint16(buf[2*i]) | uint16(buf[2*i+1])<<8))
		sum += v * v
	}
	return math.Sqrt(sum / float64(n))
}

// A run handed over in one batch, every note on its delay, stops when cut:
// the notes still waiting never start.
func TestCutDropsWaitingShots(t *testing.T) {
	s := NewSynth(silentParams)
	for i := 0; i < 4; i++ {
		s.TriggerShot(Shot{Name: "footstep", Gain: 1, Pan: 0.5, DelayMS: 100 + 50*float64(i), Note: 43, HasNote: true, Group: "run"})
	}
	if n := s.Cut("run", 30); n != 4 {
		t.Errorf("Cut dropped %d waiting shots, want 4", n)
	}
	if nonSilent(render(s, SampleRate/2)) {
		t.Error("a cut run still played")
	}
}

// A sounding shot fades out over the cut's fade and frees its voice, its
// sends with it: past the fade, what is left is only the echo of what it sent
// before, far under the uncut shot.
func TestCutFadesASoundingShot(t *testing.T) {
	run := func(cut bool) []byte {
		s := NewSynth(silentParams)
		s.TriggerShot(Shot{Name: "fall", Gain: 1, Pan: 0.5, Note: 60, HasNote: true, Group: "g"})
		render(s, SampleRate/10)
		if cut {
			s.Cut("g", 20)
		}
		render(s, SampleRate/40) // the fade, and a little more
		return render(s, SampleRate/5)
	}
	whole, cut := rms16(run(false)), rms16(run(true))
	if whole < 50 {
		t.Fatalf("the uncut shot is %.1f RMS after 125 ms: too quiet to show a cut", whole)
	}
	if cut > whole/10 {
		t.Errorf("cut shot %.1f RMS against uncut %.1f: it did not fade", cut, whole)
	}
	s := NewSynth(silentParams)
	s.TriggerShot(Shot{Name: "fall", Gain: 1, Pan: 0.5, Note: 60, HasNote: true, Group: "g"})
	render(s, SampleRate/10)
	s.Cut("g", 20)
	render(s, SampleRate/20)
	for i := range s.fx.shots {
		if s.fx.shots[i].active {
			t.Errorf("voice %d still active after its cut faded", i)
		}
	}
}

// A cut touches its own group alone: ungrouped shots, other groups, and
// shots of the group triggered after the cut all play as if it never came.
func TestCutSparesWhatItDoesNotName(t *testing.T) {
	others := func(s *Synth) {
		s.TriggerShot(Shot{Name: "hit", Gain: 1, Pan: 0.5, DelayMS: 50, Note: 45, HasNote: true})
		s.TriggerShot(Shot{Name: "pluck", Gain: 0.7, Pan: 0.5, DelayMS: 80, Note: 81, HasNote: true, Group: "other"})
	}
	want := NewSynth(silentParams)
	others(want)
	want.TriggerShot(Shot{Name: "footstep", Gain: 1, Pan: 0.5, DelayMS: 120, Note: 43, HasNote: true, Group: "run"})

	got := NewSynth(silentParams)
	others(got)
	got.TriggerShot(Shot{Name: "tink", Gain: 1, Pan: 0.5, DelayMS: 60, Note: 90, HasNote: true, Group: "run"})
	got.Cut("run", 30)
	got.TriggerShot(Shot{Name: "footstep", Gain: 1, Pan: 0.5, DelayMS: 120, Note: 43, HasNote: true, Group: "run"})

	if !bytes.Equal(render(want, SampleRate/2), render(got, SampleRate/2)) {
		t.Error("a cut changed shots outside its group, or one triggered after it")
	}
}

// An empty group names nothing: Cut("") leaves ungrouped shots alone.
func TestCutOfNoGroupIsANoOp(t *testing.T) {
	s := NewSynth(silentParams)
	s.Trigger("footstep", 43, 1, 0.5, 100, 1)
	if n := s.Cut("", 30); n != 0 {
		t.Errorf("Cut(\"\") dropped %d shots", n)
	}
	if !nonSilent(render(s, SampleRate/2)) {
		t.Error("an ungrouped shot was silenced by Cut(\"\")")
	}
}

// A fade out of range is held: NaN and 0 to the shortest (no click from a
// step to zero), a minute to two seconds.
func TestCutFadeIsBounded(t *testing.T) {
	for _, c := range []struct{ ms, want float64 }{{math.NaN(), minCutFadeMS}, {0, minCutFadeMS}, {60000, maxCutFadeMS}, {40, 40}} {
		s := NewSynth(silentParams)
		s.Cut("g", c.ms)
		if got := 1000 / (s.fx.cuts[0].step * SampleRate); math.Abs(got-c.want) > 1e-6 {
			t.Errorf("Cut fade %v ms: held to %v ms, want %v", c.ms, got, c.want)
		}
	}
}

// playing counts the voices sounding unfaded for group.
func playing(s *Synth, group string) int {
	n := 0
	for i := range s.fx.shots {
		if s.fx.shots[i].active && s.fx.group[i] == group && s.fx.fadeStep[i] == 0 {
			n++
		}
	}
	return n
}

// A repeat of the cut shot, triggered right after the cut and inside the
// repeat gap, plays: it is not merged into the voice that is fading.
func TestCutLetsARepeatThrough(t *testing.T) {
	s := NewSynth(silentParams)
	step := Shot{Name: "fall", Gain: 1, Pan: 0.5, Note: 60, HasNote: true, Group: "run"}
	s.TriggerShot(step)
	render(s, SampleRate/100) // 10 ms: inside shotGapMS
	s.Cut("run", 30)
	s.TriggerShot(step)
	render(s, SampleRate/200)
	if n := playing(s, "run"); n != 1 {
		t.Errorf("%d unfaded run voices after a repeat triggered past the cut, want 1", n)
	}
}

// Shots of different groups are never merged into one voice, so a cut takes
// its own and leaves the rest, whichever was louder.
func TestCutKeepsGroupsApartInOneFrame(t *testing.T) {
	for _, gains := range [][2]float64{{0.8, 0.5}, {0.5, 0.8}} {
		s := NewSynth(silentParams)
		s.TriggerShot(Shot{Name: "fall", Gain: gains[0], Pan: 0.5, Note: 60, HasNote: true, Group: "run"})
		s.TriggerShot(Shot{Name: "fall", Gain: gains[1], Pan: 0.5, Note: 60, HasNote: true})
		render(s, SampleRate/200)
		s.Cut("run", 10)
		render(s, SampleRate/20)
		if playing(s, "") != 1 || playing(s, "run") != 0 {
			t.Errorf("gains %v: %d ungrouped and %d run voices left after cutting run, want 1 and 0",
				gains, playing(s, ""), playing(s, "run"))
		}
	}
}

// Cutting one group again and again with nothing rendering (a suspended
// page) keeps one request, at the fastest fade asked for.
func TestRepeatedCutsMerge(t *testing.T) {
	s := NewSynth(silentParams)
	for i := 0; i < 1000; i++ {
		s.Cut("run", 100)
	}
	s.Cut("run", 10)
	s.Cut("other", 50)
	if len(s.fx.cuts) != 2 {
		t.Fatalf("%d cut requests queued, want 2", len(s.fx.cuts))
	}
	if got := 1000 / (s.fx.cuts[0].step * SampleRate); math.Abs(got-10) > 1e-6 {
		t.Errorf("merged cut fades over %v ms, want the faster 10", got)
	}
}
