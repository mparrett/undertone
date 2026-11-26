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
//	                 drums are kick, clap, shaker as 32 sixteenths of X x o . and the chance steps ? f
//	setChain(json, ids)  an array of grids, as setGrid takes one, and a JSON array naming each (the
//	                 page's slot numbers): plays them in order, round and round, in place of setGrid's.
//	                 The first call starts from the first grid; later ones replace the grids and keep
//	                 the place, at the next row while the names are the same and at the next grid
//	                 when they differ. "" goes back to setGrid's grid
//	setVolume(v)     the listening volume, 0..1, for the pattern and auditions alike
//	moodLevel()      the engine mood and level the current mood plays, as JSON {mood, level}
//	moods()          the moods setParams takes, as JSON: the engine's, and ruins after the dungeon
//	setParams(json)  {bpm, mood: one of moods(), voice, bass, seed, busy, tension}:
//	                 voice and bass name a lead instrument for the lead and bass lanes; "" keeps the mood's,
//	                 and "none" silences the lane: its notes stay, unheard
//	seedGrid(seed)   a starting grid derived from the current params, as JSON
//	play() / stop()  call from the tap handler: iOS resumes audio only inside one
//	restart()        start the pattern over from its first step; while stopped, the next play starts there
//	row()            the step being heard, or -1 while stopped
//	at()             [row, id]: row(), and the name of the setChain grid it is in, -1 for none
//	levels()         [lead, bass, pulse, drums, echo]: each lane's level, dry, as of the last block
//	                 rendered, then the echo's: what the shared delay returns, all lanes' at once
//	onsets()         [lead, bass, pulse, drums, struck]: how many notes each lane started since the last call,
//	                 and struck, the drum sixteenths that sounded, as lane, sixteenth, sixteenth-of-the-run triples
//	queued()         milliseconds of audio rendered but not yet heard: how far levels and onsets lead the ear
//	audition(json)   play a grid once, alone, without touching the pattern: a cell heard before it is placed
//	key()            the current key and the notes of each lane's rows 1 and 8, as JSON
//	moodBusy()       the mood's own chance for each ? drum step, which setParams' busy overrides
//	mutate(json, n)  with -tags lg only: lg's variation of a grid for seed n
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/internal/debugoverlay"
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
	Bass  string  `json:"bass"`
	Seed  uint64  `json:"seed"`
	// Busy is the chance each ? step in the drums plays, 0..1; absent, the
	// mood's own, which is 0 in every mood without a groove
	Busy *float64 `json:"busy"`
	// Tension is the game's tension, 0..1, as DeriveMood takes it: fills, the
	// mood's busyness and the bed's colour follow it. The tempo it would add
	// does not (the grid's tempo is BPM's), nor the drum gain: a grid's drums
	// play at their own level.
	Tension float64 `json:"tension"`
	// Feel is engine.Params.Feel, 0..1: how unevenly the lead and bass lanes
	// are struck.
	Feel float64 `json:"feel"`
}

func (j gridJSON) grid() engine.Grid {
	g := engine.Grid{Bed: j.Bed}
	copy(g.Lead[:], j.Lead)
	copy(g.Bass[:], j.Bass)
	copy(g.Pulse[:], j.Pulse)
	copy(g.Drums[:], j.Drums)
	return g
}

// auditioner plays a cell on a synth and an output of its own, so a note can
// be heard in its lane's voice while the pattern is stopped, or over it. The
// pattern's own output cannot carry it: stopped, it is suspended with the
// pattern's last 200 ms still queued, and resuming it would play those first.
type auditioner struct {
	syn    *engine.Synth
	vol    *webaudio.Volume // the page's listening volume, as the pattern's output has it
	player atomic.Pointer[webaudio.Player]
	gen    atomic.Int64 // counts auditions: a pause is for the one that armed it
}

// auditionRing is how long the output runs after a note, for its echo; then
// it is suspended, so an idle page renders one synth and not two.
const auditionRing = 3 * time.Second

