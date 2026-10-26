package engine

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// Line is one parameter's response to level: Base + Step*level, clamped to
// [Lo, Hi]. An unbounded side is ±Inf.
type Line struct{ Base, Step, Lo, Hi float64 }

// MoodSpec is a mood row as data: the form a designer edits and exports. A
// built-in mood converts to one and back without changing what it derives.
// Patches stay in code; Palette names a built-in bank.
type MoodSpec struct {
	Name               string
	Scales             [][]int
	BPM                Line
	RootBase, RootStep int
	Drone, Fifth       Line
	Pad, Bell, Air     Line
	Calm, BellLift     int
	Palette            string
	Leads              []string
	Grooves            []string
	Drums, Busy        Line
	Swing              float64
	TenseDrums         float64 // drum gain at full tension for drums the level leaves silent
	Open               Line    // how far level opens (+) or closes (-) the svf filters, clamped to -1..1
	Room               Room    // the reverb room; it does not move with level
}

var palettes = map[string]Palette{
	"dungeon":   dungeonPalette,
	"overworld": overworldPalette,
	"aurora":    auroraPalette,
	"rust":      rustPalette,
}

// GrooveNames lists the grooves a MoodSpec can name, sorted.
func GrooveNames() []string {
	names := make([]string, 0, len(grooveLib))
	for n := range grooveLib {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// LeadNames lists the leads a MoodSpec can name, sorted.
func LeadNames() []string {
	names := make([]string, 0, len(leadLib))
	for n := range leadLib {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// PaletteNames lists the patch banks a MoodSpec can name.
func PaletteNames() []string { return []string{"dungeon", "overworld", "aurora", "rust"} }

// PaletteByName returns a patch bank by the name a MoodSpec uses for it.
func PaletteByName(name string) (Palette, bool) {
	p, ok := palettes[name]
	return p, ok
}

func (l Line) internal() line { return line{l.Base, l.Step, l.Lo, l.Hi} }

// export converts a table line. A mood without drums leaves its drum lines
// as zero values, clamped to [0, 0]; they derive 0 either way, so they export
// unbounded, leaving a designer who adds a groove free to raise them.
func (l line) export() Line {
	if l == (line{}) {
		l = lin(0, 0)
	}
	return Line{l.base, l.step, l.lo, l.hi}
}

// cloneScales copies a scale list, so a spec and the mood table never share
// one: an edit to either would otherwise change what the other derives.
func cloneScales(sc [][]int) [][]int {
	out := make([][]int, len(sc))
	for i, s := range sc {
		out[i] = slices.Clone(s)
	}
	return out
}

// MoodByName returns a built-in mood as a spec. The spec owns its slices, so
// editing it leaves the built-in mood alone.
func MoodByName(name string) (MoodSpec, bool) {
	for _, m := range moods {
		if m.name != name {
			continue
		}
		s := MoodSpec{
			Name: m.name, Scales: cloneScales(m.scales), BPM: m.bpm.export(),
			RootBase: m.rootBase, RootStep: m.rootStep,
			Drone: m.drone.export(), Fifth: m.fifth.export(), Pad: m.pad.export(),
			Bell: m.bell.export(), Air: m.air.export(),
			Calm: m.calm, BellLift: m.bellLift,
			Leads: slices.Clone(m.leads), Grooves: slices.Clone(m.grooves),
			Drums: m.drums.export(), Busy: m.busy.export(), Swing: m.swing,
			TenseDrums: m.tenseDrums, Open: m.open.export(), Room: m.room,
		}
		for name, p := range palettes {
			if reflect.DeepEqual(p, m.palette) {
				s.Palette = name
			}
		}
		return s, true
	}
	return MoodSpec{}, false
}

// Derive maps game state to music for this spec, exactly as DeriveMood does
// for a built-in mood. It reports an error for a spec that names a palette,
// lead or groove the engine does not have, or has no scales or a scale
// outside five to seven tones.
func (s MoodSpec) Derive(seed uint64, level int, tension float64) (Params, error) {
	pal, ok := palettes[s.Palette]
	if !ok {
		return Params{}, fmt.Errorf("unknown palette %q", s.Palette)
	}
	if len(s.Scales) == 0 || len(s.Leads) == 0 {
		return Params{}, fmt.Errorf("a mood needs scales and leads")
	}
	for _, sc := range s.Scales {
		// Five tones is the fewest the pad's chord (tones 1, 3 and 5) can
		// voice without doubling; past seven, a grid's 8 rows would not
		// reach the octave.
		if len(sc) < 5 || len(sc) > 7 {
			return Params{}, fmt.Errorf("scale %v: the sequencer needs five to seven degrees", sc)
		}
	}
	for _, l := range s.Leads {
		if _, ok := leadLib[l]; !ok {
			return Params{}, fmt.Errorf("unknown lead %q", l)
		}
	}
	for _, g := range s.Grooves {
		if _, ok := grooveLib[g]; !ok {
			return Params{}, fmt.Errorf("unknown groove %q", g)
		}
	}
	m := mood{
		name: s.Name, scales: cloneScales(s.Scales), bpm: s.BPM.internal(),
		rootBase: s.RootBase, rootStep: s.RootStep,
		drone: s.Drone.internal(), fifth: s.Fifth.internal(), pad: s.Pad.internal(),
		bell: s.Bell.internal(), air: s.Air.internal(),
		calm: s.Calm, bellLift: s.BellLift, palette: pal, leads: slices.Clone(s.Leads),
		grooves: slices.Clone(s.Grooves), drums: s.Drums.internal(), busy: s.Busy.internal(), swing: s.Swing,
		tenseDrums: s.TenseDrums, open: s.Open.internal(), room: s.Room,
	}
	return m.derive(seed, level, tension), nil
}

// EDN writes the spec as an entry for the moods map, in the shape
// ambience/moods has: a clamp appears only where it binds. The table's own
// entries omit :palette, since the host picks their patches by mood name;
// an entry for a mood the host has no row for names one to pick its patches.
func (s MoodSpec) EDN() string {
	num := func(f float64) string {
		t := fmt.Sprintf("%g", f)
		if !strings.ContainsAny(t, ".e") {
			t += ".0"
		}
		return t
	}
	ln := func(l Line) string {
		out := "{:base " + num(l.Base) + " :step " + num(l.Step)
		if !math.IsInf(l.Lo, -1) {
			out += " :lo " + num(l.Lo)
		}
		if !math.IsInf(l.Hi, 1) {
			out += " :hi " + num(l.Hi)
		}
		return out + "}"
	}
	kws := func(names []string) string {
		parts := make([]string, len(names))
		for i, n := range names {
			parts[i] = ":" + n
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	scales := make([]string, len(s.Scales))
	for i, sc := range s.Scales {
		scales[i] = fmt.Sprint(sc) // [0 2 3 5 7 9 10]: an EDN vector already
	}
	var b strings.Builder
	fmt.Fprintf(&b, ":%s\n{:scales    [%s]\n", s.Name, strings.Join(scales, " "))
	fmt.Fprintf(&b, " :bpm       %s\n :root-base %d :root-step %d\n", ln(s.BPM), s.RootBase, s.RootStep)
	for _, f := range []struct {
		k string
		l Line
	}{{"drone", s.Drone}, {"fifth", s.Fifth}, {"pad", s.Pad}, {"bell", s.Bell}, {"air", s.Air}} {
		fmt.Fprintf(&b, " :%-9s %s\n", f.k, ln(f.l))
	}
	fmt.Fprintf(&b, " :calm      %d\n :bell-lift %d\n", s.Calm, s.BellLift)
	if s.Palette != "" { // a bare ":" would not read back
		fmt.Fprintf(&b, " :palette   :%s\n", s.Palette)
	}
	// the default is the zero line, unclamped; a clamp alone still moves it
	if s.Open.Base != 0 || s.Open.Step != 0 || !math.IsInf(s.Open.Lo, -1) || !math.IsInf(s.Open.Hi, 1) {
		fmt.Fprintf(&b, " :open      %s\n", ln(s.Open))
	}
	if s.Room != (Room{}) {
		fmt.Fprintf(&b, " :room      {:size %s :damp %s}\n", num(s.Room.Size), num(s.Room.Damp))
	}
	fmt.Fprintf(&b, " :leads     %s", kws(s.Leads))
	if len(s.Grooves) > 0 {
		fmt.Fprintf(&b, "\n :grooves   %s\n :drums     %s\n :busy      %s\n :swing     %s",
			kws(s.Grooves), ln(s.Drums), ln(s.Busy), num(s.Swing))
		if s.TenseDrums > 0 {
			fmt.Fprintf(&b, "\n :tense-drums %s", num(s.TenseDrums))
		}
	}
	return b.String() + "}"
}
