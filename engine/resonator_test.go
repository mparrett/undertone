package engine

import (
	"math"
	"testing"
)

// runUnits renders units on one voice for n samples at grit, the voice
// given no line of its own, so each comb or room allocates as a shot's does.
// The gate closes after gate samples.
func runUnits(t *testing.T, units []UnitSpec, note int, grit float64, seed uint64, n, gate int) []float64 {
	t.Helper()
	p, err := buildPatch(units, musicBounds)
	if err != nil {
		t.Fatal(err)
	}
	var v Voice
	v.trigger(p, note, 1000, 1, seed)
	v.grit, v.gritTo = grit, grit
	var b sends
	out := make([]float64, n)
	for i := range out {
		v.gate = i < gate
		var l, r float64
		v.run(&l, &r, &b)
		out[i] = l + r
	}
	return out
}

// dustUnits is dust held open by an env that is 1 from the first sample.
func dustUnits(rate, bipolar, depth float64) []UnitSpec {
	return []UnitSpec{
		{Op: "env", Args: []float64{0, 10, 1, 1}},
		{Op: "dust", Args: []float64{rate, bipolar, depth}},
		{Op: "mul"},
		{Op: "out", Args: []float64{1, 0.5}},
	}
}

func impulses(x []float64) (n, neg int) {
	for _, v := range x {
		if v != 0 {
			n++
		}
		if v < 0 {
			neg++
		}
	}
	return n, neg
}

// dust fires about rate times a second, the same impulses for the same seed,
// and a bipolar one either way up about equally.
func TestDustRate(t *testing.T) {
	total, negs := 0, 0
	for seed := range uint64(20) {
		x := runUnits(t, dustUnits(50, 1, 0), 60, 0, seed, 10*SampleRate, 10*SampleRate)
		n, neg := impulses(x)
		total += n
		negs += neg
	}
	// 20 seeds of 10 s at 50 a second: 10000 expected, a standard deviation
	// of 100, so 5% is five of them
	if math.Abs(float64(total)-10000) > 500 {
		t.Errorf("%d impulses in 200 s at 50 a second, want about 10000", total)
	}
	if f := float64(negs) / float64(total); math.Abs(f-0.5) > 0.05 {
		t.Errorf("%.2f of a bipolar dust's impulses were negative, want about half", f)
	}
	a := runUnits(t, dustUnits(50, 0, 0), 60, 0, 3, SampleRate, SampleRate)
	b := runUnits(t, dustUnits(50, 0, 0), 60, 0, 3, SampleRate, SampleRate)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("seed 3 played differently twice, first at sample %d", i)
		}
		if a[i] < 0 {
			t.Fatalf("a unipolar dust went negative at sample %d", i)
		}
	}
}

func TestDustRateZeroIsSilent(t *testing.T) {
	if n, _ := impulses(runUnits(t, dustUnits(0, 1, 1), 60, 1, 1, 5*SampleRate, 5*SampleRate)); n != 0 {
		t.Errorf("rate 0 made %d impulses", n)
	}
}

// Grit moves dust only as far as its depth asks: at depth 0 not a sample
// changes, and at depth 1 full grit makes it three times as busy.
func TestDustDepth(t *testing.T) {
	calm := runUnits(t, dustUnits(40, 0, 0), 60, 0, 9, 10*SampleRate, 10*SampleRate)
	tense := runUnits(t, dustUnits(40, 0, 0), 60, 1, 9, 10*SampleRate, 10*SampleRate)
	for i := range calm {
		if calm[i] != tense[i] {
			t.Fatalf("at depth 0, grit changed sample %d", i)
		}
	}
	n, _ := impulses(runUnits(t, dustUnits(40, 0, 1), 60, 1, 9, 10*SampleRate, 10*SampleRate))
	if math.Abs(float64(n)-1200) > 120 { // sd about 35
		t.Errorf("at depth 1 and grit 1, %d impulses in 10 s at 40 a second, want about 1200", n)
	}
}

