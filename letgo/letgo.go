// Package letgo binds the undertone engine to let-go as the `undertone`
// namespace: the music/one-shot state, the lg-facing natives, and the loader
// for the embedded lg sources.
//
// Importing the package registers the namespace (see init below), so an lg
// runtime that links it needs no generated interop: lg code calls
// undertone/set-parameters, undertone/set-parameters-now,
// undertone/music-start, undertone/music-stop,
// undertone/set-music-gain, undertone/glide-music-gain, undertone/set-sfx-gain,
// undertone/glide-sfx-gain, undertone/set-queue,
// undertone/trigger, undertone/register-shot, undertone/register-palette,
// undertone/register-lead and undertone/render-wav
// directly. The natives act
// on a default Music until a Go host points them elsewhere with SetMusic.
//
// It also registers the embedded lg sources, so (require 'undertone.ambience)
// resolves in any binary that links the package: a cli.Main host, a custom
// lg, a browser build, with no source path.
package letgo

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/rt"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/letgo/lg"
)

// Music holds the host side of the audio surface. The mutex guards the
// lg-facing methods against the output reading the synth.
type Music struct {
	mu     sync.Mutex
	params engine.Params
	synth  *engine.Synth
	out    output

	// starting guards the window between opening the device and the unlock
	// gesture arriving, so a second Start cannot open a second context.
	starting bool
	// nowWaiting is a set-parameters-now that came during that window; the
	// catch-up applies it at once rather than at the first boundary.
	nowWaiting bool
	// paused is a Pause not yet resumed: the output has stopped reading, so
	// the meters hold their last values, which the level natives must not
	// report as sounding.
	paused bool
	// paramsGen counts changes to params, so start can tell whether any came
	// while the output opened.
	paramsGen uint64

	// gains are the last set-/glide-music-gain and -sfx-gain, carried into a
	// synth that starts later.
	gains [nBuses]busGain
	// opening is the synth start built while its output opens (a browser
	// waiting for the unlock gesture): gain requests go straight to it, in
	// the order the game made them, as they do to a playing synth.
	opening *engine.Synth

	// theme is an undertone/play-theme that came before music-start; it
	// starts with the music.
	theme *engine.Theme

	// queue is the last undertone/set-queue, applied to an output that
	// starts later; zero leaves the output's own default.
	queue time.Duration
}

// NewMusic starts from base parameters; the lg script is expected to
// overwrite every scalar via undertone/set-parameters.
func NewMusic(p engine.Params) *Music {
	return &Music{params: p}
}

// Params returns a snapshot of the current parameter state — the demo trace
// reads BPM and PulseGain off it. Read-only by construction: it is a copy,
// scale slice included.
func (m *Music) Params() engine.Params {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.params
	p.Scale = slices.Clone(p.Scale)
	return p
}

