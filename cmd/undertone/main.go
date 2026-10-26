// undertone: a tiny generative-ambience engine. Architecturally a
// demo-sized homage to vsariola/sointu — no sointu code, just its shape: instruments are stacks of unit opcodes
// run by a tiny per-sample VM, a tracker-style sequencer drives them, and
// the whole track is derived deterministically from a seed.
//
// Register: roguelike ambience. Parameters derive from game state —
// mood (dungeon/overworld/meadow/aurora/village/rust/forge), level (depth darkens, elevation brightens),
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
//	go run ./cmd/undertone -play -mood overworld -depth 3 # bright mirror
//	go run ./cmd/undertone -play -mood meadow             # brighter still
//	go run ./cmd/undertone -play -mood aurora             # thin, slow, high
//	go run ./cmd/undertone -play -mood village -depth 5   # drums: the festival
//	go run ./cmd/undertone -play -mood rust -tension 0.6  # industrial, grinding
//	go run ./cmd/undertone -play -mood forge -depth 3     # industrial, driving
//	go run ./cmd/undertone -wav out.wav -depth 4 -lead pluck  # deterministic render
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mparrett/undertone/engine"
)

func main() {
	play := flag.Bool("play", false, "play live (endless) via oto")
	wav := flag.String("wav", "", "render to WAV file")
	dur := flag.Float64("dur", 48, "WAV duration seconds")
	seed := flag.Uint64("seed", 1, "level seed")
	depth := flag.Int("depth", 0, "level within the mood (for dungeon, negative = overworld elevation)")
	mood := flag.String("mood", "dungeon", strings.Join(engine.MoodNames(), " | ")+" (level = depth)")
	lead := flag.String("lead", "", "lead instrument: bell|chime|clang|kalimba|reed|pluck (default: seed picks)")
	tension := flag.Float64("tension", 0, "0..1: tempo up, pulse in, harmony sours")
	vu := flag.String("vu", "bars", "meter style: bars | compact (one cell per band) | tall (two cells, 2x range)")
	flag.Parse()

	if _, ok := engine.DeriveMood(*mood, 0, 0, 0); !ok {
		fmt.Fprintf(os.Stderr, "unknown mood %q; want one of: %s\n", *mood, strings.Join(engine.MoodNames(), ", "))
		os.Exit(2)
	}
	m, level := signedDepth(*mood, *depth)
	*mood = m
	p := deriveParams(*mood, *seed, level, *tension, *lead)
	s := engine.NewSynth(p)
	desc := fmt.Sprintf("mood=%s seed=%d level=%d tension=%.2f lead=%s bpm=%.0f root=%d", *mood, *seed, level, *tension, p.LeadName, p.BPM, p.Root)
	if p.Groove != "" {
		desc += " groove=" + p.Groove
	}
	switch {
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
