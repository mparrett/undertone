package engine

import (
	"math"
	"testing"
)

// sweepError plays a swept oscillator unit alone and returns how far its
// pitch strays, at worst, from the curve it stands for (the sweep falling by
// the same factor every sample), in cents, and how far its phase has
// drifted from that curve's by the time the sweep has died away, in turns.
func sweepError(u Unit, note int) (cents, turns float64) {
	v := Voice{}
	patch := Patch{u}
	v.trigger(patch, note, 1, 1, 1)
	s := &v.units[0]
	sweep, exactPhase, phase := 1.0, 0.0, 0.0
	for i := 0; i < 4*SampleRate && sweep > 1e-6; i++ {
		exact := noteInc(float64(note) + u.p[0] + u.p[2]*sweep)
		sweep -= sweep / (u.p[3]*SampleRate + 1)
		before := s.phase
		var l, r float64
		v.run(&l, &r, &sends{})
		inc := s.phase - before
		inc -= math.Floor(inc)
		cents = max(cents, math.Abs(1200*math.Log2(inc/exact)))
		exactPhase += exact
		phase += inc
	}
	return cents, math.Abs(phase - exactPhase)
}

// A sweep's pitch is worked out every 16th sample and runs straight between
// (see opOsc). For every swept oscillator in a built-in patch that line
// stays within a few cents of the curve, far inside what an ear can place
// in a sweep that lasts milliseconds, and the phase ends within a small
// fraction of a turn of where the curve would have left it.
func TestSweepFollowsItsCurve(t *testing.T) {
	patches := map[string]Patch{
		"kick": kick, "clap": clap, "pulse": pulse, "slam": slam, "grind": grind,
	}
	for name, d := range shotLib {
		patches["shot "+name] = d.patch
	}
	swept := 0
	for name, p := range patches {
		for i, u := range p {
			if u.op != opOsc || u.p[2] == 0 || int(u.p[1]) == shpNoise {
				continue
			}
			swept++
			cents, turns := sweepError(u, 45)
			t.Logf("%-14s unit %2d: sweep %5.1f semis over %.3fs: %.3f cents, %.4f turns", name, i, u.p[2], u.p[3], cents, turns)
			if cents > 3 || turns > 0.02 {
				t.Errorf("%s unit %d: strays %.2f cents and %.4f turns from its curve, want within 3 and 0.02", name, i, cents, turns)
			}
		}
	}
	if swept < 5 {
		t.Fatalf("found %d swept oscillators; the patch list is out of date", swept)
	}
}

// sine stands in for math.Sin on a phase in 0..1.
func TestSineIsAccurate(t *testing.T) {
	worst := 0.0
	const n = 1 << 20
	for i := 0; i <= n; i++ {
		x := float64(i) / n
		worst = max(worst, math.Abs(sine(x)-math.Sin(2*math.Pi*x)))
	}
	if worst > 1e-9 {
		t.Errorf("sine is off by up to %g, want within 1e-9", worst)
	}
	t.Logf("worst error %g", worst)
}
