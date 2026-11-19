package engine

import (
	"math"
	"testing"
)

// stringUnits is a pluck held open by its env, so what is measured is the
// string alone: no filter, no release.
func stringUnits(feedback float64) []UnitSpec {
	return []UnitSpec{
		{Op: "env", Args: []float64{0.001, 10, 1, 1}},
		{Op: "pluck", Args: []float64{0, feedback}},
		{Op: "mul"},
		{Op: "out", Args: []float64{1, 0.5}},
	}
}

// stringAt renders a string patch on note for secs.
func stringAt(t *testing.T, note int, secs float64) []float64 {
	t.Helper()
	p, err := buildPatch(stringUnits(0.9995), shotBounds)
	if err != nil {
		t.Fatal(err)
	}
	var voice Voice
	voice.trigger(p, note, 1000, 1, 7)
	var b sends
	out := make([]float64, int(secs*SampleRate))
	for i := range out {
		var l, r float64
		voice.run(&l, &r, &b)
		out[i] = l + r
	}
	return out
}

// peakHz is the frequency within lo..hi where x's Hann-windowed spectrum
// peaks, found by golden-section search on the spectrum itself rather than
// read off FFT bins, which at a few hundred milliseconds are cents wide.
// The string's sound is a sum of decaying partials; an envelope that is real
// leaves each partial's peak where its frequency is, however fast it decays.
func peakHz(x []float64, lo, hi float64) float64 {
	n := len(x)
	w := make([]float64, n)
	for i, v := range x {
		w[i] = v * (0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n)))
	}
	mag := func(f float64) float64 {
		// a phasor turned by complex multiplication, renormalized now
		// and then, instead of a sin and cos per sample
		step := 2 * math.Pi * f / SampleRate
		cr, ci := math.Cos(step), -math.Sin(step)
		pr, pi := 1.0, 0.0
		var re, im float64
		for i, v := range w {
			re += v * pr
			im += v * pi
			pr, pi = pr*cr-pi*ci, pr*ci+pi*cr
			if i&1023 == 1023 {
				m := math.Hypot(pr, pi)
				pr, pi = pr/m, pi/m
			}
		}
		return re*re + im*im
	}
	g := (math.Sqrt(5) - 1) / 2
	a, b := hi-g*(hi-lo), lo+g*(hi-lo)
	fa, fb := mag(a), mag(b)
	for range 60 {
		if fa > fb {
			hi, b, fb = b, a, fa
			a = hi - g*(hi-lo)
			fa = mag(a)
		} else {
			lo, a, fa = a, b, fb
			b = lo + g*(hi-lo)
			fb = mag(b)
		}
	}
	return (lo + hi) / 2
}

func noteHz(note int) float64 { return 440 * math.Pow(2, float64(note-69)/12) }

// stringCents is how far the string plays from note, measured.
func stringCents(t *testing.T, note int) float64 {
	t.Helper()
	// skip the env's attack; a quarter second resolves a cent at A2
	x := stringAt(t, note, 0.26)[SampleRate/100:]
	want := noteHz(note)
	return 1200 * math.Log2(peakHz(x, want*0.95, want*1.05)/want)
}

// A string plays the note it is given, within a cent, on every semitone a
// lead or a pickup plays (A2 to C8).
func TestPluckIsInTune(t *testing.T) {
	step := 1
	if testing.Short() {
		step = 5
	}
	for note := 45; note <= 108; note += step {
		if c := stringCents(t, note); math.Abs(c) > 1 {
			t.Errorf("note %d: %+.2f cents, want within 1", note, c)
		}
	}
}

// A line of whole samples alone, the string before the allpass, played C7
// 47 cents sharp (its loop the line less the averager's half sample). Held
// here, where the sweep above has a step under -short.
func TestPluckC7IsInTune(t *testing.T) {
	if c := stringCents(t, 96); math.Abs(c) > 1 {
		t.Errorf("C7: %+.2f cents, want within 1", c)
	}
}
