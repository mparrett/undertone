package engine

import (
	"math"
	"testing"
)

// tailRMS is the RMS of a take's samples from `from` seconds on.
func tailRMS(pcm []int16, from float64) float64 {
	var sum float64
	i0 := 2 * int(from*SampleRate)
	for _, v := range pcm[i0:] {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(pcm)-i0))
}

// A dry trigger never feeds the delay: the bow's twang dies in a quarter of
// a second, so what is left a second later is its echo, and only the wet one
// has it. Dry left at zero plays as authored, sample for sample.
func TestDryTriggerHasNoEcho(t *testing.T) {
	take := func(dry float64) Take {
		return Take{Params: DeriveDungeon(7, 3, 0.5), Seconds: 3, Shots: []Shot{
			{Name: "twang", Gain: 1, Pan: 0.5, Seed: 1, Note: 57, HasNote: true, Dry: dry},
		}}
	}
	wet, dry := tailRMS(take(0).Render(), 1), tailRMS(take(1).Render(), 1)
	if wet < 20 {
		t.Fatalf("the wet twang's tail is %.1f RMS: no echo to remove, the test proves nothing", wet)
	}
	if dry > wet/100 {
		t.Errorf("dry tail %.1f RMS against wet %.1f: the send was not removed", dry, wet)
	}

	s := NewSynth(DeriveDungeon(7, 3, 0.5))
	s.SetMusicGain(0) // as the take, whose MusicGain is 0
	s.Trigger("twang", 57, 1, 0.5, 0, 1)
	want := render(s, 3*SampleRate)
	got := take(0).Render()
	for i, v := range got {
		if int16(uint16(want[2*i])|uint16(want[2*i+1])<<8) != v {
			t.Fatalf("Dry 0 differs from Trigger at sample %d", i)
		}
	}
}

// The cut is applied to the trigger's own copy, so the registered patch, and
// every later trigger of it, keeps its send. Out-of-range cuts are held to
// 0..1.
func TestDryLeavesTheRegisteredPatch(t *testing.T) {
	s := NewSynth(DeriveDungeon(7, 3, 0.5))
	s.TriggerShot(Shot{Name: "twang", Gain: 1, Note: 57, HasNote: true, Dry: 1})
	d, _ := lookupShot("twang")
	for _, u := range d.patch {
		if u.op == opSend && u.p[0] == 0 {
			t.Fatal("a dry trigger zeroed the registered patch's send")
		}
	}
	for _, c := range []struct{ dry, want float64 }{{-1, 0.2}, {math.NaN(), 0.2}, {0.5, 0.1}, {7, 0}} {
		p := humanize(d.patch, 1)
		unsend(p, c.dry, 0)
		for _, u := range p {
			if u.op == opSend && math.Abs(u.p[0]-c.want) > 1e-12 {
				t.Errorf("Dry %v: send %v, want %v", c.dry, u.p[0], c.want)
			}
		}
	}
}

// DryVerb cuts the room send as Dry cuts the delay's. No built-in shot has a
// verb unit, so this registers one.
func TestDryVerbCutsTheRoom(t *testing.T) {
	t.Cleanup(func() {
		shotsMu.Lock()
		delete(shotReg, "dry-test-room")
		shotsMu.Unlock()
	})
	if err := RegisterShot(ShotSpec{Name: "dry-test-room", GateMS: 10, Units: []UnitSpec{
		{Op: "env", Args: []float64{0.001, 0.1, 0, 0.05}},
		{Op: "osc", Shape: "sine", Args: []float64{0, 0, 1}},
		{Op: "mul"},
		{Op: "verb", Args: []float64{0.6}},
		{Op: "out", Args: []float64{0.5, 0.5}},
	}}); err != nil {
		t.Fatal(err)
	}
	d, _ := lookupShot("dry-test-room")
	verb := func(dry, dryVerb float64) float64 {
		p := humanize(d.patch, 1)
		unsend(p, dry, dryVerb)
		for _, u := range p {
			if u.op == opVerb {
				return u.p[0]
			}
		}
		t.Fatal("no verb unit")
		return 0
	}
	if v := verb(1, 0); v != 0.6 {
		t.Errorf("Dry alone moved the room send to %v", v)
	}
	if v := verb(0, 0.5); math.Abs(v-0.3) > 1e-12 {
		t.Errorf("DryVerb 0.5: room send %v, want 0.3", v)
	}
	take := func(dryVerb float64) Take {
		return Take{Params: DeriveDungeon(7, 3, 0.5), Seconds: 2, Shots: []Shot{
			{Name: "dry-test-room", Gain: 1, Pan: 0.5, Seed: 1, Note: 60, HasNote: true, DryVerb: dryVerb},
		}}
	}
	wet, dry := tailRMS(take(0).Render(), 0.5), tailRMS(take(1).Render(), 0.5)
	if wet < 5 || dry > wet/100 {
		t.Errorf("room tail: wet %.1f RMS, dry %.1f: DryVerb 1 should leave next to nothing", wet, dry)
	}
}

// A dry and a wet trigger of one note in one frame are two sounds: as one
// repeat the louder (wet) one took the voice and the dry one echoed.
func TestDryAndWetAreNotOneRepeat(t *testing.T) {
	s := NewSynth(DeriveDungeon(7, 3, 0.5))
	s.SetMusicGain(0)
	s.TriggerShot(Shot{Name: "twang", Gain: 0.5, Note: 57, HasNote: true, Dry: 1})
	s.TriggerShot(Shot{Name: "twang", Gain: 0.8, Note: 57, HasNote: true})
	render(s, 64)
	n := 0
	for i := range s.fx.shots {
		if s.fx.shots[i].active {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d voices for a dry and a wet twang, want 2", n)
	}
}
