package engine

import (
	"math"
	"testing"
)

// play runs a patch on one voice for n samples and returns the left channel.
func play(p Patch, note, n int) []float64 {
	var v Voice
	v.trigger(p, note, 1<<20, 1, 1)
	out := make([]float64, n)
	for i := range out {
		var l, r float64
		var d sends
		v.run(&l, &r, &d)
		out[i] = l
	}
	return out
}

func rmsOf(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v * v
	}
	return math.Sqrt(s / float64(len(x)))
}

var held = Unit{opEnv, [4]float64{0, 0.01, 1, 0.1}} // full level at once, held

// A resonant bandpass passes a tone at its cutoff and rejects one two
// octaves away, more sharply as resonance rises.
func TestSvfBandpassResonates(t *testing.T) {
	ratio := func(res float64) float64 {
		at := func(semis float64) float64 {
			p := Patch{held, {opOsc, [4]float64{semis, shpSine, 0, 1}}, {opMul, [4]float64{}},
				{opSvf, [4]float64{440, res, 1, 0}}, {opOut, [4]float64{1, 0}}}
			return rmsOf(play(p, 69, SampleRate/2)[SampleRate/4:])
		}
		return at(0) / at(24)
	}
	soft, sharp := ratio(0), ratio(0.95)
	if soft < 2 || sharp < 4*soft {
		t.Errorf("pass/reject ratio %0.1f at resonance 0, %0.1f at 0.95: the filter does not resonate", soft, sharp)
	}
}

// An lfo on a lowpass's cutoff moves it within a note: a saw's top end
// comes and goes with the lfo, where a fixed cutoff leaves it steady.
func TestLfoSweepsTheCutoff(t *testing.T) {
	spread := func(depth float64) float64 {
		p := Patch{held, {opOsc, [4]float64{0, shpSaw, 0, 1}}, {opMul, [4]float64{}}}
		if depth != 0 { // only a modulated svf takes a value off the stack
			p = append(p, Unit{opLfo, [4]float64{1, 0}})
		}
		p = append(p, Unit{opSvf, [4]float64{400, 0.3, 0, depth}},
			Unit{opHpf, [4]float64{2000, 0, 0, 0}}, Unit{opOut, [4]float64{1, 0}})
		x := play(p, 45, 2*SampleRate)
		lo, hi := math.Inf(1), 0.0
		for w := SampleRate / 4; w+SampleRate/20 <= len(x); w += SampleRate / 20 {
			e := rmsOf(x[w : w+SampleRate/20])
			lo, hi = min(lo, e), max(hi, e)
		}
		return hi / lo
	}
	if still, swept := spread(0), spread(3); still > 1.2 || swept < 10 {
		t.Errorf("top-end swing %0.1fx fixed, %0.1fx swept: want steady, then moving", still, swept)
	}
}

// Sample-and-hold steps: it holds one value for a whole cycle, then jumps.
func TestLfoSampleAndHoldSteps(t *testing.T) {
	p := Patch{held, {opLfo, [4]float64{10, 2}}, {opMul, [4]float64{}}, {opOut, [4]float64{1, 0}}}
	x := play(p, 60, SampleRate/2)
	period := SampleRate / 10
	steps := 0
	for i := 1; i < len(x); i++ {
		if x[i] != x[i-1] {
			if i%period > 1 && i%period < period-1 {
				t.Fatalf("value changed mid-cycle at sample %d", i)
			}
			steps++
		}
	}
	if steps < 3 {
		t.Errorf("%d steps in five cycles", steps)
	}
}

// The bandpass passes a tone at its cutoff at unity gain whatever the
// resonance: more resonance narrows the band without making it louder.
func TestSvfBandpassIsUnityAtTheCutoff(t *testing.T) {
	for _, res := range []float64{0, 0.5, 1} {
		p := Patch{held, {opOsc, [4]float64{0, shpSine, 0, 1}}, {opMul, [4]float64{}},
			{opSvf, [4]float64{440, res, 1, 0}}, {opOut, [4]float64{1, 0}}}
		if g := rmsOf(play(p, 69, SampleRate/2)[SampleRate/4:]) / math.Sqrt(0.5); math.Abs(g-1) > 0.05 {
			t.Errorf("resonance %v: gain %0.2f at the cutoff, want 1", res, g)
		}
	}
}

// A legato retrigger keeps a sample-and-hold lfo's held value, as it keeps
// the phase: the step lands at the cycle's edge, not at the new note.
func TestLegatoKeepsTheHeldValue(t *testing.T) {
	p := Patch{held, {opLfo, [4]float64{2, 2}}, {opMul, [4]float64{}}, {opOut, [4]float64{1, 0}}}
	var v Voice
	v.trigger(p, 60, 1<<20, 1, 1)
	sample := func() float64 {
		var l, r float64
		var d sends
		v.run(&l, &r, &d)
		return l
	}
	for range SampleRate / 8 { // a quarter of the way into a cycle
		sample()
	}
	before := sample()
	v.trigger(p, 62, 1<<20, 1, 2)
	if after := sample(); after != before {
		t.Errorf("held %v, then %v after a legato retrigger", before, after)
	}
}

// Open moves an svf's cutoff: a lowpassed saw keeps more of its top end at
// open 1 than at 0, and less at -1. It leaves a patch without an svf alone.
func TestOpenMovesTheCutoff(t *testing.T) {
	top := func(p Patch, open float64) float64 {
		var v Voice
		v.open, v.openTo = open, open
		v.trigger(p, 45, 1<<20, 1, 1)
		x := make([]float64, SampleRate/2)
		for i := range x {
			var l, r float64
			var d sends
			v.run(&l, &r, &d)
			x[i] = l
		}
		// what a 2 kHz highpass lets through: the top end
		hp, prev, e := 0.0, 0.0, 0.0
		c := 1 - math.Exp(-2*math.Pi*2000/SampleRate)
		for _, s := range x[SampleRate/4:] {
			hp += c * (s - hp)
			prev = s - hp
			e += prev * prev
		}
		return e
	}
	svf := Patch{held, {opOsc, [4]float64{0, shpSaw, 0, 1}}, {opMul, [4]float64{}},
		{opSvf, [4]float64{400, 0.3, 0, 0}}, {opOut, [4]float64{1, 0}}}
	if shut, rest, wide := top(svf, -1), top(svf, 0), top(svf, 1); !(shut < rest && rest < wide) {
		t.Errorf("top end %.3g closed, %.3g at rest, %.3g open: want rising", shut, rest, wide)
	}
	plain := Patch{held, {opOsc, [4]float64{0, shpSaw, 0, 1}}, {opMul, [4]float64{}}, {opOut, [4]float64{1, 0}}}
	if a, b := top(plain, 0), top(plain, 1); a != b {
		t.Errorf("open changed a patch with no svf: %v vs %v", a, b)
	}
}