// setParameters merges a partial lg map over current state and stages it.
// Unknown keys are ignored (forward compatibility) and absent keys keep their
// current value, so live-coding is just calling it again with what changed.
// An unknown :palette is an exception: it comes from an exported mood, which
// MoodSpec.Derive rejects for the same name, so it is an error here too rather
// than a mood playing on whatever patches were loaded before. A :mute layer
// name that isn't one is the other, so a typo can't leave a layer playing.
//
// With now, the change is heard within a block rather than at the next
// pattern boundary (engine.Synth.SetParametersNow).
func (m *Music) setParameters(v vm.Value, now bool) (vm.Value, error) {
	lgm, ok := v.(*vm.PersistentMap)
	if !ok {
		return vm.NIL, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := mergeParams(&m.params, lgm); err != nil {
		fn := "set-parameters"
		if now {
			fn += "-now"
		}
		return vm.NIL, fmt.Errorf("undertone/%s: %w", fn, err)
	}
	m.paramsGen++
	switch {
	case m.synth == nil:
		m.nowWaiting = m.nowWaiting || now && m.starting
	case now:
		m.synth.SetParametersNow(m.params)
	default:
		m.synth.SetParameters(m.params)
	}
	return vm.NIL, nil
}

// mergeParams merges a partial lg map over p. Unknown keys are ignored
// (forward compatibility) and absent keys keep their current value. An unknown
// :palette or :mute layer is an error, checked before p changes, so a rejected
// map leaves nothing behind for the next call to play.
func mergeParams(p *engine.Params, lgm *vm.PersistentMap) error {
	if err := retiredKey(lgm, "bell-density", "bell-lift"); err != nil {
		return err
	}
	var pal *engine.Palette
	if s, ok := kwStr(lgm, "palette"); ok {
		bank, ok := engine.PaletteByName(s)
		if !ok {
			return fmt.Errorf("unknown palette %q", s)
		}
		pal = &bank
	}
	mute, setMute := p.Mute, false
	if v := at(lgm, "mute"); v != vm.NIL { // absent keeps the current mute, as every key does
		m, err := muteLayers(v)
		if err != nil {
			return err
		}
		mute, setMute = m, true
	}
	// :mood selects the patch palette, which is the one thing not expressible
	// as a scalar in the map. Deriving a throwaway params for the mood is how
	// the host gets that palette without exporting the patch tables.
	if s, ok := kwStr(lgm, "mood"); ok {
		if d, ok := engine.DeriveMood(s, 1, 0, 0); ok {
			// The palette's lead track holds the mood's seed-1 lead, so its name and
			// gate come too; a :lead below still overrides all three.
			p.Palette = d.Palette
			p.LeadName, p.LeadGate = d.LeadName, d.LeadGate
		}
	}
	// :palette names a bank directly, for a mood the host has no row for. The
	// bank's lead track takes the current lead, since a partial map
	// need not carry :lead.
	if pal != nil {
		p.Palette = *pal
		engine.ApplyLead(p, p.LeadName)
	}
	flt(lgm, "bpm", &p.BPM)
	integer(lgm, "root", &p.Root)
	flt(lgm, "drone-gain", &p.DroneGain)
	flt(lgm, "fifth-gain", &p.FifthGain)
	flt(lgm, "pad-density", &p.PadDensity)
	flt(lgm, "lead-density", &p.LeadDensity)
	flt(lgm, "air-gain", &p.AirGain)
	flt(lgm, "pulse-gain", &p.PulseGain)
	flt(lgm, "grit", &p.Grit)
	flt(lgm, "open", &p.Open)
	flt(lgm, "room-size", &p.Room.Size)
	flt(lgm, "room-damp", &p.Room.Damp)
	integer(lgm, "fifth-offset", &p.FifthOffset)
	integer(lgm, "lead-lift", &p.LeadLift)
	if sc, ok := ints(lgm, "scale"); ok {
		p.Scale = sc
	}
	if s, ok := kwStr(lgm, "groove"); ok {
		if s == "none" {
			s = ""
		}
		p.Groove = s
	}
	flt(lgm, "drum-gain", &p.DrumGain)
	flt(lgm, "drum-busy", &p.DrumBusy)
	flt(lgm, "swing", &p.Swing)
	flt(lgm, "lead-db", &p.LeadDB)
	flt(lgm, "pad-db", &p.PadDB)
	flt(lgm, "bass-db", &p.BassDB)
	if setMute {
		p.Mute = mute
	}
	if s, ok := kwStr(lgm, "lead"); ok {
		if s == "none" {
			// As a lead-less mood derives: no name, so the generator plays
			// nothing, and the bell lead back on the lead track, where a
			// grid's authored lead plays. Every bank, registered ones too, starts
			// on the bell lead, so with no :palette here any bank's will do.
			bank, _ := engine.PaletteByName("dungeon")
			if pal != nil {
				bank = *pal
			}
			p.LeadName, p.LeadGate, p.Palette.Lead = "", 0, bank.Lead
		} else {
			engine.ApplyLead(p, s)
		}
	}
	// :seed is int64 on the lg side; the engine's rng streams are uint64.
	var seed int
	if integer(lgm, "seed", &seed) {
		p.Seed = uint64(seed)
	}
	return nil
}

// trigger accepts one trigger map or a vector of them (the shape
// sfx/for-events returns) and stages each as a one-shot. Fire-and-forget by
// contract: with no synth running (music not started, or a -dry demo) the
// triggers drop silently, because the write-only rule means nothing upstream
// may ever learn whether a sound played.
func (m *Music) trigger(v vm.Value) vm.Value {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.synth == nil {
		return vm.NIL
	}
	for _, sh := range shots(v) {
		m.synth.TriggerShot(sh)
	}
	return vm.NIL
}

// shots reads one trigger map or a vector of them; a map without :shot is
// skipped.
func shots(v vm.Value) []engine.Shot {
	var out []engine.Shot
	var maps []*vm.PersistentMap
	if pm, ok := v.(*vm.PersistentMap); ok {
		maps = append(maps, pm)
	} else if vec, ok := asVec(v); ok {
		for _, e := range vec {
			if pm, ok := e.(*vm.PersistentMap); ok {
				maps = append(maps, pm)
			}
		}
	}
	for _, lgm := range maps {
		name, ok := kwStr(lgm, "shot")
		if !ok {
			continue
		}
		sh := engine.Shot{Name: name, Gain: 1, Pan: 0.5}
		flt(lgm, "gain", &sh.Gain)
		flt(lgm, "pan", &sh.Pan)
		flt(lgm, "delay-ms", &sh.DelayMS)
		if integer(lgm, "note", &sh.Note) {
			sh.HasNote = true
		} else if integer(lgm, "degree", &sh.Degree) {
			sh.HasDegree = true
			integer(lgm, "octave", &sh.Octave)
		}
		// Seeds may arrive as an lg int or as a decimal string (a seed that
		// has to survive a JSON hop loses int64 precision as a number).
		// Accept both.
		var seed int
		if integer(lgm, "seed", &seed) {
			sh.Seed = uint64(seed)
		} else if s, ok := kwStr(lgm, "seed"); ok {
			if v, err := strconv.ParseInt(s, 10, 64); err == nil {
				sh.Seed = uint64(v)
			}
		}
		out = append(out, sh)
	}
	return out
}

// Start opens the audio device and begins streaming. Exported because the
// demo drivers call it directly — in the browser it must run inside a user
// gesture (autoplay policy), not from lg.
func (m *Music) Start() vm.Value {
	return m.start(openOutput)
}

func (m *Music) start(open func(io.Reader) (output, error)) vm.Value {
	m.mu.Lock()
	if m.out != nil || m.starting {
		m.mu.Unlock()
		return vm.NIL
	}
	m.starting = true
	syn := engine.NewSynth(m.params)
	// Output initialization may render audio before it returns.
	m.applyGains(syn)
	for b := range m.gains {
		m.gains[b].pending = false
	}
	m.opening = syn
	opening := m.theme
	gen := m.paramsGen
	if opening != nil {
		syn.PlayTheme(opening)
	}
	m.mu.Unlock()

	// Deliberately NOT holding the mutex here. In a browser the output is not
	// ready until the user makes an unlocking gesture, which can be never —
	// and holding the lock across that wait deadlocks every other method,
	// including Started(), which callers poll precisely to find out whether
	// this finished. (Cost a real debugging session: it presented as the wasm
	// runtime killing the module for deadlock, not as a lock-ordering bug.)
	out, err := open(syn)

	m.mu.Lock()
	defer m.mu.Unlock()
	waited := m.nowWaiting
	m.starting, m.nowWaiting = false, false
	m.opening = nil
	if err != nil {
		fmt.Fprintln(os.Stderr, "undertone/music-start:", err)
		return vm.NIL
	}
	m.out = out
	m.synth = syn
	m.paused = false
	// Parameters may have moved while we waited for the gesture; the synth was
	// built from a snapshot, so catch it up to whatever is current. Only if
	// they moved: staging the snapshot again would change nothing heard, but
	// undertone/position would report it pending for the whole first pattern.
	switch {
	case waited:
		m.synth.SetParametersNow(m.params)
	case m.paramsGen != gen:
		m.synth.SetParameters(m.params)
	}
	// A play-theme during the wait replaces the one the synth opened with.
	if m.theme != opening {
		m.synth.PlayTheme(m.theme)
	}
	m.theme = nil
	if m.queue > 0 {
		m.applyQueue()
	}
	return vm.NIL
}

// queuer is an output whose queue can change while it plays: webaudio's.
// Natively the queue is fixed when the device opens (nativeaudio.Options and
// UNDERTONE_PLAYER_BUFFER, or under -tags audio_oto let-go's
// audio.Options and LETGO_AUDIO_QUEUE), so neither native output
// implements it.
type queuer interface {
	SetQueue(time.Duration) time.Duration
}

// setQueue sets how far ahead of the speaker the output keeps audio queued,
// now if playing and from the start otherwise.
func (m *Music) setQueue(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queue = d
	if m.out != nil {
		m.applyQueue()
	}
}

// applyQueue runs with m.mu held. The output clamps to what its ring holds.
// The native stays write-only, so a clamp goes to stderr (the console, in a
// browser build) rather than back to the game.
func (m *Music) applyQueue() {
	q, ok := m.out.(queuer)
	if !ok {
		return
	}
	// The output also floors to whole frames, which is rounding, not a clamp.
	const frame = time.Second / engine.SampleRate
	if got := q.SetQueue(m.queue); got < m.queue-frame || got > m.queue {
		fmt.Fprintf(os.Stderr, "undertone/set-queue: %v clamped to %v\n", m.queue, got)
	}
}

// queueArg reads undertone/set-queue's milliseconds.
func queueArg(v vm.Value) (time.Duration, error) {
	ms, err := numArg("set-queue", "ms", v)
	if err != nil {
		return 0, err
	}
	if !(ms > 0) { // also refuses NaN
		return 0, fmt.Errorf("undertone/set-queue: ms must be positive, got %v", ms)
	}
	// The ring holds about a second, so anything past a minute only risks
	// overflowing the Duration; the output's clamp does the rest.
	ms = min(ms, 60000)
	return time.Duration(ms * float64(time.Millisecond)), nil
}

// bus names one of the synth's two mixer levels.
type bus int

const (
	musicBus bus = iota // the bed, under the one-shots
	sfxBus              // the one-shots, over the bed
	nBuses
)

// busGain is the last level a game asked of one bus. set keeps the zero value
// from meaning silence; secs is the glide asked for, negative for the
// engine's default. start is the last plain set. pending marks a request no
// synth has had yet: a fade asked for before music-start.
type busGain struct {
	g, secs float64
	set     bool
	start   float64
	started bool
	pending bool
}

// request hands one set (secs < 0) or glide to syn.
func request(b bus, syn *engine.Synth, g, secs float64) {
	switch {
	case b == musicBus && secs < 0:
		syn.SetMusicGain(g)
	case b == musicBus:
		syn.GlideMusicGain(g, secs)
	case secs < 0:
		syn.SetSfxGain(g)
	default:
		syn.GlideSfxGain(g, secs)
	}
}

// apply hands the level to a synth that has not rendered. A pending glide
// still plays, from the last set (the engine's rule for a glide before the
// first frame); one some earlier synth already played is just where the
// level ended up, so a restart resumes there instead of fading again.
func (bg busGain) apply(b bus, syn *engine.Synth) {
	switch {
	case !bg.set:
	case bg.pending && bg.secs >= 0:
		if bg.started {
			request(b, syn, bg.start, -1)
		}
		request(b, syn, bg.g, bg.secs)
	default:
		request(b, syn, bg.g, -1)
	}
}

// setGain sets a bus's level (0..1), now if playing and from the start
// otherwise.
func (m *Music) setGain(b bus, g float64) { m.glideGain(b, g, -1) }

// glideGain is setGain over the given seconds while playing; a negative
// seconds means the engine's default glide. With no synth yet it is kept
// for the next music-start.
func (m *Music) glideGain(b bus, g, seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bg := &m.gains[b]
	bg.g, bg.secs, bg.set = g, seconds, true
	if seconds < 0 {
		bg.start, bg.started = g, true
	}
	syn := m.synth
	if syn == nil {
		syn = m.opening
	}
	bg.pending = syn == nil
	if syn != nil {
		request(b, syn, g, seconds)
	}
}

// applyGains carries the levels set so far into syn. Called with m.mu held.
func (m *Music) applyGains(syn *engine.Synth) {
	for b, bg := range m.gains {
		bg.apply(bus(b), syn)
	}
}

// Started reports whether playback is live. Go-side only, deliberately not a
// native: a guest-visible (undertone/playing?) would let game logic branch on audio
// state, which is exactly what the write-only rule forbids.
func (m *Music) Started() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.out != nil
}

