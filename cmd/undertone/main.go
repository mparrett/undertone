// undertone: a tiny generative-ambience engine. Architecturally a
// demo-sized homage to vsariola/sointu — no sointu code, just its shape: instruments are stacks of unit opcodes
// run by a tiny per-sample VM, a tracker-style sequencer drives them, and
// the whole track is derived deterministically from a seed.
//
// Register: roguelike ambience. Parameters derive from game state —
// mood (dungeon/void/overworld/meadow/aurora/stratosphere/village/rust/forge), level (depth darkens, elevation brightens),
// tension (tempo up, pulse in, harmony sours), and the seed, which also
// picks the lead instrument from a mood-weighted library. SetParameters
// applies a new map at the next pattern boundary, the same transactional
// semantics the letgo binding exposes as undertone/set-parameters.
//
// This is the Go CLI front end; the engine lives in ../../engine and the
// let-go binding in ../../letgo.
//
//	go run ./cmd/undertone -play                          # live, endless (oto; webaudio in a browser)
//	go run ./cmd/undertone -play -depth 5 -tension 0.8    # the abyss, something coming
//	go run ./cmd/undertone -play -mood void               # past the dungeon
//	go run ./cmd/undertone -play -mood overworld -depth 3 # bright mirror
//	go run ./cmd/undertone -play -mood meadow             # brighter still
//	go run ./cmd/undertone -play -mood aurora             # thin, slow, high
//	go run ./cmd/undertone -play -mood stratosphere       # thinner still, no lead
//	go run ./cmd/undertone -play -mood village -depth 5   # drums: the festival
//	go run ./cmd/undertone -play -mood rust -tension 0.6  # industrial, grinding
//	go run ./cmd/undertone -play -mood forge -depth 3     # industrial, driving
//	go run ./cmd/undertone -wav out.wav -depth 4 -lead pluck  # deterministic render
//	go run ./cmd/undertone -midi-file out.mid -mood forge -dur 60  # the notes, one channel per layer
//	go run ./cmd/undertone -midi-live -mood village       # a virtual MIDI source "undertone" (macOS)
//	go run ./cmd/undertone -midi-live -midi-split         # a source per layer: "undertone drone", ...
//	go run ./cmd/undertone -midi-file out.mid -theme title.json  # an authored theme, as its notes
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mparrett/undertone/engine"
)

func main() {
	play := flag.Bool("play", false, "play live (endless) via oto")
	wav := flag.String("wav", "", "render to WAV file")
	midiFile := flag.String("midi-file", "", "render the notes to a Standard MIDI File (with -wav, the same render)")
	midiLive := flag.Bool("midi-live", false, "play the notes live to a virtual MIDI source named undertone, without audio (macOS)")
	midiSplit := flag.Bool("midi-split", false, "with -midi-live, a virtual source per layer, named \"undertone <layer>\", each layer on its usual channel")
	layer := flag.String("layer", "", "with -midi-live, send only these layers, comma-separated (with -midi-split, only their sources open): "+layerList())
	dur := flag.Float64("dur", 48, "WAV or MIDI file duration seconds")
	seed := seedFlag(1)
	flag.Var(&seed, "seed", "level seed; a negative one is read as a signed 64-bit seed's bits, as a game passes it")
	themeFile := flag.String("theme", "", "play an authored theme from a JSON `file`: a vector of grids, or {grids, then}; without -dur, a render is the theme's length")
	depth := flag.Int("depth", 0, "level within the mood (for dungeon, negative = overworld elevation)")
	mood := flag.String("mood", "dungeon", strings.Join(engine.MoodNames(), " | ")+" (level = depth)")
	lead := flag.String("lead", "", "lead instrument: bell|chime|clang|kalimba|reed|pluck (default: seed picks)")
	tension := flag.Float64("tension", 0, "0..1: tempo up, pulse in, harmony sours")
	feel := flag.Float64("feel", 0, "0..1: with -theme, how unevenly its notes are struck: accents on the strong beats, a harder note brighter, each a little different and a little late")
	vu := flag.String("vu", "bars", "meter style: bars | compact (one cell per band) | tall (two cells, 2x range)")
	flag.Parse()

	if *layer != "" && !*midiLive {
		fmt.Fprintln(os.Stderr, "-layer filters -midi-live only; a -midi-file has a track per layer to mute")
		os.Exit(2)
	}
	if *midiSplit && !*midiLive {
		fmt.Fprintln(os.Stderr, "-midi-split splits -midi-live's source; a -midi-file has a track per layer already")
		os.Exit(2)
	}
	if _, ok := engine.DeriveMood(*mood, 0, 0, 0); !ok {
		fmt.Fprintf(os.Stderr, "unknown mood %q; want one of: %s\n", *mood, strings.Join(engine.MoodNames(), ", "))
		os.Exit(2)
	}
	m, level := signedDepth(*mood, *depth)
	*mood = m
	p := deriveParams(*mood, uint64(seed), level, *tension, *lead)
	p.Feel = *feel
	s := engine.NewSynth(p)
	leadName := p.LeadName
	if leadName == "" {
		leadName = "none"
	}
	desc := fmt.Sprintf("mood=%s seed=%d level=%d tension=%.2f lead=%s bpm=%.0f root=%d", *mood, uint64(seed), level, *tension, leadName, p.BPM, p.Root)
	if p.Groove != "" {
		desc += " groove=" + p.Groove
	}
	if *themeFile != "" {
		t, err := loadTheme(*themeFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "-theme %s: %v\n", *themeFile, err)
			os.Exit(2)
		}
		// Before the first frame is rendered, so the theme's first grid starts at frame 0.
		s.PlayTheme(t)
		desc += fmt.Sprintf(" theme=%s (%d grids)", filepath.Base(*themeFile), len(t.Grids))
		durSet := false
		flag.Visit(func(f *flag.Flag) { durSet = durSet || f.Name == "dur" })
		if !durSet {
			*dur = themeSeconds(t, p, *dur)
		}
	}
	switch {
	case *midiFile != "":
		if err := writeMIDIFile(*midiFile, *wav, s, p, *dur); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("rendered %.0fs %s -> %s\n", *dur, desc, *midiFile)
	case *midiLive:
		if err := setLayers(*layer); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := playMIDILive(s, p, desc, *midiSplit); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case *wav != "":
		if err := engine.WriteWAV(*wav, s, *dur); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("rendered %.0fs %s -> %s\n", *dur, desc, *wav)
	case *play:
		stop, err := startPlayback(s)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer stop()
		runMeterUI(s, desc, *vu)
	default:
		flag.Usage()
	}
}

