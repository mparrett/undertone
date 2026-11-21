// Package engine is undertone's generative-ambience engine: a stack-of-unit-
// opcodes synth VM, a tracker-style sequencer, and the game-state -> music
// parameter derivation, all deterministic from a seed.
//
// The package boundary exists so more than one front end can drive the same
// engine: the CLI in ../cmd/undertone, and the let-go binding in ../letgo,
// which moves the derivation layer into lg (lg is the control language, Go is
// the DSP engine). The package imports nothing from let-go.
package engine

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sync/atomic"
	"time"
)

const SampleRate = 44100

// ---- unit VM ----

const (
	opEnv      = iota // p: attack s, decay s, sustain lvl, release s   -> push env
	opOsc             // p: transpose semis, shape, sweep semis, sweep decay s -> push sample
	opMul             // pop a, b -> push a*b
	opLpf             // p: cutoff Hz; pop x -> push lowpassed x
	opSend            // p: amount; peek top, add top*amount to delay bus
	opOut             // p: gain, pan 0..1; pop x -> accumulate to L/R
	opPluck           // p: transpose semis, feedback; Karplus-Strong string -> push sample
	opHpf             // p: cutoff Hz; pop x -> push highpassed x
	opSvf             // p: cutoff Hz, resonance 0..1, mode (0 low, 1 band, 2 high), depth octaves; [pop m;] pop x -> push filtered x
	opLfo             // p: rate Hz, shape (0 sine, 1 triangle, 2 sample-and-hold) -> push -1..1
	opDrive           // p: drive, bias -1..1; pop x -> push tanh(drive*x + bias) - tanh(bias)
	opCrush           // p: hold Hz, bits; pop x -> push x held and quantized
	opVerb            // p: amount; peek top, add top*amount to the reverb bus
	opScale           // p: gain, offset; top = top*gain + offset
	opPitch           // p: depth semis; pop m, bend every later osc by m*depth
	opDust            // p: rate Hz, bipolar 0|1, depth 0..1 -> push random impulses
	opComb            // p: transpose semis, feedback, damp; pop x -> push x resonated at the note
	opRoom            // p: freq Hz, feedback, damp; pop x -> push x resonated at freq
	opPluckRaw        // p: transpose semis, feedback; pluck without its tuning -> push sample
	// comb and room's p[3] is set by buildPatch, not given: 1 when the input
	// dies with an env, so the ring may keep the voice alive
)

const (
	shpSine = iota
	shpSaw
	shpPulse
	shpNoise
)

type Unit struct {
	op int
	p  [4]float64
}

type Patch []Unit

// maxUnits is the longest patch a voice runs: the bound on a registered
// shot's, and well past the longest built-in.
const maxUnits = 32

// per-voice, per-unit mutable state
type ustate struct {
	phase, env, lp, sweep float64 // comb, room: env follows the ring's level
	ic2                   float64 // svf: the second integrator (lp holds the first); comb, room: the damping lowpass
	stage                 int     // env: 0 attack, 1 decay, 2 sustain, 3 release
	rng                   uint64
	ks                    []float64 // pluck, comb, room: delay line, allocated on first sample
	kpos                  int       // pluck, comb, room: read position; swept svf and bent osc: control-rate counter
	k                     float64   // cached per-note coefficient: osc phase increment, lpf c, or an allpass's
	dk                    float64   // swept osc: k's step per sample
	tick                  int       // swept osc: control-rate counter (kpos is the bend's); comb, room: the line's length
	g, g1, g2             float64   // svf, drive, crush: constants derived from grit g; osc: g1 is its bend ratio
	gset                  bool      // g1 and g2 hold for grit g
	ko                    float64   // svf: the open its coefficient k was computed at
	kset                  bool
	// comb, room: retriggered legato, so the ring carries into the new note
	// rather than the line being emptied, which would cut it with a click
	held bool
}

type Voice struct {
	patch    Patch
	note     int
	gain     float64
	gate     bool
	gateRows int
	units    []ustate
	active   bool
	// grit roughens drive, crush and svf. It glides to gritTo, which apply
	// keeps at Params.Grit, so a sounding note roughens over about a tenth of
	// a second rather than stepping. gtick counts samples between glide steps.
	grit, gritTo float64
	open, openTo float64 // Params.Open, gliding the same way: scales svf cutoff
	gtick        int

	// A mid-pattern mood change (applyNow) moves gain linearly to gainTo over
	// fadeFrames rather than stepping it, which would click. A note it has to
	// replace waits in pend until the old one has faded to silence.
	gainTo, gainStep float64
	pend             trackRow
	pendRows         int
	pendSeed         uint64

	// gateSamples is the one-shot lane's gate clock (sfx.go). Sequencer
	// voices gate in rows via step(); one-shots are off the grid, so their
	// gate expires in samples instead. Zero for sequencer voices.
	gateSamples int

	// line is a Karplus-Strong line the voice was given with the synth
	// (NewSynth), maxPluckLine long, so its strings allocate nothing on the
	// audio thread. One pluck unit per note plays in it; lineOut says one
	// has taken it. A voice made without one, or a patch's second string,
	// allocates its own.
	line    []float64
	lineOut bool
}

func (v *Voice) trigger(patch Patch, note, gateRows int, gain float64, seed uint64) {
	// A voice retriggered with the patch it is already sounding carries its
	// phase, level and filter state into the new note and attacks from where
	// it is. Starting from zero cut a sounding drone to silence in one sample:
	// a click at every pattern boundary (a step of 2336 against a normal
	// largest of 402). A plucked string is struck afresh either way.
	legato := v.active && samePatch(v.patch, patch)
	v.patch = patch
	v.note = clampNote(note)
	v.gain = gain
	v.gainTo = gain
	v.pend = trackRow{}
	v.gate = true
	v.gateRows = gateRows
	v.active = true
	if cap(v.units) < len(patch) {
		v.units = make([]ustate, len(patch))
	}
	v.units = v.units[:len(patch)]
	v.lineOut = false
	for i := range v.units {
		u := &v.units[i]
		// A unit keeps a line of its own for its next note, emptied so the
		// string is struck afresh. The voice's line it hands back: the patch
		// may have changed, and the string now be another unit.
		ks := u.ks[:0]
		if v.line != nil && cap(ks) > 0 && &ks[:1][0] == &v.line[0] {
			ks = nil
		}
		kpos, tick := u.kpos, u.tick
		if legato {
			*u = ustate{phase: u.phase, env: u.env, lp: u.lp, ic2: u.ic2, ks: ks}
		} else {
			*u = ustate{ks: ks}
		}
		if legato && (patch[i].op == opComb || patch[i].op == opRoom) {
			u.held, u.kpos, u.tick = true, kpos, tick
		}
		u.sweep = 1
		u.rng = seed*2862933555777941757 + uint64(i) + 3037000493
		// a sample-and-hold lfo's first value; legato keeps the held one
		if !legato && patch[i].op == opLfo {
			u.lp = u.noise()
		}
	}
}

// noteInc is the phase an oscillator advances per sample at a pitch in
// semitones, 69 being 440 Hz.
func noteInc(semis float64) float64 {
	return 440 * math.Pow(2, (semis-69)/12) / SampleRate
}

// minLineSweep is the shortest sweep decay, in seconds, whose pitch is
// stepped in 16-sample lines; a quicker one bends too far within a line and
// is worked out every sample.
const minLineSweep = 0.01

// sine is sin(2πx) for a phase x in 0..1, within 1e-9: about 180 dB under
// the wave. There is a sine in most patches, and math.Sin is accurate to the
// last bit over the whole number line, which costs more than a phase already
// in one turn needs. The phase is folded onto
// the quarter wave either side of zero and the series summed there, where
// seven terms are enough.
func sine(x float64) float64 {
	x -= 0.5 // sin(2πx) = -sin(2π(x-0.5)), and x-0.5 is within a half turn of zero
	if x > 0.25 {
		x = 0.5 - x
	} else if x < -0.25 {
		x = -0.5 - x
	}
	z := 2 * math.Pi * x
	z2 := z * z
	return -z * (1 + z2*(-1.0/6+z2*(1.0/120+z2*(-1.0/5040+z2*(1.0/362880+z2*(-1.0/39916800+z2*(1.0/6227020800)))))))
}

// noise steps the unit's LCG and returns a sample in -1..1.
func (s *ustate) noise() float64 {
	s.rng = s.rng*6364136223846793005 + 1442695040888963407
	return float64(int64(s.rng>>11))/(1<<52) - 1
}

// u01 steps the unit's LCG and returns a value in 0..1.
func (s *ustate) u01() float64 {
	s.rng = s.rng*6364136223846793005 + 1442695040888963407
	return float64(s.rng>>11) / (1 << 53)
}

// level is the voice's loudest envelope, or ring that keeps it alive,
// times its gain: how much it would be missed if cut.
func (v *Voice) level() float64 {
	l := 0.0
	for i := range v.units {
		switch u := v.patch[i]; {
		case u.op == opEnv, (u.op == opComb || u.op == opRoom) && u.p[3] != 0:
			l = max(l, v.units[i].env)
		}
	}
	return l * v.gain
}

// stealLevel is level as the shot pool compares it when choosing a voice to
// steal. A voice still in an attack counts at its gain, where the attack is
// heading: one started this instant has an envelope of 0, so a chord
// triggered against a full pool stole its own notes, each new note taking
// the voice the previous one had just been given. retrigger keeps level: it
// asks whether a fade would be heard now, not what is about to be.
func (v *Voice) stealLevel() float64 {
	for i := range v.units {
		if v.patch[i].op == opEnv && v.units[i].stage == 0 {
			return v.gain
		}
	}
	return v.level()
}

// glideKnobs moves grit and open a step toward their targets every 16
// samples, so the units they scale recompute their constants at control
// rate, not every sample.
func (v *Voice) glideKnobs() {
	v.gtick = (v.gtick + 1) & 15
	if v.gtick != 0 {
		return
	}
	glide := func(x *float64, to float64) {
		*x += (to - *x) * 0.02
		if math.Abs(to-*x) < 1e-3 {
			*x = to
		}
	}
	glide(&v.grit, v.gritTo)
	glide(&v.open, v.openTo)
}

// A voice's note is held to minNote..maxNote. Notes reach a voice from a
// game's arithmetic (a trigger's :note, a scale's intervals, a lead lift, a
// grid's degrees), and nothing in the write-only contract can refuse one, so
// the voice bounds what it is given. Past the top an oscillator's frequency
// overflows, and the NaN that makes circulates in the delay and reverb lines
// for good. The range is wide of anything played: the generator's lowest
// note is its pulse, a few semitones under note 0, and its highest a lifted
// lead near 140.
const (
	minNote = -48
	maxNote = 180
)

func clampNote(n int) int { return max(minNote, min(n, maxNote)) }

// maxPluckLine is the longest Karplus-Strong line, in samples: a string at
// 16 Hz, just under note 12. The line is a period long, so without a bound a
// note far enough down asked for megabytes, and further down for a slice too
// long to make, which panicked. No built-in plucks below note 12.
const maxPluckLine = SampleRate / 16

