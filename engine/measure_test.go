package engine

import (
	"math"
	"testing"
)

// boardTake is a fight's blow and cry with the bed silent. Its numbers below
// are the sfx board's script's (build.py: levels(), momentary()) over this
// take rendered to a WAV.
var boardTake = Take{
	Params:    DeriveDungeon(7, 3, 0.5),
	MusicGain: 0,
	Shots: []Shot{
		{Name: "hit", Gain: 1, Pan: 0.5, Seed: 1, Note: 48, HasNote: true},
		{Name: "cry", Gain: 0.8, Pan: 0.3, DelayMS: 250, Seed: 2, Note: 60, HasNote: true},
	},
	Seconds: 2,
}

func TestMeasureMatchesBoard(t *testing.T) {
	m := MeasurePCM(boardTake.Render())

	// Rendered PCM can differ in its last bits across architectures (Go
	// fuses multiply-adds on arm64), so a rounded thousandth may land one
	// either side; against the same samples Measure agrees with the script
	// to well inside these.
	if got := math.Round(1000 * m.Peak); math.Abs(got-353) > 1 {
		t.Errorf("peak %.4f, the board's 0.353", m.Peak)
	}
	if math.Abs(m.MaxLUFS-(-19.803143976437834)) > 0.01 {
		t.Errorf("max momentary %.4f LUFS, the board's -19.8031", m.MaxLUFS)
	}
	if m.Length != 2 {
		t.Errorf("rings %v s, the board's 2", m.Length)
	}
	if len(m.EnvPeak) != 200 || len(m.EnvRMS) != 200 {
		t.Fatalf("%d/%d envelope hops, the board's 200", len(m.EnvPeak), len(m.EnvRMS))
	}
	pk := []int{353, 337, 310, 248, 195, 163, 135, 106, 89, 74, 55, 44, 38, 31, 24, 21, 16, 13, 11, 9, 7, 6, 5, 4, 3, 144, 175, 173, 170, 162}
	rms := []int{204, 197, 171, 144, 122, 93, 81, 64, 54, 45, 37, 28, 24, 20, 15, 13, 11, 8, 8, 6, 5, 4, 3, 3, 2, 35, 70, 69, 68, 67}
	everyPk := []int{353, 7, 129, 62, 29, 15, 7, 6, 88, 48}
	everyRMS := []int{204, 5, 52, 27, 13, 6, 3, 2, 40, 22}
	near := func(what string, i int, got float64, want int) {
		if g := math.Round(1000 * got); math.Abs(g-float64(want)) > 1 {
			t.Errorf("%s hop %d: %v thousandths, the board's %d", what, i, g, want)
		}
	}
	for i := range pk {
		near("peak", i, m.EnvPeak[i], pk[i])
		near("rms", i, m.EnvRMS[i], rms[i])
	}
	for k := range everyPk {
		near("peak", 20*k, m.EnvPeak[20*k], everyPk[k])
		near("rms", 20*k, m.EnvRMS[20*k], everyRMS[k])
	}
}

func tone(seconds, hz, ampL, ampR float64) []float32 {
	n := int(seconds * SampleRate)
	x := make([]float32, 2*n)
	for i := range n {
		v := math.Sin(2 * math.Pi * hz * float64(i) / SampleRate)
		x[2*i], x[2*i+1] = float32(ampL*v), float32(ampR*v)
	}
	return x
}

// BS.1770's own check: a 997 Hz sine at full scale in one channel reads
// -3.01 LUFS, and K-weighting leaves 997 Hz nearly untouched.
func TestMeasureLoudnessOfASine(t *testing.T) {
	for _, c := range []struct {
		name       string
		ampL, ampR float64
		want       float64
	}{
		{"one channel, full scale", 1, 0, -3.01},
		{"one channel, -20 dB", 0.1, 0, -23.01},
		{"both channels, -20 dB", 0.1, 0.1, -20.0},
	} {
		m := Measure(tone(2, 997, c.ampL, c.ampR))
		if math.Abs(m.MaxLUFS-c.want) > 0.05 {
			t.Errorf("%s: %.3f LUFS, want %.2f", c.name, m.MaxLUFS, c.want)
		}
	}
}

func TestMeasureLevelsAndLength(t *testing.T) {
	// Half a second at -6 dB, then a quiet tail under the floor, then a
	// second of silence.
	x := tone(0.5, 1000, 0.5, 0.25)
	x = append(x, tone(0.3, 1000, 0.003, 0.003)...)
	x = append(x, make([]float32, 2*SampleRate)...)
	m := Measure(x)
	if math.Abs(m.Peak-0.5) > 1e-5 {
		t.Errorf("peak %v, want 0.5", m.Peak)
	}
	// The tail is under the floor, so the sound rings 0.5 s, but it is not
	// silent, so the envelope runs on through it.
	if math.Abs(m.Length-0.5) > 1e-9 {
		t.Errorf("rings %v s, want 0.5", m.Length)
	}
	if len(m.EnvPeak) != 80 {
		t.Errorf("%d hops, want 80: the silence dropped, the quiet tail kept", len(m.EnvPeak))
	}
	// Ringing to its end through a short final hop, a sound rings its own
	// length, not the whole hop's.
	if m := Measure(tone(0.205, 1000, 0.5, 0.5)); math.Abs(m.Length-0.205) > 1.0/SampleRate {
		t.Errorf("a 205 ms tone rings %v s", m.Length)
	}
	// A hop holds ten whole cycles of 1 kHz; its RMS over both channels: sqrt((0.5²+0.25²)/4).
	if want := math.Sqrt((0.25 + 0.0625) / 4); math.Abs(m.EnvRMS[10]-want) > 1e-3 {
		t.Errorf("rms %v, want %v", m.EnvRMS[10], want)
	}
}

func TestMeasureSilenceAndShortSounds(t *testing.T) {
	m := Measure(make([]float32, 2*SampleRate))
	if m.Peak != 0 || m.Length != 0 || len(m.EnvPeak) != 0 || math.Abs(m.MaxLUFS-(-120.691)) > 1e-9 {
		t.Errorf("silence: %+v", m)
	}
	if m := Measure(nil); m.Peak != 0 || math.Abs(m.MaxLUFS-(-120.691)) > 1e-9 {
		t.Errorf("nothing: %+v", m)
	}
	// Shorter than one 400 ms window: measured as one, padded with silence,
	// so 100 ms of a sine reads a quarter of its power.
	full := Measure(tone(2, 997, 0.1, 0.1)).MaxLUFS
	short := Measure(tone(0.1, 997, 0.1, 0.1)).MaxLUFS
	if d := full - short; math.Abs(d-10*math.Log10(4)) > 0.1 {
		t.Errorf("100 ms reads %.2f LU under the full sine, want %.2f", d, 10*math.Log10(4))
	}
}

// The int16 path reads samples as the board's script reads its WAVs.
func TestMeasurePCMScale(t *testing.T) {
	m := MeasurePCM([]int16{32767, -32767, 16384, 0})
	if m.Peak != 1 {
		t.Errorf("full-scale peak %v, want 1", m.Peak)
	}
	if odd := MeasurePCM([]int16{100, 100, 32767}); odd.Peak != 100.0/32767 {
		t.Errorf("a trailing odd sample was read: peak %v", odd.Peak)
	}
}
