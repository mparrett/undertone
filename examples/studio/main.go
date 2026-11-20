//go:build js

// studio is a spike of a sound studio: open a one-shot, a lead or a palette,
// edit its units, register it and hear it, then take it away as the EDN that
// undertone/register-shot, register-lead or register-palette reads. The mood
// designer shapes a mood's lines; this shapes the sounds they play.
//
//	just studio    # builds main.wasm, copies wasm_exec.js, serves on :8374
//
// JS surface, on window.undertoneStudio. Specs travel as the engine's types
// in JSON (ShotSpec, LeadSpec, PaletteSpec, field names as in Go).
//
//	names(kind)              "shot", "lead", "palette" or "mood": the names, as JSON
//	get(kind, name)          a built-in or registered spec, as JSON, or null
//	register(kind, json)     validate and register; "" or the error
//	edn(kind, json)          the spec as the map its register-* takes
//	stack(json)              the stack after each of a patch's units (engine.StackTrace):
//	                         [{Signal: [bool, ...], Error}], as JSON, or {error} when
//	                         the units don't parse
//	bed(json)                play a mood under the sound: {mood, seed, level, tension,
//	                         palette, lead, muted, musicGain}; "" or the error. musicGain 0
//	                         silences the bed while shots still feed its delay and room
//	shot(name, degree, gain) trigger a shot on a degree of the bed's scale
//	trigger(json)            a take: one shot map or a vector of them, as undertone/trigger
//	                         reads them: {shot, note | degree (+ octave), gain, pan,
//	                         delay-ms, seed}; returns which were staged, as JSON
//	render(json, secs)       the same take rendered offline over the bed's params and music
//	                         gain, from a fresh synth (engine.Take): interleaved stereo
//	                         Float32Array, at most 60 s
//	measure(json, secs[, g]) that render (g overrides the music gain: 0 for a shot alone) measured as the sfx board does (engine.MeasurePCM,
//	                         on the 16-bit samples): {hash, peak, envPeak, envRms, envHop,
//	                         maxLufs, length}, as JSON
//	hash(json, secs[, g])    the take's hash alone (engine.Take.Hash), without rendering,
//	                         for a page that caches renders
//	notes()                  the bed's notes since the last call, flat: layer (a LayerNames
//	                         index), MIDI note, gain for each
//	position()               [row, pattern] the bed has rendered to, or null
//	theme(json)              play grids through the bed's voices ({lead, bass, drums, bed}); "" stops
//	themePlaying()           whether that theme is still playing
//	levels()                 each layer's level, as JSON in LayerNames order
//	play() / stop()          call from the tap handler: iOS resumes audio only inside one
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall/js"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/internal/debugoverlay"
	"github.com/mparrett/undertone/webaudio"
)

type gate struct {
	src io.Reader
	on  atomic.Bool
}

func (g *gate) Read(p []byte) (int, error) {
	if !g.on.Load() {
		clear(p)
		return len(p), nil
	}
	return g.src.Read(p)
}

var layers = map[string]engine.Layers{
	"drone": engine.LayerDrone, "fifth": engine.LayerFifth, "pad": engine.LayerPad, "lead": engine.LayerLead,
	"air": engine.LayerAir, "pulse": engine.LayerPulse, "drums": engine.LayerDrums,
}

func marshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// bedReq is what the page plays under the sound being edited.
type bedReq struct {
	Mood    string   `json:"mood"`
	Seed    seed     `json:"seed"`
	Level   int      `json:"level"`
	Tension float64  `json:"tension"`
	Palette string   `json:"palette"` // "" keeps the mood's
	Lead    string   `json:"lead"`    // "" keeps the mood's picks
	Muted   []string `json:"muted"`
	// MusicGain scales the bed, 1 when left out; 0 leaves only the shots and
	// the delay and room they send to.
	MusicGain *float64 `json:"musicGain"`
}

// seed reads a JSON number or a decimal string. A game's seeds are full 64-bit
// (a JSON number loses them past 2^53), and lg passes them as int64, so a
// string may be negative; it is read as int64 and kept as its bits, as
// undertone/trigger does.
type seed uint64

