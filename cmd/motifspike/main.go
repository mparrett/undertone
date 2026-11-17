// motifspike renders a mood with and without motif memory and the phrase
// arc, for listening to the spike side by side. Tension can hold or ramp.
//
//	go run ./cmd/motifspike -mood rust -seed 7 -dur 150 -temper -wav on.wav
//	go run ./cmd/motifspike -mood dungeon -ramp 0,0.8 -wav ramp-off.wav
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
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
	temper := flag.Bool("temper", false, "turn on motif memory and the phrase arc")
	wav := flag.String("wav", "", "output WAV")
	trace := flag.Bool("trace", false, "print the arc and each lane's choice per pattern")
	flag.Parse()

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
		if *temper {
			tm, ok := engine.TemperamentFor(*mood)
			if !ok {
				fmt.Fprintf(os.Stderr, "mood %q has no temperament\n", *mood)
				os.Exit(2)
			}
			p.Temperament = &tm
		}
		return p
	}

	s := engine.NewSynth(params(from))
	total := int(*dur * engine.SampleRate)
	pcm := make([]byte, total*4)
	const block = 1024
	lastSec, lastLead, lastPad := "", "", ""
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
		if *trace {
			sec, ph, lead, pad := s.MotifTrace()
			if sec != lastSec || lead != lastLead || pad != lastPad {
				fmt.Printf("%6.1fs t=%.2f phrase %d %-6s lead=%-7s pad=%s\n", float64(done)/engine.SampleRate, t, ph, sec, lead, pad)
				lastSec, lastLead, lastPad = sec, lead, pad
			}
		}
	}
	el := time.Since(start)
	fmt.Fprintf(os.Stderr, "%s seed=%d level=%d tension=%v..%v temper=%v: %.0fs in %v (%.0fx real time)\n",
		*mood, *seed, *level, from, to, *temper, *dur, el.Round(time.Millisecond), *dur/el.Seconds())
	if *wav != "" {
		if err := engine.WriteWAV(*wav, bytes.NewReader(pcm), *dur); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