// Pause holds the output, and with it the music's clock, until Resume.
// Go-side only, like Started: pausing is the page's business, not the game's.
func (m *Music) Pause() {
	m.mu.Lock()
	o := m.out
	m.paused = o != nil
	m.mu.Unlock()
	if o != nil {
		o.Pause()
	}
}

// Resume continues after Pause. In a browser, call it from the gesture
// handler itself (iOS only resumes audio inside one).
func (m *Music) Resume() {
	m.mu.Lock()
	o := m.out
	m.paused = false
	m.mu.Unlock()
	if o != nil {
		o.Resume()
	}
}

func (m *Music) Stop() vm.Value {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.out != nil {
		m.out.Close()
		m.out = nil
	}
	return vm.NIL
}

// output is where a started Music plays: oto natively, through let-go's
// audio seam when the host is built with -tags audio_oto, and webaudio in the
// browser (output_native.go, output_native_seam.go, output_js.go).
type output interface {
	Close()
	Pause()
	Resume()
}

// shotSpec reads an lg shot map into a spec; RegisterShot judges the patch.
func shotSpec(v vm.Value) (engine.ShotSpec, error) {
	var s engine.ShotSpec
	m, ok := v.(*vm.PersistentMap)
	if !ok {
		return s, fmt.Errorf("want a map, got %s", v.Type().Name())
	}
	if s.Name, ok = kwStr(m, "name"); !ok {
		return s, fmt.Errorf("want :name as a keyword or string")
	}
	if !flt(m, "gate-ms", &s.GateMS) {
		return s, fmt.Errorf("want :gate-ms as a number")
	}
	var err error
	s.Units, err = unitSpecs(at(m, "patch"))
	return s, err
}