func (s *seed) UnmarshalJSON(b []byte) error {
	t := strings.Trim(string(b), `"`)
	if t == "" || t == "null" {
		*s = 0
		return nil
	}
	if v, err := strconv.ParseInt(t, 10, 64); err == nil {
		*s = seed(v)
		return nil
	}
	v, err := strconv.ParseUint(t, 10, 64)
	if err != nil {
		return fmt.Errorf("seed %s: want an integer, or one as a decimal string", b)
	}
	*s = seed(v)
	return nil
}

// theme reads grids as {lead, bass, drums, bed}: lead and bass are 16
// digits, a scale degree each (0 rests), drums up to DrumLanes groove lanes.
// The theme hands back to the generator when it ends.
func theme(data string) (*engine.Theme, error) {
	var gs []struct {
		Lead, Bass string
		Drums      []string
		Bed        bool
	}
	if err := json.Unmarshal([]byte(data), &gs); err != nil {
		return nil, err
	}
	t := &engine.Theme{Then: engine.ThenGenerate}
	for i, g := range gs {
		var out engine.Grid
		for lane, src := range map[*[engine.Steps]int]string{&out.Lead: g.Lead, &out.Bass: g.Bass} {
			if len(src) > engine.Steps {
				return nil, fmt.Errorf("grid %d: a lane is %d steps, at most %d", i, len(src), engine.Steps)
			}
			for j, c := range src {
				if c < '0' || c > '0'+engine.Degrees {
					return nil, fmt.Errorf("grid %d: %q is not a degree 0-%d", i, c, engine.Degrees)
				}
				lane[j] = int(c - '0')
			}
		}
		if len(g.Drums) > engine.DrumLanes {
			return nil, fmt.Errorf("grid %d: %d drum lanes, at most %d", i, len(g.Drums), engine.DrumLanes)
		}
		copy(out.Drums[:], g.Drums)
		out.Bed = g.Bed
		t.Grids = append(t.Grids, out)
	}
	return t, nil
}

// shotJSON is one shot of a take, in undertone/trigger's keys. A pointer
// field is one the take may leave out.
type shotJSON struct {
	Shot    string   `json:"shot"`
	Note    *int     `json:"note"`
	Degree  *int     `json:"degree"`
	Octave  int      `json:"octave"`
	Gain    *float64 `json:"gain"`
	Pan     *float64 `json:"pan"`
	DelayMS float64  `json:"delay-ms"`
	Seed    seed     `json:"seed"`
}

func (j shotJSON) shot() engine.Shot {
	sh := engine.Shot{Name: j.Shot, Gain: 1, Pan: 0.5, DelayMS: j.DelayMS, Seed: uint64(j.Seed), Octave: j.Octave}
	if j.Gain != nil {
		sh.Gain = *j.Gain
	}
	if j.Pan != nil {
		sh.Pan = *j.Pan
	}
	switch {
	case j.Note != nil:
		sh.Note, sh.HasNote = *j.Note, true
	case j.Degree != nil:
		sh.Degree, sh.HasDegree = *j.Degree, true
	}
	return sh
}

// take reads one shot map or a vector of them.
func take(data string) ([]engine.Shot, error) {
	var many []shotJSON
	if err := json.Unmarshal([]byte(data), &many); err != nil {
		var one shotJSON
		if err := json.Unmarshal([]byte(data), &one); err != nil {
			return nil, err
		}
		many = []shotJSON{one}
	}
	out := make([]engine.Shot, len(many))
	for i, j := range many {
		out[i] = j.shot()
	}
	return out, nil
}

