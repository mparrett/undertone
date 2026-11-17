package engine

import "math"

// Measure reads a rendered sound the way the sfx board shows it: its peak,
// a peak and RMS envelope the board draws, its loudest moment as momentary
// loudness, and how long it actually sounds. One implementation, so the page
// that authors a sound, the board that lists it and a native build agree on
// its numbers rather than each keeping a copy that drifts.
//
// The board's figures came from a Python script over rendered WAVs; Measure
// reproduces them (TestMeasureMatchesBoard pins a render against them).

// EnvHop is the envelope's resolution in frames: 10 ms, one value per hop
// of the board's meter.
const EnvHop = SampleRate / 100

// ringFloor is where a sound has stopped sounding, in thousandths of full
// scale: the board's envelope holds rounded thousandths and its meter's
// -48 dBFS floor (3.98) reads as 4, so a hop rings while it rounds to 4 or
// more, from -49.1 dBFS. Cutting at -48 exactly would shorten most of the
// board's sounds by a hop or more, some by 0.2 s.
const ringFloor = 3.5

// Measurement describes a stereo render. Levels are linear, 1 being full
// scale; EnvPeak and EnvRMS hold one value per EnvHop frames, the last hop
// possibly short, with trailing silent hops dropped.
type Measurement struct {
	Peak    float64 // max |sample| over both channels
	EnvPeak []float64
	EnvRMS  []float64 // over both channels' samples in the hop together

	// MaxLUFS is the loudest momentary loudness (ITU-R BS.1770: K-weighted,
	// 400 ms windows every 100 ms, the channels' mean squares summed). The
	// loudest window says whether a sound cuts through the bed. Silence
	// reads about -120.7, the floor the board's script clamps to.
	MaxLUFS float64

	// Length is how long the sound sounds, in seconds: to the end of the
	// last hop whose peak reaches the board meter's floor (its "rings N s";
	// see ringFloor), or 0 if none does.
	Length float64
}

// Measure measures interleaved stereo float samples at SampleRate, 1.0 being
// full scale. A trailing odd sample is ignored.
func Measure(x []float32) Measurement { return measure(x, 1) }

// MeasurePCM measures interleaved stereo 16-bit samples at SampleRate, as
// Synth.Read writes them. Full scale is 32767, as the board's script reads
// its WAVs.
func MeasurePCM(x []int16) Measurement { return measure(x, 1.0/32767) }

func measure[T int16 | float32](x []T, scale float64) Measurement {
	frames := len(x) / 2
	x = x[:frames*2]
	var m Measurement

	for i := 0; i < frames; i += EnvHop {
		w := x[2*i : 2*min(i+EnvHop, frames)]
		var pk, sq float64
		for _, v := range w {
			a := float64(v) * scale
			pk = max(pk, math.Abs(a))
			sq += a * a
		}
		m.EnvPeak = append(m.EnvPeak, pk)
		m.EnvRMS = append(m.EnvRMS, math.Sqrt(sq/float64(len(w))))
		m.Peak = max(m.Peak, pk)
	}
	n := len(m.EnvPeak)
	for n > 0 && m.EnvPeak[n-1] == 0 {
		n--
	}
	m.EnvPeak, m.EnvRMS = m.EnvPeak[:n], m.EnvRMS[:n]
	for i := n - 1; i >= 0; i-- {
		if 1000*m.EnvPeak[i] >= ringFloor {
			m.Length = float64(min((i+1)*EnvHop, frames)) / SampleRate
			break
		}
	}

	m.MaxLUFS = maxMomentary(x, scale)
	return m
}

// Momentary loudness windows: 400 ms every 100 ms. A window is four blocks.
const (
	lufsBlock  = SampleRate / 10
	lufsBlocks = 4
)

// maxMomentary is the loudest 400 ms window's loudness. The squares are
// summed per 100 ms block and a window adds four blocks, rather than keeping
// a running sum per sample: a minute's render would need a 2.6M-entry prefix
// array, and the blocks are what the windows are made of anyway.
func maxMomentary[T int16 | float32](x []T, scale float64) float64 {
	frames := len(x) / 2
	nblocks := (frames + lufsBlock - 1) / lufsBlock
	// A sound shorter than one window is measured as one window, padded with
	// silence, as the board's script does.
	nblocks = max(nblocks, lufsBlocks)
	blocks := make([]float64, nblocks)
	var fl, fr [2]biquadState
	for i := range frames {
		l := float64(x[2*i]) * scale
		r := float64(x[2*i+1]) * scale
		for k, f := range kWeighting {
			l = f.step(&fl[k], l)
			r = f.step(&fr[k], r)
		}
		blocks[i/lufsBlock] += l*l + r*r
	}
	// Windows end within the sound, as the script's do: a part-filled final
	// block is never a window's end, except in the one padded window.
	whole := max(frames, lufsBlocks*lufsBlock) / lufsBlock
	best := 0.0
	for j := 0; j+lufsBlocks <= whole; j++ {
		var s float64
		for _, b := range blocks[j : j+lufsBlocks] {
			s += b
		}
		best = max(best, s)
	}
	ms := best / (lufsBlocks * lufsBlock)
	return -0.691 + 10*math.Log10(max(ms, 1e-12))
}

// biquad is a normalized second-order section (a0 = 1), run in transposed
// direct form II, as scipy's lfilter runs one.
type biquad struct{ b0, b1, b2, a1, a2 float64 }

type biquadState struct{ z1, z2 float64 }

func (f biquad) step(s *biquadState, x float64) float64 {
	y := f.b0*x + s.z1
	s.z1 = f.b1*x - f.a1*y + s.z2
	s.z2 = f.b2*x - f.a2*y
	return y
}

// kWeighting is BS.1770's K-weighting as two biquads, a high shelf and then
// a high-pass, with the coefficients derived for SampleRate as pyloudnorm
// derives them (rather than the spec's tabled 48 kHz ones).
var kWeighting = [2]biquad{
	rbjBiquad(true, 4, 1/math.Sqrt2, 1500),
	rbjBiquad(false, 0, 0.5, 38),
}

func rbjBiquad(shelf bool, gainDB, q, fc float64) biquad {
	a := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * fc / SampleRate
	alpha, cw := math.Sin(w0)/(2*q), math.Cos(w0)
	var b, d [3]float64
	if shelf {
		sa := 2 * math.Sqrt(a) * alpha
		b = [3]float64{a * ((a + 1) + (a-1)*cw + sa), -2 * a * ((a - 1) + (a+1)*cw), a * ((a + 1) + (a-1)*cw - sa)}
		d = [3]float64{(a + 1) - (a-1)*cw + sa, 2 * ((a - 1) - (a+1)*cw), (a + 1) - (a-1)*cw - sa}
	} else {
		b = [3]float64{(1 + cw) / 2, -(1 + cw), (1 + cw) / 2}
		d = [3]float64{1 + alpha, -2 * cw, 1 - alpha}
	}
	return biquad{b[0] / d[0], b[1] / d[0], b[2] / d[0], d[1] / d[0], d[2] / d[0]}
}