// unitSpecs reads an lg patch vector, [[:env ...] [:osc ...] ...], into units.
func unitSpecs(v vm.Value) ([]engine.UnitSpec, error) {
	units, ok := asVec(v)
	if !ok {
		return nil, fmt.Errorf("want :patch as a vector of units")
	}
	var out []engine.UnitSpec
	for i, uv := range units {
		u, ok := asVec(uv)
		if !ok || len(u) == 0 {
			return nil, fmt.Errorf("unit %d: want a vector such as [:out 0.5 0.5]", i)
		}
		var us engine.UnitSpec
		if k, ok := u[0].(vm.Keyword); ok {
			us.Op = string(k)
		}
		for j, x := range u[1:] {
			switch x := x.(type) {
			case vm.Float:
				us.Args = append(us.Args, float64(x))
			case vm.Int:
				us.Args = append(us.Args, float64(x))
			case vm.Keyword:
				// an osc names its shape second: [:osc transpose :sine sweep decay]
				if us.Op != "osc" || j != 1 {
					return nil, fmt.Errorf("unit %d: %s at %d; only an osc names a shape, second", i, x, j+1)
				}
				us.Shape = string(x)
			default:
				return nil, fmt.Errorf("unit %d: %s is not a number or a shape", i, x.Type().Name())
			}
		}
		out = append(out, us)
	}
	return out, nil
}

