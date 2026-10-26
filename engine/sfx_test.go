package engine

import (
	"bytes"
	"testing"
)

// The one-shot lane's contract has two halves: it must be audible (a trigger
// makes sound, exactly where asked), and it must be inert toward the music
// (the sequencer's streams and pattern content cannot see it). The tests pin
// both, plus the failure mode the env design invites — a voice stuck alive in
// its sustain stage forever.

func render(s *Synth, samples int) []byte {
	buf := make([]byte, samples*4)
	s.Read(buf)
	return buf
}

// silentParams makes a synth whose sequencer emits nothing: every gain and
// density is zero and the palette is empty, so its voices run empty patches.
// What remains audible is the one-shot lane alone. The scale must still be
// present — buildPatterns indexes it before it checks any gain.
var silentParams = Params{BPM: 120, Seed: 1, Scale: []int{0, 2, 3, 5, 7, 9, 10}}

func TestGainZeroShotLeavesMusicByteIdentical(t *testing.T) {
	p := DeriveDungeon(7, 3, 0.5)
	a := NewSynth(p)
	b := NewSynth(p)
	if !b.Trigger("footstep", 43, 0, 0.5, 0, 99) {
		t.Fatal("trigger refused")
	}
	if !bytes.Equal(render(a, SampleRate*2), render(b, SampleRate*2)) {
		t.Error("a gain-0 one-shot changed the music render; the lane must be inert toward the sequencer")
	}
}

func TestTriggerIsAudibleAndDeterministic(t *testing.T) {
	run := func() []byte {
		s := NewSynth(silentParams)
		s.Trigger("hit", 45, 1, 0.5, 0, 7)
		s.Trigger("pluck", 81, 0.7, 0.5, 70, 8)
		return render(s, SampleRate)
	}
	first := run()
	if !nonSilent(first) {
		t.Fatal("two one-shots rendered pure silence")
	}
	if !bytes.Equal(first, run()) {
		t.Error("same triggers, same seeds, different PCM — one-shots must render deterministically")
	}
}

func TestHumanizeVariesBySeed(t *testing.T) {
	run := func(seed uint64) []byte {
		s := NewSynth(silentParams)
		s.Trigger("footstep", 43, 1, 0.5, 0, seed)
		return render(s, SampleRate/4)
	}
	if bytes.Equal(run(1), run(2)) {
		t.Error("different trigger seeds rendered identical footsteps; humanize is what stops the machine-gun effect")
	}
}

func TestDelayedShotStartsOnTime(t *testing.T) {
	const delayMS = 100.0
	s := NewSynth(silentParams)
	s.Trigger("footstep", 43, 1, 0.5, delayMS, 3)
	buf := render(s, SampleRate/2)
	first := -1
	for i := 0; i < len(buf); i += 4 {
		if buf[i] != 0 || buf[i+1] != 0 || buf[i+2] != 0 || buf[i+3] != 0 {
			first = i / 4
			break
		}
	}
	want := int(delayMS * SampleRate / 1000)
	if first < want || first > want+SampleRate/100 {
		t.Errorf("first audible sample at %d, want within [%d, %d] — the delay is meant to be sample-accurate",
			first, want, want+SampleRate/100)
	}
}

func TestPanIsPerVoice(t *testing.T) {
	// footstep has no opSend, so with the sequencer silent a hard-left pan
	// must leave the right channel at exact digital zero.
	s := NewSynth(silentParams)
	s.Trigger("footstep", 43, 1, 0, 0, 4)
	buf := render(s, SampleRate/4)
	left, right := false, false
	for i := 0; i < len(buf); i += 4 {
		if buf[i] != 0 || buf[i+1] != 0 {
			left = true
		}
		if buf[i+2] != 0 || buf[i+3] != 0 {
			right = true
		}
	}
	if !left {
		t.Error("hard-left footstep: left channel silent")
	}
	if right {
		t.Error("hard-left footstep leaked into the right channel; pan must be per voice")
	}
}

// The env's sustain stage keeps a voice alive while gate is true, so a
// one-shot whose gate never expired would run its patch forever — inaudible
// at sustain 0, but a permanent per-sample cost that stacks with every
// footstep taken. The sample-clock gate in runShots is what prevents that.
func TestShotVoicesDie(t *testing.T) {
	s := NewSynth(silentParams)
	for i, name := range []string{"footstep", "hit", "pluck", "thud"} {
		s.Trigger(name, 43, 1, 0.5, 0, uint64(5+i))
	}
	render(s, SampleRate*4)
	for i := range s.fx.shots {
		if s.fx.shots[i].active {
			t.Errorf("shot voice %d still active after 4s; its gate never expired", i)
		}
	}
}

func TestPoolOverflowStealsInsteadOfGrowing(t *testing.T) {
	s := NewSynth(silentParams)
	for i := 0; i < nShots*4; i++ { // distinct notes, so the repeat gap thins none of them
		s.Trigger("hit", 45+i, 0.5, 0.5, float64(i), uint64(i))
	}
	render(s, SampleRate*nShots*4/1000)
	busy := 0
	for i := range s.fx.shots {
		if s.fx.shots[i].active {
			busy++
		}
	}
	if busy != nShots {
		t.Fatalf("%d of %d shot voices busy: the burst never filled the pool", busy, nShots)
	}
	render(s, SampleRate) // must not panic, and everything must still decay
	render(s, SampleRate*3)
	for i := range s.fx.shots {
		if s.fx.shots[i].active {
			t.Errorf("shot voice %d still active after an overflow burst", i)
		}
	}
}

