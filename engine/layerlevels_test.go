package engine

import (
	"math"
	"testing"
)

// droneOnly is dungeon with every layer but the drone silenced at the
// source, not muted.
func droneOnly() Params {
	p := DeriveDungeon(1, 2, 0)
	p.FifthGain, p.PadDensity, p.BellDensity, p.AirGain, p.PulseGain = 0, 0, 0, 0, 0
	p.LeadName, p.Groove = "", ""
	return p
}

func TestLayerLevelsLightOnlyWhatPlays(t *testing.T) {
	s := NewSynth(droneOnly())
	render(s, 2*SampleRate)
	got := s.LayerLevels()
	names := LayerNames()
	for i, v := range got {
		if i == 0 && v < 1e-3 {
			t.Errorf("drone level %g, want it lit", v)
		}
		if i != 0 && v != 0 {
			t.Errorf("%s level %g with only the drone playing, want 0", names[i], v)
		}
	}
}

// A muted layer reads as it would unmuted: the board dims it from the mute
// bits, so it can still show that the layer plays.
func TestLayerLevelsIgnoreMute(t *testing.T) {
	p := droneOnly()
	a := NewSynth(p)
	p.Mute = LayerDrone
	b := NewSynth(p)
	render(a, SampleRate)
	render(b, SampleRate)
	if la, lb := a.LayerLevels()[0], b.LayerLevels()[0]; math.Abs(la-lb) > 1e-12 || lb == 0 {
		t.Errorf("drone level %g unmuted, %g muted; want the same, lit", la, lb)
	}
	if lv := b.Levels(); lv != [4]float64{} {
		t.Errorf("the mix meter reads %v with the only layer muted, want silence", lv)
	}
}

func TestLayerLevelsSeeTheDrums(t *testing.T) {
	p, _ := DeriveMood("forge", 7, 4, 1)
	if p.Groove == "" {
		t.Fatal("forge at tension 1 has no groove to test with")
	}
	s := NewSynth(p)
	render(s, 2*rows*s.samplesPerRow)
	if v := s.LayerLevels()[layerDrums]; v < 1e-3 {
		t.Errorf("drums level %g in a mood with a groove, want it lit", v)
	}
}

// meterFrames runs n silent frames through m from frame f, folding as the
// synth's frame loop does, and returns the frame reached.
func meterFrames(m *layerMeter, f, n int) int {
	for range n {
		if f++; f%layerFold == 0 {
			m.fold(layerFoldDecay)
		}
	}
	return f
}

func published(m *layerMeter, i int) float64 { return math.Float64frombits(m.out[i].Load()) }

// The envelope takes a peak at once and lets go over about 400 ms.
func TestLayerMeterDecays(t *testing.T) {
	var m layerMeter
	m.see(2, -0.8)
	f := meterFrames(&m, 0, 1)
	m.publish(f % layerFold)
	if got := published(&m, 2); got != 0.4 {
		t.Fatalf("after a peak of 0.8 summed: %g, want 0.4 at once", got)
	}
	f = meterFrames(&m, f, SampleRate/10)
	m.publish(f % layerFold)
	mid := published(&m, 2)
	f = meterFrames(&m, f, SampleRate)
	m.publish(f % layerFold)
	end := published(&m, 2)
	if !(mid < 0.4 && mid > 0.05 && end < 1e-4) {
		t.Errorf("decay: %g after 100 ms, %g after 1.1 s; want falling to near 0", mid, end)
	}
}

// A peak at the start of a long Read has decayed by its end, within one
// fold of where a per-sample envelope would be; publishing partway through
// a block does not decay it twice.
func TestLayerMeterDecaysWithinALongRead(t *testing.T) {
	for _, reads := range [][]int{{4096}, {100, 1000, 2996}} {
		var m layerMeter
		m.see(0, 1)
		f := 0
		for _, n := range reads {
			f = meterFrames(&m, f, n)
			m.publish(f % layerFold)
		}
		got := published(&m, 0)
		exact := 0.5 * math.Pow(mdecay, 4096)
		if got < exact*(1-1e-12) || got > exact/layerFoldDecay*(1+1e-12) {
			t.Errorf("reads %v: a peak 4096 frames back reads %g, want %g to %g", reads, got, exact, exact/layerFoldDecay)
		}
	}
}
