// Package letgo binds the undertone engine to let-go as the `undertone`
// namespace: the music/one-shot state, the lg-facing natives, and the loader
// for the embedded lg sources.
//
// Importing the package registers the namespace (see init below), so an lg
// runtime that links it needs no generated interop: lg code calls
// undertone/set-parameters, undertone/set-parameters-now,
// undertone/music-start, undertone/music-stop,
// undertone/set-music-gain, undertone/trigger, undertone/register-shot and
// undertone/render-wav directly. The natives act on a default Music until a
// Go host points them elsewhere with SetMusic.
//
// It also registers the embedded lg sources, so (require 'undertone.ambience)
// resolves in any binary that links the package: a cli.Main host, a custom
// lg, a browser build, with no source path.
package letgo

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

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

	// musicGain is the last undertone/set-music-gain, carried into a synth that
	// starts later; gainSet keeps the zero value from meaning silence.
	musicGain float64
	gainSet   bool

	// theme is an undertone/play-theme that came before music-start; it
	// starts with the music.
	theme *engine.Theme
}

// NewMusic starts from base parameters; the lg script is expected to
// overwrite every scalar via undertone/set-parameters.
func NewMusic(p engine.Params) *Music {
	return &Music{params: p}
}

// Params returns a snapshot of the current parameter state — the demo trace
// reads BPM and PulseGain off it. Read-only by construction: it is a copy.
func (m *Music) Params() engine.Params {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.params
}

