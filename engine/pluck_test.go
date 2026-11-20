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
	// down from the top, so a short run still checks C8, where the period is
	// shortest and an approximate coefficient drifts most
	for note := 108; note >= 45; note -= step {
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

// v020String is pluck's loop as undertone v0.2.0 ran it, kept here as the
// reference pluckraw answers to: a line int(SampleRate/freq) long, each pass
// averaging a sample with the next, no allpass.
func v020String(note int, transpose, feedback float64, rng uint64, samples int) []float64 {
	s := ustate{rng: rng}
	freq := 440 * math.Pow(2, (float64(note)+transpose-69)/12)
	ks := make([]float64, int(max(2, min(SampleRate/freq, maxPluckLine))))
	for i := range ks {
		ks[i] = s.noise()
	}
	out := make([]float64, samples)
	pos := 0
	for i := range out {
		x := ks[pos]
		next := ks[(pos+1)%len(ks)]
		ks[pos] = (x + next) * 0.5 * feedback
		pos = (pos + 1) % len(ks)
		out[i] = x
	}
	return out
}

// pluckraw plays v0.2.0's string sample for sample, so a sound made against
// that string can be had on later releases. Checked across the range a lead,
// a bass or a shot plays in, with a transpose, at the built-ins' feedbacks.
func TestPluckRawIsV020(t *testing.T) {
	for _, c := range []struct {
		note          int
		transpose, fb float64
	}{{33, -12, 0.996}, {45, 0, 0.972}, {57, 0, 0.96}, {69, 7, 0.996}, {84, 0, 0.9995}, {96, 12, 0.972}, {108, 0, 0.99}} {
		p, err := buildPatch([]UnitSpec{
			{Op: "env", Args: []float64{0, 10, 1, 1}},
			{Op: "pluckraw", Args: []float64{c.transpose, c.fb}},
			{Op: "mul"},
			{Op: "out", Args: []float64{1, 0.5}},
		}, shotBounds)
		if err != nil {
			t.Fatal(err)
		}
		var v Voice
		v.trigger(p, c.note, 1000, 1, 11)
		// the rng trigger gives the string, unit 1
		want := v020String(c.note, c.transpose, c.fb, v.units[1].rng, SampleRate/2)
		var b sends
		for i, w := range want {
			var l, r float64
			v.run(&l, &r, &b)
			if l+r != w {
				t.Fatalf("note %d %+g: sample %d is %v, v0.2.0's string gives %v", c.note, c.transpose, i, l+r, w)
			}
		}
	}
}

// and so it is out of tune the way that string was: sharp, more so up high
func TestPluckRawPlaysSharp(t *testing.T) {
	cents := func(note int) float64 {
		p, err := buildPatch([]UnitSpec{
			{Op: "env", Args: []float64{0.001, 10, 1, 1}},
			{Op: "pluckraw", Args: []float64{0, 0.9995}},
			{Op: "mul"},
			{Op: "out", Args: []float64{1, 0.5}},
		}, shotBounds)
		if err != nil {
			t.Fatal(err)
		}
		var v Voice
		v.trigger(p, note, 1000, 1, 7)
		var b sends
		x := make([]float64, SampleRate/4+SampleRate/100)
		for i := range x {
			var l, r float64
			v.run(&l, &r, &b)
			x[i] = l + r
		}
		want := noteHz(note)
		return 1200 * math.Log2(peakHz(x[SampleRate/100:], want*0.9, want*1.1)/want)
	}
	// measured in the engine before the allpass (pluck-ab, 2026-11-19)
	for _, c := range []struct {
		note  int
		cents float64
	}{{60, 11}, {96, 47.5}} {
		if got := cents(c.note); math.Abs(got-c.cents) > 1.5 {
			t.Errorf("note %d: %+.1f cents, want about %+.1f as v0.2.0 played it", c.note, got, c.cents)
		}
	}
}