// burst is how long burstInto's gate stays open, in samples: past it the
// env releases to nothing, where a held sustain of 0 would leave a trickle of
// noise feeding the resonator.
const burst = SampleRate / 200

// burstInto is a few milliseconds of noise into res, a comb or room.
func burstInto(res UnitSpec) []UnitSpec {
	return []UnitSpec{
		{Op: "env", Args: []float64{0, 0.002, 0, 0.001}},
		{Op: "osc", Shape: "noise", Args: []float64{0, 0, 0}},
		{Op: "mul"},
		res,
		{Op: "out", Args: []float64{1, 0.5}},
	}
}

// ringCents is how far a resonator's ring is from want Hz, measured past
// the burst that strikes it.
func ringCents(t *testing.T, res UnitSpec, note int, want float64) float64 {
	t.Helper()
	x := runUnits(t, burstInto(res), note, 0, 5, SampleRate/4+SampleRate/100, burst)[SampleRate/100:]
	return 1200 * math.Log2(peakHz(x, want*0.95, want*1.05)/want)
}

// comb rings at the voice's note and room at its frequency, within a cent,
// on every semitone from A2 to C8 undamped and to C7 at damp 0.5: the loop's
// lowpass delays the fundamental, and the line is shortened by as much
// (without that, C4 at damp 0.5 played 10 cents flat). Above C7 that damped,
// the ring is gone in a few milliseconds and its peak is too broad to hold to
// a cent; measured 2026-11-19 it was 2 cents flat at D7 and 6 at C8.
func TestResonatorsAreInTune(t *testing.T) {
	step := 1
	if testing.Short() {
		step = 5
	}
	for _, damp := range []float64{0, 0.5} {
		top := 108
		if damp > 0 {
			top = 96
		}
		for note := top; note >= 45; note -= step {
			hz := noteHz(note)
			if c := ringCents(t, UnitSpec{Op: "comb", Args: []float64{0, 0.98, damp}}, note, hz); math.Abs(c) > 1 {
				t.Errorf("comb, damp %g, note %d: %+.2f cents, want within 1", damp, note, c)
			}
			if hz > 4000 {
				continue // past room's range
			}
			if c := ringCents(t, UnitSpec{Op: "room", Args: []float64{hz, 0.98, damp}}, 60, hz); math.Abs(c) > 1 {
				t.Errorf("room %.1f Hz, damp %g: %+.2f cents, want within 1", hz, damp, c)
			}
		}
	}
	// transpose moves comb from the note
	if c := ringCents(t, UnitSpec{Op: "comb", Args: []float64{7, 0.98, 0.3}}, 60, noteHz(67)); math.Abs(c) > 1 {
		t.Errorf("comb 7 semitones over C4: %+.2f cents from G4, want within 1", c)
	}
}

// At the edge of feedback, undamped, either sign, a ring still dies: below
// -60 dB of its loudest within 8 s at A2 (0.99 a period takes 687 periods).
func TestResonatorsDecay(t *testing.T) {
	for _, fb := range []float64{0.99, -0.99} {
		x := runUnits(t, burstInto(UnitSpec{Op: "room", Args: []float64{110, fb, 0}}), 60, 0, 2, 8*SampleRate, burst)
		peak, tail := 0.0, 0.0
		for i, v := range x {
			if i < len(x)-SampleRate/10 {
				peak = max(peak, math.Abs(v))
			} else {
				tail = max(tail, math.Abs(v))
			}
		}
		if peak == 0 || tail > peak*1e-3 {
			t.Errorf("feedback %g: tail %.2g against a peak of %.2g, want under -60 dB", fb, tail, peak)
		}
	}
}

