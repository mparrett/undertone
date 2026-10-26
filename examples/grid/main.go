//go:build js

// grid is a step sequencer over the undertone engine: a 16-step pattern of
// lead, bass, pulse and drum lanes, played by the engine's own tracker over its
// generated bed. The page draws the grid and owns its state; this program
// turns that state into engine calls and reports the playhead.
//
//	GOOS=js GOARCH=wasm go build -o examples/grid/main.wasm ./examples/grid
//	cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/grid/
//
// Serve the folder over HTTPS (or localhost): the browser output needs
// AudioWorklet, which Safari only offers in a secure context.
//
// JS surface, on window.undertoneGrid:
//
//	setGrid(json)    {lead:[16], bass:[16], pulse:[16], drums:[3], bed}: degrees 1..8, 0 = rest;
//	                 drums are kick, clap, shaker as 32 sixteenths of X x o .
//	setParams(json)  {bpm, mood: "village"|"aurora"|"meadow"|"overworld"|"ruins"|"dungeon", voice, seed}
//	seedGrid(seed)   a starting grid derived from the current params, as JSON
//	play() / stop()  call from the tap handler: iOS resumes audio only inside one
//	row()            the step being heard, or -1 while stopped
//	key()            the current key and the notes of each lane's rows 1 and 8, as JSON
//	mutate(json, n)  with -tags lg only: lg's variation of a grid for seed n
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/webaudio"
)

// gate keeps the synth from advancing while stopped: the output may start
// on any tap (that is how iOS unlocks audio), and a grid should not begin
// playing until the play button says so.
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

type gridJSON struct {
	Lead  []int    `json:"lead"`
	Bass  []int    `json:"bass"`
	Pulse []bool   `json:"pulse"`
	Drums []string `json:"drums"`
	Bed   bool     `json:"bed"`
}

type paramsJSON struct {
	BPM   float64 `json:"bpm"`
	Mood  string  `json:"mood"`
	Voice string  `json:"voice"`
	Seed  uint64  `json:"seed"`
}

func (j gridJSON) grid() engine.Grid {
	g := engine.Grid{Bed: j.Bed}
	copy(g.Lead[:], j.Lead)
	copy(g.Bass[:], j.Bass)
	copy(g.Pulse[:], j.Pulse)
	copy(g.Drums[:], j.Drums)
	return g
}

// extraAPI lets an optional build add to window.undertoneGrid (lg.go).
var extraAPI func(api map[string]any)

func derive(c paramsJSON) engine.Params {
	mood, level := c.Mood, 0
	switch mood {
	case "dungeon", "overworld":
		level = 2
	case "ruins":
		// The dungeon's shallowest level, where it is still dorian: minor,
		// but with a raised sixth that keeps it from turning grim.
		mood = "dungeon"
	}
	p, ok := engine.DeriveMood(mood, c.Seed, level, 0)
	if !ok {
		p = engine.DeriveOverworld(c.Seed, 2, 0)
	}
	if c.Voice != "" {
		engine.ApplyLead(&p, c.Voice)
	}
	if c.BPM > 0 {
		p.BPM = min(max(c.BPM, 40), 200)
	}
	return p
}

var noteNames = []string{"C", "C♯", "D", "E♭", "E", "F", "F♯", "G", "A♭", "A", "B♭", "B"}

// modeNames names the engine's scales (scalesByDarkness, scalesByBrightness,
// the pentatonics), keyed by fmt.Sprint of the scale.
var modeNames = map[string]string{
	"[0 2 3 5 7 9 10]": "dorian",
	"[0 2 3 5 7 8 10]": "minor",
	"[0 1 3 5 7 8 10]": "phrygian",
	"[0 2 4 5 7 9 10]": "mixolydian",
	"[0 2 4 5 7 9 11]": "major",
	"[0 2 4 6 7 9 11]": "lydian",
	"[0 2 4 7 9]":      "major pentatonic",
	"[0 3 5 7 10]":     "minor pentatonic",
}

// keyJSON describes where the grid's rows land: the key, and the note each
// lane's rows 1 and 8 play (row 8 is row 1 an octave up in a seven-note
// scale, and higher in a five-note one).
func keyJSON(p engine.Params) string {
	mode, ok := modeNames[fmt.Sprint(p.Scale)]
	if !ok {
		mode = "custom scale"
	}
	lead, bass := engine.GridBases(p)
	name := func(midi int) string { return fmt.Sprintf("%s%d", noteNames[midi%12], midi/12-1) }
	b, _ := json.Marshal(map[string]string{
		"key":  noteNames[p.Root%12] + " " + mode,
		"lead": name(lead), "lead8": name(engine.DegreeNote(p, lead, engine.Degrees)),
		"bass": name(bass), "bass8": name(engine.DegreeNote(p, bass, engine.Degrees)),
	})
	return string(b)
}

func toJSON(g engine.Grid) string {
	out := gridJSON{Lead: g.Lead[:], Bass: g.Bass[:], Pulse: g.Pulse[:], Drums: g.Drums[:], Bed: g.Bed}
	b, _ := json.Marshal(out)
	return string(b)
}

func main() {
	cur := paramsJSON{BPM: 110, Mood: "overworld", Voice: "kalimba", Seed: 1}
	syn := engine.NewSynth(derive(cur))
	g0 := engine.SeedGrid(derive(cur))
	syn.SetGrid(&g0)
	src := &gate{src: syn}

	var player atomic.Pointer[webaudio.Player]
	go func() {
		// A shorter queue than webaudio's default: an edit is heard the next
		// time the playhead passes it, and every millisecond queued is a
		// millisecond the edit trails the playhead.
		p, err := webaudio.Open(src, webaudio.Options{SampleRate: engine.SampleRate, Queue: 200 * time.Millisecond})
		if err != nil {
			js.Global().Call("undertoneGridFailed", err.Error())
			return
		}
		player.Store(p)
		if !src.on.Load() {
			p.Pause()
		}
	}()

	api := map[string]any{
		"setGrid": js.FuncOf(func(_ js.Value, a []js.Value) any {
			var j gridJSON
			if json.Unmarshal([]byte(a[0].String()), &j) != nil {
				return nil
			}
			g := j.grid()
			syn.SetGrid(&g)
			return nil
		}),
		"setParams": js.FuncOf(func(_ js.Value, a []js.Value) any {
			if json.Unmarshal([]byte(a[0].String()), &cur) == nil {
				syn.SetParametersNow(derive(cur))
			}
			return nil
		}),
		"seedGrid": js.FuncOf(func(_ js.Value, a []js.Value) any {
			p := derive(cur)
			p.Seed = uint64(a[0].Int())
			return toJSON(engine.SeedGrid(p))
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
		"row": js.FuncOf(func(js.Value, []js.Value) any {
			p := player.Load()
			if p == nil || !src.on.Load() {
				return -1
			}
			// what is heard now: rendered minus what is still queued
			heard := syn.Frames() - int64(p.Queued().Seconds()*engine.SampleRate)
			return syn.RowAt(heard)
		}),
		"key":     js.FuncOf(func(js.Value, []js.Value) any { return keyJSON(derive(cur)) }),
		"initial": toJSON(g0),
	}
	if extraAPI != nil {
		extraAPI(api)
	}
	js.Global().Set("undertoneGrid", js.ValueOf(api))
	js.Global().Call("undertoneGridReady")
	select {}
}
