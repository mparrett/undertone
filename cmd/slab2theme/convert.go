package main

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/mparrett/undertone/engine"
)

// Theme timing: rows are eighth notes and a grid is engine.Steps of them, so
// two bars of 4/4. Drum lanes run in sixteenths.
const (
	rowsPerBeat  = 2
	drumsPerBeat = 4
	gridBeats    = float64(engine.Steps) / rowsPerBeat
	onGrid       = 1e-6 // in rows or sixteenths: closer than this is on the grid
)

// lint is one thing in the Slab project the theme cannot say as written.
type lint struct {
	kind  string // off-grid, off-scale, range, overlap, chord, unmapped, merged
	track string
	beat  float64
	msg   string
}

// options is how to read the project: which tracks feed which lane, the key
// the melody was written in, and the window of song beats to take.
type options struct {
	scale     []int
	scaleName string
	root      int // pitch class of the key
	leadRoot  int // MIDI note degree 1 sits on in the lead lane; -1 picks an octave
	bassRoot  int // the same for the bass lane
	from      float64
	grids     int // 0: as many as the notes need
	bed       bool
	then      string
}

// lanes is the events of each lane, before conversion.
type lanes struct {
	lead, bass, pulse, drums []event
	drumMap                  func(pitch int) (lane int, ok bool)
}

// result is the converted theme and what was reported on the way.
type result struct {
	grids              []engine.Grid
	leadRoot, bassRoot int
	lints              []lint
	outside            int // notes before from or past the last grid
}

// convert quantizes the lanes into grids. It never moves or drops a note
// silently: every note that does not land as written is reported.
func convert(l lanes, o options) result {
	var r result
	n := o.grids
	if n == 0 {
		end := o.from
		for _, evs := range [][]event{l.lead, l.bass, l.pulse, l.drums} {
			for _, e := range evs {
				end = math.Max(end, e.beat+math.Max(e.len, 1e-9))
			}
		}
		n = max(1, int(math.Ceil((end-o.from)/gridBeats-onGrid)))
	}
	r.grids = make([]engine.Grid, n)
	for i := range r.grids {
		r.grids[i].Bed = o.bed
	}
	total := n * engine.Steps

	r.leadRoot = o.leadRoot
	if r.leadRoot < 0 {
		r.leadRoot = pickRoot(l.lead, o.scale, o.root)
	}
	r.bassRoot = o.bassRoot
	if r.bassRoot < 0 {
		r.bassRoot = pickRoot(l.bass, o.scale, o.root)
	}

	melodic := func(evs []event, root int, keepHigh bool, set func(g *engine.Grid, step, deg int)) {
		evs = sorted(evs)
		taken := map[int]event{}
		var held *event // the note sounding longest so far
		for i, e := range evs {
			row, ok := r.quantize(e, o.from, rowsPerBeat, total, "row")
			if !ok {
				continue
			}
			// Notes starting together are a chord, reported below.
			if held != nil && e.beat > held.beat+onGrid && e.beat < held.beat+held.len-onGrid {
				r.add("overlap", e, fmt.Sprintf("%s starts while %s still sounds; the lane is monophonic, and undertone keeps no note lengths", noteName(e.pitch), noteName(held.pitch)))
			}
			if held == nil || e.beat+e.len > held.beat+held.len {
				held = &evs[i]
			}
			deg, in := degreeOf(o.scale, root, e.pitch)
			if !in {
				r.add("off-scale", e, fmt.Sprintf("%s is not in %s %s; dropped", noteName(e.pitch), pcName(o.root), o.scaleName))
				continue
			}
			if deg < 1 || deg > engine.Degrees {
				r.add("range", e, fmt.Sprintf("%s is degree %d from %s, outside 1..%d; dropped", noteName(e.pitch), deg, noteName(root), engine.Degrees))
				continue
			}
			g, step := &r.grids[row/engine.Steps], row%engine.Steps
			if prev, ok := taken[row]; ok {
				keep, drop := prev, e
				if (e.pitch > prev.pitch) == keepHigh {
					keep, drop = e, prev
					set(g, step, deg)
				}
				r.add("chord", drop, fmt.Sprintf("%s and %s on one row; kept %s, dropped %s", noteName(prev.pitch), noteName(e.pitch), noteName(keep.pitch), noteName(drop.pitch)))
				taken[row] = keep
				continue
			}
			taken[row] = e
			set(g, step, deg)
		}
	}
	melodic(l.lead, r.leadRoot, true, func(g *engine.Grid, s, d int) { g.Lead[s] = d })
	melodic(l.bass, r.bassRoot, false, func(g *engine.Grid, s, d int) { g.Bass[s] = d })

	for _, e := range sorted(l.pulse) {
		if row, ok := r.quantize(e, o.from, rowsPerBeat, total, "row"); ok {
			r.grids[row/engine.Steps].Pulse[row%engine.Steps] = true
		}
	}

	if len(l.drums) > 0 {
		hits := make([][]byte, engine.DrumLanes)
		vel := make([][]int, engine.DrumLanes)
		for d := range hits {
			hits[d] = []byte(strings.Repeat(".", 2*total))
			vel[d] = make([]int, 2*total)
		}
		for _, e := range sorted(l.drums) {
			s, ok := r.quantize(e, o.from, drumsPerBeat, 2*total, "16th")
			if !ok {
				continue
			}
			d, ok := l.drumMap(e.pitch)
			if !ok {
				r.add("unmapped", e, fmt.Sprintf("drum note %s (%d) has no lane among kick, clap, shaker; dropped", noteName(e.pitch), e.pitch))
				continue
			}
			if vel[d][s] > 0 {
				r.add("merged", e, fmt.Sprintf("two hits on one 16th of the %s lane; kept the louder", drumLaneNames[d]))
			}
			if e.vel > vel[d][s] {
				vel[d][s] = e.vel
				hits[d][s] = velSymbol(e.vel)
			}
		}
		per := 2 * engine.Steps
		for i := range r.grids {
			for d := range hits {
				r.grids[i].Drums[d] = string(hits[d][i*per : (i+1)*per])
			}
		}
	}
	sort.SliceStable(r.lints, func(i, j int) bool { return r.lints[i].beat < r.lints[j].beat })
	return r
}

