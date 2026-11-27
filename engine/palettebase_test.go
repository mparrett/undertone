package engine

import (
	"reflect"
	"slices"
	"testing"
)

// unregisterPalette undoes a test's RegisterPalette: the registry is the
// package's, so a palette left registered under a built-in's name would
// change every later test that derives a mood built on it.
func unregisterPalette(t *testing.T, name string) {
	t.Cleanup(func() {
		regMu.Lock()
		defer regMu.Unlock()
		delete(paletteReg, name)
	})
}

// A spec with a Base takes every track it leaves empty, and the kit, from
// the base; a Base the engine lacks is refused.
func TestPaletteBase(t *testing.T) {
	unregisterPalette(t, "base-soft")
	soft := PaletteSpec{Name: "base-soft", Base: "aurora", Bass: PatchSpec{Name: "reed"}}
	if err := RegisterPalette(soft); err != nil {
		t.Fatal(err)
	}
	want, _ := PaletteSpecByName("aurora")
	want.Name, want.Bass = "base-soft", patchSpec(reed)
	if got, _ := PaletteSpecByName("base-soft"); !reflect.DeepEqual(got, want) {
		t.Errorf("based on aurora, read back\n%+v\nwant\n%+v", got, want)
	}
	if err := RegisterPalette(PaletteSpec{Name: "base-bad", Base: "nope"}); err == nil {
		t.Error("an unknown Base registered")
	}
	// EDN writes only what the spec changes, and lg reads it back the same
	if edn := soft.EDN(); edn != "{:name :base-soft :base :aurora\n :bass :reed}" {
		t.Errorf("EDN of a based spec:\n%s", edn)
	}
}

// A palette registered under a built-in's name stands in for it in the moods
// built on it and at PaletteByName, is listed once, and a Base of its own
// name is still the built-in.
func TestRegisteredPaletteShadowsBuiltin(t *testing.T) {
	before, _ := DeriveMood("meadow", 1, 0, 0) // meadow plays overworld's palette
	unregisterPalette(t, "overworld")
	if err := RegisterPalette(PaletteSpec{Name: "overworld", Base: "overworld", Bass: PatchSpec{Name: "reed"}}); err != nil {
		t.Fatal(err)
	}
	after, _ := DeriveMood("meadow", 1, 0, 0)
	if !samePatch(after.Palette.Bass, reed) {
		t.Error("meadow does not play the registered overworld's bass")
	}
	after.Palette.Bass = before.Palette.Bass
	if !reflect.DeepEqual(after, before) {
		t.Error("shadowing overworld's bass changed more of meadow than its bass")
	}
	if pl, _ := PaletteByName("overworld"); !samePatch(pl.Bass, reed) {
		t.Error("PaletteByName gives the built-in, not the registered overworld")
	}
	if n := PaletteNames(); len(n) != len(slices.Compact(slices.Sorted(slices.Values(n)))) {
		t.Errorf("PaletteNames lists overworld twice: %v", n)
	}
	// re-registered on its own name, the base is the built-in, not the
	// registration it replaces: this pad change keeps the built-in's bass
	if err := RegisterPalette(PaletteSpec{Name: "overworld", Base: "overworld", Pad: PatchSpec{Name: "veil"}}); err != nil {
		t.Fatal(err)
	}
	if pl, _ := PaletteByName("overworld"); pl.Bass != nil {
		t.Error("a Base of the palette's own name read the earlier registration")
	}
}
