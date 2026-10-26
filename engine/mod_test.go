package engine

import (
	"math"
	"testing"
)

// crossings counts upward zero crossings in x, which track its pitch.
func crossings(x []float64) int {
	n := 0
	for i := 1; i < len(x); i++ {
		if x[i-1] < 0 && x[i] >= 0 {
			n++
		}
	}
	return n
}

// An lfo into pitch makes a vibrato: a sine's frequency, read off its zero
// crossings in short windows, swings above and below the note; without the
// pitch unit it holds steady.
func TestPitchMakesVibrato(t *testing.T) {
	swing := func(p Patch) (lo, hi int) {
		x := play(p, 69, SampleRate)
		lo = math.MaxInt
		for w := 0; w+SampleRate/10 <= len(x); w += SampleRate / 10 {
			c := crossings(x[w : w+SampleRate/10])
			lo, hi = min(lo, c), max(hi, c)
		}
		return
	}
	plain := Patch{held, {opOsc, [4]float64{0, shpSine, 0, 1}}, {opMul, [4]float64{}}, {opOut, [4]float64{1, 0}}}
	vib := Patch{held, {opLfo, [4]float64{2, 0}}, {opPitch, [4]float64{2}},
		{opOsc, [4]float64{0, shpSine, 0, 1}}, {opMul, [4]float64{}}, {opOut, [4]float64{1, 0}}}
	if lo, hi := swing(plain); hi-lo > 1 {
		t.Errorf("a plain sine wanders %d..%d crossings per 100 ms", lo, hi)
	}
	// two semitones is about 12% either way: 44 crossings per 100 ms ±5
	if lo, hi := swing(vib); lo > 41 || hi < 47 {
		t.Errorf("vibrato spans %d..%d crossings per 100 ms; want at least 41..47", lo, hi)
	}
}

// pitch bends only the oscs after it in the patch.
func TestPitchBendsOnlyLaterOscs(t *testing.T) {
	before := Patch{held, {opOsc, [4]float64{0, shpSine, 0, 1}}, {opLfo, [4]float64{2, 0}}, {opPitch, [4]float64{12}},
		{opMul, [4]float64{}}, {opOut, [4]float64{1, 0}}}
	plain := Patch{held, {opOsc, [4]float64{0, shpSine, 0, 1}}, {opMul, [4]float64{}}, {opOut, [4]float64{1, 0}}}
	a, b := play(before, 69, SampleRate/4), play(plain, 69, SampleRate/4)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("sample %d: a pitch unit after the osc changed it", i)
		}
	}
}

// An lfo scaled into 0.4..1 and multiplied in makes a tremolo: the level
// swings by that ratio and never falls silent.
func TestScaleMakesTremolo(t *testing.T) {
	p := Patch{held, {opOsc, [4]float64{0, shpSine, 0, 1}}, {opMul, [4]float64{}},
		{opLfo, [4]float64{4, 0}}, {opScale, [4]float64{0.3, 0.7}}, {opMul, [4]float64{}}, {opOut, [4]float64{1, 0}}}
	x := play(p, 69, SampleRate)
	lo, hi := math.Inf(1), 0.0
	for w := 0; w+SampleRate/100 <= len(x); w += SampleRate / 100 {
		e := rmsOf(x[w : w+SampleRate/100])
		lo, hi = min(lo, e), max(hi, e)
	}
	if r := lo / hi; r < 0.3 || r > 0.5 {
		t.Errorf("level swings %.2f..%.2f (ratio %.2f); want about 0.4", lo, hi, r)
	}
}

// A modulator grown past any one unit's bounds by chained scales still
// bends a finite amount: the bend is clamped, so the patch stays finite.
func TestChainedScalesCannotOverflowTheBend(t *testing.T) {
	units := []UnitSpec{{Op: "env", Args: []float64{0.001, 0.1, 1, 0.05}}, {Op: "lfo", Args: []float64{1, 0}}}
	for range 10 {
		units = append(units, UnitSpec{Op: "scale", Args: []float64{2, 2}})
	}
	units = append(units, UnitSpec{Op: "pitch", Args: []float64{12}},
		UnitSpec{Op: "osc", Shape: "sine", Args: []float64{0, 0, 1}}, UnitSpec{Op: "mul"}, UnitSpec{Op: "out", Args: []float64{0.5, 0.5}})
	if err := RegisterShot(ShotSpec{Name: "overbent", GateMS: 100, Units: units}); err != nil {
		t.Fatalf("the patch the test needs was refused: %v", err)
	}
	s := NewSynth(silentParams)
	s.Trigger("overbent", 60, 1, 0.5, 0, 1)
	for range SampleRate / 10 {
		var l, r float64
		s.runShots(&l, &r)
		if math.IsNaN(l) || math.IsInf(l, 0) {
			t.Fatalf("rendered %v", l)
		}
	}
}
