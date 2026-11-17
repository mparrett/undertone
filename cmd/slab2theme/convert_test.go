package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/letgo"
)

var dorian = scales["dorian"]

func TestDegreeOf(t *testing.T) {
	for _, c := range []struct {
		scale       []int
		root, pitch int
		deg         int
		in          bool
	}{
		{dorian, 62, 62, 1, true},                     // D in D dorian
		{dorian, 62, 65, 3, true},                     // F, the minor third
		{dorian, 62, 71, 6, true},                     // B, dorian's major sixth
		{dorian, 62, 74, 8, true},                     // the octave
		{dorian, 62, 76, 9, true},                     // past the top: the caller lints it
		{dorian, 62, 60, 0, true},                     // C below the root
		{dorian, 62, 50, -6, true},                    // an octave down: 1 - 7
		{dorian, 62, 66, 0, false},                    // F#
		{dorian, 62, 70, 0, false},                    // Bb
		{scales["minor-pentatonic"], 57, 69, 6, true}, // a five-note octave is degree 6
	} {
		d, in := degreeOf(c.scale, c.root, c.pitch)
		if d != c.deg || in != c.in {
			t.Errorf("degreeOf(%v, %d, %d) = %d, %v; want %d, %v", c.scale, c.root, c.pitch, d, in, c.deg, c.in)
		}
	}
}

// Each degree degreeOf gives plays the same pitch back through the engine.
func TestDegreeRoundTrip(t *testing.T) {
	p := engine.Params{Scale: dorian}
	for pitch := 62; pitch <= 74; pitch++ {
		d, in := degreeOf(dorian, 62, pitch)
		if !in {
			continue
		}
		if got := engine.DegreeNote(p, 62, d); got != pitch {
			t.Errorf("degree %d of D dorian plays %d, want %d", d, got, pitch)
		}
	}
}

func TestParseNote(t *testing.T) {
	for _, c := range []struct {
		in     string
		pitch  int
		octave bool
	}{
		{"D", 2, false}, {"F#", 6, false}, {"Bb", 10, false}, {"c", 0, false},
		{"C4", 60, true}, {"D5", 74, true}, {"Eb3", 51, true}, {"62", 62, true},
	} {
		p, o, err := parseNote(c.in)
		if err != nil || p != c.pitch || o != c.octave {
			t.Errorf("parseNote(%q) = %d, %v, %v; want %d, %v", c.in, p, o, err, c.pitch, c.octave)
		}
	}
	for _, bad := range []string{"", "H", "C#x", "200"} {
		if _, _, err := parseNote(bad); err == nil {
			t.Errorf("parseNote(%q) took a bad note", bad)
		}
	}
}

func TestPickRootFitsTheMelody(t *testing.T) {
	evs := []event{{pitch: 65}, {pitch: 69}, {pitch: 72}, {pitch: 74}} // F4 A4 C5 D5
	if r := pickRoot(evs, dorian, 2); r != 62 {
		t.Errorf("pickRoot = %s, want D4: the only D that puts all of F4..D5 within 8 degrees", noteName(r))
	}
	// A4..A5 cannot fit: D5 takes three of the four, D4 two.
	evs = []event{{pitch: 69}, {pitch: 74}, {pitch: 77}, {pitch: 81}}
	if r := pickRoot(evs, dorian, 2); r != 74 {
		t.Errorf("pickRoot = %s, want D5", noteName(r))
	}
}

func ev(beat, l float64, pitch int) event { return event{beat, l, pitch, 100, "T"} }

func kinds(r result) []string {
	var k []string
	for _, l := range r.lints {
		k = append(k, l.kind)
	}
	return k
}

func TestQuantizeAndLint(t *testing.T) {
	o := options{scale: dorian, scaleName: "dorian", root: 2, leadRoot: 62, bassRoot: 38}
	r := convert(lanes{lead: []event{
		ev(0, 0.5, 62),    // row 0, degree 1
		ev(1, 0.5, 65),    // row 2, degree 3
		ev(2.25, 0.5, 69), // off the row grid: rounds to row 5 (2.5 beats), linted
		ev(3, 0.5, 66),    // F#: off the scale, dropped
		ev(4, 0.5, 76),    // E5 above the octave, dropped
		ev(5, 0.5, 64),    // row 10
		ev(5, 0.5, 67),    // the same row: a chord, the higher kept
		ev(6, 2, 74),      // held...
		ev(7, 0.5, 72),    // ...when this starts: overlap
		ev(9, 0.5, 62),    // grid 2, row 2
	}}, o)
	if len(r.grids) != 2 {
		t.Fatalf("got %d grids, want 2 to hold beat 9", len(r.grids))
	}
	want := [engine.Steps]int{1, 0, 3, 0, 0, 5, 0, 0, 0, 0, 4, 0, 8, 0, 7, 0}
	if r.grids[0].Lead != want {
		t.Errorf("grid 1 lead = %v, want %v", r.grids[0].Lead, want)
	}
	if r.grids[1].Lead[2] != 1 {
		t.Errorf("grid 2 lead = %v, want degree 1 on row 2", r.grids[1].Lead)
	}
	if got, want := kinds(r), []string{"off-grid", "off-scale", "range", "chord", "overlap"}; !slices.Equal(got, want) {
		t.Errorf("lints = %v, want %v", got, want)
	}
}