// lg map readers.
// Absent keys leave the target untouched; that is the partial-merge contract.

func at(m *vm.PersistentMap, k string) vm.Value {
	return m.ValueAtOr(vm.Keyword(k), vm.NIL)
}

// muteLayers reads :mute, a vector of layer names; [] mutes none. An
// unknown name is an error rather than ignored, since a typo would leave
// the layer playing that the caller meant to silence.
func muteLayers(v vm.Value) (engine.Layers, error) {
	vec, ok := asVec(v)
	if !ok {
		return 0, fmt.Errorf(":mute wants a vector of layer names, got %v", v)
	}
	var m engine.Layers
	for _, e := range vec {
		var name string // :lead or "lead", as kwStr reads every other name
		switch n := e.(type) {
		case vm.Keyword:
			name = string(n)
		case vm.String:
			name = string(n)
		}
		l, ok := muteNames[name]
		if !ok {
			return 0, fmt.Errorf(":mute: unknown layer %v (drone fifth pad lead air pulse drums)", e)
		}
		m |= l
	}
	return m, nil
}

var muteNames = map[string]engine.Layers{
	"drone": engine.LayerDrone, "fifth": engine.LayerFifth, "pad": engine.LayerPad, "lead": engine.LayerLead,
	"air": engine.LayerAir, "pulse": engine.LayerPulse, "drums": engine.LayerDrums,
}

// flt accepts both lg ints and floats, so a derivation may write :bpm 60.
func flt(m *vm.PersistentMap, k string, dst *float64) bool {
	switch v := at(m, k).(type) {
	case vm.Float:
		*dst = float64(v)
	case vm.Int:
		*dst = float64(v)
	default:
		return false
	}
	return true
}

func integer(m *vm.PersistentMap, k string, dst *int) bool {
	v, ok := at(m, k).(vm.Int)
	if !ok {
		return false
	}
	*dst = int(v)
	return true
}

// kwStr reads a key that may be written as either :chime or "chime".
func kwStr(m *vm.PersistentMap, k string) (string, bool) {
	switch v := at(m, k).(type) {
	case vm.Keyword:
		return string(v), true
	case vm.String:
		return string(v), true
	}
	return "", false
}

