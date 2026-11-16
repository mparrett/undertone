package engine

import (
	"fmt"
	"reflect"
	"testing"
)

// unitsBuild decodes units back to a patch with no checks. UnitSpec.unit
// holds a unit to a one-shot's bounds, and the music's patches go past them
// (the dungeon drone releases over 4 s; a shot may take 2), so it cannot be
// the round trip's other half.
func unitsBuild(t *testing.T, what string, us []UnitSpec) Patch {
	t.Helper()
	p := make(Patch, len(us))
	for i, u := range us {
		op := indexOf(opNames, u.Op)
		if op < 0 || len(u.Args) != opArgs[op] {
			t.Fatalf("%s unit %d: op %q with %d args", what, i, u.Op, len(u.Args))
		}
		p[i].op = op
		copy(p[i].p[:], u.Args)
		if op == opOsc { // the shape sits between transpose and the sweep
			p[i].p = [4]float64{u.Args[0], float64(indexOf(shapeNames, u.Shape)), u.Args[1], u.Args[2]}
		}
	}
	return p
}

// Every built-in palette read as data names each patch and rebuilds it
// exactly, drum lanes included, so the spec is the palette and not a sketch.
func TestPaletteSpecRoundTrip(t *testing.T) {
	for _, name := range PaletteNames() {
		pl, _ := PaletteByName(name)
		s, ok := PaletteSpecByName(name)
		if !ok || s.Name != name {
			t.Fatalf("%s: no spec", name)
		}
		type track struct {
			track string
			spec  PatchSpec
			p     Patch
		}
		tracks := []track{
			{"drone", s.Drone, pl.Drone}, {"fifth", s.Fifth, pl.Fifth}, {"pad", s.Pad, pl.Pad},
			{"bell", s.Bell, pl.Bell}, {"air", s.Air, pl.Air}, {"pulse", s.Pulse, pl.Pulse},
		}
		// the lane the engine plays, default-kit fallback included
		params := Params{Palette: pl}
		for i := range nDrums {
			d := params.sound(i)
			tracks = append(tracks, track{fmt.Sprintf("kit lane %d", i), s.Kit[i].PatchSpec, d.patch})
			if s.Kit[i].Note != d.note {
				t.Errorf("%s kit lane %d: note %d, want %d", name, i, s.Kit[i].Note, d.note)
			}
			if def := pl.Kit[i].patch == nil; s.Kit[i].Default != def {
				t.Errorf("%s kit lane %d: Default %v, want %v", name, i, s.Kit[i].Default, def)
			}
		}
		for _, tr := range tracks {
			what := name + " " + tr.track
			if tr.spec.Name == "" {
				t.Errorf("%s: unnamed patch", what)
			}
			if got := unitsBuild(t, what, tr.spec.Units); !reflect.DeepEqual(got, tr.p) {
				t.Errorf("%s (%s): the units build a different patch", what, tr.spec.Name)
			}
		}
	}
	if _, ok := PaletteSpecByName("no-such-palette"); ok {
		t.Error("an unknown palette has a spec")
	}
}

// Every lead read as data rebuilds its patch and keeps its gate.
func TestLeadSpecRoundTrip(t *testing.T) {
	for _, name := range LeadNames() {
		d := leadLib[name]
		s, ok := LeadSpecByName(name)
		if !ok || s.Name != name || s.Gate != d.gate {
			t.Fatalf("%s: spec %+v", name, s)
		}
		if got := unitsBuild(t, name, s.Units); !reflect.DeepEqual(got, d.patch) {
			t.Errorf("%s: the units build a different patch", name)
		}
	}
}