// A bass lane keeps the lower note of a chord, and notes outside the window
// are counted, not linted.
func TestBassChordAndWindow(t *testing.T) {
	o := options{scale: dorian, scaleName: "dorian", root: 2, leadRoot: 62, bassRoot: 38, from: 8, grids: 1}
	r := convert(lanes{bass: []event{ev(0, 1, 38), ev(8, 1, 45), ev(8, 1, 50), ev(16, 1, 38)}}, o)
	if r.grids[0].Bass[0] != 5 {
		t.Errorf("bass row 0 = %d, want 5 (A, the lower of A and D)", r.grids[0].Bass[0])
	}
	if r.outside != 2 || !slices.Equal(kinds(r), []string{"chord"}) {
		t.Errorf("outside = %d, lints = %v; want 2 and [chord]", r.outside, kinds(r))
	}
}

func TestDrums(t *testing.T) {
	o := options{scale: dorian, root: 2, leadRoot: -1, bassRoot: -1}
	r := convert(lanes{drumMap: drum2Lane, drums: []event{
		{0, 0.1, 36, 122, "K"},    // kick accent
		{1, 0.1, 38, 100, "K"},    // snare -> clap lane
		{0.5, 0.1, 42, 58, "K"},   // closed hat ghost -> shaker
		{0.5, 0.1, 46, 100, "K"},  // open hat on the same 16th: merged, louder kept
		{2, 0.1, 45, 100, "K"},    // tom: no lane
		{2.1, 0.1, 36, 100, "K"},  // off the 16th grid
		{7.75, 0.1, 48, 100, "K"}, // C an octave up is still the kick on drum2
	}}, o)
	g := r.grids[0]
	if want := "X.......x......................x"; g.Drums[0] != want {
		t.Errorf("kick = %q, want %q", g.Drums[0], want)
	}
	if want := "....x..........................."; g.Drums[1] != want {
		t.Errorf("clap = %q, want %q", g.Drums[1], want)
	}
	if want := "..x............................."; g.Drums[2] != want {
		t.Errorf("shaker = %q, want %q", g.Drums[2], want)
	}
	if got, want := kinds(r), []string{"merged", "unmapped", "off-grid"}; !slices.Equal(got, want) {
		t.Errorf("lints = %v, want %v", got, want)
	}
}

func TestBarBeat(t *testing.T) {
	p := &project{}
	if got := p.barBeat(9.5); got != "3:2.5" {
		t.Errorf("4/4 beat 9.5 = %s, want 3:2.5", got)
	}
	p.Meter = []meterPoint{{0, 3, 4}, {2, 4, 4}} // two bars of 3/4, then 4/4
	for beat, want := range map[float64]string{0: "1:1", 3: "2:1", 5.5: "2:3.5", 6: "3:1", 10: "4:1"} {
		if got := p.barBeat(beat); got != want {
			t.Errorf("beat %v = %s, want %s", beat, got, want)
		}
	}
}

// A hand-written project per Slab's format doc: a lead clip that starts at
// beat 4, a note past the clip's end (silent in Slab), a muted clip, and a
// bus that shares the lead's name.
const fixture = `{
 "slab": "project", "schema": 1,
 "transport": {"bpm": 90},
 "tracks": [
  {"name": "LEAD", "kind": "bus"},
  {"name": "Lead", "instrument": {"machine": "cream"}, "clips": [
   {"type": "note", "start": 4, "len": 4, "notes": [
    {"pitch": 74, "start": 0, "len": 1, "vel": 100},
    {"pitch": 77, "start": 1, "len": 1, "vel": 100},
    {"pitch": 81, "start": 2, "len": 2, "vel": 100},
    {"pitch": 84, "start": 4, "len": 1, "vel": 100}]},
   {"type": "note", "start": 8, "len": 4, "muted": true, "notes": [
    {"pitch": 66, "start": 0, "len": 1, "vel": 100}]}]},
  {"name": "BASS", "instrument": {"machine": "cream"}, "clips": [
   {"type": "note", "start": 0, "len": 8, "notes": [
    {"pitch": 38, "start": 0, "len": 4, "vel": 100},
    {"pitch": 45, "start": 4, "len": 4, "vel": 100}]}]},
  {"name": "KIT", "instrument": {"machine": "drum2"}, "clips": [
   {"type": "note", "start": 0, "len": 8, "notes": [
    {"pitch": 36, "start": 0, "len": 0.1, "vel": 122},
    {"pitch": 39, "start": 1, "len": 0.1, "vel": 100}]}]}
 ]}`

func exportFixture(t *testing.T, extra ...string) (edn string, code int, stderr string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "song.slab")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	args := append([]string{"-lead", "lead", "-bass", "BASS", "-drums", "KIT", "-root", "D", "-mood", "dungeon"}, extra...)
	code = run(append(args, path), &out, &errb)
	return out.String(), code, errb.String()
}