// seedFlag is -seed: an unsigned seed, or a negative number kept as its
// bits. A game's seeds are signed 64-bit, and one of those has to be
// playable here as the game gave it.
type seedFlag uint64

func (s *seedFlag) String() string { return strconv.FormatUint(uint64(*s), 10) }

func (s *seedFlag) Set(v string) error {
	u, err := parseSeed(v)
	if err != nil {
		return err
	}
	*s = seedFlag(u)
	return nil
}

// parseSeed reads a seed as -seed and the browser's scene switch take it.
// Base 0, as flag.Uint64 read -seed before it took negatives: 0x and 0o
// prefixes and underscores still work.
func parseSeed(v string) (uint64, error) {
	if u, err := strconv.ParseUint(v, 0, 64); err == nil {
		return u, nil
	}
	i, err := strconv.ParseInt(v, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("want an integer from %d to %d", int64(-1<<63), uint64(1<<64-1))
	}
	return uint64(i), nil
}

// layerList is the layers -layer takes, as setLayers reads them.
func layerList() string {
	n := engine.LayerNames()
	return strings.Join(n[:], ", ")
}

// themeTail is how long a render runs past a theme's last grid, for its last
// notes and the room to ring out.
const themeTail = 4.0

// themeSeconds is how long a render of t lasts when -dur was not given: the
// theme and its tail. A looping theme has no end, so it keeps def.
func themeSeconds(t *engine.Theme, p engine.Params, def float64) float64 {
	if t.Then == engine.ThenLoop {
		return def
	}
	grid := float64(engine.Steps*rowFrames(p)) / engine.SampleRate
	return float64(len(t.Grids))*grid + themeTail
}

// signedDepth applies the signed-depth sugar, where dungeon depth -3 means
// overworld elevation 3. It applies only to the dungeon, so an explicitly
// chosen mood is never replaced.
func signedDepth(mood string, depth int) (string, int) {
	if depth < 0 && (mood == "dungeon" || mood == "") {
		return "overworld", -depth
	}
	return mood, depth
}

// deriveParams turns the CLI's scene description into engine parameters. The
// browser build also calls it when the page switches scenes (play_js.go).
func deriveParams(mood string, seed uint64, level int, tension float64, lead string) engine.Params {
	p, ok := engine.DeriveMood(mood, seed, level, tension)
	if !ok {
		p = engine.DeriveDungeon(seed, level, tension)
	}
	if lead != "" {
		engine.ApplyLead(&p, lead)
	}
	return p
}