// quantize puts e on the nearest step of a grid with perBeat steps a beat,
// reporting it when that moves it. ok is false for a note outside the theme.
func (r *result) quantize(e event, from float64, perBeat, total int, unit string) (step int, ok bool) {
	x := (e.beat - from) * float64(perBeat)
	s := int(math.Round(x))
	if s < 0 || s >= total {
		r.outside++
		return 0, false
	}
	if off := x - float64(s); math.Abs(off) > onGrid {
		r.add("off-grid", e, fmt.Sprintf("%s is %s %s off the grid; moved %s", noteName(e.pitch), trimFloat(math.Abs(off)), unitOf(unit), direction(off)))
	}
	return s, true
}

func unitOf(unit string) string {
	if unit == "row" {
		return "rows"
	}
	return "16ths"
}

func direction(off float64) string {
	if off > 0 {
		return "earlier"
	}
	return "later"
}

func (r *result) add(kind string, e event, msg string) {
	r.lints = append(r.lints, lint{kind, e.track, e.beat, msg})
}

func sorted(evs []event) []event {
	evs = append([]event(nil), evs...)
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].beat < evs[j].beat })
	return evs
}

// degreeOf is the theme degree pitch plays in scale, counting degree 1 at
// root and wrapping by octave as the engine's degrees do: in a seven-note
// scale root+12 is degree 8, and the scale's last tone below root degree 0.
// in is false for a pitch not in the scale.
func degreeOf(scale []int, root, pitch int) (deg int, in bool) {
	off := pitch - root
	oct := floorDiv(off, 12)
	pc := off - 12*oct
	for i, t := range scale {
		if t == pc {
			return oct*len(scale) + i + 1, true
		}
	}
	return 0, false
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// pickRoot chooses the octave of pitch class pc that puts the most of a
// lane's in-scale notes within degrees 1..Degrees, the higher of two that
// tie. It gives middle C's octave for an empty lane.
func pickRoot(evs []event, scale []int, pc int) int {
	if len(evs) == 0 {
		return 60 + pc
	}
	lo, hi := evs[0].pitch, evs[0].pitch
	for _, e := range evs {
		lo, hi = min(lo, e.pitch), max(hi, e.pitch)
	}
	best, bestN := -1, -1
	for root := lo - 36; root <= hi; root++ {
		if ((root-pc)%12+12)%12 != 0 {
			continue
		}
		n := 0
		for _, e := range evs {
			if d, in := degreeOf(scale, root, e.pitch); in && d >= 1 && d <= engine.Degrees {
				n++
			}
		}
		if n >= bestN {
			best, bestN = root, n
		}
	}
	return best
}

var drumLaneNames = [engine.DrumLanes]string{"kick", "clap", "shaker"}

// drum2Lane maps a note for Slab's drum2 machine, which plays by pitch class
// (any octave): C kick, D snare, D# clap, F# closed hat, A# open hat. Its
// tom (A) has no lane here.
func drum2Lane(pitch int) (int, bool) {
	switch pitch % 12 {
	case 0, 11: // C kick, B a second kick
		return 0, true
	case 1, 2, 3, 4: // rim, snare, clap
		return 1, true
	case 6, 8, 10: // closed, pedal and open hat
		return 2, true
	}
	return 0, false
}

// gmLane maps General MIDI drum notes, which a sampler kit uses.
func gmLane(pitch int) (int, bool) {
	switch pitch {
	case 35, 36:
		return 0, true
	case 37, 38, 39, 40:
		return 1, true
	case 42, 44, 46, 54, 69, 70, 82: // hats, tambourine, cabasa, maracas, shaker
		return 2, true
	}
	return 0, false
}

// velSymbol is the groove symbol nearest a velocity, between slabkit's
// accent, hit and ghost velocities (122, 100, 58).
func velSymbol(v int) byte {
	switch {
	case v >= 111:
		return 'X'
	case v >= 79:
		return 'x'
	}
	return 'o'
}

var pcNames = []string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

func pcName(pc int) string { return pcNames[((pc%12)+12)%12] }

// noteName spells a MIDI note with C4 = 60, as Slab does.
func noteName(p int) string { return fmt.Sprintf("%s%d", pcName(p), floorDiv(p, 12)-1) }

// parseNote reads "D", "D4", "Eb3", "F#" or a MIDI number. octave is false
// when only a pitch class was given.
func parseNote(s string) (pitch int, octave bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false, fmt.Errorf("empty note")
	}
	if s[0] >= '0' && s[0] <= '9' {
		var n int
		if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n < 0 || n > 127 {
			return 0, false, fmt.Errorf("bad MIDI note %q", s)
		}
		return n, true, nil
	}
	pc := strings.Index("C D EF G A B", strings.ToUpper(s[:1]))
	if pc < 0 || s[0] == ' ' {
		return 0, false, fmt.Errorf("bad note %q", s)
	}
	rest := s[1:]
	for len(rest) > 0 && (rest[0] == '#' || rest[0] == 'b') {
		if rest[0] == '#' {
			pc++
		} else {
			pc--
		}
		rest = rest[1:]
	}
	if rest == "" {
		return ((pc % 12) + 12) % 12, false, nil
	}
	var oct int
	if _, err := fmt.Sscanf(rest, "%d", &oct); err != nil {
		return 0, false, fmt.Errorf("bad note %q", s)
	}
	return pc + 12*(oct+1), true, nil
}