// open starts the output, suspended. It runs at boot as the pattern's does:
// iOS starts a context only from a gesture that comes after the context
// exists, so one opened inside the audition's own click would stay silent
// until some later tap.
func (a *auditioner) open() {
	p, err := webaudio.Open(a.vol, webaudio.Options{SampleRate: engine.SampleRate, Queue: 60 * time.Millisecond})
	if err != nil {
		return // the pattern's output reports a failure to open; auditions stay silent
	}
	a.player.Store(p)
	// an audition made while this was opening is sounding now
	if n := a.gen.Load(); n == 0 {
		p.Pause()
	} else {
		a.rest(n)
	}
}

// rest suspends the output once audition n has rung, unless a later one has
// played since.
func (a *auditioner) rest(n int64) {
	time.AfterFunc(auditionRing, func() {
		if p := a.player.Load(); p != nil && a.gen.Load() == n {
			p.Pause()
		}
	})
}

func (a *auditioner) play(g engine.Grid) {
	g.Bed = false
	a.syn.PlayTheme(&engine.Theme{Grids: []engine.Grid{g}, Then: engine.ThenSilence})
	n := a.gen.Add(1)
	if p := a.player.Load(); p != nil {
		p.Resume()
		a.rest(n)
	}
}

// extraAPI lets an optional build add to window.undertoneGrid (lg.go).
var extraAPI func(api map[string]any)

// voiceNone is the voice that silences a lane.
const voiceNone = "none"

// moodLevel is the engine mood and level the grid's mood names: the level
// each plays at, and ruins, which is the dungeon's.
func moodLevel(name string) (string, int) {
	switch name {
	case "dungeon", "overworld":
		return name, 2
	case "ruins":
		// The dungeon's shallowest level, where it is still dorian: minor,
		// but with a raised sixth that keeps it from turning grim.
		return "dungeon", 0
	}
	return name, 0
}

