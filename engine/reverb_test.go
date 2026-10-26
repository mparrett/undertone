package engine

import (
	"math"
	"testing"
)

// An impulse into the reverb rings as a decorrelated stereo tail that is
// still sounding after half a second and gone within ten.
func TestReverbTailDecays(t *testing.T) {
	v := newReverb()
	peak := func(from, to int, in float64) (pl, pr, diff float64) {
		for i := from; i < to; i++ {
			l, r := v.process(in)
			in = 0
			if math.IsNaN(l) || math.IsInf(l, 0) {
				t.Fatalf("sample %d: %v", i, l)
			}
			pl, pr = max(pl, math.Abs(l)), max(pr, math.Abs(r))
			diff = max(diff, math.Abs(l-r))
		}
		return
	}
	early, _, diff := peak(0, SampleRate/2, 1)
	late, _, _ := peak(SampleRate/2, SampleRate, 0)
	peak(SampleRate, 10*SampleRate, 0) // let it ring out
	gone, _, _ := peak(10*SampleRate, 11*SampleRate, 0)
	if late < 1e-3 || late > early || gone > 1e-5 || diff < 1e-3 {
		t.Errorf("peaks %.2g in the first half second, %.2g in the next, %.2g after ten; L-R differ by %.2g", early, late, gone, diff)
	}
}

// A synth whose patches never send to the reverb never runs it.
func TestReverbStaysOffWithoutSends(t *testing.T) {
	s := NewSynth(DeriveDungeon(1, 3, 0.5))
	render(s, 2*SampleRate)
	if s.verb.on {
		t.Error("dungeon has no verb sends, yet the reverb is running")
	}
	p, _ := DeriveMood("rust", 1, 0, 0)
	s = NewSynth(p)
	render(s, 2*SampleRate)
	if !s.verb.on {
		t.Error("rust sends to the reverb, yet it is off")
	}
}

// The reverb turns off once its last send has rung out, and a room that
// rang before renders a new send exactly as a fresh one does.
func TestReverbTurnsOffWhenQuiet(t *testing.T) {
	used, fresh := newReverb(), newReverb()
	used.process(1)
	for range verbIdle {
		used.process(0)
	}
	if used.on {
		t.Fatal("the reverb is still running after its tail rang out")
	}
	for i := range SampleRate {
		in := float64(i % 2)
		a, b := used.process(in)
		if c, d := fresh.process(in); a != c || b != d {
			t.Fatalf("sample %d: a used room renders %v,%v, a fresh one %v,%v", i, a, b, c, d)
		}
	}
}

// The zero room renders as the reverb always has; a size or a damping
// changes what a mood that sends to the reverb sounds like.
func TestRoomChangesTheRender(t *testing.T) {
	base, _ := DeriveMood("rust", 1, 0, 0)
	want := render(NewSynth(base), SampleRate)
	for _, r := range []Room{{}, {Size: 0.5}, {Damp: -0.5}} {
		p := base
		p.Room = r
		got := render(NewSynth(p), SampleRate)
		if same := string(got) == string(want); same != (r == Room{}) {
			t.Errorf("room %+v: renders the same as the default room: %v", r, same)
		}
	}
}

// The largest, brightest room a mood can ask for still decays, and rings
// below -100 dB before it turns off; a room out of range is clamped to it.
func TestLargestRoomDecays(t *testing.T) {
	p := Params{Room: Room{Size: 5, Damp: math.NaN()}}.sanitized()
	if p.Room != (Room{Size: 1, Damp: 0}) {
		t.Fatalf("sanitized room %+v", p.Room)
	}
	v := newReverb()
	v.setRoom(Room{Size: 1, Damp: -1})
	if v.fb >= 1 {
		t.Fatalf("feedback %v", v.fb)
	}
	v.process(1)
	last := 0.0
	for i := 1; v.on; i++ {
		l, r := v.process(0)
		if math.IsNaN(l) || math.IsInf(l, 0) {
			t.Fatalf("sample %d: %v", i, l)
		}
		if i > v.idleAfter-SampleRate {
			last = max(last, math.Abs(l), math.Abs(r))
		}
	}
	if last > 1e-5 {
		t.Errorf("the tail peaks at %.2g in the second before the room turns off", last)
	}
}

// A room changed mid-tail glides there: the feedback does not step, so the
// tail's level does not jump a comb length later, and it never reaches 1.
func TestRoomChangeGlides(t *testing.T) {
	v := newReverb()
	for range SampleRate / 2 {
		v.process(0.1)
	}
	v.setRoom(Room{Size: 1})
	if v.fb != verbFeedback {
		t.Fatalf("a ringing room stepped its feedback to %v", v.fb)
	}
	for i := range SampleRate / 2 {
		v.process(0)
		if v.fb >= 1 || v.fb < verbFeedback {
			t.Fatalf("sample %d: feedback %v", i, v.fb)
		}
		if i == 1640 && v.fb > verbFeedback+0.6*(v.fbTo-verbFeedback) {
			t.Errorf("feedback %v a comb pass in: more a step than a glide", v.fb)
		}
	}
	if v.fb != v.fbTo {
		t.Errorf("feedback %v after half a second, want %v", v.fb, v.fbTo)
	}
}

// SetParametersNow changes the room as it changes everything else.
func TestSetParametersNowChangesTheRoom(t *testing.T) {
	p, _ := DeriveMood("rust", 1, 0, 0)
	s := NewSynth(p)
	render(s, SampleRate/4)
	p.Room = Room{Size: -1}
	s.SetParametersNow(p)
	render(s, 2*nowBlock)
	if s.verb.fbTo != 0.7 {
		t.Errorf("feedback heading for %v, want 0.7", s.verb.fbTo)
	}
}
