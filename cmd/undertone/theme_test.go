package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/mparrett/undertone/engine"
)

const testTheme = `{"grids": [
  {"lead": "1030500080000000", "bass": "1000500010005000", "pulse": "1000100010001000", "drums": ["x...x...", "", ""]},
  {"lead": "5040302010000000", "bass": "4000400010001000"}
 ], "then": "silence"}`

func writeTheme(t *testing.T, json string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "theme.json")
	if err := os.WriteFile(path, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A theme file plays from the file's first tick, its bass lane goes out on
// channel 8, and with no bed nothing sounds once it and its tail are over.
func TestThemeFileRendersToMIDI(t *testing.T) {
	th, err := loadTheme(writeTheme(t, testTheme))
	if err != nil {
		t.Fatal(err)
	}
	p := deriveParams("dungeon", 1, 1, 0, "chime")
	s := engine.NewSynth(p)
	s.PlayTheme(th)
	secs := themeSeconds(th, p, 48)
	if want := 2*float64(engine.Steps*rowFrames(p))/engine.SampleRate + themeTail; secs != want {
		t.Fatalf("a two-grid theme renders %v s, want %v", secs, want)
	}
	msgs, err := renderMIDI(s, p, discard{}, secs)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := writeSMF(&b, p, msgs); err != nil {
		t.Fatal(err)
	}
	f := parseSMF(t, b.Bytes())

	const lead, bass = 3, 7 // LayerNames indexes, which are the channels
	row := int64(ppq / 2)
	var leadOn, bassOn []int64
	last := int64(0)
	for _, n := range f.notes {
		switch n.ch {
		case lead:
			leadOn = append(leadOn, n.on)
		case bass:
			bassOn = append(bassOn, n.on)
		}
		last = max(last, n.on)
	}
	// grid 1's lead is steps 0, 2, 4, 8 and grid 2's 0, 2, 4, 6, 8, a grid being Steps rows
	wantLead := []int64{0, 2, 4, 8, 16, 18, 20, 22, 24}
	if len(leadOn) != len(wantLead) {
		t.Fatalf("lead notes start at ticks %v, want rows %v", leadOn, wantLead)
	}
	for i, r := range wantLead {
		if leadOn[i] != r*row {
			t.Errorf("lead note %d starts at tick %d, want row %d (tick %d)", i, leadOn[i], r, r*row)
		}
	}
	if len(bassOn) != 8 || bassOn[0] != 0 {
		t.Errorf("bass notes on channel 8 start at ticks %v, want 8 from tick 0", bassOn)
	}
	if end := 2 * engine.Steps * row; last >= end {
		t.Errorf("a note starts at tick %d, after the theme's end at %d: then is silence", last, end)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestLoadThemeErrors(t *testing.T) {
	if _, err := loadTheme(filepath.Join(t.TempDir(), "none.json")); err == nil {
		t.Error("a missing file loaded")
	}
	for _, bad := range []string{`[]`, `{"grids": []}`, `{"grids": [{"lead": "9"}]}`, `not json`} {
		if _, err := loadTheme(writeTheme(t, bad)); err == nil {
			t.Errorf("%s loaded", bad)
		}
	}
}

// A looping theme never ends, so a render of one keeps the default length.
func TestThemeSecondsLoop(t *testing.T) {
	th := &engine.Theme{Grids: make([]engine.Grid, 3), Then: engine.ThenLoop}
	if got := themeSeconds(th, deriveParams("dungeon", 1, 0, 0, ""), 48); got != 48 {
		t.Errorf("a looping theme renders %v s, want the default 48", got)
	}
}

// -seed takes a game's signed seed and keeps its bits.
func TestSeedFlag(t *testing.T) {
	for in, want := range map[string]uint64{
		"1":                    1,
		"18446744073709551615": 1<<64 - 1,
		"-1":                   1<<64 - 1,
		"-2288356883509901239": 16158387190199650377,
	} {
		s := seedFlag(7)
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.Var(&s, "seed", "")
		if err := fs.Parse([]string{"-seed", in}); err != nil || uint64(s) != want {
			t.Errorf("-seed %s: %d, %v; want %d", in, uint64(s), err, want)
		}
	}
	for _, bad := range []string{"abc", "1.5", "18446744073709551616", "-9223372036854775809", ""} {
		s := seedFlag(7)
		if err := s.Set(bad); err == nil {
			t.Errorf("-seed %q was taken as %d", bad, uint64(s))
		}
	}
}
