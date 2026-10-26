// One-shot sfx: a small voice lane beside the sequencer.
//
// The sequencer's loops never see these voices — step() is bounded by nTracks
// and the shots live in their own array — so a one-shot can never perturb the
// music's rng streams or pattern content. That property is by construction,
// and TestGainZeroShotLeavesMusicByteIdentical pins it.
//
// Panning is per voice, applied outside the patch: the voice renders through
// its patch (authored center), the stereo pair is mono-summed, and the same
// linear law opOut uses splits it. That keeps pan out of the per-sample unit
// VM entirely.
package engine

import (
	"math"
	"sync"
	"sync/atomic"
)

// ---- one-shot patches ----
// Same recipe book as the leads: filtered noise reads as impact, a swept sine
// as weight. Quiet is a design property of the footstep, not a mix decision —
// it plays every turn.

// footstep: a bright tick, a shorter body under it, and only a trace of the
// low knock. The shape is set by where it has to be HEARD, not by what a boot
// on stone looks like in isolation. Measured against the depth-2 bed, the
// first cut put 89% of its energy below 120 Hz — 17 dB under the drone, in
// the one band the bed owns outright and a phone speaker cannot reproduce at
// all. It was inaudible twice over. Energy above 350 Hz costs nothing there:
// the bed's own noise and drone are lowpassed at 280-300, so the step has
// that range to itself. See TestFootstepIsHeardOverTheBed.
var footstep = Patch{
	// tick: the part that cuts. Short and bright, one pole so it keeps top end.
	{opEnv, [4]float64{0.001, 0.028, 0, 0.02}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{3000, 0, 0, 0}},
	{opOut, [4]float64{0.5, 0.5}},
	// body: boot on stone rather than a click, in a band a phone still has.
	{opEnv, [4]float64{0.001, 0.05, 0, 0.03}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{900, 0, 0, 0}},
	{opLpf, [4]float64{900, 0, 0, 0}},
	{opOut, [4]float64{0.75, 0.5}},
	// knock: kept, quietly, so the step still has weight on headphones.
	{opEnv, [4]float64{0.001, 0.05, 0, 0.03}},
	{opOsc, [4]float64{0, shpSine, 5, 0.02}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.22, 0.5}},
}

// hit: the classic impact — a sine thump falling fast from above, plus a
// bright noise crack. A small send puts it in the same room as the music.
var hit = Patch{
	{opEnv, [4]float64{0.001, 0.11, 0, 0.05}},
	{opOsc, [4]float64{0, shpSine, 26, 0.03}},
	{opMul, [4]float64{}},
	{opSend, [4]float64{0.15, 0, 0, 0}},
	{opOut, [4]float64{0.8, 0.5}},
	{opEnv, [4]float64{0.001, 0.045, 0, 0.03}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{2800, 0, 0, 0}},
	{opOut, [4]float64{0.5, 0.5}},
}

// thud: damage you TAKE. The deliberate inverse of hit — no bright crack, a
// slower body, and a low filtered noise wash under it. Dealt damage should
// read as impact you caused; taken damage as something landing on you.
var thud = Patch{
	{opEnv, [4]float64{0.002, 0.22, 0, 0.12}},
	{opOsc, [4]float64{-7, shpSine, 14, 0.06}},
	{opMul, [4]float64{}},
	{opSend, [4]float64{0.2, 0, 0, 0}},
	{opOut, [4]float64{0.95, 0.5}},
	{opEnv, [4]float64{0.004, 0.14, 0, 0.08}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{500, 0, 0, 0}},
	{opLpf, [4]float64{500, 0, 0, 0}},
	{opOut, [4]float64{0.75, 0.5}},
}

// creak: a door. A saw gliding UP through a wooden lowpass with a breath of
// noise over it — the glide is what says hinge rather than tone. Sweep params
// are the starting offset in semitones and how fast it decays back to the
// note, so a negative offset rises and a positive one falls.
var creak = Patch{
	{opEnv, [4]float64{0.02, 0.30, 0, 0.14}},
	{opOsc, [4]float64{0, shpSaw, -7, 0.30}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{1100, 0, 0, 0}},
	{opSend, [4]float64{0.25, 0, 0, 0}},
	{opOut, [4]float64{0.34, 0.5}},
	{opEnv, [4]float64{0.015, 0.22, 0, 0.1}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{2000, 0, 0, 0}},
	{opOut, [4]float64{0.16, 0.5}},
}

