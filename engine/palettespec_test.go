package engine

import (
	"fmt"
	"reflect"
	"testing"
)

// unitsBuild builds units as a registered music patch is built, so the round
// trips below also hold every built-in patch to the bounds a game's are held
// to: a bound none of them passes would refuse a copy of the music itself.
func unitsBuild(t *testing.T, what string, us []UnitSpec) Patch {
	t.Helper()
	p, err := buildPatch(us, musicBounds)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return p
}

// Every built-in palette read as data names each patch and rebuilds it
// exactly, drum lanes included, so the spec is the palette and not a sketch.
func TestPaletteSpecRoundTrip(t *testing.T) {
	for name, pl := range palettes {
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
	for name, d := range leadLib {
		s, ok := LeadSpecByName(name)
		if !ok || s.Name != name || s.Gate != d.gate {
			t.Fatalf("%s: spec %+v", name, s)
		}
		if got := unitsBuild(t, name, s.Units); !reflect.DeepEqual(got, d.patch) {
			t.Errorf("%s: the units build a different patch", name)
		}
	}
}
