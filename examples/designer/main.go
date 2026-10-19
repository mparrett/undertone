//go:build js

// designer is a spike of a mood designer: it plays the generator alone, from
// a mood spec the page edits, so a change to a mood's lines is heard as it is
// made, at any level and tension. The page exports the spec as an entry for
// the moods map in letgo/lg/ambience.lg.
//
//	just designer    # builds main.wasm, copies wasm_exec.js, serves on :8373
//
// JS surface, on window.undertoneDesigner:
//
//	moods()                  the built-in moods, as JSON
//	spec(name)               a built-in mood's spec, as JSON (an unbounded clamp is null)
//	apply(json, seed, level, tension, muted)
//	                         derive from a spec and play it at once, with the
//	                         layers named in muted (a JSON array) silenced; returns
//	                         {error} or what was derived: {bpm, key, lead, groove, gains}
//	edn(json)                the spec as an ambience.lg moods entry
//	palettes(), grooves()    the names a spec can use, as JSON
//	play() / stop()          call from the tap handler: iOS resumes audio only inside one
package main

import (
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

// lineJSON is a Line with its unbounded clamps as null, which JSON can carry
// where ±Inf cannot.
type lineJSON struct {
	Base float64  `json:"base"`
	Step float64  `json:"step"`
	Lo   *float64 `json:"lo"`
	Hi   *float64 `json:"hi"`
}

func toLine(l engine.Line) lineJSON {
	j := lineJSON{Base: l.Base, Step: l.Step}
	if !math.IsInf(l.Lo, -1) {
		j.Lo = &l.Lo
	}
	if !math.IsInf(l.Hi, 1) {
		j.Hi = &l.Hi
	}
	return j
}

func (j lineJSON) line() engine.Line {
	l := engine.Line{Base: j.Base, Step: j.Step, Lo: math.Inf(-1), Hi: math.Inf(1)}
	if j.Lo != nil {
		l.Lo = *j.Lo
	}
	if j.Hi != nil {
		l.Hi = *j.Hi
	}
	return l
}

type specJSON struct {
	Name     string   `json:"name"`
	Scales   [][]int  `json:"scales"`
	BPM      lineJSON `json:"bpm"`
	RootBase int      `json:"rootBase"`
	RootStep int      `json:"rootStep"`
	Drone    lineJSON `json:"drone"`
	Fifth    lineJSON `json:"fifth"`
	Pad      lineJSON `json:"pad"`
	Bell     lineJSON `json:"bell"`
	Air      lineJSON `json:"air"`
	Calm     int      `json:"calm"`
	BellLift int      `json:"bellLift"`
	Palette  string   `json:"palette"`
	Leads    []string `json:"leads"`
	Grooves  []string `json:"grooves"`
	Drums    lineJSON `json:"drums"`
	Busy     lineJSON `json:"busy"`
	Swing    float64  `json:"swing"`
	Tense    float64  `json:"tenseDrums"`
	Open     lineJSON `json:"open"`
}

func toJSON(s engine.MoodSpec) specJSON {
	return specJSON{
		Name: s.Name, Scales: s.Scales, BPM: toLine(s.BPM), RootBase: s.RootBase, RootStep: s.RootStep,
		Drone: toLine(s.Drone), Fifth: toLine(s.Fifth), Pad: toLine(s.Pad), Bell: toLine(s.Bell), Air: toLine(s.Air),
		Calm: s.Calm, BellLift: s.BellLift, Palette: s.Palette, Leads: s.Leads, Grooves: s.Grooves,
		Drums: toLine(s.Drums), Busy: toLine(s.Busy), Swing: s.Swing, Tense: s.TenseDrums, Open: toLine(s.Open),
	}
}

func (j specJSON) spec() engine.MoodSpec {
	return engine.MoodSpec{
		Name: j.Name, Scales: j.Scales, BPM: j.BPM.line(), RootBase: j.RootBase, RootStep: j.RootStep,
		Drone: j.Drone.line(), Fifth: j.Fifth.line(), Pad: j.Pad.line(), Bell: j.Bell.line(), Air: j.Air.line(),
		Calm: j.Calm, BellLift: j.BellLift, Palette: j.Palette, Leads: j.Leads, Grooves: j.Grooves,
		Drums: j.Drums.line(), Busy: j.Busy.line(), Swing: j.Swing, TenseDrums: j.Tense, Open: j.Open.line(),
	}
}

var noteNames = []string{"C", "C♯", "D", "E♭", "E", "F", "F♯", "G", "A♭", "A", "B♭", "B"}

// layers are the names the page mutes by; pulse is tension's own layer.
var layers = map[string]engine.Layers{
	"drone": engine.LayerDrone, "fifth": engine.LayerFifth, "pad": engine.LayerPad, "lead": engine.LayerLead,
	"air": engine.LayerAir, "pulse": engine.LayerPulse, "drums": engine.LayerDrums,
}

func marshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func main() {
	start, _ := engine.MoodByName("village")
	p0, _ := start.Derive(1, 0, 0)
	syn := engine.NewSynth(p0)
	src := &gate{src: syn}

	var player atomic.Pointer[webaudio.Player]
	go func() {
		p, err := webaudio.Open(src, webaudio.Options{SampleRate: engine.SampleRate})
		if err != nil {
			js.Global().Call("undertoneDesignerFailed", err.Error())
			return
		}
		player.Store(p)
		if !src.on.Load() {
			p.Pause()
		}
	}()

	parse := func(v js.Value) (engine.MoodSpec, bool) {
		var j specJSON
		if json.Unmarshal([]byte(v.String()), &j) != nil {
			return engine.MoodSpec{}, false
		}
		return j.spec(), true
	}

	api := map[string]any{
		"moods":    js.FuncOf(func(js.Value, []js.Value) any { return marshal(engine.MoodNames()) }),
		"palettes": js.FuncOf(func(js.Value, []js.Value) any { return marshal(engine.PaletteNames()) }),
		"grooves":  js.FuncOf(func(js.Value, []js.Value) any { return marshal(engine.GrooveNames()) }),
		"leads":    js.FuncOf(func(js.Value, []js.Value) any { return marshal(engine.LeadNames()) }),
		"spec": js.FuncOf(func(_ js.Value, a []js.Value) any {
			s, ok := engine.MoodByName(a[0].String())
			if !ok {
				return nil
			}
			return marshal(toJSON(s))
		}),
		"apply": js.FuncOf(func(_ js.Value, a []js.Value) any {
			s, ok := parse(a[0])
			if !ok {
				return marshal(map[string]string{"error": "unreadable spec"})
			}
			p, err := s.Derive(uint64(a[1].Int()), a[2].Int(), a[3].Float())
			if err != nil {
				return marshal(map[string]string{"error": err.Error()})
			}
			// Muting is the designer's, not the spec's, so it never reaches
			// the EDN; the engine silences the layers at the mix.
			if len(a) > 4 {
				var muted []string
				_ = json.Unmarshal([]byte(a[4].String()), &muted)
				for _, l := range muted {
					p.Mute |= layers[l]
				}
			}
			syn.SetParametersNow(p)
			return marshal(map[string]any{
				"bpm": math.Round(p.BPM), "key": noteNames[((p.Root%12)+12)%12], "root": p.Root,
				"scale": p.Scale, "lead": p.LeadName, "groove": p.Groove,
				"gains": map[string]float64{
					"drone": p.DroneGain, "fifth": p.FifthGain, "pad": p.PadDensity, "bell": p.BellDensity,
					"air": p.AirGain, "pulse": p.PulseGain, "drums": p.DrumGain, "busy": p.DrumBusy,
				},
			})
		}),
		"edn": js.FuncOf(func(_ js.Value, a []js.Value) any {
			s, ok := parse(a[0])
			if !ok {
				return ""
			}
			return s.EDN()
		}),
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
	js.Global().Set("undertoneDesigner", js.ValueOf(api))
	js.Global().Call("undertoneDesignerReady")
	select {}
}
