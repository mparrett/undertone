package engine

import (
	"math"
	"testing"
)

// playGrit runs a patch on one voice at a grit for n samples.
func playGrit(p Patch, grit float64, n int) []float64 {
	var v Voice
	v.grit, v.gritTo = grit, grit
	v.trigger(p, 69, 1<<20, 1, 1)
	out := make([]float64, n)
	for i := range out {
		var l, r float64
		var d sends
		v.run(&l, &r, &d)
		out[i] = l
	}
	return out
}

// harmonics is the share of a 440 Hz tone's energy that is not at 440 Hz.
func harmonics(x []float64) float64 {
	var re, im float64
	for i, v := range x {
		w := 2 * math.Pi * 440 * float64(i) / SampleRate
		re += v * math.Cos(w)
		im += v * math.Sin(w)
	}
	fund := 2 * (re*re + im*im) / float64(len(x)*len(x)) // power at 440 Hz
	total := rmsOf(x) * rmsOf(x)
	return 1 - fund/total
}

// Grit makes a driven tone rougher, not louder: more of its energy moves
// into harmonics while its level stays within a couple of dB.
func TestGritRoughensDriveWithoutLoudness(t *testing.T) {
	p := Patch{held, {opOsc, [4]float64{0, shpSine, 0, 1}}, {opMul, [4]float64{}},
		{opDrive, [4]float64{1.5, 0.1}}, {opOut, [4]float64{1, 0}}}
	calm := playGrit(p, 0, SampleRate)[SampleRate/2:]
	rough := playGrit(p, 1, SampleRate)[SampleRate/2:]
	if h0, h1 := harmonics(calm), harmonics(rough); h1 < 2*h0 {
		t.Errorf("harmonics %0.3f at grit 0, %0.3f at grit 1: grit did not roughen", h0, h1)
	}
	if db := 20 * math.Log10(rmsOf(rough)/rmsOf(calm)); math.Abs(db) > 2 {
		t.Errorf("grit changed the level by %0.1f dB", db)
	}
	if x := playGrit(Patch{held, {opOsc, [4]float64{0, shpSine, 0, 1}}, {opEnv, [4]float64{0, 0.01, 0, 0.01}}, {opMul, [4]float64{}},
		{opDrive, [4]float64{3, 0.5}}, {opOut, [4]float64{1, 0}}}, 0, SampleRate/4); math.Abs(x[len(x)-1]) > 1e-9 {
		t.Errorf("a biased drive turns silence into %v", x[len(x)-1])
	}
}

// Crush holds each sample for a hold period and rounds it to its bit depth;
// grit takes bits off and slows the hold.
func TestCrushHoldsAndQuantizes(t *testing.T) {
	p := Patch{held, {opOsc, [4]float64{0, shpSine, 0, 1}}, {opMul, [4]float64{}},
		{opCrush, [4]float64{SampleRate / 8, 4}}, {opOut, [4]float64{1, 0}}}
	levels := func(grit float64) (distinct int, run int) {
		x := playGrit(p, grit, SampleRate/10)
		seen := map[float64]bool{}
		longest, cur := 0, 1
		for i, v := range x {
			seen[v] = true
			if i > 0 && v == x[i-1] {
				cur++
			} else {
				cur = 1
			}
			longest = max(longest, cur)
		}
		return len(seen), longest
	}
	d0, r0 := levels(0)
	// mid-tread, so silence stays silent: 4 bits is -8..8 eighths
	if d0 > 17 || r0 < 8 {
		t.Errorf("4 bits at an eighth of the rate: %d levels, holds of up to %d samples; want at most 17 levels and holds of 8", d0, r0)
	}
	if d1, r1 := levels(1); d1 >= d0 || r1 <= r0 {
		t.Errorf("grit 1: %d levels, holds of %d; want fewer levels and longer holds than %d and %d", d1, r1, d0, r0)
	}
}

// Grit raises an svf's resonance: its bandpass rejects a tone two octaves
// off more sharply.
func TestGritSharpensTheSvf(t *testing.T) {
	reject := func(grit float64) float64 {
		at := func(semis float64) float64 {
			p := Patch{held, {opOsc, [4]float64{semis, shpSine, 0, 1}}, {opMul, [4]float64{}},
				{opSvf, [4]float64{440, 0.5, 1, 0}}, {opOut, [4]float64{1, 0}}}
			return rmsOf(playGrit(p, grit, SampleRate/2)[SampleRate/4:])
		}
		return at(0) / at(24)
	}
	if r0, r1 := reject(0), reject(1); r1 < 1.5*r0 {
		t.Errorf("pass/reject %0.1f at grit 0, %0.1f at grit 1", r0, r1)
	}
}

// A grit change reaches a sounding note as a glide, not a step: the first
// sample after it is still at the old grit, and it settles within a quarter
// of a second.
func TestGritGlidesOnASoundingNote(t *testing.T) {
	p, _ := DeriveMood("rust", 1, 0, 0)
	s := NewSynth(p)
	render(s, SampleRate)
	v := &s.voices[0]
	if !v.active || v.grit != 0 {
		t.Fatalf("the drone is not sounding at grit 0 (active %v, grit %v)", v.active, v.grit)
	}
	p.Grit = 1
	s.SetParametersNow(p)
	render(s, nowBlock+160) // ten glide steps past the block where it applies
	if v.gritTo != 1 || v.grit >= 0.5 {
		t.Errorf("a block after the change, grit is %v toward %v: want a glide under way", v.grit, v.gritTo)
	}
	render(s, SampleRate/4)
	if v.grit != 1 {
		t.Errorf("a quarter second later grit is %v, want 1", v.grit)
	}
}

// One-shots stay at grit 0 whatever the music's grit, as they stay at full
// level whatever its gain.
func TestShotsIgnoreGrit(t *testing.T) {
	p := silentParams
	p.Grit = 1
	s := NewSynth(p)
	for _, v := range s.fx.shots {
		if v.grit != 0 || v.gritTo != 0 {
			t.Fatalf("a shot voice has grit %v toward %v", v.grit, v.gritTo)
		}
	}
}