func TestUnknownShotNameIsDroppedNotFatal(t *testing.T) {
	s := NewSynth(silentParams)
	if s.Trigger("kazoo", 60, 1, 0.5, 0, 1) {
		t.Error("unknown shot name accepted; the vocabulary lives above this seam and stale names must drop")
	}
	buf := render(s, SampleRate/10)
	if nonSilent(buf) {
		t.Error("unknown shot made sound")
	}
}

func nonSilent(buf []byte) bool {
	for _, c := range buf {
		if c != 0 {
			return true
		}
	}
	return false
}

// The production shape: the lg thread triggers while the audio callback
// goroutine renders. Run under -race this is the test that proves the lane's
// locking discipline; without -race it still shakes out ordering panics.
func TestConcurrentTriggerWhileRendering(t *testing.T) {
	s := NewSynth(silentParams)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			s.Trigger("footstep", 43, 0.5, 0.5, float64(i%40), uint64(i))
		}
	}()
	render(s, SampleRate*2)
	<-done
	render(s, SampleRate*4)
	for i := range s.fx.shots {
		if s.fx.shots[i].active {
			t.Errorf("shot voice %d still active after the burst settled", i)
		}
	}
}

// Every patch in the library must make sound on its own. A patch whose units
// mis-stack renders silence rather than erroring, so this is the check that
// catches a bad edit to the tables.
func TestEveryShotIsAudible(t *testing.T) {
	for name := range shotLib {
		s := NewSynth(silentParams)
		s.Trigger(name, 45, 1, 0.5, 0, 11)
		if !nonSilent(render(s, SampleRate)) {
			t.Errorf("%s rendered silence", name)
		}
	}
}

// A music gain of 0 must leave the one-shots at full level: the bed goes,
// the shot stays. A full bed at gain 0 renders the same bytes as a synth
// whose music is silent by its parameters.
func TestMusicGainZeroKeepsShots(t *testing.T) {
	ref := NewSynth(silentParams)
	ref.Trigger("hit", 60, 1, 0.5, 0, 11)
	want := render(ref, SampleRate/4)

	s := NewSynth(DeriveDungeon(7, 3, 0.5)) // a full bed, drone and drums and all
	s.SetMusicGain(0)
	s.Trigger("hit", 60, 1, 0.5, 0, 11)
	got := render(s, SampleRate/4)
	if len(got) != len(want) {
		t.Fatalf("length %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample %d: %d, want %d", i, got[i], want[i])
		}
	}
}

// A burst of the same shot at the same note starts once; a different note, or
// a repeat after shotGapMS, starts again.
func TestShotRepeatsWithinTheGapAreDropped(t *testing.T) {
	active := func(s *Synth) (n int) {
		for i := range s.fx.shots {
			if s.fx.shots[i].active {
				n++
			}
		}
		return n
	}
	ms := func(m float64) int { return int(m * SampleRate / 1000) }
	for _, c := range []struct {
		name  string
		shots func(s *Synth)
		want  int
	}{
		{"a dozen at once", func(s *Synth) {
			for i := 0; i < 12; i++ {
				s.Trigger("hit", 40, 1, 0.5, 0, uint64(i))
			}
		}, 1},
		{"two notes", func(s *Synth) {
			s.Trigger("hit", 40, 1, 0.5, 0, 1)
			s.Trigger("hit", 47, 1, 0.5, 0, 2)
		}, 2},
		{"a repeat inside the gap", func(s *Synth) {
			s.Trigger("hit", 40, 1, 0.5, 0, 1)
			s.Trigger("hit", 40, 1, 0.5, shotGapMS/2, 2)
		}, 1},
		{"a repeat after the gap", func(s *Synth) {
			s.Trigger("hit", 40, 1, 0.5, 0, 1)
			s.Trigger("hit", 40, 1, 0.5, shotGapMS+10, 2)
		}, 2},
	} {
		s := NewSynth(silentParams)
		c.shots(s)
		render(s, ms(shotGapMS+20))
		if got := active(s); got != c.want {
			t.Errorf("%s: %d shot voices started, want %d", c.name, got, c.want)
		}
	}
}

// Of repeats in the same frame the loudest plays, with its own pan.
func TestLouderRepeatInTheSameFrameWins(t *testing.T) {
	s := NewSynth(silentParams)
	s.Trigger("hit", 40, 0.1, 0, 0, 1)
	s.Trigger("hit", 40, 1, 1, 0, 2)
	s.Trigger("hit", 40, 0.5, 0.5, 0, 3)
	render(s, 64)
	n := 0
	for i := range s.fx.shots {
		if v := &s.fx.shots[i]; v.active {
			n++
			if v.gain != 1 || s.fx.pan[i] != 1 {
				t.Errorf("the hit that played has gain %v pan %v, want the loudest: 1, 1", v.gain, s.fx.pan[i])
			}
		}
	}
	if n != 1 {
		t.Errorf("%d hits started, want 1", n)
	}
}