func derive(c paramsJSON) engine.Params {
	mood, level := moodLevel(c.Mood)
	t := min(max(c.Tension, 0), 1)
	p, ok := engine.DeriveMood(mood, c.Seed, level, t)
	if !ok {
		p = engine.DeriveOverworld(c.Seed, 2, t)
	}
	// "none" mutes the lane at the mix: it still plays, unheard, so nothing
	// else about the pattern or the bed moves.
	switch c.Voice {
	case "":
	case voiceNone:
		p.Mute |= engine.LayerLead
	default:
		engine.ApplyLead(&p, c.Voice)
	}
	switch c.Bass {
	case "":
	case voiceNone:
		p.Mute |= engine.LayerBass
	default:
		engine.ApplyBass(&p, c.Bass)
	}
	if c.BPM > 0 {
		p.BPM = min(max(c.BPM, 40), 200)
	}
	if c.Busy != nil {
		p.DrumBusy = min(max(*c.Busy, 0), 1)
	}
	p.Feel = c.Feel
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
	vol := webaudio.NewVolume(src)
	asyn := engine.NewSynth(derive(cur))
	aud := &auditioner{syn: asyn, vol: webaudio.NewVolume(asyn)}
	go aud.open()

	var player atomic.Pointer[webaudio.Player]
	go func() {
		// A shorter queue than webaudio's default: an edit is heard the next
		// time the playhead passes it, and every millisecond queued is a
		// millisecond the edit trails the playhead.
		p, err := webaudio.Open(vol, webaudio.Options{SampleRate: engine.SampleRate, Queue: 200 * time.Millisecond})
		if err != nil {
			js.Global().Call("undertoneGridFailed", err.Error())
			return
		}
		player.Store(p)
		if !src.on.Load() {
			p.Pause()
		}
	}()
	debugoverlay.Show(syn, player.Load)

	var noteCursor uint64
	noteBuf := make([]engine.NoteEvent, 0, 256)
	// the lanes the page draws, by LayerNames index
	lanes := map[int]int{3: 0, 7: 1, 5: 2, engine.DrumsLayer: 3}

	chained := false // setChain's grids are playing, not setGrid's
	// chain names setChain's grids: ids for the grids playing, and while
	// moving, next for those that take over once pattern after has played
	var chain struct {
		ids, next []int
		after     int
		moving    bool
	}
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
		"setChain": js.FuncOf(func(_ js.Value, a []js.Value) any {
			var list []gridJSON
			json.Unmarshal([]byte(a[0].String()), &list) // "" leaves it empty
			if len(list) == 0 {
				if chained {
					syn.PlayTheme(nil)
				}
				chained = false
				return nil
			}
			var ids []int
			if len(a) > 1 {
				json.Unmarshal([]byte(a[1].String()), &ids)
			}
			t := &engine.Theme{Then: engine.ThenLoop}
			for _, j := range list {
				t.Grids = append(t.Grids, j.grid())
			}
			pos, _ := syn.Position()
			// the names as of the last pattern rendered: a change staged for
			// a grid that has since started is in effect
			if chain.moving && pos.Pattern > chain.after {
				chain.ids, chain.moving = chain.next, false
			}
			moved := !slices.Equal(ids, chain.ids) || chain.moving && !slices.Equal(ids, chain.next)
			switch {
			case !chained || !syn.ThemePlaying() || moved && !src.on.Load():
				// stopped, nobody hears the place lost
				syn.PlayTheme(t)
				chain.ids, chain.moving = ids, false
			case moved || chain.moving:
				// The engine keeps its place by number, so with other grids
				// before it the number would name another grid mid-pattern.
				// It lands as the next grid starts instead, and so does any
				// edit until then, which would otherwise land the new list
				// at once.
				syn.ReviseTheme(t, engine.AtNextGrid)
				chain.next, chain.after, chain.moving = ids, pos.Pattern, true
			default:
				// an edit to one grid must not send the listener back to the first
				syn.ReviseTheme(t, engine.AtNextRow)
			}
			chained = true
			return nil
		}),
		"setParams": js.FuncOf(func(_ js.Value, a []js.Value) any {
			if json.Unmarshal([]byte(a[0].String()), &cur) == nil {
				syn.SetParametersNow(derive(cur))
				aud.syn.SetParametersNow(derive(cur))
			}
			return nil
		}),
		"seedGrid": js.FuncOf(func(_ js.Value, a []js.Value) any {
			p := derive(cur)
			p.Seed = uint64(a[0].Int())
			return toJSON(engine.SeedGrid(p))
		}),
		// grooves() lists the groove library; grooveDrums(name, seed) is that
		// groove written out as SeedGrid writes a mood's, chances drawn from
		// the seed and polymetric lanes kept.
		"grooves": js.FuncOf(func(js.Value, []js.Value) any {
			b, _ := json.Marshal(engine.GrooveNames())
			return string(b)
		}),
		"grooveDrums": js.FuncOf(func(_ js.Value, a []js.Value) any {
			p := derive(cur)
			p.Groove, p.Seed = a[0].String(), uint64(a[1].Int())
			b, _ := json.Marshal(engine.SeedGrid(p).Drums)
			return string(b)
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
		"restart": js.FuncOf(func(js.Value, []js.Value) any {
			// While stopped the gate holds the synth still, so this waits
			// for the next play; what the output had queued is heard first.
			syn.Rewind()
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
		"at": js.FuncOf(func(js.Value, []js.Value) any {
			p := player.Load()
			if p == nil || !src.on.Load() {
				return []any{-1, -1}
			}
			pos, ok := syn.PositionAt(syn.Frames() - int64(p.Queued().Seconds()*engine.SampleRate))
			if !ok {
				return []any{-1, -1}
			}
			// the names in effect at the row heard, which trails the rendering
			ids := chain.ids
			if chain.moving && pos.Pattern > chain.after {
				ids = chain.next
			}
			if !pos.Theme || pos.Grid >= len(ids) {
				return []any{pos.Row, -1}
			}
			return []any{pos.Row, ids[pos.Grid]}
		}),
		"levels": js.FuncOf(func(js.Value, []js.Value) any {
			l := syn.LayerLevels()
			// a muted layer still reports its level; a silenced lane's strip should lie flat
			if cur.Voice == voiceNone {
				l[3] = 0
			}
			if cur.Bass == voiceNone {
				l[7] = 0
			}
			return []any{l[3], l[7], l[5], l[engine.DrumsLayer], syn.EchoLevel()} // LayerNames order: lead 3, bass 7, pulse 5
		}),
		"onsets": js.FuncOf(func(js.Value, []js.Value) any {
			var evs []engine.NoteEvent
			evs, noteCursor, _ = syn.NoteEvents(noteCursor, noteBuf[:0])
			out := []any{0, 0, 0, 0}
			// drum sixteenths struck, as lane, sixteenth of the pattern and
			// sixteenth of the run: the page lights a step that played, and
			// a looping lane draws where in its loop the run has got to
			var struck []any
			from := syn.RunStart()
			for _, e := range evs {
				// a hit from before the run began (a restart, or a new run, since the
				// last poll) belongs to a run the page no longer draws: patterns only
				// count up, so it is one that comes before RunStart
				if (e.Kind == engine.DrumOn || e.Kind == engine.DrumOff) && e.Pattern*16+e.Row >= from {
					six := 2 * e.Row
					if e.Kind == engine.DrumOff {
						six++
					}
					struck = append(struck, e.Lane, six, 2*(e.Pattern*16+e.Row-from)+six%2)
				}
				if k, ok := lanes[e.Layer]; ok && !(k == 0 && cur.Voice == voiceNone) && !(k == 1 && cur.Bass == voiceNone) {
					out[k] = out[k].(int) + 1
				}
			}
			return append(out, struck)
		}),
		"queued": js.FuncOf(func(js.Value, []js.Value) any {
			if p := player.Load(); p != nil {
				return float64(p.Queued()) / float64(time.Millisecond)
			}
			return 0
		}),
		"audition": js.FuncOf(func(_ js.Value, a []js.Value) any {
			var j gridJSON
			if json.Unmarshal([]byte(a[0].String()), &j) == nil {
				aud.play(j.grid())
			}
			return nil
		}),
		"key": js.FuncOf(func(js.Value, []js.Value) any { return keyJSON(derive(cur)) }),
		// The engine's moods, so the page's list cannot fall behind the
		// engine's as it once did, and ruins, the dungeon's shallowest level,
		// which the grid offers as a mood of its own.
		"moods": js.FuncOf(func(js.Value, []js.Value) any {
			var names []string
			for _, n := range engine.MoodNames() {
				names = append(names, n)
				if n == "dungeon" {
					names = append(names, "ruins")
				}
			}
			b, _ := json.Marshal(names)
			return string(b)
		}),
		// setVolume sets the listening volume, 0..1, of the pattern and the
		// auditions alike
		"setVolume": js.FuncOf(func(_ js.Value, a []js.Value) any {
			vol.Set(a[0].Float())
			aud.vol.Set(a[0].Float())
			return nil
		}),
		// The engine mood and level the current mood plays, for a link to a
		// page that takes them (the studio's #theme=).
		"moodLevel": js.FuncOf(func(js.Value, []js.Value) any {
			m, l := moodLevel(cur.Mood)
			b, _ := json.Marshal(map[string]any{"mood": m, "level": l})
			return string(b)
		}),
		// the mood's own busyness, what a busy left unset plays at
		"moodBusy": js.FuncOf(func(js.Value, []js.Value) any { c := cur; c.Busy = nil; return derive(c).DrumBusy }),
		"initial":  toJSON(g0),
	}
	if extraAPI != nil {
		extraAPI(api)
	}
	js.Global().Set("undertoneGrid", js.ValueOf(api))
	js.Global().Call("undertoneGridReady")
	select {}
}
