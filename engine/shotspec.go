package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

// ShotSpec is a one-shot as data: a patch and its gate, the form a game
// registers a sound of its own in, as MoodSpec is the form of a mood row. A
// built-in shot converts to one and back without changing a sample.
type ShotSpec struct {
	Name   string
	GateMS float64
	Units  []UnitSpec
}

// UnitSpec is one unit of a patch. Args are the op's parameters in order,
// and an osc's waveform is named in Shape rather than numbered:
//
//	env   attack s, decay s, sustain 0..1, release s  -> push envelope
//	osc   transpose semis, sweep semis, sweep decay s (Shape: sine saw pulse noise) -> push sample
//	pluck transpose semis, feedback 0..1              -> push a Karplus-Strong string
//	mul                                               pop two, push their product
//	lpf   cutoff Hz                                   lowpass the top
//	hpf   cutoff Hz                                   highpass the top
//	svf   cutoff Hz, resonance 0..1, mode 0 low 1 band 2 high, depth octaves
//	      -> filter the top; a nonzero depth first pops a modulation value
//	lfo   rate Hz, shape 0 sine 1 triangle 2 sample-and-hold -> push -1..1
//	drive drive, bias -1..1                           overdrive the top
//	crush hold Hz, bits                               sample-hold and quantize the top
//	send  amount                                      add the top to the delay bus
//	verb  amount                                      add the top to the reverb bus
//	scale gain, offset                                top = top*gain + offset
//	pitch depth semis                                 pop m, bend every later osc by m*depth
//	out   gain, pan 0..1                              pop and mix
type UnitSpec struct {
	Op    string
	Shape string
	Args  []float64
}

var opNames = []string{opEnv: "env", opOsc: "osc", opMul: "mul", opLpf: "lpf", opSend: "send", opOut: "out", opPluck: "pluck", opHpf: "hpf", opSvf: "svf", opLfo: "lfo", opDrive: "drive", opCrush: "crush", opVerb: "verb", opScale: "scale", opPitch: "pitch"}

// opArgs is how many Args a unit of each op takes: the parameters its op
// reads, less an osc's shape, which travels in UnitSpec.Shape.
var opArgs = []int{opEnv: 4, opOsc: 3, opMul: 0, opLpf: 1, opSend: 1, opOut: 2, opPluck: 2, opHpf: 1, opSvf: 4, opLfo: 2, opDrive: 2, opCrush: 2, opVerb: 1, opScale: 2, opPitch: 1}

var shapeNames = []string{shpSine: "sine", shpSaw: "saw", shpPulse: "pulse", shpNoise: "noise"}

// Shots a game registered, beside the built-in shotLib. Registration runs on
// a game's thread while Trigger runs on lg's, hence the lock.
var (
	shotsMu sync.RWMutex
	shotReg = map[string]shotDef{}
)

// HasShot reports whether name is a built-in or registered one-shot.
func HasShot(name string) bool {
	_, ok := lookupShot(name)
	return ok
}

func lookupShot(name string) (shotDef, bool) {
	if d, ok := shotLib[name]; ok {
		return d, true
	}
	shotsMu.RLock()
	defer shotsMu.RUnlock()
	d, ok := shotReg[name]
	return d, ok
}