// setParameters merges a partial lg map over current state and stages it.
// Unknown keys are ignored (forward compatibility) and absent keys keep their
// current value, so live-coding is just calling it again with what changed.
// An unknown :palette is the exception: it comes from an exported mood, which
// MoodSpec.Derive rejects for the same name, so it is an error here too rather
// than a mood playing on whatever patches were loaded before.
//
// With now, the change is heard within a block rather than at the next
// pattern boundary (engine.Synth.SetParametersNow).
func (m *Music) setParameters(v vm.Value, now bool) (vm.Value, error) {
	lgm, ok := v.(*vm.PersistentMap)
	if !ok {
		return vm.NIL, nil
	}
	var pal *engine.Palette
	if s, ok := kwStr(lgm, "palette"); ok {
		p, ok := engine.PaletteByName(s)
		if !ok {
			fn := "set-parameters"
			if now {
				fn += "-now"
			}
			return vm.NIL, fmt.Errorf("undertone/%s: unknown palette %q", fn, s)
		}
		pal = &p
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// :mood selects the patch palette, which is the one thing not expressible
	// as a scalar in the map. Deriving a throwaway params for the mood is how
	// the host gets that palette without exporting the patch tables.
	if s, ok := kwStr(lgm, "mood"); ok {
		if p, ok := engine.DeriveMood(s, 1, 0, 0); ok {
			// The palette's bell is the mood's seed-1 lead, so its name and
			// gate come too; a :lead below still overrides all three.
			m.params.Palette = p.Palette
			m.params.LeadName, m.params.LeadGate = p.LeadName, p.LeadGate
		}
	}
	// :palette names a bank directly, for a mood the host has no row for. The
	// bank's own bell is replaced by the current lead, since a partial map
	// need not carry :lead.
	if pal != nil {
		m.params.Palette = *pal
		engine.ApplyLead(&m.params, m.params.LeadName)
	}
	flt(lgm, "bpm", &m.params.BPM)
	integer(lgm, "root", &m.params.Root)
	flt(lgm, "drone-gain", &m.params.DroneGain)
	flt(lgm, "fifth-gain", &m.params.FifthGain)
	flt(lgm, "pad-density", &m.params.PadDensity)
	flt(lgm, "bell-density", &m.params.BellDensity)
	flt(lgm, "air-gain", &m.params.AirGain)
	flt(lgm, "pulse-gain", &m.params.PulseGain)
	flt(lgm, "grit", &m.params.Grit)
	flt(lgm, "open", &m.params.Open)
	integer(lgm, "fifth-offset", &m.params.FifthOffset)
	integer(lgm, "bell-lift", &m.params.BellLift)
	if sc, ok := ints(lgm, "scale"); ok {
		m.params.Scale = sc
	}
	if s, ok := kwStr(lgm, "groove"); ok {
		if s == "none" {
			s = ""
		}
		m.params.Groove = s
	}
	flt(lgm, "drum-gain", &m.params.DrumGain)
	flt(lgm, "drum-busy", &m.params.DrumBusy)
	flt(lgm, "swing", &m.params.Swing)
	if s, ok := kwStr(lgm, "lead"); ok {
		engine.ApplyLead(&m.params, s)
	}
	// :seed is int64 on the lg side; the engine's rng streams are uint64.
	var seed int
	if integer(lgm, "seed", &seed) {
		m.params.Seed = uint64(seed)
	}

	switch {
	case m.synth == nil:
	case now:
		m.synth.SetParametersNow(m.params)
	default:
		m.synth.SetParameters(m.params)
	}
	return vm.NIL, nil
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
	var maps []*vm.PersistentMap
	switch t := v.(type) {
	case *vm.PersistentMap:
		maps = append(maps, t)
	case vm.ArrayVector:
		for _, e := range t {
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
		m.synth.TriggerShot(sh)
	}
	return vm.NIL
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
	if m.gainSet {
		syn.SetMusicGain(m.musicGain)
	}
	opening := m.theme
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
	m.starting = false
	if err != nil {
		fmt.Fprintln(os.Stderr, "undertone/music-start:", err)
		return vm.NIL
	}
	m.out = out
	m.synth = syn
	if m.gainSet {
		m.synth.SetMusicGain(m.musicGain)
	}
	// Parameters may have moved while we waited for the gesture; the synth was
	// built from a snapshot, so catch it up to whatever is current.
	m.synth.SetParameters(m.params)
	// A play-theme during the wait replaces the one the synth opened with.
	if m.theme != opening {
		m.synth.PlayTheme(m.theme)
	}
	m.theme = nil
	return vm.NIL
}

// setMusicGain sets the music bed's level (0..1) under the one-shots, now if
// playing and from the start otherwise.
func (m *Music) setMusicGain(g float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.musicGain, m.gainSet = g, true
	if m.synth != nil {
		m.synth.SetMusicGain(g)
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

// renderWAV is the offline lane: it makes "same parameters ->
// byte-identical audio" checkable in a test rather than by ear.
func (m *Music) renderWAV(path string, seconds float64) vm.Value {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := engine.NewSynth(m.params)
	if err := engine.WriteWAV(path, s, seconds); err != nil {
		fmt.Fprintln(os.Stderr, "undertone/render-wav:", err)
	}
	return vm.NIL
}

// output is where a started Music plays: oto natively, webaudio in the
// browser (output_native.go, output_js.go).
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
	units, ok := at(m, "patch").(vm.ArrayVector)
	if !ok {
		return s, fmt.Errorf("want :patch as a vector of units")
	}
	for i, uv := range units {
		u, ok := uv.(vm.ArrayVector)
		if !ok || len(u) == 0 {
			return s, fmt.Errorf("unit %d: want a vector such as [:out 0.5 0.5]", i)
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
					return s, fmt.Errorf("unit %d: %s at %d; only an osc names a shape, second", i, x, j+1)
				}
				us.Shape = string(x)
			default:
				return s, fmt.Errorf("unit %d: %s is not a number or a shape", i, x.Type().Name())
			}
		}
		s.Units = append(s.Units, us)
	}
	return s, nil
}

// lg map readers.
// Absent keys leave the target untouched; that is the partial-merge contract.

func at(m *vm.PersistentMap, k string) vm.Value {
	return m.ValueAtOr(vm.Keyword(k), vm.NIL)
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
	vec, ok := at(m, k).(vm.ArrayVector)
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
	ns.Def("set-music-gain", native("set-music-gain", 1, func(a []vm.Value) (vm.Value, error) {
		var g float64
		switch v := a[0].(type) {
		case vm.Float:
			g = float64(v)
		case vm.Int:
			g = float64(v)
		default:
			return vm.NIL, fmt.Errorf("undertone/set-music-gain: gain must be a number, got %s", a[0].Type().Name())
		}
		current.Load().setMusicGain(g)
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
	ns.Def("play-theme", native("play-theme", 1, func(a []vm.Value) (vm.Value, error) {
		return current.Load().playTheme(a[0])
	}))
	ns.Def("theme-playing?", native("theme-playing?", 0, func([]vm.Value) (vm.Value, error) {
		return vm.Boolean(current.Load().themePlaying()), nil
	}))
	ns.Def("trigger", native("trigger", 1, func(a []vm.Value) (vm.Value, error) {
		return current.Load().trigger(a[0]), nil
	}))
	// Offline render: how determinism gets checked without a sound card.
	ns.Def("render-wav", native("render-wav", 2, func(a []vm.Value) (vm.Value, error) {
		path, ok := a[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("undertone/render-wav: path must be a string, got %s", a[0].Type().Name())
		}
		var secs float64
		switch v := a[1].(type) {
		case vm.Float:
			secs = float64(v)
		case vm.Int:
			secs = float64(v)
		default:
			return vm.NIL, fmt.Errorf("undertone/render-wav: seconds must be a number, got %s", a[1].Type().Name())
		}
		return current.Load().renderWAV(string(path), secs), nil
	}))
	rt.RegisterNS(ns)
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
	if m.synth != nil {
		m.synth.SetParameters(p)
	}
}
