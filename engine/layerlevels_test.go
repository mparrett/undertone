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

// The envelope takes a peak at once and lets go over about 400 ms.
func TestLayerMeterDecays(t *testing.T) {
	var m layerMeter
	m.see(2, -0.8)
	m.publish(512)
	if got := math.Float64frombits(m.out[2].Load()); got != 0.4 {
		t.Fatalf("after a peak of 0.8 summed: %g, want 0.4 at once", got)
	}
	m.publish(SampleRate / 10)
	mid := math.Float64frombits(m.out[2].Load())
	m.publish(SampleRate)
	end := math.Float64frombits(m.out[2].Load())
	if !(mid < 0.4 && mid > 0.05 && end < 1e-4) {
		t.Errorf("decay: %g after 100 ms, %g after 1.1 s; want falling to near 0", mid, end)
	}
}