// ShotNames lists the shots a trigger can name, built-in and registered, sorted.
func ShotNames() []string {
	shotsMu.RLock()
	defer shotsMu.RUnlock()
	names := make([]string, 0, len(shotLib)+len(shotReg))
	for n := range shotLib {
		names = append(names, n)
	}
	for n := range shotReg {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ShotByName returns a built-in or registered shot as a spec.
func ShotByName(name string) (ShotSpec, bool) {
	d, ok := lookupShot(name)
	if !ok {
		return ShotSpec{}, false
	}
	return ShotSpec{Name: name, GateMS: d.gateMS, Units: unitSpecs(d.patch)}, true
}

// RegisterShot makes a spec triggerable by its name, replacing an earlier
// registration of that name. It reports an error, and registers nothing, for
// a built-in's name or a patch the synth could not run safely: the patch runs
// on the audio thread, where a bad one would take the music down with it.
func RegisterShot(s ShotSpec) error {
	p, err := s.patch()
	if err != nil {
		return fmt.Errorf("shot %q: %w", s.Name, err)
	}
	shotsMu.Lock()
	defer shotsMu.Unlock()
	shotReg[s.Name] = shotDef{p, s.GateMS}
	return nil
}

// patch checks the spec and builds its patch.
func (s ShotSpec) patch() (Patch, error) {
	if !validName(s.Name) {
		return nil, fmt.Errorf("a shot needs a name of letters, digits and dashes, as a keyword")
	}
	if _, ok := shotLib[s.Name]; ok {
		return nil, fmt.Errorf("a built-in shot has that name")
	}
	// The gate is counted down in samples and closes at zero; a gate under a
	// sample never closes, and a sustaining voice would hold its slot forever.
	if !(s.GateMS >= 1 && s.GateMS <= 10000) {
		return nil, fmt.Errorf("gate %v ms: want 1 to 10000", s.GateMS)
	}
	return buildPatch(s.Units, shotBounds)
}

// bounds are the limits that differ between a one-shot and a music patch:
// the longest release (see UnitSpec.unit) and the loudest out.
type bounds struct{ release, gain float64 }

var (
	shotBounds = bounds{release: 2, gain: 2}
	// The music's patches pass these, and the round trips in
	// palettespec_test.go hold them to it: the beds release over 2.5 to 6 s,
	// and void's rumble, a sub-bass the ear hears little of, outs at 2.5.
	musicBounds = bounds{release: 10, gain: 4}
)

// buildPatch checks units as a patch the synth can run safely within b, and
// builds it. Its other checks hold for any patch, since every patch runs on
// the audio thread, where a bad one takes the music down too.
func buildPatch(units []UnitSpec, b bounds) (Patch, error) {
	if len(units) == 0 || len(units) > maxUnits {
		return nil, fmt.Errorf("%d units: want 1 to %d", len(units), maxUnits)
	}
	p := make(Patch, len(units))
	envs, outs := 0, 0
	var w stackWalk
	for i, us := range units {
		u, err := us.unit(b)
		if err != nil {
			return nil, fmt.Errorf("unit %d (%s): %w", i, us.Op, err)
		}
		if err := w.step(u); err != nil {
			return nil, fmt.Errorf("unit %d (%s): %w", i, us.Op, err)
		}
		envs += b2i(u.op == opEnv)
		outs += b2i(u.op == opOut)
		p[i] = u
	}
	if envs == 0 || outs == 0 {
		return nil, fmt.Errorf("a patch needs an env, which ends the sound, and an out, which makes it heard")
	}
	return p, nil
}

// stackWalk is the patch's stack as buildPatch counts it, unit by unit. The
// VM's stack holds eight and checks nothing, so the spec must.
type stackWalk struct {
	depth int
	// whether each stack slot carries signal (from an osc or pluck) or a
	// control value (an env or lfo): an offset is DC on a signal, which the
	// envelope does not shape and which clicks when the voice ends
	signal [9]bool
}

// step runs one unit over the stack, or says why the unit cannot run there.
func (w *stackWalk) step(u Unit) error {
	need := 0 // values the unit reads off the stack
	switch u.op {
	case opMul:
		need = 2
	case opOut, opLpf, opHpf, opSend, opDrive, opCrush, opVerb, opScale, opPitch:
		need = 1
	case opSvf:
		need = 1
		if u.p[3] != 0 { // the modulation value, then the signal
			need = 2
		}
	}
	if w.depth < need {
		return fmt.Errorf("the stack would run dry")
	}
	if u.op == opScale && u.p[1] != 0 && w.signal[w.depth-1] {
		return fmt.Errorf("an offset on a signal is DC; scale an env or lfo, then mul it in")
	}
	switch u.op {
	case opEnv, opOsc, opPluck, opLfo:
		if w.depth < len(w.signal) {
			w.signal[w.depth] = u.op == opOsc || u.op == opPluck
		}
		w.depth++
	case opMul:
		w.depth--
		w.signal[w.depth-1] = w.signal[w.depth-1] || w.signal[w.depth]
	case opOut, opPitch:
		w.depth--
	case opSvf:
		w.depth -= need - 1
	}
	if w.depth > 8 {
		return fmt.Errorf("the stack would overflow")
	}
	return nil
}

// StackStep is the stack after one unit of a patch, as StackTrace walks it.
type StackStep struct {
	// Signal has a slot per value on the stack, bottom first: true where
	// the value is signal (from an osc or pluck), false where it is control
	// (from an env or lfo).
	Signal []bool
	// Error is why the unit cannot run, as registering the patch would
	// report it; Signal is empty then.
	Error string
}

// StackTrace walks units as registering them as a patch does and returns the
// stack after each, for an editor to draw the engine's answer rather than a
// copy of its rules. The walk stops at the first unit that fails, so the
// trace is shorter than units when one does. A unit's args are held to a
// music patch's bounds, the wider ones: a shot's tighter release and gain
// are for registering it to refuse, and the stack does not depend on them.
// What a whole patch needs (its count of units, an env and an out) is
// registering's to report too.
func StackTrace(units []UnitSpec) []StackStep {
	var w stackWalk
	out := make([]StackStep, 0, len(units))
	for _, us := range units {
		u, err := us.unit(musicBounds)
		if err == nil {
			err = w.step(u)
		}
		if err != nil {
			return append(out, StackStep{Signal: []bool{}, Error: err.Error()})
		}
		out = append(out, StackStep{Signal: append([]bool{}, w.signal[:w.depth]...)})
	}
	return out
}

// validName keeps a name one EDN reads back as a keyword.
func validName(n string) bool {
	for i, c := range n {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return n != ""
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// unit checks one unit's op and arguments and builds it.
func (us UnitSpec) unit(b bounds) (Unit, error) {
	op := indexOf(opNames, us.Op)
	if op < 0 {
		return Unit{}, fmt.Errorf("unknown op %q", us.Op)
	}
	if want := opArgs[op]; len(us.Args) != want {
		return Unit{}, fmt.Errorf("%d args, want %d", len(us.Args), opArgs[op])
	}
	for _, a := range us.Args {
		if math.IsNaN(a) || math.IsInf(a, 0) {
			return Unit{}, fmt.Errorf("args must be finite")
		}
	}
	u := Unit{op: op}
	copy(u.p[:], us.Args)
	a := u.p
	ok, rule := true, ""
	switch op {
	case opEnv:
		// Decay and release divide each step by their length in samples;
		// under a sample, a step overshoots and the envelope blows up.
		// Attack is offset by a sample, so any length is stable. A voice
		// lives until its gate closes and then until the release has fallen
		// to 1e-4, about 9.2 releases, so the gate's 10 s and a 2 s release
		// keep a shot from holding a voice past about half a minute. A music
		// patch's voice is its track's alone, and a retrigger fades it out
		// whatever its envelope, so its release may run longer: the beds
		// release over 2.5 to 6 s.
		ok = a[0] >= 0 && a[0] <= 10 && a[1] >= 0.001 && a[1] <= 10 && a[2] >= 0 && a[2] <= 1 && a[3] >= 0.001 && a[3] <= b.release
		rule = fmt.Sprintf("attack 0..10 s, decay 0.001..10 s, release 0.001..%g s, sustain 0..1", b.release)
	case opOsc:
		sh := indexOf(shapeNames, us.Shape)
		if sh < 0 {
			return Unit{}, fmt.Errorf("unknown shape %q", us.Shape)
		}
		u.p = [4]float64{a[0], float64(sh), a[1], a[2]}
		ok = math.Abs(a[0]) <= 48 && math.Abs(a[1]) <= 48 && a[2] >= 0
		rule = "transpose and sweep within 48 semitones, sweep decay >= 0"
	case opPluck:
		// the string's delay line is a period long, allocated on the audio
		// thread at the first note, so how far down it may go is bounded
		ok = math.Abs(a[0]) <= 24 && a[1] >= 0 && a[1] < 1
		rule = "transpose within 24 semitones, feedback 0 to under 1"
	case opLpf, opHpf:
		ok = a[0] > 0 && a[0] <= SampleRate/2
		rule = fmt.Sprintf("a cutoff above 0 and at most %d Hz", SampleRate/2)
	case opSvf:
		ok = a[0] > 0 && a[0] <= SampleRate/2 && a[1] >= 0 && a[1] <= 1 && whole(a[2], 2) && math.Abs(a[3]) <= 8
		rule = fmt.Sprintf("a cutoff above 0 and at most %d Hz, resonance 0..1, mode 0, 1 or 2, depth within 8 octaves", SampleRate/2)
	case opLfo:
		ok = a[0] > 0 && a[0] <= 100 && whole(a[1], 2)
		rule = "a rate above 0 and at most 100 Hz, shape 0, 1 or 2"
	case opDrive:
		ok = a[0] >= 0.1 && a[0] <= 20 && math.Abs(a[1]) <= 1
		rule = "drive 0.1..20, bias -1..1"
	case opCrush:
		ok = a[0] >= 50 && a[0] <= SampleRate && a[1] >= 1 && a[1] <= 16
		rule = fmt.Sprintf("hold 50..%d Hz, bits 1..16", SampleRate)
	case opScale:
		// bounded so a scaled value stays near an envelope's range: an
		// offset left on a signal is DC the mix would carry
		ok = math.Abs(a[0]) <= 2 && math.Abs(a[1]) <= 2
		rule = "gain and offset within 2"
	case opPitch:
		ok = math.Abs(a[0]) <= 12
		rule = "depth within 12 semitones"
	case opSend, opVerb:
		ok = a[0] >= 0 && a[0] <= 1
		rule = "an amount 0..1"
	case opOut:
		ok = a[0] >= 0 && a[0] <= b.gain && a[1] >= 0 && a[1] <= 1
		rule = fmt.Sprintf("gain 0..%g, pan 0..1", b.gain)
	}
	if !ok {
		return Unit{}, fmt.Errorf("%v: want %s", us.Args, rule)
	}
	return u, nil
}

// whole reports whether x is one of 0..n.
func whole(x float64, n int) bool {
	return x >= 0 && x <= float64(n) && x == math.Trunc(x)
}

func indexOf(names []string, n string) int {
	for i, s := range names {
		if s == n {
			return i
		}
	}
	return -1
}

// EDN writes the spec as an lg map, the form undertone/register-shot takes.
func (s ShotSpec) EDN() string {
	return fmt.Sprintf("{:name :%s :gate-ms %s\n :patch %s}", s.Name, ednNum(s.GateMS), unitsEDN(s.Units, "         "))
}

// ednNum writes a float as lg reads one back: with a point, so 1 stays a float.
func ednNum(f float64) string {
	t := fmt.Sprintf("%g", f)
	if !strings.ContainsAny(t, ".e") {
		t += ".0"
	}
	return t
}

// unitsEDN writes units as an lg patch vector, a unit to a line after the
// first, each indented by indent.
func unitsEDN(units []UnitSpec, indent string) string {
	var b strings.Builder
	b.WriteString("[")
	for i, u := range units {
		if i > 0 {
			b.WriteString("\n" + indent)
		}
		b.WriteString("[:" + u.Op)
		for j, a := range u.Args {
			if u.Op == "osc" && j == 1 {
				b.WriteString(" :" + u.Shape)
			}
			b.WriteString(" " + ednNum(a))
		}
		b.WriteString("]")
	}
	b.WriteString("]")
	return b.String()
}