func ints(m *vm.PersistentMap, k string) ([]int, bool) {
	vec, ok := asVec(at(m, k))
	if !ok {
		return nil, false
	}
	out := make([]int, 0, len(vec))
	for _, e := range vec {
		n, ok := e.(vm.Int)
		if !ok {
			return nil, false
		}
		out = append(out, int(n))
	}
	return out, true
}

// current is the Music instance the audio natives act on. It is indirect
// because namespace registration must happen exactly once per process: a
// second vm.NewNamespace("undertone") does replace the registry entry, but lg code
// loaded against the first one keeps referring to the first one's Vars, so a
// re-register silently leaves earlier callers writing to the abandoned
// instance. Swapping the target instead keeps every caller pointed at the
// live one.
var current atomic.Pointer[Music]

// init registers the namespace directly rather than through
// rt.RegisterInstaller: pkg/rt drains its installer queue during its own
// init, which Go runs before this package's, so a queued installer would
// silently never run. let-go's go-interop guide prescribes the direct call for
// out-of-tree bindings.
func init() {
	current.Store(NewMusic(engine.DeriveDungeon(1, 0, 0)))
	installNS()
	// The name is qualified because a registered namespace shadows a file
	// of the same name on any source path (nooga/let-go#1064).
	for _, s := range lg.Sources {
		rt.RegisterEmbeddedSource(s.Name, s.Text)
	}
}

// SetMusic points the undertone namespace at m. A Go host that wants to own the
// instance (to start it from a user gesture, or read Params for a trace)
// creates its own and calls this before running lg code; until then the
// natives act on a default dungeon-palette Music.
func SetMusic(m *Music) { current.Store(m) }

// CurrentMusic returns the instance the audio natives act on.
func CurrentMusic() *Music { return current.Load() }

// maxRenderSeconds bounds an offline render, which is held in memory whole:
// ten minutes is about 106 MB.
const maxRenderSeconds = 600

