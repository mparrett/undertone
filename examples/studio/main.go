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
//	bed(json)                play a mood under the sound: {mood, seed, level, tension,
//	                         palette, lead, muted, musicGain}; "" or the error. musicGain 0
//	                         silences the bed while shots still feed its delay and room
//	shot(name, degree, gain) trigger a shot on a degree of the bed's scale
//	trigger(json)            a take: one shot map or a vector of them, as undertone/trigger
//	                         reads them: {shot, note | degree (+ octave), gain, pan,
//	                         delay-ms, seed}; returns which were staged, as JSON
//	render(json, secs)       the same take rendered offline over the bed's params and music
//	                         gain, from a fresh synth: interleaved stereo Float32Array,
//	                         at most 60 s
//	levels()                 each layer's level, as JSON in LayerNames order
//	play() / stop()          call from the tap handler: iOS resumes audio only inside one
package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"sync/atomic"
	"syscall/js"

	"github.com/mparrett/undertone/engine"
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
	Seed    uint64   `json:"seed"`
	Level   int      `json:"level"`
	Tension float64  `json:"tension"`
	Palette string   `json:"palette"` // "" keeps the mood's
	Lead    string   `json:"lead"`    // "" keeps the mood's picks
	Muted   []string `json:"muted"`
	// MusicGain scales the bed, 1 when left out; 0 leaves only the shots and
	// the delay and room they send to.
	MusicGain *float64 `json:"musicGain"`
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
	Seed    uint64   `json:"seed"`
}

func (j shotJSON) shot() engine.Shot {
	sh := engine.Shot{Name: j.Shot, Gain: 1, Pan: 0.5, DelayMS: j.DelayMS, Seed: j.Seed, Octave: j.Octave}
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
	cur, gain := p0, 1.0 // what the bed plays, for render's fresh synth
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
			p, err := m.Derive(r.Seed, r.Level, r.Tension)
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
		"shot": js.FuncOf(func(_ js.Value, a []js.Value) any {
			return syn.TriggerShot(engine.Shot{Name: a[0].String(), Degree: a[1].Int(), HasDegree: true,
				Gain: a[2].Float(), Pan: 0.5})
		}),
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
			shots, err := take(a[0].String())
			if err != nil {
				return nil
			}
			secs := min(max(a[1].Float(), 0), 60)
			// A fresh synth, so a render never disturbs what is playing and
			// starts from silence: the bed's params and gain, then the take.
			r := engine.NewSynth(cur)
			r.SetMusicGain(gain)
			for _, sh := range shots {
				r.TriggerShot(sh)
			}
			pcm := make([]byte, int(secs*engine.SampleRate)*4)
			_, _ = r.Read(pcm)
			f := make([]byte, len(pcm)*2)
			for i := 0; i < len(pcm); i += 2 {
				v := float32(int16(binary.LittleEndian.Uint16(pcm[i:]))) / 32768
				binary.LittleEndian.PutUint32(f[i*2:], math.Float32bits(v))
			}
			u8 := js.Global().Get("Uint8Array").New(len(f))
			js.CopyBytesToJS(u8, f)
			return js.Global().Get("Float32Array").New(u8.Get("buffer"))
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