// pluckTuning sizes a string for freq: a line of n samples, and the
// coefficient c of the one-pole allpass in its loop. Averaging each sample
// with the next makes the loop the line less half a sample, so a line of
// whole samples alone plays sharp, by tens of cents and up to most of a
// tone in the top octaves, and rounds a detune of a few cents away. With the
// allpass's delay d the loop is n - 1/2 + d, so n is the period's whole
// samples and d = period - n + 1/2.
//
// d stays in 1/2..3/2, away from 0 as Jaffe and Smith advise: near 0 the
// allpass's pole nears the unit circle and it rings on each strike. c is the
// exact coefficient for delay d at the fundamental, not the low-frequency
// (1-d)/(1+d): the two differ by several cents in the top octaves, where a
// period is a dozen samples. A period is held to at least 4 samples (a note
// near 11 kHz), above which c would near -1 and the loop ring forever; it is
// bounded as a float, since converting a period too long for an int gives a
// different answer on each architecture.
func pluckTuning(freq float64) (n int, c float64) {
	period := max(4, min(SampleRate/freq, maxPluckLine))
	n = int(period)
	d := period - float64(n) + 0.5
	return n, allpassCoef(d, 2*math.Pi/period)
}

// allpassCoef is the coefficient of a one-pole allpass that delays a sine of
// w radians a sample by d samples.
func allpassCoef(d, w float64) float64 {
	return math.Sin(w*(1-d)/2) / math.Sin(w*(1+d)/2)
}

// resonatorTuning sizes a comb or room's loop for freq: a line of n whole samples,
// then an allpass of coefficient c for the fraction, as a string's is
// (pluckTuning). The loop's damping lowpass, coefficient damp, also delays
// the fundamental, by up to a sample or two at the usual damps, so the line
// is that much shorter and the ring stays in tune however damped. The
// allpass's delay is kept in 1/2..3/2 for the reason a string's is. Past
// what the line can give (a damp near 1 on a short period, whose lowpass
// delays more than the period), the loop is as short as it goes and plays
// flat; a note that high, that damped, has little left to hear.
func resonatorTuning(freq, damp float64) (n int, c float64) {
	period := max(4, min(SampleRate/freq, maxPluckLine))
	w := 2 * math.Pi / period
	lag := math.Atan2(damp*math.Sin(w), 1-damp*math.Cos(w)) / w
	line := max(1.5, period-lag)
	n = int(line - 0.5)
	return n, allpassCoef(line-float64(n), w)
}

// fadeFrames is how long a gain glide takes: about 5 ms, short enough to
// sound like a switch and long enough not to click.
const fadeFrames = 220

func (v *Voice) glideGain(to float64) {
	v.gainTo = to
	v.gainStep = (to - v.gain) / fadeFrames
}

// stepGain moves gain one sample toward gainTo, and starts a pending note
// once the old one is silent.
func (v *Voice) stepGain() {
	if math.Abs(v.gainTo-v.gain) <= math.Abs(v.gainStep) {
		v.gain = v.gainTo
	} else {
		v.gain += v.gainStep
	}
	if v.gain == 0 && v.pend.note != 0 {
		v.firePend()
	}
}

// firePend starts the pending note from silence: the old note has faded out
// or died, so nothing of it carries over.
func (v *Voice) firePend() {
	tr := v.pend
	v.active = false
	v.trigger(tr.patch, tr.note, v.pendRows, tr.gain, v.pendSeed)
	v.gain = 0
	v.glideGain(tr.gain)
}

// retrigger starts tr on v without a click: a sounding voice fades out
// first, and the new note fades in. A voice already below -60 dB, such as a
// release tail, has nothing left to click, so the note starts at once.
func (v *Voice) retrigger(tr trackRow, gateRows int, seed uint64) {
	if !v.active || v.level() < 1e-3 {
		v.trigger(tr.patch, tr.note, gateRows, tr.gain, seed)
		return
	}
	v.pend, v.pendRows, v.pendSeed = tr, gateRows, seed
	v.glideGain(0)
}

// samePatch reports whether a and b are the same patch value. Patches are
// package-level tables, so identity is the first unit's address.
func samePatch(a, b Patch) bool {
	return len(a) == len(b) && len(a) > 0 && &a[0] == &b[0]
}

// run executes the patch for one sample, accumulating into l/r.
func (v *Voice) run(l, r *float64, b *sends) {
	if v.grit != v.gritTo || v.open != v.openTo {
		v.glideKnobs()
	}
	if v.gain != v.gainTo {
		v.stepGain()
	}
	var stack [8]float64
	sp := 0
	alive := false
	bend := 0.0 // semitones from the last pitch unit, for the oscs after it
	for i := range v.patch {
		u := &v.patch[i]
		s := &v.units[i]
		switch u.op {
		case opEnv:
			switch s.stage {
			case 0:
				s.env += 1 / (u.p[0]*SampleRate + 1)
				if s.env >= 1 {
					s.env, s.stage = 1, 1
				}
			case 1:
				s.env += (u.p[2] - s.env) / (u.p[1] * SampleRate)
				if s.env-u.p[2] < 1e-3 {
					s.stage = 2
				}
			case 3:
				s.env -= s.env / (u.p[3] * SampleRate)
			}
			if !v.gate && s.stage < 3 {
				s.stage = 3
			}
			if s.stage < 3 || s.env > 1e-4 {
				alive = true
			}
			stack[sp] = s.env
			sp++
		case opOsc:
			if int(u.p[1]) == shpNoise {
				// noise has no pitch to work out
				stack[sp] = s.noise()
				sp++
				break
			}
			var inc float64
			if u.p[2] == 0 {
				// no sweep: pitch is fixed for the note's life, so pow runs once
				if !s.kset {
					s.k = noteInc(float64(v.note) + u.p[0])
					s.kset = true
				}
				inc = s.k
			} else if u.p[3] < minLineSweep {
				// too quick a sweep for the line below to follow: a shot
				// spec may ask for one that is over within a few samples
				inc = noteInc(float64(v.note) + u.p[0] + u.p[2]*s.sweep)
				s.sweep -= s.sweep / (u.p[3]*SampleRate + 1)
			} else {
				// A sweep's pitch is worked out every 16th sample and runs
				// in a straight line between, sparing a pow every sample
				// (the kick, the pulse and most one-shots sweep).
				// TestSweepFollowsItsCurve holds the line to the curve;
				// s.sweep is the sweep at the end of the line, and falls
				// by the same factor each sample as it did when it was
				// stepped each sample.
				if s.tick == 0 {
					base := float64(v.note) + u.p[0]
					if !s.kset {
						s.k = noteInc(base + u.p[2]*s.sweep)
						s.kset = true
					}
					r := 1 - 1/(u.p[3]*SampleRate+1)
					r *= r
					r *= r
					r *= r
					r *= r // 16 samples on
					s.sweep *= r
					s.dk = (noteInc(base+u.p[2]*s.sweep) - s.k) / 16
				}
				s.tick = (s.tick + 1) & 15
				inc = s.k
				s.k += s.dk
			}
			if bend != 0 {
				// exp2 every 16th sample, as a swept svf does: vibrato
				// and drift are slow enough not to step audibly
				if s.kpos == 0 {
					s.g1 = math.Exp2(bend / 12)
				}
				s.kpos = (s.kpos + 1) & 15
				inc *= s.g1
			}
			s.phase += inc
			s.phase -= math.Floor(s.phase)
			var x float64
			switch int(u.p[1]) {
			case shpSine:
				x = sine(s.phase)
			case shpSaw:
				x = 2*s.phase - 1
			case shpPulse:
				if s.phase < 0.5 {
					x = 1
				} else {
					x = -1
				}
			}
			stack[sp] = x
			sp++
		case opMul:
			sp--
			stack[sp-1] *= stack[sp]
		case opLpf:
			if !s.kset {
				s.k = 1 - math.Exp(-2*math.Pi*u.p[0]/SampleRate)
				s.kset = true
			}
			c := s.k
			s.lp += c * (stack[sp-1] - s.lp)
			stack[sp-1] = s.lp
		case opHpf:
			// one pole: what the lowpass leaves out. Drums need it, because
			// noise lowpassed is a hiss and noise highpassed is a shaker.
			if !s.kset {
				s.k = 1 - math.Exp(-2*math.Pi*u.p[0]/SampleRate)
				s.kset = true
			}
			s.lp += s.k * (stack[sp-1] - s.lp)
			stack[sp-1] -= s.lp
		case opSvf:
			// A state-variable filter in the trapezoidal form (Zavalishin,
			// Simper), which stays stable while its cutoff moves every
			// sample. A nonzero depth takes a modulation value from the
			// top of the stack and moves the cutoff by that many octaves
			// per unit, so an env or lfo pushed before it sweeps it.
			fc := u.p[0]
			if u.p[3] != 0 {
				// tan and exp2 every 16th sample (0.36 ms) halve a swept
				// filter's cost; a sweep that fast is still smooth
				sp--
				if s.kpos == 0 {
					fc *= math.Exp2(stack[sp]*u.p[3] + 2*v.open)
					fc = max(20, min(fc, 0.45*SampleRate))
					s.k = math.Tan(math.Pi * fc / SampleRate)
				}
				s.kpos = (s.kpos + 1) & 15
			} else if !s.kset || s.ko != v.open {
				// open moves the cutoff up to two octaves either way
				fc *= math.Exp2(2 * v.open)
				s.k = math.Tan(math.Pi * max(20, min(fc, 0.45*SampleRate)) / SampleRate)
				s.kset, s.ko = true, v.open
			}
			// resonance 1 leaves k at 0.2, a Q of 5: it rings and whistles
			// but never self-oscillates, so a voice still dies with its env,
			// and the lowpass peak at the cutoff stays within 14 dB
			if !s.gset || s.g != v.grit {
				res := u.p[1] + (1-u.p[1])*0.6*v.grit
				s.g, s.g1, s.gset = v.grit, 2-1.8*res, true
			}
			g, k := s.k, s.g1
			a1 := 1 / (1 + g*(g+k))
			a2 := g * a1
			x := stack[sp-1]
			v3 := x - s.ic2
			v1 := a1*s.lp + a2*v3
			v2 := s.ic2 + a2*s.lp + g*a2*v3
			s.lp, s.ic2 = flush(2*v1-s.lp), flush(2*v2-s.ic2)
			switch int(u.p[2]) {
			case 0:
				stack[sp-1] = v2
			case 1:
				// scaled by k for unity gain at the cutoff, whatever
				// the resonance: narrower, not louder
				stack[sp-1] = k * v1
			default:
				stack[sp-1] = x - k*v1 - v2
			}
		case opLfo:
			var x float64
			switch int(u.p[1]) {
			case 0:
				x = sine(s.phase)
			case 1:
				x = 1 - 4*math.Abs(s.phase-0.5)
			default:
				x = s.lp
			}
			s.phase += u.p[0] / SampleRate
			if s.phase >= 1 {
				s.phase -= 1
				s.lp = s.noise()
			}
			stack[sp] = x
			sp++
		case opDrive:
			// Grit up to triples the drive, scaled back so a full-scale input
			// peaks where it did at grit 0: grit squares the wave off rather
			// than raising its peak. The bias makes the curve lopsided, for
			// the even harmonics of an overdriven amp, and is subtracted
			// back out so silence stays silent.
			if !s.gset || s.g != v.grit {
				k := u.p[0] * (1 + 2*v.grit)
				s.g, s.g1, s.g2, s.gset = v.grit, k, math.Tanh(u.p[0])/math.Tanh(k), true
			}
			stack[sp-1] = (math.Tanh(s.g1*stack[sp-1]+u.p[1]) - math.Tanh(u.p[1])) * s.g2
		case opCrush:
			// Sample-rate reduction then bit depth, both computed only when
			// a new sample is held. Grit takes off up to four bits and
			// lowers the hold rate by up to an octave and a half.
			if !s.kset { // a new note holds its first sample at once
				s.phase, s.kset = 1, true
			}
			if !s.gset || s.g != v.grit {
				s.g, s.gset = v.grit, true
				s.g1 = u.p[0] * math.Exp2(-1.5*v.grit) / SampleRate
				s.g2 = math.Exp2(max(1, u.p[1]-4*v.grit) - 1)
			}
			s.phase += s.g1
			if s.phase >= 1 {
				s.phase -= math.Floor(s.phase)
				s.lp = math.Round(stack[sp-1]*s.g2) / s.g2
			}
			stack[sp-1] = s.lp
		case opScale:
			// an lfo's -1..1 made into a range: gain 0.3, offset 0.7 swings
			// 0.4..1, a tremolo once multiplied into a signal
			stack[sp-1] = stack[sp-1]*u.p[0] + u.p[1]
		case opPitch:
			// clamped: chained scales can grow a modulator past what
			// validation sees, and exp2 of a huge bend would overflow
			sp--
			bend = max(-24, min(stack[sp]*u.p[0], 24))
		case opPluck, opPluckRaw:
			// Karplus-Strong: a noise burst circulating in a delay line
			// the length of the period, averaged (lowpassed) each pass,
			// with an allpass making up the period's fraction of a sample
			// (pluckTuning).
			//
			// pluckraw is the string as undertone played it to v0.2.0, the
			// original Karplus-Strong: a line of whole samples and no
			// allpass, so it plays sharp, by a few cents low down and most
			// of a tone in the top octave. Kept so a sound made with it can
			// still be had; TestPluckRawIsV020 holds it to that code.
			if len(s.ks) == 0 { // a new note: strike the string
				freq := 440 * math.Pow(2, (float64(v.note)+u.p[0]-69)/12)
				var n int
				if u.op == opPluckRaw {
					// bounded as a float: converting a period too long for
					// an int gives a different answer on each architecture
					n = int(max(2, min(SampleRate/freq, maxPluckLine)))
				} else {
					n, s.k = pluckTuning(freq)
				}
				// lp is the allpass's state: a legato retrigger keeps lp,
				// and a new string starts from rest
				s.lp = 0
				// The voice's line if it has one free, else the unit's own.
				// One it has to make takes room for the longest, so no
				// later note makes another: sized to its own note, a lead
				// allocated again each time it walked lower than it had.
				switch {
				case v.line != nil && !v.lineOut:
					s.ks, v.lineOut = v.line[:n], true
				case cap(s.ks) >= n:
					s.ks = s.ks[:n]
				default:
					s.ks = make([]float64, n, maxPluckLine)
				}
				for i := range s.ks {
					s.ks[i] = s.noise()
				}
			}
			x := s.ks[s.kpos]
			next := s.ks[(s.kpos+1)%len(s.ks)]
			a := (x + next) * 0.5 * u.p[1]
			if u.op == opPluck {
				// one-pole allpass, transposed direct form II: a delay of a
				// fraction of a sample at the fundamental, and gain 1 at every
				// frequency, so the string decays as it would without it
				y := a
				a = s.k*y + s.lp
				s.lp = flush(y - s.k*a) // recursive, so kept out of subnormals as the svf's state is
			}
			s.ks[s.kpos] = a
			s.kpos = (s.kpos + 1) % len(s.ks)
			stack[sp] = x
			sp++
		case opDust:
			// Each sample fires with probability rate/SampleRate, so the
			// impulses average rate a second with Poisson spacing: irregular,
			// never a pulse. depth lets the music's grit make it up to three
			// times as busy; one-shots play at grit 0, so it is a bed's knob.
			x := 0.0
			if s.u01() < u.p[0]*(1+2*u.p[2]*v.grit)/SampleRate {
				x = s.u01()
				if u.p[1] != 0 && s.u01() < 0.5 {
					x = -x
				}
			}
			stack[sp] = x
			sp++
		case opComb, opRoom:
			// A feedback comb: the line a period long, fed back through a
			// one-pole lowpass so high partials die first, as in a tube or
			// a room, with an allpass for the period's fraction of a sample
			// (resonatorTuning). The period is fixed for the note's life.
			if len(s.ks) == 0 {
				freq := u.p[0]
				if u.op == opComb {
					freq = 440 * math.Pow(2, (float64(v.note)+u.p[0]-69)/12)
				}
				var n int
				n, s.k = resonatorTuning(freq, u.p[2])
				// The voice's line if free, else the unit's own, as a
				// string takes one. A new note starts silent; a legato one
				// keeps what the line held, retuned, so the ring goes on.
				switch {
				case v.line != nil && !v.lineOut:
					s.ks, v.lineOut = v.line[:n], true
				case cap(s.ks) >= n:
					s.ks = s.ks[:n]
				default:
					s.ks = make([]float64, n, maxPluckLine)
					s.held = false // a fresh line has nothing to carry
				}
				if s.held {
					// a lower note lengthens the line: what lies past the
					// old length is an earlier note's, or another unit's
					if n > s.tick {
						clear(s.ks[s.tick:])
					}
					s.kpos %= n
				} else {
					clear(s.ks)
					s.lp, s.ic2, s.kpos, s.env = 0, 0, 0, 0
				}
				s.tick = n
			}
			r := s.ks[s.kpos]
			y := s.k*r + s.lp // allpass, as pluck's
			s.lp = flush(r - s.k*y)
			s.ic2 = flush(s.ic2 + (1-u.p[2])*(y-s.ic2))
			s.ks[s.kpos] = flush(stack[sp-1] + u.p[1]*s.ic2)
			s.kpos = (s.kpos + 1) % len(s.ks)
			stack[sp-1] = y
			// The ring outlives the env that struck it: at feedback 0.99 a
			// low one takes seconds to fall 60 dB. Cutting it when the env
			// is spent clicked, so a ring whose input has died with an env
			// keeps the voice while it is heard. Its level falls by e a
			// period between peaks, so it doesn't drop out between them.
			s.env = max(math.Abs(y), s.env*(1-1/float64(len(s.ks))))
			if u.p[3] != 0 && s.env > 1e-4 {
				alive = true
			}
		case opSend:
			b.delay += stack[sp-1] * u.p[0] * v.gain
		case opVerb:
			b.verb += stack[sp-1] * u.p[0] * v.gain
		case opOut:
			sp--
			*l += stack[sp] * u.p[0] * (1 - u.p[1]) * v.gain
			*r += stack[sp] * u.p[0] * u.p[1] * v.gain
		}
	}
	v.active = alive
	if !alive && v.pend.note != 0 {
		v.firePend() // the old note died before its fade finished
	}
}