func main() {
	start, _ := engine.MoodByName("village")
	p0, _ := start.Derive(1, 0, 0)
	syn := engine.NewSynth(p0)
	var noteCursor uint64
	noteBuf := make([]engine.NoteEvent, 0, 256)
	cur, gain := p0, 1.0 // what the bed plays, for a take's fresh synth

	// newTake reads (take json, seconds) as an engine.Take over the bed, so a
	// render, its measurement and its hash all describe the same thing.
	newTake := func(a []js.Value) (engine.Take, bool) {
		shots, err := take(a[0].String())
		if err != nil {
			return engine.Take{}, false
		}
		g := gain
		if len(a) > 2 && a[2].Type() == js.TypeNumber {
			g = a[2].Float() // a page measuring a shot alone passes 0
		}
		return engine.Take{Params: cur, MusicGain: g, Shots: shots, Seconds: a[1].Float()}, true
	}
	src := &gate{src: syn}

	var player atomic.Pointer[webaudio.Player]
	go func() {
		p, err := webaudio.Open(src, webaudio.Options{SampleRate: engine.SampleRate})
		if err != nil {
			js.Global().Call("undertoneStudioFailed", err.Error())
			return
		}
		player.Store(p)
		if !src.on.Load() {
			p.Pause()
		}
	}()
	debugoverlay.Show(syn, player.Load)

	get := func(kind, name string) any {
		var v any
		var ok bool
		switch kind {
		case "shot":
			v, ok = engine.ShotByName(name)
		case "lead":
			v, ok = engine.LeadSpecByName(name)
		case "palette":
			v, ok = engine.PaletteSpecByName(name)
		}
		if !ok {
			return nil
		}
		return marshal(v)
	}

	// spec decodes a page spec, registers it when reg is set, and returns
	// its EDN; the EDN of an unregistrable spec is still written, so the page
	// can show what it has.
	spec := func(kind, data string, reg bool) (string, error) {
		switch kind {
		case "shot":
			var s engine.ShotSpec
			if err := json.Unmarshal([]byte(data), &s); err != nil {
				return "", err
			}
			if reg {
				return s.EDN(), engine.RegisterShot(s)
			}
			return s.EDN(), nil
		case "lead":
			var s engine.LeadSpec
			if err := json.Unmarshal([]byte(data), &s); err != nil {
				return "", err
			}
			if reg {
				return s.EDN(), engine.RegisterLead(s)
			}
			return s.EDN(), nil
		case "palette":
			var s engine.PaletteSpec
			if err := json.Unmarshal([]byte(data), &s); err != nil {
				return "", err
			}
			if reg {
				return s.EDN(), engine.RegisterPalette(s)
			}
			return s.EDN(), nil
		}
		return "", nil
	}

	api := map[string]any{
		"names": js.FuncOf(func(_ js.Value, a []js.Value) any {
			switch a[0].String() {
			case "shot":
				return marshal(engine.ShotNames())
			case "lead":
				return marshal(engine.LeadNames())
			case "palette":
				return marshal(engine.PaletteNames())
			case "mood":
				return marshal(engine.MoodNames())
			}
			return "[]"
		}),
		"get": js.FuncOf(func(_ js.Value, a []js.Value) any { return get(a[0].String(), a[1].String()) }),
		"register": js.FuncOf(func(_ js.Value, a []js.Value) any {
			_, err := spec(a[0].String(), a[1].String(), true)
			return errString(err)
		}),
		"edn": js.FuncOf(func(_ js.Value, a []js.Value) any {
			e, _ := spec(a[0].String(), a[1].String(), false)
			return e
		}),
		"stack": js.FuncOf(func(_ js.Value, a []js.Value) any {
			var units []engine.UnitSpec
			if err := json.Unmarshal([]byte(a[0].String()), &units); err != nil {
				return marshal(map[string]string{"error": err.Error()})
			}
			return marshal(engine.StackTrace(units))
		}),
		"bed": js.FuncOf(func(_ js.Value, a []js.Value) any {
			var r bedReq
			if err := json.Unmarshal([]byte(a[0].String()), &r); err != nil {
				return err.Error()
			}
			m, ok := engine.MoodByName(r.Mood)
			if !ok {
				return "no mood " + r.Mood
			}
			if r.Palette != "" {
				m.Palette = r.Palette
			}
			if r.Lead != "" {
				m.Leads = []string{r.Lead}
			}
			p, err := m.Derive(uint64(r.Seed), r.Level, r.Tension)
			if err != nil {
				return err.Error()
			}
			for _, l := range r.Muted {
				p.Mute |= layers[l]
			}
			syn.SetParametersNow(p)
			gain = 1
			if r.MusicGain != nil {
				gain = *r.MusicGain
			}
			syn.SetMusicGain(gain)
			cur = p
			return ""
		}),
		// padNotes is the note each audition pad 1-8 plays under the bed's
		// mood: a shot's degree from the root, as TriggerShot resolves it, or
		// (lead true) the theme lead lane's note, where a lead or palette's
		// pads play. null before the bed has a scale.
		"padNotes": js.FuncOf(func(_ js.Value, a []js.Value) any {
			if len(cur.Scale) == 0 {
				return nil
			}
			base := cur.Root
			if a[0].Bool() {
				base, _ = engine.GridBases(cur)
			}
			out := make([]any, 8)
			for d := range out {
				out[d] = engine.DegreeNote(cur, base, d+1)
			}
			return js.ValueOf(out)
		}),
		"shot": js.FuncOf(func(_ js.Value, a []js.Value) any {
			return syn.TriggerShot(engine.Shot{Name: a[0].String(), Degree: a[1].Int(), HasDegree: true,
				Gain: a[2].Float(), Pan: 0.5})
		}),
		// notes drains the bed's note events since the last call, as the layer
		// index of each, for a page that lights a layer as it plays.
		"notes": js.FuncOf(func(_ js.Value, _ []js.Value) any {
			var evs []engine.NoteEvent
			evs, noteCursor, _ = syn.NoteEvents(noteCursor, noteBuf[:0])
			out := make([]any, 0, 3*len(evs))
			for _, e := range evs {
				out = append(out, e.Layer, e.Note, e.Gain)
			}
			return js.ValueOf(out)
		}),
		// position is the row and pattern the bed has rendered to, or null
		// before the first row.
		"position": js.FuncOf(func(_ js.Value, _ []js.Value) any {
			pos, ok := syn.Position()
			if !ok {
				return nil
			}
			return js.ValueOf([]any{pos.Row, pos.Pattern})
		}),
		// theme(json) plays grids through the bed's own voices, as a game's
		// theme does: a vector of {lead, bass: 16 digits, a degree 1-8 or 0 to
		// rest; drums: up to three groove lanes; bed}. "" or null stops it.
		// Returns "" or an error.
		"theme": js.FuncOf(func(_ js.Value, a []js.Value) any {
			if len(a) == 0 || a[0].IsNull() || a[0].String() == "" {
				syn.PlayTheme(nil)
				return ""
			}
			t, err := theme(a[0].String())
			if err != nil {
				return err.Error()
			}
			syn.PlayTheme(t)
			return ""
		}),
		"themePlaying": js.FuncOf(func(_ js.Value, _ []js.Value) any { return syn.ThemePlaying() }),
		"trigger": js.FuncOf(func(_ js.Value, a []js.Value) any {
			shots, err := take(a[0].String())
			if err != nil {
				return marshal(map[string]string{"error": err.Error()})
			}
			staged := make([]bool, len(shots))
			for i, sh := range shots {
				staged[i] = syn.TriggerShot(sh)
			}
			return marshal(staged)
		}),
		"render": js.FuncOf(func(_ js.Value, a []js.Value) any {
			t, ok := newTake(a)
			if !ok {
				return nil
			}
			pcm := t.Render()
			f := make([]byte, len(pcm)*4)
			for i, v := range pcm {
				binary.LittleEndian.PutUint32(f[i*4:], math.Float32bits(float32(v)/32768))
			}
			u8 := js.Global().Get("Uint8Array").New(len(f))
			js.CopyBytesToJS(u8, f)
			return js.Global().Get("Float32Array").New(u8.Get("buffer"))
		}),
		"measure": js.FuncOf(func(_ js.Value, a []js.Value) any {
			t, ok := newTake(a)
			if !ok {
				return nil
			}
			m := engine.MeasurePCM(t.Render())
			return marshal(map[string]any{
				"hash": t.Hash(), "peak": m.Peak, "envPeak": m.EnvPeak, "envRms": m.EnvRMS,
				"envHop": float64(engine.EnvHop) / engine.SampleRate, "maxLufs": m.MaxLUFS, "length": m.Length,
			})
		}),
		"hash": js.FuncOf(func(_ js.Value, a []js.Value) any {
			t, ok := newTake(a)
			if !ok {
				return nil
			}
			return t.Hash()
		}),
		"levels": js.FuncOf(func(js.Value, []js.Value) any { return marshal(syn.LayerLevels()) }),
		"play": js.FuncOf(func(js.Value, []js.Value) any {
			src.on.Store(true)
			if p := player.Load(); p != nil {
				p.Resume()
			}
			return nil
		}),
		"stop": js.FuncOf(func(js.Value, []js.Value) any {
			src.on.Store(false)
			if p := player.Load(); p != nil {
				p.Pause()
			}
			return nil
		}),
	}
	js.Global().Set("undertoneStudio", js.ValueOf(api))
	js.Global().Call("undertoneStudioReady")
	select {}
}