// cry: something died. A falling saw with an inharmonic partial over it, long
// enough to read as a voice rather than an impact, and sent hard to the delay
// so it belongs to the room the music is in.
var cry = Patch{
	{opEnv, [4]float64{0.012, 0.45, 0, 0.28}},
	{opOsc, [4]float64{12, shpSaw, 13, 0.28}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{2400, 0, 0, 0}},
	{opSend, [4]float64{0.4, 0, 0, 0}},
	{opOut, [4]float64{0.4, 0.5}},
	{opEnv, [4]float64{0.012, 0.3, 0, 0.2}},
	{opOsc, [4]float64{19.02, shpSine, 13, 0.28}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.16, 0.5}},
}

// sink / lift: changing floor. The longest sounds in the set, because they
// score a transition rather than an action — a noise wash for the movement
// and a swept sine for the direction, falling to go down and rising to come
// up. The bright layer is what survives a phone speaker.
var sink = Patch{
	{opEnv, [4]float64{0.05, 0.5, 0, 0.4}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{2600, 0, 0, 0}},
	{opOut, [4]float64{0.3, 0.5}},
	{opEnv, [4]float64{0.06, 0.55, 0, 0.4}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{700, 0, 0, 0}},
	{opSend, [4]float64{0.45, 0, 0, 0}},
	{opOut, [4]float64{0.5, 0.5}},
	{opEnv, [4]float64{0.04, 0.6, 0, 0.45}},
	{opOsc, [4]float64{-5, shpSine, 15, 0.4}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.4, 0.5}},
}

var lift = Patch{
	{opEnv, [4]float64{0.05, 0.5, 0, 0.4}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{3000, 0, 0, 0}},
	{opOut, [4]float64{0.32, 0.5}},
	{opEnv, [4]float64{0.06, 0.55, 0, 0.4}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{900, 0, 0, 0}},
	{opSend, [4]float64{0.45, 0, 0, 0}},
	{opOut, [4]float64{0.42, 0.5}},
	{opEnv, [4]float64{0.04, 0.6, 0, 0.45}},
	{opOsc, [4]float64{-5, shpSine, -15, 0.4}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.4, 0.5}},
}

// thunk: the game said no. Short, dull and unmusical on purpose — a refusal
// should not be pleasant enough to want to hear again — but with enough top
// end to actually register on a phone.
var thunk = Patch{
	{opEnv, [4]float64{0.002, 0.08, 0, 0.05}},
	{opOsc, [4]float64{0, shpSine, 7, 0.025}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.55, 0.5}},
	{opEnv, [4]float64{0.002, 0.05, 0, 0.03}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{1500, 0, 0, 0}},
	{opOut, [4]float64{0.3, 0.5}},
}