// A legato retrigger, as the sequencer gives a bed's next note, carries the
// ring into the new note; emptying the line would cut it to silence for a
// period. A fresh strike does start from silence.
func TestCombRingsThroughLegato(t *testing.T) {
	// a held env, so the voice is still sounding when it is retriggered,
	// and a short burst striking the comb
	units := []UnitSpec{
		{Op: "env", Args: []float64{0, 0.002, 0, 0.001}},
		{Op: "osc", Shape: "noise", Args: []float64{0, 0, 0}},
		{Op: "mul"},
		{Op: "comb", Args: []float64{0, 0.98, 0.2}},
		{Op: "env", Args: []float64{0, 10, 1, 1}},
		{Op: "mul"},
		{Op: "out", Args: []float64{1, 0.5}},
	}
	firstPeriod := func(legato bool) float64 {
		p, err := buildPatch(units, musicBounds)
		if err != nil {
			t.Fatal(err)
		}
		var v Voice
		var b sends
		run := func(n int) (rms float64) {
			for range n {
				var l, r float64
				v.run(&l, &r, &b)
				rms += (l + r) * (l + r)
			}
			return math.Sqrt(rms / float64(n))
		}
		v.trigger(p, 57, 1000, 1, 1)
		run(SampleRate / 10)
		if !legato {
			v.active = false
		}
		v.trigger(p, 60, 1000, 1, 1)
		// C4's period is 168 samples, and the new burst comes out of the
		// line only after it
		return run(160)
	}
	if rms := firstPeriod(true); rms < 0.01 {
		t.Errorf("the first period after a legato retrigger played at rms %.2g: the ring was cut", rms)
	}
	if rms := firstPeriod(false); rms != 0 {
		t.Errorf("the first period after a fresh strike played at rms %.2g, want silence", rms)
	}
}

// The sounds dust, comb and room were made for (the proposal's examples)
// register as shots and sound.
func TestTextureShotsRegister(t *testing.T) {
	u := func(op string, args ...float64) UnitSpec { return UnitSpec{Op: op, Args: args} }
	noise := UnitSpec{Op: "osc", Shape: "noise", Args: []float64{0, 0, 0}}
	shots := map[string][]UnitSpec{
		"drips-in-key":  {u("env", 0.01, 1, 1, 2), u("dust", 3, 0, 0), u("comb", 12, 0.96, 0.4), u("mul"), u("verb", 0.5), u("out", 1, 0.5)},
		"drips-in-cave": {u("env", 0.01, 1, 1, 2), u("dust", 3, 0, 0), u("room", 180, 0.95, 0.5), u("mul"), u("verb", 0.5), u("out", 1, 0.5)},
		"embers":        {u("env", 0.05, 1, 1, 1), u("dust", 40, 1, 0), u("hpf", 1800), u("mul"), u("out", 1, 0.5)},
		"gravel":        {u("env", 0.001, 0.12, 0, 0.05), u("dust", 600, 1, 0), u("lpf", 3500), u("mul"), u("out", 1, 0.5)},
		"metal-hit":     {u("env", 0.001, 0.4, 0, 0.3), noise, u("mul"), u("room", 110, -0.9, 0.15), u("out", 1, 0.5)},
		"rain":          {u("env", 0.5, 1, 1, 2), u("dust", 120, 1, 0), u("svf", 2500, 0.3, 1, 0), u("mul"), u("verb", 0.3), u("out", 1, 0.5)},
		"geiger":        {u("env", 1, 1, 1, 2), u("dust", 2, 0, 1), u("comb", 0, 0.9, 0.6), u("mul"), u("out", 1, 0.5)},
	}
	for name, units := range shots {
		if err := RegisterShot(ShotSpec{Name: "texture-" + name, GateMS: 1000, Units: units}); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		s := NewSynth(silentParams)
		s.Trigger("texture-"+name, 60, 1, 0.5, 0, 4)
		if rms := playedRMS(s, 2); !(rms > 1e-4) {
			t.Errorf("%s played at rms %.2g over 2 s", name, rms)
		}
	}
}
