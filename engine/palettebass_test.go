package engine

import (
	"reflect"
	"testing"
)

// bassUnder is the patch a theme's bass note plays under palette pl.
func bassUnder(t *testing.T, pl Palette) Patch {
	t.Helper()
	p := DeriveOverworld(1, 2, 0)
	p.Palette = pl
	g := Grid{Bed: true}
	g.Bass[0] = 1
	s := NewSynth(p)
	s.pats = buildPatterns(p, 0)
	s.applyGrid(&g, 0)
	tr := s.pats[trackBass][0]
	if tr.note == 0 {
		t.Fatal("no bass note on row 0")
	}
	return tr.patch
}

// A palette that names no bass plays the string bass every theme had; one
// that names its own plays that, at the same lane gain.
func TestPaletteBassPlaysUnderATheme(t *testing.T) {
	rust, _ := PaletteByName("rust")
	if got := bassUnder(t, rust); !reflect.DeepEqual(got, gridBass) {
		t.Error("a built-in palette's theme bass is not the default string bass")
	}

	s, _ := PaletteSpecByName("rust")
	s.Name, s.Bass = "test-bass-pad", PatchSpec{Name: "pad"}
	if err := RegisterPalette(s); err != nil {
		t.Fatal(err)
	}
	pl, _ := PaletteByName("test-bass-pad")
	if got := bassUnder(t, pl); !reflect.DeepEqual(got, pad) {
		t.Error("a palette's own bass is not what its theme bass plays")
	}
}

// Left out, the bass reads back as the default, and a palette registered
// from that read-back equals the original; a bass of its own survives the
// trip through EDN's spec.
func TestPaletteBassReadsBack(t *testing.T) {
	s, _ := PaletteSpecByName("rust")
	s.Name, s.Bass = "test-bass-none", PatchSpec{}
	if err := RegisterPalette(s); err != nil {
		t.Fatal(err)
	}
	got, _ := PaletteSpecByName("test-bass-none")
	if !reflect.DeepEqual(got.Bass, patchSpec(gridBass)) {
		t.Errorf("an empty bass reads back as %+v, want the default %+v", got.Bass, patchSpec(gridBass))
	}
	got.Name = "test-bass-copy"
	if err := RegisterPalette(got); err != nil {
		t.Fatal(err)
	}
	a, _ := PaletteByName("test-bass-none")
	b, _ := PaletteByName("test-bass-copy")
	if a.Bass != nil || b.Bass != nil {
		t.Error("the default bass, left out or named, should be stored as the default")
	}

	own := PatchSpec{Units: unitSpecs(pad)}
	s.Name, s.Bass = "test-bass-own", own
	if err := RegisterPalette(s); err != nil {
		t.Fatal(err)
	}
	got, _ = PaletteSpecByName("test-bass-own")
	if !reflect.DeepEqual(got.Bass, own) {
		t.Errorf("a bass of its own reads back as %+v", got.Bass)
	}
}