func installNS() {
	ns := vm.NewNamespace("undertone")
	ns.Def("set-parameters", native("set-parameters", 1, func(a []vm.Value) (vm.Value, error) {
		return current.Load().setParameters(a[0], false)
	}))
	ns.Def("set-parameters-now", native("set-parameters-now", 1, func(a []vm.Value) (vm.Value, error) {
		return current.Load().setParameters(a[0], true)
	}))
	ns.Def("music-start", native("music-start", 0, func([]vm.Value) (vm.Value, error) {
		return current.Load().Start(), nil
	}))
	ns.Def("music-stop", native("music-stop", 0, func([]vm.Value) (vm.Value, error) {
		return current.Load().Stop(), nil
	}))
	// set-music-gain is the bed's level under the one-shots, set-sfx-gain the
	// one-shots' over the bed: a game's music and effects volumes. The glide
	// forms take seconds: a short glide down to duck the bed under a big
	// one-shot, a longer one to bring it back.
	for name, b := range map[string]bus{"music": musicBus, "sfx": sfxBus} {
		set, glide := "set-"+name+"-gain", "glide-"+name+"-gain"
		ns.Def(set, native(set, 1, func(a []vm.Value) (vm.Value, error) {
			g, err := numArg(set, "gain", a[0])
			if err != nil {
				return vm.NIL, err
			}
			current.Load().setGain(b, g)
			return vm.NIL, nil
		}))
		ns.Def(glide, native(glide, 2, func(a []vm.Value) (vm.Value, error) {
			g, err := numArg(glide, "gain", a[0])
			if err != nil {
				return vm.NIL, err
			}
			secs, err := numArg(glide, "seconds", a[1])
			if err != nil {
				return vm.NIL, err
			}
			if !(secs >= 0) { // also refuses NaN
				return vm.NIL, fmt.Errorf("undertone/%s: seconds must not be negative, got %v", glide, secs)
			}
			current.Load().glideGain(b, g, secs)
			return vm.NIL, nil
		}))
	}
	// Milliseconds of audio kept queued ahead of the speaker, for tuning a
	// browser build per device (from a ?queue= page param, say) without a
	// rebuild. Browser only; natively it does nothing. Write-only like the
	// rest of the surface: a value the ring can't hold is clamped, and the
	// clamp is reported on stderr.
	ns.Def("set-queue", native("set-queue", 1, func(a []vm.Value) (vm.Value, error) {
		d, err := queueArg(a[0])
		if err != nil {
			return vm.NIL, err
		}
		current.Load().setQueue(d)
		return vm.NIL, nil
	}))
	// A game's own one-shot, in the shape engine.ShotSpec.EDN writes:
	// {:name :ping :gate-ms 20 :patch [[:env 0.001 0.2 0 0.1] [:osc 0 :sine 0 1] [:mul] [:out 0.5 0.5]]}.
	// Unlike a trigger it reports a bad patch, since one is a bug in the game.
	ns.Def("register-shot", native("register-shot", 1, func(a []vm.Value) (vm.Value, error) {
		s, err := shotSpec(a[0])
		if err == nil {
			err = engine.RegisterShot(s)
		}
		if err != nil {
			return vm.NIL, fmt.Errorf("undertone/register-shot: %w", err)
		}
		return vm.NIL, nil
	}))
	// A game's own palette, for a mood's :palette, in the shape
	// engine.PaletteSpec.EDN writes: {:name :ember :drone [units] :fifth :pad
	// ... :kit [:default {:note 35 :patch :slam} :default]}. A track is a
	// built-in patch by keyword, a units vector, or {:name :x :patch [units]}.
	ns.Def("register-palette", native("register-palette", 1, func(a []vm.Value) (vm.Value, error) {
		s, err := paletteSpec(a[0])
		if err == nil {
			err = engine.RegisterPalette(s)
		}
		if err != nil {
			return vm.NIL, fmt.Errorf("undertone/register-palette: %w", err)
		}
		return vm.NIL, nil
	}))
	// A game's own lead, for a mood's :leads or a :lead, in the shape
	// engine.LeadSpec.EDN writes: {:name :glass :gate 1 :patch [units]}.
	ns.Def("register-lead", native("register-lead", 1, func(a []vm.Value) (vm.Value, error) {
		s, err := leadSpec(a[0])
		if err == nil {
			err = engine.RegisterLead(s)
		}
		if err != nil {
			return vm.NIL, fmt.Errorf("undertone/register-lead: %w", err)
		}
		return vm.NIL, nil
	}))
	ns.Def("play-theme", native("play-theme", 1, func(a []vm.Value) (vm.Value, error) {
		return current.Load().playTheme(a[0])
	}))
	ns.Def("theme-playing?", native("theme-playing?", 0, func([]vm.Value) (vm.Value, error) {
		return vm.Boolean(current.Load().themePlaying()), nil
	}))
	ns.Def("position", native("position", 0, func([]vm.Value) (vm.Value, error) {
		return current.Load().position(), nil
	}))
	ns.Def("audio-stats", native("audio-stats", 0, func([]vm.Value) (vm.Value, error) {
		return current.Load().audioStats(), nil
	}))
	ns.Def("layer-levels", native("layer-levels", 0, func([]vm.Value) (vm.Value, error) {
		return current.Load().layerLevels(), nil
	}))
	ns.Def("levels", native("levels", 0, func([]vm.Value) (vm.Value, error) {
		return current.Load().levels(), nil
	}))
	ns.Def("note-events", native("note-events", 1, func(a []vm.Value) (vm.Value, error) {
		return current.Load().noteEvents(a[0])
	}))
	ns.Def("trigger", native("trigger", 1, func(a []vm.Value) (vm.Value, error) {
		return current.Load().trigger(a[0]), nil
	}))
	// The mood derivation, for undertone.ambience: pure functions, not audio
	// state, so the surface stays write-only.
	ns.Def("derive-spec", native("derive-spec", 4, deriveSpec))
	ns.Def("moods-edn", native("moods-edn", 0, func([]vm.Value) (vm.Value, error) {
		return vm.String(moodsEDN()), nil
	}))
	// Offline render: how determinism gets checked without a sound card.
	renderWAV, _ := vm.NativeFnType.Wrap(func(a []vm.Value) (vm.Value, error) {
		if len(a) != 2 && len(a) != 3 {
			return vm.NIL, fmt.Errorf("undertone/render-wav: wants a path, seconds and an optional script, got %d argument(s)", len(a))
		}
		if _, ok := a[0].(vm.String); !ok && a[0] != vm.NIL {
			return vm.NIL, fmt.Errorf("undertone/render-wav: path must be a string or nil, got %s", a[0].Type().Name())
		}
		secs, err := numArg("render-wav", "seconds", a[1])
		if err != nil {
			return vm.NIL, err
		}
		if !(secs > 0 && secs <= maxRenderSeconds) { // also catches NaN
			return vm.NIL, fmt.Errorf("undertone/render-wav: seconds must be above 0 and at most %d, got %v", maxRenderSeconds, secs)
		}
		script := vm.Value(vm.NIL)
		if len(a) == 3 {
			script = a[2]
		}
		return current.Load().renderWAV(a[0], secs, script)
	})
	renderWAV.(*vm.NativeFn).SetName("render-wav")
	ns.Def("render-wav", renderWAV)
	rt.RegisterNS(ns)
}

