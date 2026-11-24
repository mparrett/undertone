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
