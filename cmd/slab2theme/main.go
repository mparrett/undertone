// slab2theme turns a Slab project (.slab, the DAW's JSON project format)
// into an undertone theme, as the EDN map undertone/play-theme takes. Write
// the melody in Slab's piano roll or with its song scripts, then export it:
//
//	go run ./cmd/slab2theme -lead LEAD -bass BASS -drums DRUMS -mood village -root D song.slab > theme.edn
//	go run ./cmd/slab2theme -list song.slab          # the project's tracks
//
// A theme is written in scale degrees, so the tool needs the key the melody
// was written in: -root names its tonic and -scale (or -mood, for the
// scale a mood plays at -level) its mode. Each note becomes the degree it is
// in that key. Rows are eighth notes, sixteen to a grid (two bars of 4/4),
// and drums are sixteenths in groove notation; Slab's tempo, note lengths
// and melodic velocities have no place in a theme and are dropped.
//
// It lints rather than fixes: a note off the scale, beyond degrees 1..8,
// off the grid, or overlapping another in a monophonic lane is reported with
// its bar:beat, and -strict fails on any of them. Off-grid notes are moved
// to the nearest step; notes that cannot be written are left out.
//
// It reads Slab's file format only; no Slab code is used.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mparrett/undertone/engine"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("slab2theme", flag.ContinueOnError)
	fs.SetOutput(stderr)
	lead := fs.String("lead", "", "track(s) for the lead lane, by name; a comma joins several")
	bass := fs.String("bass", "", "track(s) for the bass lane")
	pulse := fs.String("pulse", "", "track(s) whose note starts set the pulse lane")
	drums := fs.String("drums", "", "drum track(s) for the kick, clap and shaker lanes")
	drumMap := fs.String("drum-map", "auto", "drum notes: drum2 (by pitch class), gm (General MIDI), or auto (drum2 for a drum2 track, else gm)")
	root := fs.String("root", "", "tonic of the key the melody is in: D, F#, Bb; D4 or 62 pins the lead's degree 1 to that note")
	bassRoot := fs.String("bass-root", "", "pins the bass lane's degree 1 to a note, as -root does the lead's")
	scale := fs.String("scale", "", "the mode: "+strings.Join(scaleNames(), ", ")+", or semitones like 0,2,3,5,7,9,10")
	mood := fs.String("mood", "", "take the scale a mood plays: "+strings.Join(engine.MoodNames(), ", "))
	level := fs.Int("level", 0, "the mood's level, which picks its scale")
	from := fs.Float64("from", 0, "song beat the theme starts at")
	grids := fs.Int("grids", 0, "number of grids (two bars each); 0 covers the last note")
	bed := fs.Bool("bed", true, "let the generator's drone, pad and air play under the theme")
	then := fs.String("then", "generate", "after the last grid: generate, silence or loop")
	strict := fs.Bool("strict", false, "fail, and write nothing, when anything is linted")
	out := fs.String("o", "", "write the EDN here instead of stdout")
	list := fs.Bool("list", false, "list the project's note tracks and exit")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: slab2theme [flags] song.slab")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	path := fs.Arg(0)
	fail := func(err error) int {
		fmt.Fprintln(stderr, "slab2theme:", err)
		return 2
	}
	p, err := loadProject(path)
	if err != nil {
		return fail(err)
	}
	if *list {
		for _, t := range p.Tracks {
			if t.Kind == "bus" {
				continue
			}
			evs, _ := t.events()
			fmt.Fprintf(stdout, "%-24q %-12s %d notes\n", t.Name, t.Instrument.Machine, len(evs))
		}
		return 0
	}
	if *lead == "" && *bass == "" && *pulse == "" && *drums == "" {
		return fail(fmt.Errorf("name at least one of -lead, -bass, -pulse, -drums (-list shows the tracks)"))
	}
	switch *then {
	case "generate", "silence", "loop":
	default:
		return fail(fmt.Errorf("-then must be generate, silence or loop, got %q", *then))
	}

	o := options{from: *from, grids: *grids, bed: *bed, then: *then, leadRoot: -1, bassRoot: -1}
	switch {
	case *mood != "" && *scale != "":
		return fail(fmt.Errorf("give -scale or -mood, not both"))
	case *mood != "":
		mp, ok := engine.DeriveMood(*mood, 1, *level, 0) // a mood's scale depends on its level only
		if !ok {
			return fail(fmt.Errorf("unknown mood %q; want one of %s", *mood, strings.Join(engine.MoodNames(), ", ")))
		}
		o.scale = mp.Scale
	case *scale != "":
		if o.scale, err = parseScale(*scale); err != nil {
			return fail(err)
		}
	default:
		if *lead != "" || *bass != "" {
			return fail(fmt.Errorf("the melodic lanes need a key: give -root and -scale or -mood"))
		}
		o.scale = scales["major"] // drums and pulse alone use no key
	}
	o.scaleName = scaleName(o.scale)
	if *root == "" && (*lead != "" || *bass != "") {
		return fail(fmt.Errorf("the melodic lanes need -root, the tonic of the key the melody is in"))
	}
	if *root != "" {
		n, pinned, err := parseNote(*root)
		if err != nil {
			return fail(err)
		}
		o.root = n % 12
		if pinned {
			o.leadRoot = n
		}
	}
	if *bassRoot != "" {
		n, pinned, err := parseNote(*bassRoot)
		if err != nil {
			return fail(err)
		}
		if !pinned || n%12 != o.root {
			return fail(fmt.Errorf("-bass-root %s must be a note with an octave on the tonic %s", *bassRoot, pcName(o.root)))
		}
		o.bassRoot = n
	}

	var l lanes
	for _, lane := range []struct {
		names string
		dst   *[]event
	}{{*lead, &l.lead}, {*bass, &l.bass}, {*pulse, &l.pulse}, {*drums, &l.drums}} {
		for name := range strings.SplitSeq(lane.names, ",") {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			t, err := p.findTrack(name)
			if err != nil {
				return fail(err)
			}
			evs, err := t.events()
			if err != nil {
				return fail(err)
			}
			if lane.dst == &l.drums && l.drumMap == nil {
				switch {
				case *drumMap == "drum2" || (*drumMap == "auto" && t.Instrument.Machine == "drum2"):
					l.drumMap = drum2Lane
				case *drumMap == "gm" || *drumMap == "auto":
					l.drumMap = gmLane
				default:
					return fail(fmt.Errorf("-drum-map must be drum2, gm or auto, got %q", *drumMap))
				}
			}
			*lane.dst = append(*lane.dst, evs...)
		}
	}

	r := convert(l, o)

	for _, m := range p.Meter {
		if m.Num != 4 || m.Den != 4 {
			fmt.Fprintf(stderr, "note: the song is in %d/%d from bar %d; a grid is 8 beats, so grids and bars do not line up there\n", m.Num, m.Den, m.Bar+1)
		}
	}
	for _, x := range r.lints {
		fmt.Fprintf(stderr, "%s %s %s: %s\n", x.track, p.barBeat(x.beat), x.kind, x.msg)
	}
	if r.outside > 0 {
		fmt.Fprintf(stderr, "note: %d note(s) outside the %d grid(s) from beat %s were not taken\n", r.outside, len(r.grids), trimFloat(o.from))
	}
	key := fmt.Sprintf("%s %s", pcName(o.root), o.scaleName)
	if *mood != "" {
		key += fmt.Sprintf(" (the scale of %s at level %d)", *mood, *level)
	}
	fmt.Fprintf(stderr, "%d grid(s) in %s, lead degree 1 = %s, bass degree 1 = %s; %d lint(s); Slab tempo %s bpm is not kept, the mood sets it\n",
		len(r.grids), key, noteName(r.leadRoot), noteName(r.bassRoot), len(r.lints), trimFloat(p.Transport.BPM))
	if *strict && len(r.lints) > 0 {
		fmt.Fprintln(stderr, "slab2theme: -strict: not writing a theme with lints")
		return 1
	}

	header := []string{
		"undertone theme from a Slab project, by slab2theme",
		fmt.Sprintf("key %s; lead degree 1 = %s, bass = %s", key, noteName(r.leadRoot), noteName(r.bassRoot)),
	}
	w := stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return fail(err)
		}
		defer f.Close()
		w = f
	}
	if err := writeEDN(w, r.grids, o.then, l, header); err != nil {
		return fail(err)
	}
	return 0
}

func parseScale(s string) ([]int, error) {
	if t, ok := scales[strings.ToLower(s)]; ok {
		return t, nil
	}
	var t []int
	for f := range strings.SplitSeq(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 0 || n > 11 || (len(t) > 0 && n <= t[len(t)-1]) {
			return nil, fmt.Errorf("unknown scale %q: give a name (%s) or rising semitones from 0, like 0,2,3,5,7,9,10", s, strings.Join(scaleNames(), ", "))
		}
		t = append(t, n)
	}
	if len(t) == 0 || t[0] != 0 {
		return nil, fmt.Errorf("scale %q must start at 0, the tonic", s)
	}
	return t, nil
}

func scaleNames() []string {
	return []string{"major", "minor", "dorian", "phrygian", "lydian", "mixolydian", "locrian", "major-pentatonic", "minor-pentatonic"}
}