// scales by name, for -scale. The modes are the ones undertone's moods use.
var scales = map[string][]int{
	"major":            {0, 2, 4, 5, 7, 9, 11},
	"ionian":           {0, 2, 4, 5, 7, 9, 11},
	"dorian":           {0, 2, 3, 5, 7, 9, 10},
	"phrygian":         {0, 1, 3, 5, 7, 8, 10},
	"lydian":           {0, 2, 4, 6, 7, 9, 11},
	"mixolydian":       {0, 2, 4, 5, 7, 9, 10},
	"minor":            {0, 2, 3, 5, 7, 8, 10},
	"aeolian":          {0, 2, 3, 5, 7, 8, 10},
	"locrian":          {0, 1, 3, 5, 6, 8, 10},
	"major-pentatonic": {0, 2, 4, 7, 9},
	"minor-pentatonic": {0, 3, 5, 7, 10},
}

// scaleName names a scale's intervals, or spells them out.
func scaleName(s []int) string {
	var match []string
	for name, t := range scales {
		if fmt.Sprint(t) == fmt.Sprint(s) && name != "ionian" && name != "aeolian" {
			match = append(match, name)
		}
	}
	if len(match) == 1 {
		return match[0]
	}
	return strings.Trim(strings.ReplaceAll(fmt.Sprint(s), " ", ","), "[]")
}