func TestExportFixture(t *testing.T) {
	edn, code, stderr := exportFixture(t)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		":lead  [0 0 0 0 0 0 0 0 1 0 3 0 5 0 0 0]", // D5 F5 A5 from beat 4; the C6 past the clip is silent
		":bass  [1 0 0 0 0 0 0 0 5 0 0 0 0 0 0 0]",
		`"X..............................."`,
		`"....x..........................."`,
		":then :generate",
	} {
		if !strings.Contains(edn, want) {
			t.Errorf("EDN lacks %q:\n%s", want, edn)
		}
	}
	if !strings.Contains(stderr, "0 lint(s)") {
		t.Errorf("a clean fixture linted:\n%s", stderr)
	}
}

func TestStrictFailsOnLints(t *testing.T) {
	// In D major the lead's F5 is off the scale.
	edn, code, stderr := exportFixture(t, "-strict", "-mood", "", "-scale", "major")
	if code != 1 || edn != "" {
		t.Errorf("-strict with lints: exit %d, wrote %d bytes; want exit 1 and nothing", code, len(edn))
	}
	if !strings.Contains(stderr, "Lead 2:2 off-scale: F5 is not in D major") {
		t.Errorf("no off-scale lint with its bar:beat:\n%s", stderr)
	}
}

// The EDN is what undertone/play-theme takes: rendering it from lg is
// byte-identical to the engine playing the same grids from Go.
func TestEDNPlaysFromLg(t *testing.T) {
	edn, code, stderr := exportFixture(t)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	p := engine.DeriveDungeon(1, 0, 0)
	letgo.SetMusic(letgo.NewMusic(p))
	c, err := api.NewLetGo("slab2themetest")
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Run(fmt.Sprintf(`(undertone/render-wav nil 6.0 [[0.0 :play-theme (read-string %q)]])`, edn))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := v.(vm.String)

	th := fixtureTheme(t)
	s := engine.NewSynth(p)
	s.PlayTheme(&th)
	sum := sha256.New()
	if _, err := io.CopyN(sum, s, 6*engine.SampleRate*4); err != nil {
		t.Fatal(err)
	}
	if want := hex.EncodeToString(sum.Sum(nil)); string(got) != want {
		t.Errorf("lg render of the EDN = %s, Go render of the grids = %s", got, want)
	}
}

func fixtureTheme(t *testing.T) engine.Theme {
	t.Helper()
	path := filepath.Join(t.TempDir(), "song.slab")
	os.WriteFile(path, []byte(fixture), 0o600)
	pr, err := loadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	var l lanes
	for _, x := range []struct {
		name string
		dst  *[]event
	}{{"lead", &l.lead}, {"bass", &l.bass}, {"kit", &l.drums}} {
		tr, err := pr.findTrack(x.name)
		if err != nil {
			t.Fatal(err)
		}
		*x.dst, _ = tr.events()
	}
	l.drumMap = drum2Lane
	r := convert(l, options{scale: engine.DeriveDungeon(1, 0, 0).Scale, scaleName: "dorian", root: 2, leadRoot: -1, bassRoot: -1, bed: true})
	return engine.Theme{Grids: r.grids}
}

// The same theme plays in two moods: in each, the lead sounds the theme's
// degrees in that mood's key and scale, at its tempo.
func TestThemePlaysInTwoMoods(t *testing.T) {
	th := fixtureTheme(t)
	var degs []int
	for _, g := range th.Grids {
		for _, d := range g.Lead {
			if d > 0 {
				degs = append(degs, d)
			}
		}
	}
	names := engine.LayerNames()
	lead := slices.Index(names[:], "lead")
	var played [][]int
	for _, mood := range []string{"dungeon", "village"} {
		p, _ := engine.DeriveMood(mood, 1, 0, 0)
		s := engine.NewSynth(p)
		s.PlayTheme(&th)
		var notes []int
		var cur uint64
		var buf []engine.NoteEvent
		for i := 0; i < 20 && len(notes) < len(degs); i++ {
			s.Read(make([]byte, 4*engine.SampleRate))
			buf, cur, _ = s.NoteEvents(cur, buf[:0])
			for _, e := range buf {
				if e.Layer == lead && (e.Kind == engine.NoteOn || e.Kind == engine.NoteRetrigger) {
					notes = append(notes, e.Note)
				}
			}
		}
		base, _ := engine.GridBases(p)
		var want []int
		for _, d := range degs {
			want = append(want, engine.DegreeNote(p, base, d))
		}
		if len(notes) < len(want) || !slices.Equal(notes[:len(want)], want) {
			t.Errorf("%s (root %d, %.0f bpm): lead played %v, want %v", mood, p.Root, p.BPM, notes, want)
		}
		played = append(played, want)
	}
	if slices.Equal(played[0], played[1]) {
		t.Error("the theme played the same notes in both moods; they should differ in key")
	}
}
