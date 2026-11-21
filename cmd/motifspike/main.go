// motifspike renders a mood with and without the phrase arc and motif
// memory, for listening to the spike side by side. Tension can hold or ramp.
//
//	go run ./cmd/motifspike -mood rust -seed 7 -dur 150 -temper -wav on.wav
//	go run ./cmd/motifspike -mood dungeon -ramp 0,0.8 -wav ramp-off.wav
//	go run ./cmd/motifspike -mood rust -temper -parts phrasing -notes arc.tsv
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mparrett/undertone/engine"
)

func main() {
	mood := flag.String("mood", "dungeon", "mood name")
	seed := flag.Uint64("seed", 1, "seed")
	level := flag.Int("level", 2, "level within the mood")
	tension := flag.Float64("tension", 0.3, "fixed tension (ignored with -ramp)")
	ramp := flag.String("ramp", "", "from,to: tension rises linearly over the render")
	dur := flag.Float64("dur", 150, "seconds")
	temper := flag.Bool("temper", false, "turn on the phrase arc and motif memory")
	wav := flag.String("wav", "", "output WAV")
	trace := flag.Bool("trace", false, "print the arc and each lane's choice per pattern")
	parts := flag.String("parts", "all", "with -temper, what to turn on: all, phrasing or memory")
	notesOut := flag.String("notes", "", "write every note event as TSV, to compare renders note for note")
	memPreset := flag.String("memory-preset", "", "with -temper, a memory preset in place of the mood's (held, evolving, wandering, restless)")
	phrPreset := flag.String("phrasing-preset", "", "with -temper, a phrasing preset's lengths and breaks on the mood's phrasing (patient, steady, breathless)")
	flag.Parse()
	usePhrasing, useMemory := *parts != "memory", *parts != "phrasing"
	if *parts != "all" && *parts != "phrasing" && *parts != "memory" {
		fmt.Fprintf(os.Stderr, "-parts wants all, phrasing or memory, not %q\n", *parts)
		os.Exit(2)
	}

	// presets resolve once, here, and apply in params over the mood's own
	var memP *engine.MemoryPreset
	var phrP *engine.PhrasingPreset
	if (*memPreset != "" || *phrPreset != "") && !*temper {
		fmt.Fprintln(os.Stderr, "-memory-preset and -phrasing-preset need -temper")
		os.Exit(2)
	}
	if *memPreset != "" {
		ps := engine.MemoryPresets()
		i := slices.IndexFunc(ps, func(x engine.MemoryPreset) bool { return x.Name == *memPreset })
		if i < 0 {
			fmt.Fprintf(os.Stderr, "no memory preset %q\n", *memPreset)
			os.Exit(2)
		}
		memP = &ps[i]
	}
	if *phrPreset != "" {
		ps := engine.PhrasingPresets()
		i := slices.IndexFunc(ps, func(x engine.PhrasingPreset) bool { return x.Name == *phrPreset })
		if i < 0 {
			fmt.Fprintf(os.Stderr, "no phrasing preset %q\n", *phrPreset)
			os.Exit(2)
		}
		phrP = &ps[i]
	}

	from, to := *tension, *tension
	if *ramp != "" {
		a, b, ok := strings.Cut(*ramp, ",")
		x, err1 := strconv.ParseFloat(a, 64)
		y, err2 := strconv.ParseFloat(b, 64)
		if !ok || err1 != nil || err2 != nil {
			fmt.Fprintln(os.Stderr, "-ramp wants from,to")
			os.Exit(2)
		}
		from, to = x, y
	}
	params := func(t float64) engine.Params {
		p, ok := engine.DeriveMood(*mood, *seed, *level, t)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown mood %q\n", *mood)
			os.Exit(2)
		}
		// a derived mood plays its own temperament: turn off what this
		// render isn't asking for
		p.NoPhrasing = !*temper || !usePhrasing
		p.NoMemory = !*temper || !useMemory
		if *temper {
			tm, ok := engine.TemperamentFor(*mood)
			if !ok {
				fmt.Fprintf(os.Stderr, "mood %q has no temperament\n", *mood)
				os.Exit(2)
			}
			if memP != nil {
				tm.Memory = memP.Memory
			}
			if phrP != nil {
				tm.Phrasing = phrP.Apply(tm.Phrasing)
			}
			if usePhrasing {
				p.Phrasing = &tm.Phrasing
			}
			if useMemory {
				p.Memory = &tm.Memory
			}
		}
		return p
	}

	s := engine.NewSynth(params(from))
	total := int(*dur * engine.SampleRate)
	pcm := make([]byte, total*4)
	const block = 1024
	var last engine.MotifState
	var notes bytes.Buffer
	var cursor uint64
	var evs []engine.NoteEvent
	names := engine.LayerNames()
	start := time.Now()
	for done := 0; done < total; done += block {
		n := min(block, total-done)
		// staged: the synth takes it at the next pattern boundary
		t := from + (to-from)*float64(done)/float64(total)
		if from != to {
			s.SetParameters(params(t))
		}
		if _, err := io.ReadFull(s, pcm[done*4:(done+n)*4]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if *notesOut != "" {
			var dropped uint64
			evs, cursor, dropped = s.NoteEvents(cursor, evs[:0])
			if dropped > 0 {
				fmt.Fprintf(os.Stderr, "%d note events dropped\n", dropped)
				os.Exit(1)
			}
			for _, e := range evs {
				fmt.Fprintf(&notes, "%d\t%s\t%s\t%d\t%d\t%.4f\t%d\t%d\n",
					e.Frame, names[e.Layer], e.Kind, e.Lane, e.Note, e.Gain, e.Pattern, e.Row)
			}
		}
		if *trace {
			if m := s.MotifTrace(); m != last {
				note := ""
				if m.Fill {
					note += " fill"
				}
				if m.Held && m.Section == "return" && !last.Held {
					note += " (break held off)"
				}
				fmt.Printf("%6.1fs t=%.2f phrase %d %-6s %-14s lead=%-7s pad=%-7s%s\n",
					float64(done)/engine.SampleRate, m.Tension, m.Phrase, m.Section, m.Cycle, m.Lead, m.Pad, note)
				last = m
			}
		}
	}
	el := time.Since(start)
	fmt.Fprintf(os.Stderr, "%s seed=%d level=%d tension=%v..%v temper=%v: %.0fs in %v (%.0fx real time)\n",
		*mood, *seed, *level, from, to, *temper, *dur, el.Round(time.Millisecond), *dur/el.Seconds())
	if *notesOut != "" {
		if err := os.WriteFile(*notesOut, notes.Bytes(), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *wav != "" {
		if err := engine.WriteWAV(*wav, bytes.NewReader(pcm), *dur); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
