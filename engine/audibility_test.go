package engine

import (
	"math"
	"sort"
	"testing"
)

// A one-shot has to be heard over the bed it plays against, which is a
// question about WHERE its energy sits, not how loud it is. The bed's drone
// and air track are lowpassed at 280-300 Hz, so everything below that is the
// bed's; and a phone speaker reproduces almost nothing under ~200 Hz, so
// energy spent there is spent twice over on nobody. The band above 1200 Hz is
// the one both facts leave free.
//
// Measured against the depth-2 bed, the footstep's first shape put 81% of its
// peak energy below 120 Hz (0.8 dB under the drone) and 2% above 1200 Hz
// (2.9 dB under). It was reported as inaudible in play, which is what sent
// this measurement looking. See the footstep patch in sfx.go.

// bandSplit is the same four-way split the VU meter uses (120/350/1200 Hz).
type bandSplit struct{ lp [3]float64 }

func (b *bandSplit) of(x float64) [4]float64 {
	for i, c := range mcoef {
		b.lp[i] += c * (x - b.lp[i])
	}
	return [4]float64{b.lp[0], b.lp[1] - b.lp[0], b.lp[2] - b.lp[1], x - b.lp[2]}
}

// bandStats returns per-band RMS and per-band peak. A bed is judged by its RMS
// and a 50 ms transient by its peak: averaging a tick over a turn-long window
// buries it about 10 dB and answers the wrong question.
func bandStats(s *Synth, n int) (rms, peak [4]float64) {
	var b bandSplit
	var sum [4]float64
	for i := 0; i < n; i++ {
		l, r := s.frame()
		v := (float64(l) + float64(r)) / 2 / 32768
		for j, e := range b.of(v) {
			sum[j] += e * e
			if a := math.Abs(e); a > peak[j] {
				peak[j] = a
			}
		}
	}
	for j := range sum {
		rms[j] = math.Sqrt(sum[j] / float64(n))
	}
	return rms, peak
}

func bedOnly(p Params) Params {
	p.DroneGain, p.FifthGain, p.PadDensity, p.LeadDensity, p.AirGain, p.PulseGain = 0, 0, 0, 0, 0, 0
	return p
}

var bandNames = [4]string{"sub <120Hz", "low 120-350", "mid 350-1200", "high >1200Hz"}

// shotPeaks is the per-band peak of one shot fired repeatedly at its in-game
// gain, averaged over turns so humanize's jitter does not pick the reading.
func shotPeaks(t *testing.T, p Params, name string, note int, gain float64) [4]float64 {
	t.Helper()
	s := NewSynth(bedOnly(p))
	var out [4]float64
	const turns = 16
	for i := 0; i < turns; i++ {
		if !s.Trigger(name, note, gain, 0.5, 0, uint64(i)*2654435761) {
			t.Fatalf("unknown shot %q", name)
		}
		_, pk := bandStats(s, SampleRate/2)
		for j := range out {
			out[j] += pk[j] * pk[j]
		}
	}
	for j := range out {
		out[j] = math.Sqrt(out[j] / float64(turns))
	}
	return out
}

func TestFootstepIsHeardOverTheBed(t *testing.T) {
	p := DeriveDungeon(0x5eed1234, 2, 0)
	bed, _ := bandStats(NewSynth(p), SampleRate*8)
	step := shotPeaks(t, p, "footstep", 43, 0.32) // the gain xsofy.sfx sends

	var total float64
	for _, v := range step {
		total += v * v
	}
	for j, n := range bandNames {
		t.Logf("%-13s bed %.5f  step %.5f  %+5.1f dB  %3.0f%% of step",
			n, bed[j], step[j], 20*math.Log10(step[j]/bed[j]), 100*step[j]*step[j]/total)
	}

	// It must poke above the bed somewhere a phone can reproduce.
	if step[3] <= bed[3] {
		t.Errorf("footstep peak above 1200 Hz is %.5f, not above the bed's %.5f — "+
			"it will be inaudible on a phone", step[3], bed[3])
	}
	// And it must not go back to being a sub-bass thump.
	if share := step[0] * step[0] / total; share > 0.5 {
		t.Errorf("%.0f%% of the footstep's energy is below 120 Hz; the bed owns that "+
			"band and a phone speaker does not have it", 100*share)
	}
	// It is the every-turn sound: audible, never the loudest thing playing.
	if hit := shotPeaks(t, p, "hit", 45, 0.9); step[3] >= hit[3] {
		t.Errorf("footstep is as bright as a hit (%.5f vs %.5f); the every-turn sound "+
			"must stay the quiet one", step[3], hit[3])
	}
}

// Every one-shot has to clear the bed somewhere, or it is decoration nobody
// hears. This reports the margin for the whole library and fails only on a
// shot that clears it nowhere — the per-band shape is a taste decision, but
// "audible at all against the thing it plays over" is not.
func TestEveryShotClearsTheBedSomewhere(t *testing.T) {
	p := DeriveDungeon(0x5eed1234, 2, 0)
	bed, _ := bandStats(NewSynth(p), SampleRate*8)

	names := make([]string, 0, len(shotLib))
	for n := range shotLib {
		names = append(names, n)
	}
	sort.Strings(names)

	t.Logf("%-9s %s", "shot", "peak-over-bed, dB per band (sub / low / mid / high)")
	for _, n := range names {
		pk := shotPeaks(t, p, n, 48, 0.7)
		var best float64 = math.Inf(-1)
		margins := make([]float64, 4)
		for j := range pk {
			margins[j] = 20 * math.Log10(pk[j]/bed[j])
			if margins[j] > best {
				best = margins[j]
			}
		}
		t.Logf("%-9s %+6.1f %+6.1f %+6.1f %+6.1f", n, margins[0], margins[1], margins[2], margins[3])
		if best < 0 {
			t.Errorf("%s never rises above the bed (best band %+.1f dB) — it will not be heard", n, best)
		}
	}
}