// snap: a web tearing. Dry and bright and over immediately — the one sound
// here with no low end at all, which is what keeps it from reading as a hit.
var snap = Patch{
	{opEnv, [4]float64{0.001, 0.05, 0, 0.03}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{4200, 0, 0, 0}},
	{opOut, [4]float64{0.6, 0.5}},
	{opEnv, [4]float64{0.001, 0.12, 0, 0.06}},
	{opOsc, [4]float64{12, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{2200, 0, 0, 0}},
	{opOut, [4]float64{0.22, 0.5}},
}

// twang: a bow releasing. Karplus-Strong with the feedback pulled well down,
// so the string dies in a fraction of a second instead of ringing like the
// pickup chime, plus a noise transient for the release itself.
var twang = Patch{
	{opEnv, [4]float64{0.001, 0.22, 0, 0.1}},
	{opPluck, [4]float64{0, 0.972, 0, 0}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{3200, 0, 0, 0}},
	{opSend, [4]float64{0.2, 0, 0, 0}},
	{opOut, [4]float64{0.6, 0.5}},
	{opEnv, [4]float64{0.001, 0.03, 0, 0.02}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{3800, 0, 0, 0}},
	{opOut, [4]float64{0.3, 0.5}},
}

// ---- arcade shots ----
// Game-agnostic one-shots for action games rather than a dungeon: a bounce, a
// break, a launch and a loss. All four are meant to be pitched by the caller
// (usually by scale degree), so a run of them plays a phrase in the music's
// key instead of the same note over and over. First consumer: tesserae.

// blip: a bounce. A square tick with the top shaved off, short enough to fire
// several times a second without smearing into a tone.
var blip = Patch{
	{opEnv, [4]float64{0.001, 0.05, 0, 0.03}},
	{opOsc, [4]float64{0, shpPulse, 0, 1}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{3200, 0, 0, 0}},
	{opOut, [4]float64{0.28, 0.5}},
	{opEnv, [4]float64{0.001, 0.04, 0, 0.02}},
	{opOsc, [4]float64{-12, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.3, 0.5}},
}

// tink: something breaking that you meant to break. Two inharmonic sines
// like the bell lead but with a fraction of its decay, plus a noise flick for
// the crack, and a send so a cascade of them blooms in the delay.
var tink = Patch{
	{opEnv, [4]float64{0.001, 0.32, 0, 0.18}},
	{opOsc, [4]float64{0, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opSend, [4]float64{0.3, 0, 0, 0}},
	{opOut, [4]float64{0.4, 0.5}},
	{opEnv, [4]float64{0.001, 0.12, 0, 0.08}},
	{opOsc, [4]float64{19.08, shpSine, 0, 1}},
	{opMul, [4]float64{}},
	{opOut, [4]float64{0.16, 0.5}},
	{opEnv, [4]float64{0.001, 0.02, 0, 0.01}},
	{opOsc, [4]float64{0, shpNoise, 0, 1}},
	{opMul, [4]float64{}},
	{opHpf, [4]float64{2500, 0, 0, 0}},
	{opOut, [4]float64{0.25, 0.5}},
}

// zap: a launch. A pulse rising two octaves into the note, fast, so it reads
// as "away" rather than as a pitch.
var zap = Patch{
	{opEnv, [4]float64{0.002, 0.14, 0, 0.06}},
	{opOsc, [4]float64{0, shpPulse, -24, 0.05}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{2800, 0, 0, 0}},
	{opSend, [4]float64{0.2, 0, 0, 0}},
	{opOut, [4]float64{0.32, 0.5}},
}

// fall: a loss. A saw sliding two octaves down through a lowpass,
// slow enough to hear the whole drop; the inverse of zap.
var fall = Patch{
	{opEnv, [4]float64{0.005, 0.7, 0, 0.3}},
	{opOsc, [4]float64{0, shpSaw, 24, 0.3}},
	{opMul, [4]float64{}},
	{opLpf, [4]float64{1400, 0, 0, 0}},
	{opSend, [4]float64{0.35, 0, 0, 0}},
	{opOut, [4]float64{0.5, 0.5}},
}

// shotDef pairs a patch with its gate length. One-shots are not on the
// sequencer grid, so nothing counts gate rows for them; the gate is in
// milliseconds and expires in frame().
type shotDef struct {
	patch  Patch
	gateMS float64
}

var shotLib = map[string]shotDef{
	"footstep": {footstep, 15},
	"hit":      {hit, 25},
	"thud":     {thud, 40},
	"creak":    {creak, 120},
	"cry":      {cry, 180},
	"sink":     {sink, 260},
	"lift":     {lift, 260},
	"thunk":    {thunk, 20},
	"snap":     {snap, 15},
	"twang":    {twang, 30},
	// pickup reuses the Karplus-Strong lead: pitched by the caller from the
	// floor's own scale, which is what keeps found-a-rune in key with the score.
	"pluck": {pluck, 60},
	// arcade
	"blip": {blip, 10},
	"tink": {tink, 20},
	"zap":  {zap, 20},
	"fall": {fall, 120},
}

// shotReq is a staged trigger. delay is in samples, counted down in frame(),
// so a two-note pickup arpeggio is sample-accurate rather than host-timed.
type shotReq struct {
	key   shotKey
	patch Patch
	note  int
	gain  float64
	pan   float64
	delay int
	gate  int
	seed  uint64
}

const nShots = 8

// shotGapMS is how soon the same shot at the same note may start again. A
// breakout cascade fires a dozen hits in one frame against nShots voices; the
// repeats add nothing a listener can hear apart, and they would steal every
// other sound in flight. Counted in audio time, when a shot starts, so a
// burst that arrives in one Trigger batch is thinned as surely as one spread
// over frames. A different note is a different sound: a plucked chord keeps
// every note. Of repeats that land in the same frame, the loudest plays, so a
// hard hit is not lost to a soft one queued ahead of it.
const shotGapMS = 30

type shotKey struct {
	name string
	note int
}

// recentShot is a shot that started lately, kept for shotGapMS. They live in
// a fixed ring rather than a map because they are written on the audio
// thread, which must not allocate; a burst of more distinct shots than the
// ring holds only forgets the oldest, letting a repeat of it through.
type recentShot struct {
	key   shotKey
	frame int64
	voice int
	gain  float64
}

const nRecent = 32

// maxPending bounds the queue of shots waiting to start.
const maxPending = 32

// sfx is the Synth's one-shot state, mutex-guarded because Trigger arrives on
// the lg thread while frame() drains on the audio callback goroutine.
type sfx struct {
	mu      sync.Mutex
	pending []shotReq
	waiting atomic.Bool // mirror of len(pending) > 0; lets frame() skip the lock
	shots   [nShots]Voice
	pan     [nShots]float64
	pos     int
	recent  [nRecent]recentShot
	rpos    int
}

// lately finds a start of the same shot within shotGapMS of frame.
func (fx *sfx) lately(k shotKey, frame int64) *recentShot {
	for i := range fx.recent {
		if r := &fx.recent[i]; r.key == k && frame-r.frame < shotGapMS*SampleRate/1000 {
			return r
		}
	}
	return nil
}

// Shot is one trigger-map's worth of intent, as the sfx vocabulary expresses
// it. Every host parses its own wire format (let-go values, JS values) into
// this, so the interpretation — above all the degree resolution — happens
// once, here, rather than being mirrored per host and drifting.
type Shot struct {
	Name    string
	Gain    float64
	Pan     float64
	DelayMS float64
	Seed    uint64

	// Pitch arrives one of two ways. Note is absolute, for sounds with no
	// musical context (impacts, footsteps). Degree+Octave is a position in
	// the CURRENT parameters' scale, for sounds that must land in key; the
	// synth resolves it against the same scale that pitched the music, which
	// is why resolution belongs here and not in a host.
	Note      int
	Degree    int
	Octave    int
	HasNote   bool
	HasDegree bool
}

// TriggerShot stages a Shot, resolving a scale degree if that is how pitch
// was expressed. Returns false if the patch name is unknown.
func (s *Synth) TriggerShot(sh Shot) bool {
	note := 60
	switch {
	case sh.HasNote:
		note = sh.Note
	case sh.HasDegree:
		note = s.resolveDegree(sh.Degree, sh.Octave)
	}
	return s.Trigger(sh.Name, note, sh.Gain, sh.Pan, sh.DelayMS, sh.Seed)
}

// resolveDegree maps a scale degree in the current parameters' scale to an
// absolute note, wrapping octaves past the top of the scale.
func (s *Synth) resolveDegree(degree, octave int) int {
	// Read through the atomics: this runs on the caller's goroutine while
	// the synth renders on another.
	p := *s.cur.Load()
	if n := s.next.Load(); n != nil {
		// Staged parameters have not reached a pattern boundary yet, but a
		// one-shot fires now — pitch it against what the player is about to
		// hear rather than the phrase that is ending.
		p = n.sanitized()
	}
	if degree < 0 {
		degree = 0
	}
	if len(p.Scale) == 0 {
		return p.Root + 12*octave
	}
	return p.Root + 12*octave + scaleTone(p.Scale, degree)
}

// Trigger stages a one-shot. Unknown names are dropped, not errors: the sfx
// vocabulary lives above this seam, and a stale name must not crash a game.
// gain scales the patch's own levels; pan is 0..1; delayMS offsets the start.
func (s *Synth) Trigger(name string, note int, gain, pan, delayMS float64, seed uint64) bool {
	d, ok := lookupShot(name)
	if !ok {
		return false
	}
	if pan < 0 {
		pan = 0
	} else if pan > 1 {
		pan = 1
	}
	req := shotReq{
		key:   shotKey{name, note},
		patch: humanize(d.patch, seed),
		note:  note,
		gain:  gain,
		pan:   pan,
		delay: int(delayMS * SampleRate / 1000),
		gate:  int(d.gateMS * SampleRate / 1000),
		seed:  seed,
	}
	s.fx.mu.Lock()
	// While nothing renders (stopped, or a phone has suspended the page's
	// audio) the queue only grows; keep the newest, which are the ones the
	// player will connect to what is happening when sound returns.
	if len(s.fx.pending) >= maxPending {
		s.fx.pending = append(s.fx.pending[:0], s.fx.pending[1:]...)
	}
	s.fx.pending = append(s.fx.pending, req)
	s.fx.waiting.Store(true)
	s.fx.mu.Unlock()
	return true
}

// humanize nudges a copy of the patch's envelope decays and filter cutoffs,
// deterministically from the trigger seed. This is what keeps a footstep every
// turn from machine-gunning: the patch tables stay fixed, the instance varies.
func humanize(p Patch, seed uint64) Patch {
	r := rng{seed*2862933555777941757 + 3037000493 | 1}
	out := make(Patch, len(p))
	copy(out, p)
	for i := range out {
		switch out[i].op {
		case opEnv:
			out[i].p[1] *= 0.9 + 0.2*r.f()
		case opLpf:
			out[i].p[0] *= 0.85 + 0.3*r.f()
		}
	}
	return out
}

// runShots activates due triggers and mixes the live one-shot voices.
// Called once per frame; the fast path (no shots, nothing pending) is two
// comparisons and no lock.
func (s *Synth) runShots(l, r *float64) {
	fx := &s.fx
	if fx.waiting.Load() {
		fx.mu.Lock()
		keep := fx.pending[:0]
		for _, q := range fx.pending {
			if q.delay > 0 {
				q.delay--
				keep = append(keep, q)
				continue
			}
			i := -1
			if r := fx.lately(q.key, s.nframe); r != nil {
				if r.frame != s.nframe || q.gain <= r.gain {
					continue
				}
				i, r.gain = r.voice, q.gain // a louder repeat in the same frame takes over
			} else {
				i = fx.voice()
				fx.recent[fx.rpos] = recentShot{q.key, s.nframe, i, q.gain}
				fx.rpos = (fx.rpos + 1) % nRecent
			}
			fx.shots[i].trigger(q.patch, q.note, 0, q.gain, q.seed)
			fx.shots[i].gateSamples = q.gate
			fx.pan[i] = q.pan
		}
		fx.pending = keep
		fx.waiting.Store(len(keep) > 0)
		fx.mu.Unlock()
	}
	for i := range fx.shots {
		v := &fx.shots[i]
		if !v.active {
			continue
		}
		if v.gateSamples > 0 {
			v.gateSamples--
			if v.gateSamples == 0 {
				v.gate = false
			}
		}
		var sl, sr float64
		v.run(&sl, &sr, &s.bus)
		mono := sl + sr
		*l += mono * (1 - fx.pan[i])
		*r += mono * fx.pan[i]
	}
}

// voice picks the shot voice for a new trigger: a free one if there is one,
// else the quietest, so a pool overflow cuts the sound least likely to be
// missed rather than whichever is next in turn.
func (fx *sfx) voice() int {
	best, level := 0, math.Inf(1)
	for k := 0; k < nShots; k++ {
		i := (fx.pos + k) % nShots
		v := &fx.shots[i]
		if !v.active {
			best = i
			break
		}
		if l := v.level(); l < level {
			best, level = i, l
		}
	}
	fx.pos = best + 1
	return best
}