// indexed reads v as a positional collection: a vector of any
// representation. let-go gives back an ArrayVector up to 32
// elements and a PersistentVector past that (mapv, into, conj), so a type
// check for ArrayVector alone turned every longer vector away: a trigger
// list of 33 played nothing, and said nothing. vm.Indexed is let-go's
// marker for positional types, so a new vector representation is read
// without a change here. Strings are Indexed too but are not vectors, and
// seqs (lists, ranges, lazy seqs) are refused as before: counting one
// realizes it, which never returns for (range).
func indexed(v vm.Value) (vm.Indexed, bool) {
	switch t := v.(type) {
	case vm.ArrayVector: // a Seq as well as Indexed, so it must come first
		return t, true
	case vm.String, vm.Seq:
		return nil, false
	}
	ix, ok := v.(vm.Indexed)
	return ix, ok
}

// asVec reads an lg vector (see indexed) as a slice. A map entry, as from
// (first m), is a two-element vector in lg but neither Indexed nor apart
// from Seq on the Go side, so it is read by hand.
func asVec(v vm.Value) (vm.ArrayVector, bool) {
	switch t := v.(type) {
	case vm.ArrayVector:
		return t, true
	case vm.MapEntry:
		return vm.ArrayVector{t.Key, t.Value}, true
	}
	ix, ok := indexed(v)
	if !ok {
		return nil, false
	}
	out := make(vm.ArrayVector, ix.RawCount())
	for i := range out {
		out[i] = ix.Nth(i)
	}
	return out, true
}

// numArg reads a numeric argument of the native `fn`.
func numArg(fn, what string, v vm.Value) (float64, error) {
	switch v := v.(type) {
	case vm.Float:
		return float64(v), nil
	case vm.Int:
		return float64(v), nil
	}
	return 0, fmt.Errorf("undertone/%s: %s must be a number, got %s", fn, what, v.Type().Name())
}

// native wraps fn as an lg function without reflection. vm.MustBox would
// box a typed Go func through reflect.Value.Call, which TinyGo does not
// implement: under TinyGo every undertone/ call failed, and TinyGo is what gets a
// browser bundle with let-go in it from 28 MB down to under 6.
func native(name string, arity int, fn func([]vm.Value) (vm.Value, error)) vm.Value {
	v, _ := vm.NativeFnType.Wrap(func(a []vm.Value) (vm.Value, error) {
		if len(a) != arity {
			return vm.NIL, fmt.Errorf("undertone/%s: wants %d argument(s), got %d", name, arity, len(a))
		}
		return fn(a)
	})
	f := v.(*vm.NativeFn).WithArity(arity, false)
	f.SetName(name)
	return f
}

// LoadEmbedded requires the embedded lg namespaces into c's current
// namespace under their short names, so host code can call ambience/score!
// as it could before the namespace was qualified. Since init registers
// them, lg code needs only its own (:require [undertone.ambience ...]).
func LoadEmbedded(c *api.LetGo) error {
	for _, s := range lg.Sources {
		short := s.Name[strings.LastIndexByte(s.Name, '.')+1:]
		if _, err := c.Run("(require '[" + s.Name + " :as " + short + "])"); err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
	}
	return nil
}

// LoadSource loads one lg namespace from source text through core's
// load-string — the same lane as LoadEmbedded, but for sources the caller
// supplies at runtime, such as namespaces fetched over HTTP by a browser host.
func LoadSource(c *api.LetGo, name, text string) error {
	if err := c.Def("undertone-loaded-src", text); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if _, err := c.Run("(load-string undertone-loaded-src)"); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// SetParams replaces the parameter state outright and stages it. Distinct
// from the lg-facing partial merge: a caller that owns the whole derivation
// (depth/tension sliders driving engine.Derive*) wants replacement, not merge, or
// muting the bed would leave the previous gains behind.
func (m *Music) SetParams(p engine.Params) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.params = p
	m.paramsGen++
	if m.synth != nil {
		m.synth.SetParameters(p)
	}
}
