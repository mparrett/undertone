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
	"math"
	"os"
	"slices"
	"sync/atomic"
)

const SampleRate = 44100

// ---- unit VM ----

const (
	opEnv   = iota // p: attack s, decay s, sustain lvl, release s   -> push env
	opOsc          // p: transpose semis, shape, sweep semis, sweep decay s -> push sample
	opMul          // pop a, b -> push a*b
	opLpf          // p: cutoff Hz; pop x -> push lowpassed x
	opSend         // p: amount; peek top, add top*amount to delay bus
	opOut          // p: gain, pan 0..1; pop x -> accumulate to L/R
	opPluck        // p: transpose semis, feedback; Karplus-Strong string -> push sample
	opHpf          // p: cutoff Hz; pop x -> push highpassed x
	opSvf          // p: cutoff Hz, resonance 0..1, mode (0 low, 1 band, 2 high), depth octaves; [pop m;] pop x -> push filtered x
	opLfo          // p: rate Hz, shape (0 sine, 1 triangle, 2 sample-and-hold) -> push -1..1
	opDrive        // p: drive, bias -1..1; pop x -> push tanh(drive*x + bias) - tanh(bias)
	opCrush        // p: hold Hz, bits; pop x -> push x held and quantized
	opVerb         // p: amount; peek top, add top*amount to the reverb bus
	opScale        // p: gain, offset; top = top*gain + offset
	opPitch        // p: depth semis; pop m, bend every later osc by m*depth
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

// per-voice, per-unit mutable state
type ustate struct {
	phase, env, lp, sweep float64
	ic2                   float64 // svf: the second integrator (lp holds the first)
	stage                 int     // env: 0 attack, 1 decay, 2 sustain, 3 release
	rng                   uint64
	ks                    []float64 // Karplus-Strong delay line, allocated on first sample
	kpos                  int       // pluck: read position; swept svf and bent osc: control-rate counter
	k                     float64   // cached per-note coefficient: osc phase increment (no sweep) or lpf c
	g, g1, g2             float64   // svf, drive, crush: constants derived from grit g; osc: g1 is its bend ratio
	gset                  bool      // g1 and g2 hold for grit g
	ko                    float64   // svf: the open its coefficient k was computed at
	kset                  bool
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
}

func (v *Voice) trigger(patch Patch, note, gateRows int, gain float64, seed uint64) {
	v.patch = patch
	v.note = note
	v.gain = gain
	// A voice retriggered with the patch it is already sounding carries its
	// phase, level and filter state into the new note and attacks from where
	// it is. Starting from zero cut a sounding drone to silence in one sample:
	// a click at every pattern boundary (a step of 2336 against a normal
	// largest of 402). A plucked string is struck afresh either way.
	legato := v.active && samePatch(v.patch, patch) && len(v.units) == len(patch)
	v.patch = patch
	v.note = note
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
	for i := range v.units {
		u := &v.units[i]
		if legato {
			*u = ustate{phase: u.phase, env: u.env, lp: u.lp, ic2: u.ic2, ks: u.ks[:0]}
		} else {
			*u = ustate{ks: u.ks[:0]}
		}
		u.sweep = 1
		u.rng = seed*2862933555777941757 + uint64(i) + 3037000493
		// a sample-and-hold lfo's first value; legato keeps the held one
		if !legato && patch[i].op == opLfo {
			u.lp = u.noise()
		}
	}
}

// noise steps the unit's LCG and returns a sample in -1..1.
func (s *ustate) noise() float64 {
	s.rng = s.rng*6364136223846793005 + 1442695040888963407
	return float64(int64(s.rng>>11))/(1<<52) - 1
}

// level is the voice's loudest envelope times its gain: how much it would
// be missed if cut.
func (v *Voice) level() float64 {
	l := 0.0
	for i := range v.units {
		if v.patch[i].op == opEnv {
			l = max(l, v.units[i].env)
		}
	}
	return l * v.gain
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
// first, and the new note fades in.
func (v *Voice) retrigger(tr trackRow, gateRows int, seed uint64) {
	if !v.active || v.gain == 0 {
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
			var inc float64
			if u.p[2] == 0 {
				// no sweep: pitch is fixed for the note's life, so pow runs once
				if !s.kset {
					semis := float64(v.note) + u.p[0] + u.p[2]*s.sweep
					s.k = 440 * math.Pow(2, (semis-69)/12) / SampleRate
					s.kset = true
				}
				s.sweep -= s.sweep / (u.p[3]*SampleRate + 1)
				inc = s.k
			} else {
				semis := float64(v.note) + u.p[0] + u.p[2]*s.sweep
				s.sweep -= s.sweep / (u.p[3]*SampleRate + 1)
				inc = 440 * math.Pow(2, (semis-69)/12) / SampleRate
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
				x = math.Sin(2 * math.Pi * s.phase)
			case shpSaw:
				x = 2*s.phase - 1
			case shpPulse:
				if s.phase < 0.5 {
					x = 1
				} else {
					x = -1
				}
			case shpNoise:
				x = s.noise()
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
				x = math.Sin(2 * math.Pi * s.phase)
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
		case opPluck:
			// Karplus-Strong: a noise burst circulating in a delay line
			// the length of the period, averaged (lowpassed) each pass.
			if len(s.ks) == 0 { // a new note: strike the string
				freq := 440 * math.Pow(2, (float64(v.note)+u.p[0]-69)/12)
				n := int(SampleRate / freq)
				if n < 2 {
					n = 2
				}
				// reuse the last note's line when it is long enough
				if cap(s.ks) >= n {
					s.ks = s.ks[:n]
				} else {
					s.ks = make([]float64, n)
				}
				for i := range s.ks {
					s.ks[i] = s.noise()
				}
			}
			x := s.ks[s.kpos]
			next := s.ks[(s.kpos+1)%len(s.ks)]
			s.ks[s.kpos] = (x + next) * 0.5 * u.p[1]
			s.kpos = (s.kpos + 1) % len(s.ks)
			stack[sp] = x
			sp++
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

// ---- lead instruments (the bell slot) ----
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
	{opOut, [4]float64{0.8, 0.5}},
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
	BellDensity float64
	AirGain     float64
	PulseGain   float64 // tension pulse track level (0 = silent)
	FifthOffset int     // second drone's interval from the chord, usually below; slides dissonant with tension
	BellLift    int     // semitones added to bell notes (overworld sparkle register)
	LeadName    string  // which instrument holds the bell slot
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

	// Mute silences layers at the mix, for listening to a mood a layer at a
	// time. A muted layer still plays, unheard, so the patterns and the rng
	// draws are those of the full mix; zeroing a density or gain instead would
	// change what every other layer plays.
	Mute Layers
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
// A voice missing here plays at 0, so every lead needs an entry.
var leadTrim = map[string]float64{
	"bell": 1, "chime": 1, "clang": 1, "kalimba": 1, "reed": 1, "pluck": 3.162,
}

// mood-weighted audition lists; the seed picks (kaiju trick applied to
// timbre: two seeds differ in instrument, not just notes)
var dungeonLeads = []string{"bell", "chime", "chime", "clang", "kalimba", "pluck", "pluck"}
var overworldLeads = []string{"bell", "bell", "kalimba", "kalimba", "reed", "pluck", "pluck"}
var meadowLeads = []string{"bell", "kalimba", "kalimba", "reed", "pluck", "pluck"}
var auroraLeads = []string{"chime", "chime", "bell", "bell", "kalimba"}
var villageLeads = []string{"kalimba", "kalimba", "pluck", "pluck", "reed"}
var rustLeads = []string{"clang", "clang", "pluck", "chime"}

// pickLead draws from a separate rng stream so the pattern content for a
// given seed is unchanged — only the instrument varies.
func pickLead(p *Params, names []string) {
	r := rng{p.Seed*0x9e3779b97f4a7c15 + 0x1eadf00d | 1}
	ApplyLead(p, names[r.n(len(names))])
}

func ApplyLead(p *Params, name string) {
	if d, ok := leadLib[name]; ok {
		p.LeadName, p.LeadGate, p.Palette.Bell = d.name, d.gate, d.patch
	}
}

// Palette is a mood's patch bank: mood = patches + a point in parameter space.
type Palette struct {
	Drone, Fifth, Pad, Bell, Air, Pulse Patch
	Kit                                 kit // zero: drumKit
}

var dungeonPalette = Palette{Drone: drone, Fifth: pad, Pad: pad, Bell: bell, Air: air, Pulse: pulse}
var overworldPalette = Palette{Drone: bed, Fifth: pad, Pad: pad, Bell: bell, Air: breeze, Pulse: pulse}
var auroraPalette = Palette{Drone: bed, Fifth: veil, Pad: veil, Bell: bell, Air: breeze, Pulse: pulse}
var rustPalette = Palette{Drone: rust, Fifth: smear, Pad: smear, Bell: bell, Air: hiss, Pulse: grind, Kit: industrialKit}

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

var scalesForRust = [][]int{
	{0, 1, 3, 5, 7, 8, 10}, // phrygian
	{0, 1, 3, 5, 7, 8, 10}, // phrygian
	{0, 1, 3, 5, 6, 8, 10}, // locrian
}

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
const nTracks = 6 // drone, fifth, pad, bell, air, pulse

type trackRow struct {
	note     int
	gain     float64
	gateRows int
	patch    Patch
}

// buildPatterns fills one pattern (16 rows x nTracks) from Params.
// Chord drifts between tonic, VI, and iv each pattern; bells walk the
// scale — per-pattern reseeding is the gradual-mutation mechanism.
func buildPatterns(p Params, pat int) [nTracks][rows]trackRow {
	var out [nTracks][rows]trackRow
	r := rng{p.Seed ^ uint64(pat)*0x9e3779b97f4a7c15 | 1}
	chord := p.Root + []int{0, 0, p.Scale[5] - 12, p.Scale[3] - 12}[r.n(4)]

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
			deg := []int{0, 2, 4}[r.n(3)]
			out[2][row] = trackRow{chord + p.Scale[deg], 0.7, 6, p.Palette.Pad}
		}
	}
	deg := 7 + r.n(7)
	for i := 0; i < rows; i++ {
		if r.f() < p.BellDensity {
			deg += r.n(5) - 2
			deg = max(3, min(deg, 17))
			out[3][i] = trackRow{chord + 12*(deg/7) + p.Scale[deg%7] + p.BellLift, 0.55 * leadTrim[p.LeadName], p.LeadGate, p.Palette.Bell}
		}
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
	next          atomic.Pointer[Params] // staged; applied at the next pattern boundary
	cur           atomic.Pointer[Params] // params, published for other goroutines (resolveDegree)
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

	// VU meter tap: read-only from the synthesis path's view — nothing
	// here feeds back into the samples, so renders stay byte-identical.
	mlp  [3]float64 // one-pole states at the band edges
	menv [4]float64 // per-band peak envelopes (attack instant, slow decay)
	mout [4]uint64  // envelopes published as float bits for cross-goroutine reads

	// hand-written pattern (grid.go): nil plays the generator alone
	grid   atomic.Pointer[Grid]
	now    atomic.Pointer[Params] // staged for the next block (SetParametersNow)
	nframe int64                  // frames rendered, synthesis-side
	frames atomic.Int64           // nframe as of the end of the last Read
	rowLog rowLog

	// music bus level as float bits, set from the caller's goroutine. A mixer
	// control rather than a Param: it never changes what the score derives,
	// only how loud the bed sits under the one-shots.
	musicGain atomic.Uint64
}

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
		atomic.StoreUint64(&s.mout[i], math.Float64bits(s.menv[i]))
	}
}

// Levels returns the current per-band envelopes (sub, low-mid, mid, high).
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
	s := &Synth{voices: make([]Voice, nTracks), verb: newReverb()}
	s.musicGain.Store(math.Float64bits(1))
	s.apply(p)
	return s
}

// SetMusicGain scales the music bed (sequencer voices, drums and their delay
// sends) by g, clamped to 0..1, leaving one-shots at full level. Safe to call
// from any goroutine; 1 renders byte-identically to no call at all.
func (s *Synth) SetMusicGain(g float64) {
	if !(g >= 0) { // also catches NaN
		g = 0
	}
	s.musicGain.Store(math.Float64bits(math.Min(g, 1)))
}

// SetParameters stages a new parameter map; it takes effect at the next
// pattern boundary (the undertone/set-parameters contract).
func (s *Synth) SetParameters(p Params) { s.next.Store(&p) }

// SetParametersNow applies p within nowBlock frames instead of at the next
// pattern: for an editor, or a game switching scenes, where a mood change
// should be heard at once. The rest of the pattern is redrawn from p, and
// each track moves to the note p would be sounding by now, fading over
// fadeFrames so the switch does not click. Safe to call from any goroutine.
func (s *Synth) SetParametersNow(p Params) { s.now.Store(&p) }

func (s *Synth) apply(p Params) {
	p = p.sanitized()
	s.params = p
	s.cur.Store(&p)
	s.samplesPerRow = int(SampleRate * 60 / (p.BPM * 2)) // 2 rows per beat: ambient rows are long
	s.fitDelay(s.samplesPerRow * 3)
	// The music's voices only: one-shots stay at grit 0, as they stay at
	// full level under SetMusicGain, so an sfx sounds the same in any fight.
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
func (s *Synth) applyNow(p Params) {
	old := s.samplesPerRow
	s.apply(p)
	s.rowLeft = max(1, s.rowLeft*s.samplesPerRow/old)
	pat, row := s.pat, s.row-1
	if row < 0 {
		pat, row = pat-1, rows-1
	}
	s.pats = buildPatterns(s.params, pat)
	s.drums = buildDrums(s.params, pat)
	if g := s.grid.Load(); g != nil {
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
		if v.active && v.gate && v.note == tr.note && samePatch(v.patch, tr.patch) {
			v.gateRows = left
			if v.gainTo != tr.gain {
				v.glideGain(tr.gain)
			}
		} else {
			v.retrigger(tr, left, s.params.Seed+uint64(pat*rows+r))
		}
		return
	}
	v.gate = false
}

func (s *Synth) step() {
	if s.row == 0 {
		if p := s.next.Swap(nil); p != nil {
			s.apply(*p)
		}
		s.pats = buildPatterns(s.params, s.pat)
		s.drums = buildDrums(s.params, s.pat)
	}
	if g := s.grid.Load(); g != nil {
		s.applyGrid(g, s.row)
	}
	s.rowLog.add(s.nframe, s.row)
	s.stepDrums()
	for t := 0; t < nTracks; t++ {
		v := &s.voices[t]
		tr := s.pats[t][s.row]
		seed := s.params.Seed + uint64(s.pat*rows+s.row)
		switch {
		case tr.note != 0 && (v.pend.note != 0 || v.gain != v.gainTo):
			v.retrigger(tr, tr.gateRows, seed) // mid-fade: a trigger would step the gain
		case tr.note != 0:
			v.trigger(tr.patch, tr.note, tr.gateRows, tr.gain, seed)
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
	if s.rowLeft == 0 {
		s.step()
		s.rowLeft = s.samplesPerRow
	}
	if s.nframe%nowBlock == 0 {
		if p := s.now.Swap(nil); p != nil {
			s.applyNow(*p)
		}
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
				s.voices[i].run(&ml, &mr, &mbus)
			} else {
				s.voices[i].run(&l, &r, &s.bus)
			}
		}
	}
	if s.params.Mute&LayerDrums != 0 {
		bus := s.bus
		s.runDrums(&ml, &mr)
		s.bus = bus
	} else {
		s.runDrums(&l, &r)
	}
	if g := math.Float64frombits(s.musicGain.Load()); g != 1 {
		l, r = l*g, r*g
		s.bus.delay, s.bus.verb = s.bus.delay*g, s.bus.verb*g
	}
	s.runShots(&l, &r)
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

func (s *Synth) Read(p []byte) (int, error) {
	n := len(p) / 4 * 4
	for i := 0; i < n; i += 4 {
		l, r := s.frame()
		binary.LittleEndian.PutUint16(p[i:], uint16(l))
		binary.LittleEndian.PutUint16(p[i+2:], uint16(r))
	}
	s.frames.Store(s.nframe)
	return n, nil
}

// ---- output ----

func WriteWAV(path string, s *Synth, seconds float64) error {
	data := make([]byte, int(seconds*SampleRate)*4)
	s.Read(data)
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
	w(uint32(36 + len(data)))
	ws("WAVEfmt ")
	w(uint32(16))
	w(uint16(1)) // PCM
	w(uint16(2))
	w(uint32(SampleRate))
	w(uint32(SampleRate * 4))
	w(uint16(4))
	w(uint16(16))
	ws("data")
	w(uint32(len(data)))
	if werr == nil {
		_, werr = bw.Write(data)
	}
	if werr == nil {
		werr = bw.Flush()
	}
	// Close's error matters too: it is where a full disk often shows up.
	return errors.Join(werr, f.Close())
}

// sanitized makes p safe to play. Parameters arrive from lg code and pages,
// and a bad one used to panic the synth (a scale under 7 notes indexes past
// its end; a bpm of zero or NaN makes rows infinitely long), which in a
// browser kills the whole wasm module. Out-of-range values are clamped or
// replaced, never reported: the write-only contract has no way back.
func (p Params) sanitized() Params {
	if len(p.Scale) < 7 {
		p.Scale = scalesByBrightness[0]
	}
	if !(p.BPM >= 20) { // also catches NaN
		p.BPM = 20
	}
	p.BPM = min(p.BPM, 400)
	p.Root = max(24, min(p.Root, 96))
	for _, g := range []*float64{&p.DroneGain, &p.FifthGain, &p.PadDensity, &p.BellDensity, &p.AirGain, &p.PulseGain} {
		if !(*g >= 0) {
			*g = 0
		}
		*g = min(*g, 4)
	}
	if p.LeadGate < 1 {
		p.LeadGate = 1
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