// ---- instruments (compact opcode tables, sointu-patch style) ----
// Two env→osc→out chains in one patch = two detuned layers; opOut
// accumulates, so layering needs no add opcode.

var drone = Patch{
	{opEnv, [4]float64{2.5, 2, 1, 4}},
	{opOsc, [4]float64{-0.06, shpSaw, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{280, 0, 0, 0}},
	{opOut, [4]float64{0.5, 0.42}},
	{opEnv, [4]float64{2.5, 2, 1, 4}},
	{opOsc, [4]float64{0.06, shpSaw, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{280, 0, 0, 0}},
	{opOut, [4]float64{0.5, 0.58}},
}

var pad = Patch{
	{opEnv, [4]float64{1.2, 1.5, 0.6, 1.8}},
	{opOsc, [4]float64{-0.04, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.5, 0.35}},
	{opEnv, [4]float64{1.4, 1.5, 0.6, 1.8}},
	{opOsc, [4]float64{0.04, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.5, 0.65}},
}

var bell = Patch{
	{opEnv, [4]float64{0.003, 1.4, 0, 0.5}},
	{opOsc, [4]float64{0, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opSend, [4]float64{0.7, 0, 0, 0}},
	{opOut, [4]float64{0.5, 0.6}},
}

// ---- lead instruments (the lead track) ----
// Different species, same opcodes: inharmonic partials read as metal,
// ring-mod as clang, filtered pulse as wood/breath.

// chime: three sine partials at non-integer ratios (1, ~2.29, ~3.01)
var chime = Patch{
	{opEnv, [4]float64{0.002, 1.6, 0, 0.6}},
	{opOsc, [4]float64{0, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opSend, [4]float64{0.6, 0, 0, 0}},
	{opOut, [4]float64{0.35, 0.6}},
	{opEnv, [4]float64{0.002, 1.1, 0, 0.5}},
	{opOsc, [4]float64{14.35, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.18, 0.4}},
	{opEnv, [4]float64{0.002, 0.7, 0, 0.4}},
	{opOsc, [4]float64{19.08, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.1, 0.65}},
}

// clang: ring-modulated sine pair (env x osc x osc) — eerie metal
var clang = Patch{
	{opEnv, [4]float64{0.002, 1.2, 0, 0.5}},
	{opOsc, [4]float64{0, shpSine, 0, 1}},
	{opOsc, [4]float64{16.9, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opMul, [4]float64{}},
	{opSend, [4]float64{0.5, 0, 0, 0}},
	{opOut, [4]float64{0.4, 0.55}},
}

// kalimba: struck sine with a brief bright partial — woody
var kalimba = Patch{
	{opEnv, [4]float64{0.001, 0.35, 0, 0.15}},
	{opOsc, [4]float64{0, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opSend, [4]float64{0.4, 0, 0, 0}},
	{opOut, [4]float64{0.45, 0.55}},
	{opEnv, [4]float64{0.001, 0.08, 0, 0.05}},
	{opOsc, [4]float64{19, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.2, 0.45}},
}

// pluck: Karplus-Strong string — harp/lute, the sound the osc set can't fake
var pluck = Patch{
	{opEnv, [4]float64{0.001, 2.5, 0, 0.4}},
	{opPluck, [4]float64{0, 0.996, 0, 0}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{2600, 0, 0, 0}},
	{opSend, [4]float64{0.45, 0, 0, 0}},
	{opOut, [4]float64{0.55, 0.55}},
}

// reed: filtered pulse, slow attack, sustained — breath, needs a longer gate
var reed = Patch{
	{opEnv, [4]float64{0.15, 0.5, 0.5, 0.25}},
	{opOsc, [4]float64{0, shpPulse, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{900, 0, 0, 0}},
	{opSend, [4]float64{0.3, 0, 0, 0}},
	{opOut, [4]float64{0.22, 0.5}},
}

// aurora pad: the pad's sine pair, swelling slowly and fading long, with a
// send so the delay bus smears it into a sky-wide wash
var veil = Patch{
	{opEnv, [4]float64{3, 2, 0.7, 4}},
	{opOsc, [4]float64{-0.07, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opSend, [4]float64{0.2, 0, 0, 0}},
	{opOut, [4]float64{0.25, 0.3}},
	{opEnv, [4]float64{3.4, 2, 0.7, 4}},
	{opOsc, [4]float64{0.07, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opSend, [4]float64{0.2, 0, 0, 0}},
	{opOut, [4]float64{0.25, 0.7}},
}

// overworld drone: sine pair — a bed, not a weight
var bed = Patch{
	{opEnv, [4]float64{3, 2, 1, 4}},
	{opOsc, [4]float64{-0.05, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.6, 0.45}},
	{opEnv, [4]float64{3, 2, 1, 4}},
	{opOsc, [4]float64{0.05, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.6, 0.55}},
}

// overworld air: same shape as air, opened up — wind, not rumble
var breeze = Patch{
	{opEnv, [4]float64{2, 2, 0.5, 3}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{1400, 0, 0, 0}},
	{opLpf, [4]float64{1400, 0, 0, 0}},
	{opOut, [4]float64{0.9, 0.5}},
}

// ---- the void palette (below the dungeon) ----
// The dungeon's shapes taken lower and darker: lowpasses an octave or more
// down, slower swells, and a reverb send on the bed, so a mood's room
// (a large, dark one) has something to ring.

// abyss: the drone's detuned saws through two lowpass stages at 140 Hz,
// where the drone has one at 280, so only the fundamental and a hint of the
// next partial get through
var abyss = Patch{
	{opEnv, [4]float64{4, 2, 1, 6}},
	{opOsc, [4]float64{-0.06, shpSaw, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{140, 0, 0, 0}},
	{opLpf, [4]float64{140, 0, 0, 0}},
	{opVerb, [4]float64{0.15}},
	{opOut, [4]float64{0.4, 0.42}},
	{opEnv, [4]float64{4, 2, 1, 6}},
	{opOsc, [4]float64{0.06, shpSaw, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{140, 0, 0, 0}},
	{opLpf, [4]float64{140, 0, 0, 0}},
	{opVerb, [4]float64{0.15}},
	{opOut, [4]float64{0.4, 0.58}},
}

// murk: the pad's sine pair an octave down, swelling as slowly as the veil
// and sent to the reverb instead of the delay
var murk = Patch{
	{opEnv, [4]float64{3, 2, 0.7, 4}},
	{opOsc, [4]float64{-12.05, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opVerb, [4]float64{0.3}},
	{opOut, [4]float64{0.2, 0.35}},
	{opEnv, [4]float64{3.4, 2, 0.7, 4}},
	{opOsc, [4]float64{-11.95, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opVerb, [4]float64{0.3}},
	{opOut, [4]float64{0.2, 0.65}},
}

// rumble: the air's noise under two lowpasses at 180 Hz, a pressure more
// than a sound, raised to make up the level the lower cutoff takes away
var rumble = Patch{
	{opEnv, [4]float64{3, 3, 0.4, 4.5}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{180, 0, 0, 0}},
	{opLpf, [4]float64{180, 0, 0, 0}},
	{opVerb, [4]float64{0.25}},
	{opOut, [4]float64{2.5, 0.5}},
}

// ---- the stratosphere's patch ----

// haze: the veil thinned to a single sine with a reverb send beside its
// delay send, so the mood's bright room carries the wash
var haze = Patch{
	{opEnv, [4]float64{3.5, 2, 0.6, 5}},
	{opOsc, [4]float64{0, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opSend, [4]float64{0.25, 0, 0, 0}},
	{opVerb, [4]float64{0.4}},
	{opOut, [4]float64{0.3, 0.5}},
}

// ---- the drum kit (rhythmic moods) ----
// Each drum is played at a fixed note on its own track, so its trim is
// baked into its output gain rather than kept in a table like leadTrim.
// Balanced solo (2026-10-11): the clap within 4 dB of the kick while
// sounding, the shaker about 9 dB under, as hats sit. TestKitBalance holds
// looser bounds (6 dB, and 6 to 18 dB under) so retuning a patch has room;
// re-measure and redate this when a patch changes.

// kick: the pulse's swept sine, falling fast from about 300 Hz to its note
// (play it near 62 Hz, which leaves the sub to a game's own impacts), plus a
// few milliseconds of highpassed noise so it still reads on a phone speaker,
// which cannot play the thump
var kick = Patch{
	{opEnv, [4]float64{0.001, 0.22, 0, 0.08}},
	{opOsc, [4]float64{0, shpSine, 28, 0.02}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.9, 0.5}},
	{opEnv, [4]float64{0.0005, 0.005, 0, 0.004}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opHpf, [4]float64{2000, 0, 0, 0}},
	{opOut, [4]float64{0.5, 0.5}},
}

// clap: three or four quick re-hits (noise under a squared 50 Hz sine, which
// peaks every 10 ms and falls silent between) for the first 30 ms, then a
// noise tail and a short swept sine that both swell in after them.
// Closer to a frame drum than a backbeat snare.
var clap = Patch{
	{opEnv, [4]float64{0.001, 0.025, 0, 0.01}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opOsc, [4]float64{-21.65, shpSine, 0, 1}}, // 50 Hz from the clap's note
	{opMul, [4]float64{}},
	{opOsc, [4]float64{-21.65, shpSine, 0, 1}}, // twice: sin² keeps the bursts apart
	{opMul, [4]float64{}},
	{opHpf, [4]float64{900, 0, 0, 0}},
	{opOut, [4]float64{1.1, 0.42}},
	{opEnv, [4]float64{0.05, 0.16, 0, 0.08}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opHpf, [4]float64{1000, 0, 0, 0}},
	{opLpf, [4]float64{6000, 0, 0, 0}},
	{opSend, [4]float64{0.15, 0, 0, 0}},
	{opOut, [4]float64{0.6, 0.42}},
	{opEnv, [4]float64{0.04, 0.07, 0, 0.04}},
	{opOsc, [4]float64{0, shpSine, 7, 0.02}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.45, 0.45}},
}

// shaker: noise band-passed to about 4-8 kHz, 24 dB/octave on the top side
// (four one-pole lowpasses). Highpassing alone is not enough: white noise puts most of its energy in the top octaves, so without
// the lowpasses it is a hiss centred near 14 kHz.
var shaker = Patch{
	{opEnv, [4]float64{0.004, 0.035, 0, 0.025}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opHpf, [4]float64{4000, 0, 0, 0}},
	{opHpf, [4]float64{4000, 0, 0, 0}},
	{opLpf, [4]float64{8000, 0, 0, 0}},
	{opLpf, [4]float64{8000, 0, 0, 0}},
	{opLpf, [4]float64{8000, 0, 0, 0}},
	{opLpf, [4]float64{8000, 0, 0, 0}},
	{opOut, [4]float64{1.7, 0.62}},
}

// tension pulse: a soft swept-sine tom, descendant of the techno kick
var pulse = Patch{
	{opEnv, [4]float64{0.002, 0.15, 0, 0.08}},
	{opOsc, [4]float64{0, shpSine, 18, 0.05}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.7, 0.5}},
}

var air = Patch{
	{opEnv, [4]float64{2.5, 2.5, 0.4, 3.5}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	// two one-pole stages: a single pole leaks enough top end that the
	// air track *brightened* with depth instead of darkening
	{opLpf, [4]float64{300, 0, 0, 0}},
	{opLpf, [4]float64{300, 0, 0, 0}},
	{opOut, [4]float64{1.1, 0.5}},
}

// ---- the rust palette (industrial) ----
// Resonant filters that move within a note, so the bed breathes and grinds
// instead of sitting still, and drive and crush for grit to work on: as
// tension rises the saws overdrive, the hiss degrades and the pulse snarls.
// Everything sends to the reverb: a large, dark room around the whole bed.

// rust: detuned saws through a resonant lowpass, each side swept by its own
// slow lfo at a different rate, so the pair drifts in and out of phase
var rust = Patch{
	{opEnv, [4]float64{3, 2, 1, 4}},
	{opOsc, [4]float64{-0.08, shpSaw, 0, 1}},
	{opMul, [4]float64{}},
	{opLfo, [4]float64{0.07, 0}},
	{opSvf, [4]float64{260, 0.75, 0, 1.2}},
	{opDrive, [4]float64{1.5, 0.1}},
	{opSend, [4]float64{0.15, 0, 0, 0}},
	{opVerb, [4]float64{0.35}},
	{opOut, [4]float64{0.45, 0.35}},
	{opEnv, [4]float64{3, 2, 1, 4}},
	{opOsc, [4]float64{0.08, shpSaw, 0, 1}},
	{opMul, [4]float64{}},
	{opLfo, [4]float64{0.05, 1}},
	{opSvf, [4]float64{260, 0.75, 0, 1.2}},
	{opDrive, [4]float64{1.5, 0.1}},
	{opSend, [4]float64{0.15, 0, 0, 0}},
	{opVerb, [4]float64{0.35}},
	{opOut, [4]float64{0.45, 0.65}},
}

// smear: a pulse pad in a wandering bandpass, more vowel than chord
var smear = Patch{
	{opEnv, [4]float64{1.5, 2, 0.5, 2.5}},
	{opOsc, [4]float64{0, shpPulse, 0, 1}},
	{opMul, [4]float64{}},
	{opLfo, [4]float64{0.2, 1}},
	{opSvf, [4]float64{700, 0.6, 1, 1}},
	{opSend, [4]float64{0.35, 0, 0, 0}},
	{opVerb, [4]float64{0.5}},
	{opOut, [4]float64{0.5, 0.5}},
}

// hiss: noise as an instrument, a tape-hiss bed whose band jumps somewhere
// new a couple of times a second
var hiss = Patch{
	{opEnv, [4]float64{2, 2.5, 0.5, 3.5}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLfo, [4]float64{2.3, 2}},
	{opSvf, [4]float64{1500, 0.7, 1, 1.3}},
	{opCrush, [4]float64{11025, 10}},
	{opSend, [4]float64{0.4, 0, 0, 0}},
	{opVerb, [4]float64{0.4}},
	{opOut, [4]float64{0.55, 0.5}},
}

// grind: the tension pulse with teeth, a saw whose resonant lowpass snaps
// open on each hit and closes as it decays: a filter envelope
var grind = Patch{
	{opEnv, [4]float64{0.002, 0.18, 0, 0.08}},
	{opOsc, [4]float64{0, shpSaw, 0, 1}},
	{opMul, [4]float64{}},
	{opEnv, [4]float64{0.001, 0.09, 0, 0.05}},
	{opSvf, [4]float64{90, 0.8, 0, 4}},
	{opDrive, [4]float64{2, 0.2}},
	{opVerb, [4]float64{0.2}},
	// 0.6, not 0.8: on rust's downbeats it lands with the kick and the
	// drone, and at 0.8 the three peaked over -2 dBFS (#73)
	{opOut, [4]float64{0.6, 0.5}},
}

// ---- the industrial kit (rust's drums) ----
// The kick, backbeat and top rebuilt from the grit ops, so tension, which
// brings them in, also roughens them.

// slam: the kick's falling sine overdriven into a square-ish punch
var slam = Patch{
	{opEnv, [4]float64{0.001, 0.26, 0, 0.1}},
	{opOsc, [4]float64{0, shpSine, 30, 0.03}},
	{opMul, [4]float64{}},
	{opDrive, [4]float64{3, 0.15}},
	{opOut, [4]float64{0.75, 0.5}},
	{opEnv, [4]float64{0.0005, 0.006, 0, 0.004}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opHpf, [4]float64{2500, 0, 0, 0}},
	{opOut, [4]float64{0.5, 0.5}},
}

// clank: a snare of crushed noise through a ringing bandpass, over a
// ring-modulated body that sounds struck rather than skinned
var clank = Patch{
	{opEnv, [4]float64{0.001, 0.14, 0, 0.08}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opSvf, [4]float64{1900, 0.55, 1, 0}},
	{opCrush, [4]float64{9000, 7}},
	{opVerb, [4]float64{0.3}},
	{opOut, [4]float64{1.2, 0.45}},
	{opEnv, [4]float64{0.001, 0.07, 0, 0.04}},
	{opOsc, [4]float64{0, shpSine, 0, 1}},
	{opOsc, [4]float64{13.7, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.45, 0.55}},
}

// tick: a metallic hat, two pulse waves ring-modulated at an inharmonic
// ratio and highpassed, very short
var tick = Patch{
	{opEnv, [4]float64{0.001, 0.035, 0, 0.02}},
	{opOsc, [4]float64{0, shpPulse, 0, 1}},
	{opOsc, [4]float64{11.6, shpPulse, 0, 1}},
	{opMul, [4]float64{}},
	{opMul, [4]float64{}},
	{opHpf, [4]float64{6000, 0, 0, 0}},
	{opHpf, [4]float64{6000, 0, 0, 0}},
	{opOut, [4]float64{0.9, 0.6}},
}

var industrialKit = kit{{slam, 33}, {clank, 50}, {tick, 84}}

// ---- parameters: the (seed, depth) -> music map ----
// The kaiju-elements trick: taste lives in hand-tuned option tables,
// the seed just picks. Depth darkens the pick.

type Params struct {
	BPM         float64
	Root        int   // MIDI, chord anchor
	Scale       []int // semitone intervals
	DroneGain   float64
	FifthGain   float64 // second drone at the fifth; thickens with depth
	PadDensity  float64
	LeadDensity float64
	AirGain     float64
	PulseGain   float64 // tension pulse track level (0 = silent)
	FifthOffset int     // second drone's interval from the chord, usually below; slides dissonant with tension
	LeadLift    int     // semitones added to lead notes (overworld sparkle register)
	LeadName    string  // which lead plays on the lead track
	LeadGate    int     // gate rows for the lead (sustained leads want > 1)
	Palette     Palette // the mood's patch bank
	Seed        uint64

	// Drums (drums.go); a mood without a groove leaves them off.
	Groove   string  // a grooveLib name; "" plays no drums
	DrumGain float64 // scales every drum hit
	DrumBusy float64 // 0..1: chance each ? step in the groove plays
	Swing    float64 // 0..0.25: how far past halfway the off 16th lands

	// Grit roughens the patches built to take it, 0..1: more drive, fewer
	// bits, a lower hold rate, more resonance. Moods derive it from
	// tension; patches without those units ignore it.
	Grit float64

	// Open moves the svf filters' cutoffs, -1..1: up to two octaves up or
	// down. Moods derive it from level, so a mood can open or close as a game
	// goes deeper; patches without an svf ignore it.
	Open float64

	// Room shapes the reverb that the whole mix sends to; the zero Room is
	// the long, dark room every mood had before rooms were a choice.
	Room Room

	// Mute silences layers at the mix, for listening to a mood a layer at a
	// time. A muted layer still plays, unheard, so the patterns and the rng
	// draws are those of the full mix; zeroing a density or gain instead would
	// change what every other layer plays.
	Mute Layers

	// LeadDB, PadDB and BassDB trim the lead, the pad and a theme's grid bass,
	// in dB, to balance a mix without re-authoring a patch. They are offsets,
	// not gains, so that 0, the value of every caller that never sets one,
	// plays exactly as before. They scale each note as it is struck rather
	// than a voice at the mix, because the grid bass plays on the fifth's
	// voice; SetParametersNow fades notes already sounding to the new level.
	LeadDB, PadDB, BassDB float64

	// MoodLeadDB and MoodPadDB are the mood's own trims, which a derive sets.
	// They add to LeadDB and PadDB rather than sharing them, so a mood's
	// balance and a game's mix on top are separate: a game that turns the
	// lead down keeps each mood's correction, and a mood change replaces
	// the mood's trims without resetting the game's.
	MoodLeadDB, MoodPadDB float64

	// Phrasing and Memory, when set, turn on the phrase arc and motif memory
	// (motif.go), each without the other: a spike, off by default, so nil
	// plays as before.
	Phrasing *Phrasing
	Memory   *Memory

	// MoodPhrasing and MoodMemory are the mood's own, which a derive sets.
	// They play where Phrasing or Memory is nil, unless a host turns them
	// off with NoPhrasing and NoMemory. Like the trims, the mood's and the
	// host's are kept apart, so a mood change brings its own temperament
	// without undoing what the host turned off. Off is the zero value's
	// opposite so that a mood plays its temperament unless asked not to.
	MoodPhrasing *Phrasing
	MoodMemory   *Memory
	NoPhrasing   bool
	NoMemory     bool
}

// dbGain is a trim in dB as a gain factor; 0 dB returns exactly 1, so an
// untrimmed note's gain is the same float it always was.
func dbGain(db float64) float64 {
	if db == 0 {
		return 1
	}
	return math.Pow(10, db/20)
}

// trimGain is a game's trim and a mood's together as a gain factor, held to
// the range one trim has.
func trimGain(game, mood float64) float64 {
	return dbGain(max(-60, min(game+mood, 12)))
}

// Layers is a set of the music's layers, one bit each.
type Layers uint8

const (
	LayerDrone Layers = 1 << iota // the tracks, in track order
	LayerFifth
	LayerPad
	LayerLead
	LayerAir
	LayerPulse
	LayerDrums
)

// leadDef is one entry in the lead-instrument library.
type leadDef struct {
	name  string
	patch Patch
	gate  int
}

var leadLib = map[string]leadDef{
	"bell":    {"bell", bell, 1},
	"chime":   {"chime", chime, 1},
	"clang":   {"clang", clang, 1},
	"kalimba": {"kalimba", kalimba, 1},
	"reed":    {"reed", reed, 3},
	"pluck":   {"pluck", pluck, 1},
}

// leadTrim scales a lead voice in the generator's mix, so a quiet patch is
// not lost under the bed. Chosen by ear on the lead-level tuner (2026-10-10):
// the pluck sat about 23 dB under the bell at equal gain and is raised 10 dB.
// Every built-in lead needs an entry: one missing plays at leadGain's 1.
var leadTrim = map[string]float64{
	"bell": 1, "chime": 1, "clang": 1, "kalimba": 1, "reed": 1, "pluck": 3.162,
}

// leadGain is a lead's trim. No lead plays at 0, and a registered one at 1,
// unmeasured, its patch's out gain standing in for a trim. It reads no
// registry: it runs on the audio thread, which takes no locks.
func leadGain(name string) float64 {
	if t, ok := leadTrim[name]; ok {
		return t
	}
	if name == "" {
		return 0
	}
	return 1
}

// mood-weighted audition lists; the seed picks (kaiju trick applied to
// timbre: two seeds differ in instrument, not just notes)
var dungeonLeads = []string{"bell", "chime", "chime", "clang", "kalimba", "pluck", "pluck"}
var overworldLeads = []string{"bell", "bell", "kalimba", "kalimba", "reed", "pluck", "pluck"}
var meadowLeads = []string{"bell", "kalimba", "kalimba", "reed", "pluck", "pluck"}
var auroraLeads = []string{"chime", "chime", "bell", "bell", "kalimba"}
var villageLeads = []string{"kalimba", "kalimba", "pluck", "pluck", "reed"}
var rustLeads = []string{"clang", "clang", "pluck", "chime"}
var voidLeads = []string{"chime", "chime", "clang"}

// pickLead draws from a separate rng stream so the pattern content for a
// given seed is unchanged — only the instrument varies. A mood without leads
// leaves LeadName empty, and the lead track stays silent.
func pickLead(p *Params, names []string) {
	if len(names) == 0 {
		return
	}
	r := rng{p.Seed*0x9e3779b97f4a7c15 + 0x1eadf00d | 1}
	ApplyLead(p, names[r.n(len(names))])
}

func ApplyLead(p *Params, name string) {
	if d, ok := lookupLead(name); ok {
		p.LeadName, p.LeadGate, p.Palette.Lead = d.name, d.gate, d.patch
	}
}

// Palette is a mood's patch bank: mood = patches + a point in parameter space.
type Palette struct {
	Drone, Fifth, Pad, Lead, Air, Pulse Patch
	Kit                                 kit // zero: drumKit
}

var dungeonPalette = Palette{Drone: drone, Fifth: pad, Pad: pad, Lead: bell, Air: air, Pulse: pulse}
var overworldPalette = Palette{Drone: bed, Fifth: pad, Pad: pad, Lead: bell, Air: breeze, Pulse: pulse}
var auroraPalette = Palette{Drone: bed, Fifth: veil, Pad: veil, Lead: bell, Air: breeze, Pulse: pulse}
var rustPalette = Palette{Drone: rust, Fifth: smear, Pad: smear, Lead: bell, Air: hiss, Pulse: grind, Kit: industrialKit}
var voidPalette = Palette{Drone: abyss, Fifth: pad, Pad: murk, Lead: bell, Air: rumble, Pulse: pulse}
var stratospherePalette = Palette{Drone: bed, Fifth: haze, Pad: haze, Lead: bell, Air: breeze, Pulse: pulse}

var scalesByDarkness = [][]int{
	{0, 2, 3, 5, 7, 9, 10}, // dorian
	{0, 2, 3, 5, 7, 8, 10}, // aeolian
	{0, 1, 3, 5, 7, 8, 10}, // phrygian
}

var scalesByBrightness = [][]int{
	{0, 2, 4, 5, 7, 9, 10}, // mixolydian
	{0, 2, 4, 5, 7, 9, 11}, // major
	{0, 2, 4, 6, 7, 9, 11}, // lydian
}

var scalesForVillage = [][]int{
	{0, 2, 4, 5, 7, 9, 10}, // mixolydian
	{0, 2, 4, 5, 7, 9, 11}, // major
}

var scalesForAurora = [][]int{
	{0, 2, 4, 6, 7, 9, 11}, // lydian, all the way up
}

// Void starts where the dungeon ends and goes one mode darker.
var scalesForVoid = [][]int{
	{0, 1, 3, 5, 7, 8, 10}, // phrygian
	{0, 1, 3, 5, 6, 8, 10}, // locrian
}

var scalesForRust = [][]int{
	{0, 1, 3, 5, 7, 8, 10}, // phrygian
	{0, 1, 3, 5, 7, 8, 10}, // phrygian
	{0, 1, 3, 5, 6, 8, 10}, // locrian
}

// Pentatonic scales, for a mood to name in its scales; no built-in mood
// uses them yet.
var (
	majorPentatonic = []int{0, 2, 4, 7, 9}
	minorPentatonic = []int{0, 3, 5, 7, 10}
)

// The meadow stays major longer and turns lydian only high up: its
// brightness is register and air, not the mode.
var scalesForMeadow = [][]int{
	{0, 2, 4, 5, 7, 9, 11}, // major
	{0, 2, 4, 5, 7, 9, 11}, // major
	{0, 2, 4, 6, 7, 9, 11}, // lydian
}

// tenseOffset slides the second drone's interval toward dissonance: the
// mood's calm interval, then tritone, then minor-ninth territory.
func tenseOffset(calm int, t float64) int {
	// A voice floating above the chord (aurora) sours in place, an octave
	// up, rather than dropping more than an octave to reach the dissonance.
	up := 0
	if calm > 0 {
		up = 12
	}
	switch {
	case t >= 0.75:
		return -11 + up
	case t >= 0.4:
		return -6 + up
	}
	return calm
}

// ---- generative sequencer ----

type rng struct{ s uint64 }

func (r *rng) next() uint64 {
	r.s ^= r.s << 13
	r.s ^= r.s >> 7
	r.s ^= r.s << 17
	return r.s
}
func (r *rng) f() float64  { return float64(r.next()>>11) / (1 << 53) }
func (r *rng) n(m int) int { return int(r.next() % uint64(m)) }

const rows = 16
const nTracks = 6 // drone, fifth, pad, lead, air, pulse

type trackRow struct {
	note     int
	gain     float64
	gateRows int
	patch    Patch
}

// buildPatterns fills one pattern (16 rows x nTracks) from Params.
// Chord drifts between tonic, VI, and iv each pattern; the lead walks the
// scale — per-pattern reseeding is the gradual-mutation mechanism.
func buildPatterns(p Params, pat int) [nTracks][rows]trackRow {
	return buildPatternsOn(p, pat, -1)
}

// buildPatternsOn is buildPatterns on a chosen chord, 0..3 as the generator
// draws it, or on its own draw for -1. The draw is taken either way, so the
// rest of the pattern draws the same numbers (motif.go's chord cycles).
func buildPatternsOn(p Params, pat, chordIdx int) [nTracks][rows]trackRow {
	var out [nTracks][rows]trackRow
	r := rng{p.Seed ^ uint64(pat)*0x9e3779b97f4a7c15 | 1}
	// VI and iv are the tones two and four degrees below the root, which a
	// five-note scale also has: in major pentatonic they are V and II.
	k := r.n(4)
	if chordIdx >= 0 {
		k = chordIdx
	}
	chord := p.Root + []int{0, 0, scaleTone(p.Scale, -2), scaleTone(p.Scale, -4)}[k]

	if p.DroneGain > 0.01 { // aurora has none; don't run a silent voice
		out[0][0] = trackRow{chord - 12, p.DroneGain, rows, p.Palette.Drone}
	}
	if p.FifthGain > 0.01 {
		// pad patch (sine pair), not the saw drone: the fifth should
		// thicken with depth, not brighten
		out[1][0] = trackRow{chord + p.FifthOffset, p.FifthGain, rows, p.Palette.Fifth}
	}
	for _, row := range []int{0, 8} {
		if r.f() < p.PadDensity {
			// Every other tone: a triad in seven tones. In five it is the
			// open voicing pentatonic music uses: root, third and sixth in
			// major pentatonic, a stack of fourths in minor.
			deg := []int{0, 2, 4}[r.n(3)]
			out[2][row] = trackRow{chord + p.Scale[deg], 0.7 * trimGain(p.PadDB, p.MoodPadDB), 6, p.Palette.Pad}
		}
	}
	// The lead starts an octave above the chord and walks between the
	// chord's fourth tone and the fourth tone two octaves up, counted in the
	// scale's own tones.
	n := len(p.Scale)
	deg := n + r.n(n)
	for i := 0; i < rows; i++ {
		if r.f() < p.LeadDensity {
			deg += r.n(5) - 2
			deg = max(3, min(deg, 2*n+3))
			out[3][i] = trackRow{chord + scaleTone(p.Scale, deg) + p.LeadLift, 0.55 * leadGain(p.LeadName) * trimGain(p.LeadDB, p.MoodLeadDB), p.LeadGate, p.Palette.Lead}
		}
	}
	// Without a lead the walk is still drawn, so the pulse below draws the
	// same numbers, but nothing strikes.
	if p.LeadName == "" {
		out[3] = [rows]trackRow{}
	}
	if pat%2 == 0 {
		out[4][0] = trackRow{45, p.AirGain, 10, p.Palette.Air}
	}
	// tension pulse: heartbeat on 0/8, agitation fills as tension rises.
	// Guarded so tension 0 draws no rng and leaves the stream untouched.
	if p.PulseGain > 0.01 {
		note := chord - 24
		g := 0.35 + 0.45*p.PulseGain
		out[5][0] = trackRow{note, g, 1, p.Palette.Pulse}
		out[5][8] = trackRow{note, g, 1, p.Palette.Pulse}
		for _, row := range []int{4, 12} {
			if r.f() < p.PulseGain {
				out[5][row] = trackRow{note, g * 0.8, 1, p.Palette.Pulse}
			}
		}
		if p.PulseGain > 0.7 {
			for _, row := range []int{6, 14} {
				if r.f() < (p.PulseGain-0.7)*2 {
					out[5][row] = trackRow{note + 12, g * 0.5, 1, p.Palette.Pulse}
				}
			}
		}
	}
	return out
}

// ---- synth: io.Reader producing 16-bit LE stereo ----

type Synth struct {
	params        Params
	next          atomic.Pointer[paramCmd] // staged; applied at the next pattern boundary
	cur           atomic.Pointer[paramCmd] // params, published for other goroutines (resolveDegree)
	paramSeq      atomic.Uint64            // the last seq handed to a parameter change
	applied       uint64                   // seq of the change in params; the audio thread's own
	samplesPerRow int
	rowLeft       int
	row, pat      int
	voices        []Voice
	pats          [nTracks][rows]trackRow
	dl, dr        []float64 // delay lines
	dpos          int
	bus           sends // this frame's delay and reverb sends
	verb          *reverb
	fx            sfx // one-shot lane (sfx.go); the sequencer never sees it

	// drum lanes (drums.go), silent in moods without a groove
	drums   [nDrums][rows]drumHit
	dvoices [nDrums]Voice
	offLeft [nDrums]int // frames until each lane's off 16th; 0 = none armed
	offGain [nDrums]float64
	offSeed [nDrums]uint64
	offN    [nDrums]int // how many times the armed off 16th ratchets
	rat     [nDrums]ratchet
	// an off 16th's flam grace, armed as offLeft is; an on 16th's plays
	// from flamsAhead instead
	graceLeft [nDrums]int
	graceGain [nDrums]float64
	graceSeed [nDrums]uint64
	// owner of the last row stepped (ownGen, ownGrid, ownTheme) and the row
	// its grid's polymetric lanes count from
	owner, runFrom int
	// the next row has no row before it to lead its flams in from: the
	// synth's first, or a theme's
	leadIn bool
	graced bool // the next row's graces have played

	// VU meter tap: read-only from the synthesis path's view — nothing
	// here feeds back into the samples, so renders stay byte-identical.
	mlp  [3]float64 // one-pole states at the band edges
	menv [4]float64 // per-band peak envelopes (attack instant, slow decay)
	mout [4]uint64  // envelopes published as float bits for cross-goroutine reads
	// the same per layer (layerlevels.go)
	layers layerMeter

	// hand-written pattern (grid.go): nil plays the generator alone
	grid atomic.Pointer[Grid]

	// authored themes (theme.go): themeCmd is staged by PlayTheme; the rest
	// belongs to the audio thread, except the two counters ThemePlaying reads
	themeCmd            atomic.Pointer[themeCmd]
	themeGen, themeDone atomic.Int64
	theme               *Theme
	themeAt             int
	themeOf             int64
	themeFresh          bool // the theme's first grid has not stepped yet
	silent              bool
	now                 atomic.Pointer[paramCmd] // staged for the next block (SetParametersNow)
	nframe              int64                    // frames rendered, synthesis-side
	frames              atomic.Int64             // nframe as of the end of the last Read
	rowLog              rowLog
	notes               noteRing // notes started, for a host to draw (notes.go)
	motif               motifState
	// the row stepped last, which the drum countdowns fire within: not
	// s.row-1 after a theme cuts the pattern short
	stepRow, stepPat int

	// The music and one-shot bus levels. Mixer controls rather than Params:
	// they never change what the score derives, only how loud the bed and
	// the one-shots sit against each other.
	musicLevel, sfxLevel level

	clock renderClock // times each Read (stats.go)

	// the frame a Read ended inside: its last partN bytes are still unread
	part  [4]byte
	partN int
}

// level is a mixer bus's gain. The caller sets a target from any goroutine;
// the level glides to it on the synthesis side, so a duck under a big
// one-shot and the recovery after it do not click. seen is the target being
// glided to; live is false until the first frame.
//
// Before the first frame a Set is the level playback starts at, taken at
// once, so startup renders as if it were always there. A Glide asked for
// then runs from that level (start, 1 if nothing was set): a game that sets
// 0 and then glides up gets its fade-in even when both calls land before
// the output has rendered anything, as they do when the title screen asks
// for the fade a moment after music-start.
type level struct {
	to         atomic.Pointer[levelTarget]
	gain, step float64
	left       int
	seen       *levelTarget
	live       bool
}

// levelTarget is one Set/Glide call on a level; set marks a Set. start is
// the level a first frame begins at under this target: the last Set's
// gain, carried along in each target so the pair is read in one load.
type levelTarget struct {
	gain, start float64
	frames      int
	set         bool
}

func (b *level) init() {
	b.seen = &levelTarget{gain: 1, start: 1}
	b.gain = 1
	b.to.Store(b.seen)
}

// set is a Set*Gain: g over levelGlide while playing, and the starting
// level before the first frame.
func (b *level) set(g float64) {
	g = clampLevel(g)
	b.to.Store(&levelTarget{gain: g, start: g, frames: glideFrames(levelGlide), set: true})
}

// glide asks for g, clamped to 0..1, over the given seconds, keeping the
// starting level of the target it replaces. The compare-and-swap keeps a
// concurrent set's start from being lost.
func (b *level) glide(g, seconds float64) {
	t := &levelTarget{gain: clampLevel(g), frames: glideFrames(seconds)}
	for {
		old := b.to.Load()
		t.start = old.start
		if b.to.CompareAndSwap(old, t) {
			return
		}
	}
}

func clampLevel(g float64) float64 {
	if !(g >= 0) { // also catches NaN
		return 0
	}
	return math.Min(g, 1)
}

func glideFrames(seconds float64) int {
	if !(seconds >= 0) {
		seconds = 0
	}
	// Round up, so a glide shorter than a frame still takes one rather than
	// becoming the instant step (frames 0) it exists to avoid. Past a minute
	// only risks overflowing the frame count.
	return int(math.Ceil(math.Min(seconds, 60) * SampleRate))
}

// next advances the level one frame toward the last requested target.
func (b *level) next() float64 {
	if t := b.to.Load(); t != b.seen {
		b.seen = t
		if !b.live {
			b.gain = t.start
		}
		if t.frames == 0 || (!b.live && t.set) {
			b.gain, b.left = t.gain, 0
		} else {
			b.step, b.left = (t.gain-b.gain)/float64(t.frames), t.frames
		}
	}
	b.live = true
	if b.left > 0 {
		b.left--
		b.gain += b.step
		if b.left == 0 {
			b.gain = b.seen.gain // land exactly, so 1 is byte-identical again
		}
	}
	return b.gain
}

// levelGlide is the Set*Gain glide: short enough to sound like a switch,
// long enough that a large step does not click.
const levelGlide = 0.05

// band-edge one-pole coefficients (120/350/1200 Hz) and ~400ms decay
var mcoef = [3]float64{
	1 - math.Exp(-2*math.Pi*120/SampleRate),
	1 - math.Exp(-2*math.Pi*350/SampleRate),
	1 - math.Exp(-2*math.Pi*1200/SampleRate),
}

const mdecay = 0.99974

func (s *Synth) meter(l, r float64) {
	mono := (l + r) / 2
	for i, c := range mcoef {
		s.mlp[i] += c * (mono - s.mlp[i])
	}
	bands := [4]float64{
		s.mlp[0],            // sub
		s.mlp[1] - s.mlp[0], // low-mid
		s.mlp[2] - s.mlp[1], // mid
		mono - s.mlp[2],     // high
	}
	for i, b := range bands {
		if b < 0 {
			b = -b
		}
		s.menv[i] = flush(s.menv[i] * mdecay)
		if b > s.menv[i] {
			s.menv[i] = b
		}
	}
}

// publishLevels makes the envelopes readable from other goroutines. Read
// calls it once per buffer, not once per sample: a meter is drawn a few
// dozen times a second.
func (s *Synth) publishLevels() {
	for i := range s.menv {
		atomic.StoreUint64(&s.mout[i], math.Float64bits(s.menv[i]))
	}
}

// Levels returns the per-band envelopes (sub, low-mid, mid, high) as of the
// end of the last Read. Safe to call from any goroutine.
func (s *Synth) Levels() [4]float64 {
	var v [4]float64
	for i := range v {
		v[i] = math.Float64frombits(atomic.LoadUint64(&s.mout[i]))
	}
	return v
}

func NewSynth(p Params) *Synth {
	// The reverb's lines (about 200 KB) are allocated here for every synth,
	// even one whose mood never sends to it: a mood or one-shot that does may
	// arrive at any pattern boundary, on the audio thread, which must not
	// allocate them.
	s := &Synth{voices: make([]Voice, nTracks), verb: newReverb(), leadIn: true}
	s.notes.epoch = newEpoch()
	// Every voice's unit states too (about 80 KB), for the same reason: left
	// to its first note, a voice allocated on the audio thread whenever that
	// came, a lead with a low density many patterns in. And a string's line
	// for each track and one-shot voice (about 300 KB): any of them may be
	// given a plucked patch. No drum is a string.
	n := len(s.voices) + len(s.dvoices) + len(s.fx.shots)
	states := make([]ustate, n*maxUnits)
	lines := make([]float64, (len(s.voices)+len(s.fx.shots))*maxPluckLine)
	i := 0
	for _, vs := range [][]Voice{s.voices, s.fx.shots[:], s.dvoices[:]} {
		for j := range vs {
			vs[j].units = states[i*maxUnits : i*maxUnits : (i+1)*maxUnits]
			if (i+1)*maxPluckLine <= len(lines) {
				vs[j].line = lines[i*maxPluckLine : (i+1)*maxPluckLine : (i+1)*maxPluckLine]
			}
			i++
		}
	}
	s.clock.now = time.Now
	s.musicLevel.init()
	s.sfxLevel.init()
	s.apply(&paramCmd{p: p.owned()})
	return s
}

// SetMusicGain scales the music bed (sequencer voices, drums and their delay
// sends) by g, clamped to 0..1, leaving one-shots at their own level. Playback
// glides to g over levelGlide; a call before the first frame is the level
// playback starts at.
// Safe to call from any goroutine; 1 renders byte-identically to no call at
// all.
func (s *Synth) SetMusicGain(g float64) { s.musicLevel.set(g) }

// GlideMusicGain is SetMusicGain over the given seconds: a game ducks the
// bed under a big one-shot with a short glide and brings it back with a
// longer one. A later call starts from wherever the last glide had reached;
// one before the first frame starts from the last SetMusicGain (or 1), so
// SetMusicGain(0) then a glide up fades the music in from silence.
func (s *Synth) GlideMusicGain(g, seconds float64) { s.musicLevel.glide(g, seconds) }

// SetSfxGain scales every one-shot, with its reverb and delay sends, by g,
// clamped to 0..1, leaving the music alone: the game's effects volume, as
// SetMusicGain is its music volume. Glides and starts like SetMusicGain;
// 1 renders byte-identically to no call at all.
func (s *Synth) SetSfxGain(g float64) { s.sfxLevel.set(g) }

// GlideSfxGain is SetSfxGain over the given seconds.
func (s *Synth) GlideSfxGain(g, seconds float64) { s.sfxLevel.glide(g, seconds) }

// SetParameters stages a new parameter map; it takes effect at the next
// pattern boundary (the undertone/set-parameters contract).
func (s *Synth) SetParameters(p Params) { s.publish(&s.next, p) }

// SetParametersNow applies p within nowBlock frames instead of at the next
// pattern: for an editor, or a game switching scenes, where a mood change
// should be heard at once. The rest of the pattern is redrawn from p, and
// each track moves to the note p would be sounding by now, fading over
// fadeFrames so the switch does not click. A change SetParameters staged
// for the boundary is dropped, since p is newer and complete: otherwise it
// would land at the boundary and undo p. Safe to call from any goroutine.
func (s *Synth) SetParametersNow(p Params) { s.publish(&s.now, p) }

// paramCmd is one parameter change. The two mailboxes are separate, so seq
// is what orders changes across them: the audio thread applies a change only
// if it is newer than the one playing, and so an immediate change can never
// undo a boundary change made after it, nor a late producer a newer one.
type paramCmd struct {
	p   Params // sanitized, and owning its scale
	seq uint64
}

// publish stores p in slot unless a concurrent producer has already put a
// newer change there. The copy and sanitizing happen here, on the caller's
// goroutine, so the audio thread neither allocates for them nor shares a
// slice the caller may go on editing.
func (s *Synth) publish(slot *atomic.Pointer[paramCmd], p Params) {
	c := &paramCmd{p: p.owned(), seq: s.paramSeq.Add(1)}
	for {
		old := slot.Load()
		if old != nil && old.seq > c.seq {
			return
		}
		if slot.CompareAndSwap(old, c) {
			return
		}
	}
}

// effective returns the newest change that has applied or is still on its
// way: what a one-shot fired now should be pitched against. A staged change
// counts, though it waits for the pattern boundary: the player is about to
// hear it, and the phrase that is ending is not worth pitching to. Safe to
// call from any goroutine.
func (s *Synth) effective() *paramCmd {
	// The mailboxes before cur: the audio thread swaps a change out of its
	// mailbox before publishing it as cur, so in this order one of the loads
	// sees it.
	pending := [...]*paramCmd{s.now.Load(), s.next.Load()}
	c := s.cur.Load()
	for _, m := range pending {
		if m != nil && m.seq > c.seq {
			c = m
		}
	}
	return c
}

// staged returns the boundary change that will still take effect, if any:
// one newer than both what is playing and any immediate change on its way,
// which would otherwise supersede it. Safe to call from any goroutine.
func (s *Synth) staged() *paramCmd {
	n := s.next.Load()
	if n == nil || n.seq <= s.cur.Load().seq {
		return nil
	}
	if w := s.now.Load(); w != nil && w.seq > n.seq {
		return nil
	}
	return n
}

// landsAtBoundary reports whether staged change c should apply at the
// pattern boundary: it is newer than the change playing, and than any
// immediate change waiting for its block, which would only replace it a few
// frames in after its row-0 notes had started. Audio thread only.
func (s *Synth) landsAtBoundary(c *paramCmd) bool {
	if c.seq <= s.applied {
		return false
	}
	w := s.now.Load()
	return w == nil || w.seq < c.seq
}

func (s *Synth) apply(c *paramCmd) {
	p := c.p
	s.params = p
	s.applied = c.seq
	s.cur.Store(c)
	s.samplesPerRow = int(SampleRate * 60 / (p.BPM * 2)) // 2 rows per beat: ambient rows are long
	s.fitDelay(s.samplesPerRow * 3)
	// The music's voices only: one-shots stay at grit 0, as SetMusicGain
	// leaves their level alone, so an sfx sounds the same in any fight.
	for _, vs := range [][]Voice{s.voices, s.dvoices[:]} {
		for i := range vs {
			vs[i].gritTo = p.Grit
			if !vs[i].active {
				vs[i].grit = p.Grit
			}
		}
	}
	// Open reaches the bed only: a drum's filters are its tuning, which a
	// level should not move.
	for i := range s.voices {
		s.voices[i].openTo = p.Open
		if !s.voices[i].active {
			s.voices[i].open = p.Open
		}
	}
	s.verb.setRoom(p.Room)
}

// fitDelay keeps the delay three rows long as the tempo changes; set once,
// it fell off the grid as soon as tension raised the tempo. The ring is first
// rotated to read from its start, so the echoes due soonest play on without a
// jump; a shorter line keeps those, and a longer one follows them with
// silence rather than an old echo. Only a tempo slower than any this synth has
// played allocates.
func (s *Synth) fitDelay(n int) {
	if n == len(s.dl) {
		return
	}
	for _, d := range []*[]float64{&s.dl, &s.dr} {
		line := *d
		rotate(line, s.dpos)
		if n > cap(line) {
			grown := make([]float64, n)
			copy(grown, line)
			*d = grown
			continue
		}
		old := len(line)
		line = line[:n]
		if n > old {
			clear(line[old:])
		}
		*d = line
	}
	s.dpos = 0
}

// rotate moves a[k:] to the front of a, in place.
func rotate(a []float64, k int) {
	slices.Reverse(a[:k])
	slices.Reverse(a[k:])
	slices.Reverse(a)
}

// nowBlock is how often, in frames, the synth looks for a SetParametersNow:
// about 3 ms, well inside one output buffer.
const nowBlock = 128

// applyNow switches to p mid-pattern. The pattern and drums are redrawn
// for the pattern still playing (the previous one, if the last row has just
// stepped); generation is deterministic per pattern, so unchanged params
// redraw the same notes and nothing is retriggered.
func (s *Synth) applyNow(c *paramCmd) {
	old := s.samplesPerRow
	s.apply(c)
	s.rowLeft = max(1, s.rowLeft*s.samplesPerRow/old)
	s.rescaleDrums(old)
	pat, row := s.pat, s.row-1
	if row < 0 {
		pat, row = pat-1, rows-1
	}
	if s.params.motifOn() && s.motif.started {
		s.motifNow(pat, row)
	} else {
		s.pats = buildPatterns(s.params, pat)
		s.drums = buildDrums(s.params, pat)
	}
	if g := s.playingGrid(); g != nil {
		for r := 0; r <= row; r++ {
			s.applyGrid(g, r)
		}
	}
	for t := range s.voices {
		s.revoice(t, pat, row)
	}
}

// revoice moves track t to the note the redrawn pattern has sounding at row:
// the latest one there, if its gate has not run out.
func (s *Synth) revoice(t, pat, row int) {
	v := &s.voices[t]
	for r := row; r >= 0; r-- {
		tr := s.pats[t][r]
		if tr.note == 0 {
			continue
		}
		left := tr.gateRows - (row - r)
		if left <= 0 {
			break
		}
		if v.active && v.gate && v.note == clampNote(tr.note) && samePatch(v.patch, tr.patch) {
			v.gateRows = left
			if v.gainTo != tr.gain {
				v.glideGain(tr.gain)
			}
		} else {
			v.retrigger(tr, left, s.params.Seed+uint64(pat*rows+r))
			s.noteAt(t, NoteRevoice, tr.note, tr.gain, r, pat)
		}
		return
	}
	v.gate = false
}

func (s *Synth) step() {
	_, s.owner, s.runFrom = s.nextOwner()
	if s.row == 0 {
		s.advanceTheme()
		if c := s.next.Swap(nil); c != nil && s.landsAtBoundary(c) {
			s.apply(c)
		}
		if !s.params.motifOn() {
			s.pats = buildPatterns(s.params, s.pat)
			s.drums = buildDrums(s.params, s.pat)
			s.motif.started = false
		}
	}
	if s.params.motifOn() {
		s.motifRow()
	}
	if g := s.playingGrid(); g != nil {
		s.applyGrid(g, s.row)
	}
	s.rowLog.add(s.nframe, s.row, s.pat)
	s.stepRow, s.stepPat = s.row, s.pat
	s.stepDrums()
	for t := 0; t < nTracks; t++ {
		v := &s.voices[t]
		tr := s.pats[t][s.row]
		seed := s.params.Seed + uint64(s.pat*rows+s.row)
		switch {
		case tr.note != 0 && (v.pend.note != 0 || v.gain != v.gainTo || v.active && !samePatch(v.patch, tr.patch)):
			// mid-fade, a trigger would step the gain; on a new patch it
			// would cut the old one off
			v.retrigger(tr, tr.gateRows, seed)
			s.noteAt(t, NoteRetrigger, tr.note, tr.gain, s.row, s.pat)
		case tr.note != 0:
			v.trigger(tr.patch, tr.note, tr.gateRows, tr.gain, seed)
			s.noteAt(t, NoteOn, tr.note, tr.gain, s.row, s.pat)
		default:
			if v.pend.note != 0 {
				v.pendRows--
				if v.pendRows <= 0 {
					v.pend = trackRow{}
				}
			}
			if v.active && v.gate {
				v.gateRows--
				if v.gateRows <= 0 {
					v.gate = false
				}
			}
		}
	}
	s.row++
	if s.row == rows {
		s.row = 0
		s.pat++
	}
}

func (s *Synth) frame() (int16, int16) {
	if s.nframe%nowBlock == 0 {
		if c := s.themeCmd.Swap(nil); c != nil {
			s.startTheme(c)
		}
	}
	if s.leadIn {
		// hold the row back so its flams' graces can sound first
		s.leadIn = false
		if s.rowLeft == 0 && s.flamsAhead(false) {
			s.rowLeft, s.graced = s.flamLead(), false
		}
	}
	if s.rowLeft == 0 {
		s.step()
		s.rowLeft, s.graced = s.samplesPerRow, false
	}
	if s.nframe%nowBlock == 0 {
		if c := s.now.Swap(nil); c != nil && c.seq > s.applied {
			s.applyNow(c)
		}
	}
	// at or past the lead rather than on it: applyNow above can rescale
	// rowLeft across it in either direction
	if !s.graced && s.rowLeft <= s.flamLead() {
		s.graced = true
		s.flamsAhead(true)
	}
	s.rowLeft--
	s.nframe++
	var l, r float64
	s.bus = sends{}
	var ml, mr float64 // where a muted layer plays
	var mbus sends
	for i := range s.voices {
		if s.voices[i].active {
			if s.params.Mute&(1<<i) != 0 {
				before := ml + mr
				s.voices[i].run(&ml, &mr, &mbus)
				s.layers.see(i, ml+mr-before)
			} else {
				before := l + r
				s.voices[i].run(&l, &r, &s.bus)
				s.layers.see(i, l+r-before)
			}
		}
	}
	if s.params.Mute&LayerDrums != 0 {
		bus, before := s.bus, ml+mr
		s.runDrums(&ml, &mr)
		s.bus = bus
		s.layers.see(layerDrums, ml+mr-before)
	} else {
		before := l + r
		s.runDrums(&l, &r)
		s.layers.see(layerDrums, l+r-before)
	}
	if s.nframe%layerFold == 0 {
		s.layers.fold(layerFoldDecay)
	}
	if s.motif.started && s.motif.phr {
		s.motif.mix.run(&l, &r, &s.bus)
	}
	if g := s.musicLevel.next(); g != 1 {
		l, r = l*g, r*g
		s.bus.delay, s.bus.verb = s.bus.delay*g, s.bus.verb*g
	}
	// At 1 the shots add straight into the mix: summing them apart first
	// would reorder the float additions and move every golden render.
	if g := s.sfxLevel.next(); g == 1 {
		s.runShots(&l, &r, &s.bus)
	} else {
		var sl, sr float64
		var sb sends
		s.runShots(&sl, &sr, &sb)
		l, r = l+sl*g, r+sr*g
		s.bus.delay, s.bus.verb = s.bus.delay+sb.delay*g, s.bus.verb+sb.verb*g
	}
	// cross-fed stereo delay
	wl, wr := s.dl[s.dpos], s.dr[s.dpos]
	s.dl[s.dpos] = flush(s.bus.delay + wr*0.5)
	s.dr[s.dpos] = flush(s.bus.delay*0.7 + wl*0.5)
	s.dpos = (s.dpos + 1) % len(s.dl)
	l += wl * 0.5
	r += wr * 0.5
	vl, vr := s.verb.process(s.bus.verb)
	l, r = l+vl, r+vr
	l, r = math.Tanh(l*0.8), math.Tanh(r*0.8)
	s.meter(l, r)
	return int16(l * 32000), int16(r * 32000)
}

// Read fills p with 16-bit little-endian stereo frames, four bytes each. A p
// that ends inside a frame gets that frame's first bytes, and the next Read
// starts with the rest: io.Reader allows any length, and a reader returning
// (0, nil) for a short tail leaves io.ReadFull looping forever.
func (s *Synth) Read(p []byte) (int, error) {
	n := copy(p, s.part[len(s.part)-s.partN:])
	s.partN -= n
	if n > 0 && s.partN == 0 {
		s.frames.Store(s.nframe) // the split frame is now delivered whole
	}
	whole := (len(p) - n) / 4 * 4
	tail := len(p) - n - whole
	frames := whole / 4
	if tail > 0 {
		frames++
	}
	if frames == 0 {
		return n, nil
	}
	t0 := s.clock.now()
	for i := n; i < n+whole; i += 4 {
		s.pcmFrame(p[i:])
	}
	if tail > 0 {
		s.pcmFrame(s.part[:])
		copy(p[n+whole:], s.part[:tail])
		s.partN = len(s.part) - tail
	}
	// Frames counts frames delivered, which a split one is not yet: a
	// playhead set by it would run a frame ahead of the bytes.
	s.frames.Store(s.nframe - int64(min(s.partN, 1)))
	s.publishLevels()
	s.layers.publish(int(s.nframe % layerFold))
	s.clock.record(t0, frames)
	return len(p), nil
}

// pcmFrame renders one frame into b's first four bytes.
func (s *Synth) pcmFrame(b []byte) {
	l, r := s.frame()
	binary.LittleEndian.PutUint16(b, uint16(l))
	binary.LittleEndian.PutUint16(b[2:], uint16(r))
}

// ---- output ----

// maxWAVData is the most PCM a WAV can hold: RIFF sizes are 32 bits, and the
// RIFF chunk's counts the 36 header bytes after it too.
const maxWAVData = math.MaxUint32 - 36

// WriteWAV renders seconds of src, a Synth or something reading through one,
// to a 16-bit stereo WAV at path: whole frames, a fraction of one dropped.
// It streams, so a long render needs no buffer its size. A duration that is
// negative, NaN or too long for a WAV is an error, and src is not read; so
// is src ending early, and then no file is left at path.
func WriteWAV(path string, src io.Reader, seconds float64) error {
	if !(seconds >= 0) || seconds*SampleRate*4 > maxWAVData {
		return fmt.Errorf("undertone: WAV duration %v s: want 0 to %d s", seconds, maxWAVData/4/SampleRate)
	}
	size := int64(seconds*SampleRate) * 4
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(f)
	var werr error
	w := func(v any) {
		if werr == nil {
			werr = binary.Write(bw, binary.LittleEndian, v)
		}
	}
	ws := func(s string) {
		if werr == nil {
			_, werr = bw.WriteString(s)
		}
	}
	ws("RIFF")
	w(uint32(36 + size))
	ws("WAVEfmt ")
	w(uint32(16))
	w(uint16(1)) // PCM
	w(uint16(2))
	w(uint32(SampleRate))
	w(uint32(SampleRate * 4))
	w(uint16(4))
	w(uint16(16))
	ws("data")
	w(uint32(size))
	if werr == nil {
		if _, werr = io.CopyN(bw, src, size); werr == io.EOF {
			werr = io.ErrUnexpectedEOF
		}
	}
	if werr == nil {
		werr = bw.Flush()
	}
	// Close's error matters too: it is where a full disk often shows up.
	if err := errors.Join(werr, f.Close()); err != nil {
		os.Remove(path) // a truncated WAV would claim frames it lacks
		return err
	}
	return nil
}

// sanitized makes p safe to play. Parameters arrive from lg code and pages,
// and a bad one used to panic the synth (a scale under 5 notes indexes past
// its end in the pad; a bpm of zero or NaN makes rows infinitely long), which
// in a browser kills the whole wasm module. Out-of-range values are clamped or
// replaced, never reported: the write-only contract has no way back.
func (p Params) sanitized() Params {
	if len(p.Scale) < 5 {
		p.Scale = scalesByBrightness[0]
	}
	if !(p.BPM >= 20) { // also catches NaN
		p.BPM = 20
	}
	p.BPM = min(p.BPM, 400)
	p.Root = max(24, min(p.Root, 96))
	for _, g := range []*float64{&p.DroneGain, &p.FifthGain, &p.PadDensity, &p.LeadDensity, &p.AirGain, &p.PulseGain} {
		if !(*g >= 0) {
			*g = 0
		}
		*g = min(*g, 4)
	}
	if p.LeadGate < 1 {
		p.LeadGate = 1
	}
	// a trim past +12 dB would clip the mix; under -60 dB it is silence anyway
	for _, d := range []*float64{&p.LeadDB, &p.PadDB, &p.BassDB, &p.MoodLeadDB, &p.MoodPadDB} {
		if *d != *d {
			*d = 0
		}
		*d = max(-60, min(*d, 12))
	}
	if !(p.DrumGain >= 0) {
		p.DrumGain = 0
	}
	p.DrumGain = min(p.DrumGain, 4)
	if !(p.DrumBusy >= 0) {
		p.DrumBusy = 0
	}
	p.DrumBusy = min(p.DrumBusy, 1)
	if !(p.Swing >= 0) {
		p.Swing = 0
	}
	p.Swing = min(p.Swing, 0.25)
	if !(p.Grit >= 0) {
		p.Grit = 0
	}
	p.Grit = min(p.Grit, 1)
	if math.IsNaN(p.Open) {
		p.Open = 0
	}
	p.Open = max(-1, min(p.Open, 1))
	if p.Phrasing == nil && !p.NoPhrasing {
		p.Phrasing = p.MoodPhrasing
	}
	if p.Memory == nil && !p.NoMemory {
		p.Memory = p.MoodMemory
	}
	if m := p.Memory; m != nil {
		// a copy, so a caller's Memory is never changed under it
		c := *m
		for _, x := range []*float64{&c.Restlessness, &c.Leap, &c.ReturnDrift, &c.CycleChurn} {
			if !(*x >= 0) {
				*x = 0
			}
			*x = min(*x, 1)
		}
		if c != *m {
			p.Memory = &c
		}
	}
	for _, x := range []*float64{&p.Room.Size, &p.Room.Damp} {
		if math.IsNaN(*x) {
			*x = 0
		}
		*x = max(-1, min(*x, 1))
	}
	return p
}

// owned is p sanitized, with a copy of its scale: Params is passed by value,
// but the scale slice is not, and a caller editing the one it passed must
// not reach a synth already playing it.
func (p Params) owned() Params {
	p = p.sanitized()
	p.Scale = slices.Clone(p.Scale)
	return p
}

// flush zeroes values too small to hear, so a decaying feedback line never
// drifts into denormals, which are slow on some CPUs.
func flush(x float64) float64 {
	if x > -1e-20 && x < 1e-20 {
		return 0
	}
	return x
}
