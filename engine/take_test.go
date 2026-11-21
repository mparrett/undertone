package engine

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
)

// A take renders as a fresh synth given its params, gain and shots renders.
func TestTakeRendersAsAFreshSynth(t *testing.T) {
	s := NewSynth(boardTake.Params)
	s.SetMusicGain(boardTake.MusicGain)
	for _, sh := range boardTake.Shots {
		s.TriggerShot(sh)
	}
	want := render(s, 2*SampleRate)
	got := boardTake.Render()
	if len(got) != len(want)/2 {
		t.Fatalf("%d samples, want %d", len(got), len(want)/2)
	}
	for i, v := range got {
		if v != int16(binary.LittleEndian.Uint16(want[2*i:])) {
			t.Fatalf("sample %d differs", i)
		}
	}
	if n := len((Take{Seconds: 1e9}).Render()); n != 2*MaxTakeSeconds*SampleRate {
		t.Errorf("an hour's take rendered %d samples, want a minute's", n)
	}
	if n := len((Take{Seconds: -1}).Render()); n != 0 {
		t.Errorf("a negative take rendered %d samples", n)
	}
}

func TestTakeHash(t *testing.T) {
	// Built apart, equal takes hash equal: nothing in the digest is an
	// address or a map order.
	twin := Take{
		Params:  DeriveDungeon(7, 3, 0.5),
		Shots:   slices.Clone(boardTake.Shots),
		Seconds: 2,
	}
	h := boardTake.Hash()
	if twin.Hash() != h {
		t.Fatal("equal takes hash differently")
	}

	t.Cleanup(func() {
		shotsMu.Lock()
		delete(shotReg, "take-beep")
		shotsMu.Unlock()
	})
	beep := func(hz float64) ShotSpec {
		return ShotSpec{Name: "take-beep", GateMS: 10, Units: []UnitSpec{
			{Op: "env", Args: []float64{0.001, 0.1, 0, 0.05}},
			{Op: "osc", Shape: "sine", Args: []float64{hz, 0, 1}},
			{Op: "mul"},
			{Op: "out", Args: []float64{0.5, 0.5}},
		}}
	}
	if err := RegisterShot(beep(0)); err != nil {
		t.Fatal(err)
	}
	withBeep := twin
	withBeep.Shots = []Shot{{Name: "take-beep", Gain: 1, Pan: 0.5, Note: 60, HasNote: true}}
	before := withBeep.Hash()

	seen := map[string]string{h: "the board take"}
	differs := func(what string, k Take) {
		t.Helper()
		got := k.Hash()
		if prev, ok := seen[got]; ok {
			t.Errorf("%s hashes as %s", what, prev)
		}
		seen[got] = what
	}
	vary := func(what string, f func(*Take)) {
		t.Helper()
		k := twin
		k.Shots = slices.Clone(twin.Shots)
		f(&k)
		differs(what, k)
	}
	vary("a longer take", func(k *Take) { k.Seconds = 3 })
	vary("the bed heard", func(k *Take) { k.MusicGain = 1 })
	vary("another seed for the bed", func(k *Take) { k.Params.Seed++ })
	vary("another room", func(k *Take) { k.Params.Room.Size = 0.5 })
	vary("a shot's seed", func(k *Take) { k.Shots[0].Seed = 9 })
	vary("a shot's delay", func(k *Take) { k.Shots[1].DelayMS = 300 })
	vary("a shot more", func(k *Take) { k.Shots = append(k.Shots, k.Shots[0]) })
	vary("an unknown shot", func(k *Take) { k.Shots[0].Name = "no-such-shot" })
	differs("a registered shot", withBeep)

	// Re-registered under the same name with new units, the shot sounds
	// different, so the take must hash differently.
	if err := RegisterShot(beep(7)); err != nil {
		t.Fatal(err)
	}
	if withBeep.Hash() == before {
		t.Error("re-registering a shot left its take's hash alone")
	}
}

// renderGolden holds measurements of reference takes, stamped with the
// RenderVersion they were made under. A change to what the engine renders
// moves them, and this test then asks for RenderVersion to be bumped, so a
// page caching measurements by a take's Hash re-measures. The tolerances sit
// above what fused multiply-adds move across architectures and below what a
// listener or the board would notice.
const renderGolden = "testdata/render.golden"

func goldenTakes() []struct {
	name string
	take Take
} {
	var out []struct {
		name string
		take Take
	}
	quiet := DeriveDungeon(7, 3, 0.5)
	names := make([]string, 0, len(shotLib))
	for n := range shotLib {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		out = append(out, struct {
			name string
			take Take
		}{n, Take{Params: quiet, Shots: []Shot{{Name: n, Gain: 1, Pan: 0.4, Seed: 3, Note: 57, HasNote: true}}, Seconds: 3}})
	}
	for _, mood := range []string{"dungeon", "forge", "village", "rust"} {
		m, ok := MoodByName(mood)
		if !ok {
			continue
		}
		p, err := m.Derive(5, 2, 0.4)
		if err != nil {
			panic(err)
		}
		out = append(out, struct {
			name string
			take Take
		}{"bed/" + mood, Take{Params: bare(p), MusicGain: 1, Seconds: 6}})
	}
	// the beds above are each mood's full mix, as Studio measures a shot
	// against; this one is what a game hears first, the opening build
	p, _ := DeriveMood("rust", 5, 2, 0.4)
	out = append(out, struct {
		name string
		take Take
	}{"bed-arc/rust", Take{Params: p, MusicGain: 1, Seconds: 6}})
	return out
}

func goldenRow(name string, m Measurement) string {
	var rms float64
	for _, v := range m.EnvRMS {
		rms += v
	}
	return fmt.Sprintf("%s peak=%.5f lufs=%.4f rings=%.2f rms-sum=%.4f", name, m.Peak, m.MaxLUFS, m.Length, rms)
}

func parseGoldenRow(s string) (name string, peak, lufs, rings, rmsSum float64, err error) {
	_, err = fmt.Sscanf(s, "%s peak=%f lufs=%f rings=%f rms-sum=%f", &name, &peak, &lufs, &rings, &rmsSum)
	return
}

func TestRenderVersionGolden(t *testing.T) {
	var rows []string
	for _, g := range goldenTakes() {
		rows = append(rows, goldenRow(g.name, MeasurePCM(g.take.Render())))
	}
	head := fmt.Sprintf("render-version %d", RenderVersion)

	f, err := os.ReadFile(renderGolden)
	if err != nil && !*update {
		t.Fatal(err)
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(f))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	sameVersion := len(lines) > 0 && lines[0] == head
	// Rows are matched by name: a shot or mood added or dropped changes no
	// other take's sound, so it asks for -update but not for a new version.
	golden := map[string]string{}
	for _, l := range lines[min(1, len(lines)):] {
		n, _, _, _, _, err := parseGoldenRow(l)
		if err != nil {
			t.Fatalf("%s: %q: %v", renderGolden, l, err)
		}
		golden[n] = l
	}
	moved, reshaped := false, len(golden) != len(rows)
	for _, row := range rows {
		n, p, l, r, s, _ := parseGoldenRow(row)
		g, ok := golden[n]
		if !ok {
			reshaped = true
			continue
		}
		_, gp, gl, gr, gs, _ := parseGoldenRow(g)
		if math.Abs(gp-p) > 1e-4 || math.Abs(gl-l) > 0.01 || math.Abs(gr-r) > 0.011 ||
			math.Abs(gs-s) > 1e-3*max(1, gs) {
			moved = true
			t.Logf("%s moved:\n  golden %s\n  now    %s", n, g, row)
		}
	}

	if *update {
		if moved && sameVersion {
			t.Fatalf("what takes render moved, but RenderVersion is still %d: bump it, then -update", RenderVersion)
		}
		out := head + "\n" + strings.Join(rows, "\n") + "\n"
		if err := os.WriteFile(renderGolden, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !sameVersion {
		t.Fatalf("%s is for another RenderVersion than %d: run go test ./engine -run TestRenderVersionGolden -update", renderGolden, RenderVersion)
	}
	if moved {
		t.Errorf("what takes render moved: if that is intended, bump RenderVersion and run go test ./engine -run TestRenderVersionGolden -update")
	}
	if reshaped {
		t.Errorf("the reference takes changed: run go test ./engine -run TestRenderVersionGolden -update")
	}
}

// The hash reads a temperament's values, not its address: copies hash
// alike, and a changed knob hashes differently.
func TestTakeHashTemperament(t *testing.T) {
	mk := func(leap float64) Take {
		k := Take{Params: DeriveDungeon(7, 3, 0.5), Seconds: 2}
		m := *k.Params.MoodMemory
		m.Leap = leap
		k.Params.MoodMemory = &m
		return k
	}
	a, b := mk(0.25), mk(0.25) // two allocations of one value
	if a.Hash() != b.Hash() {
		t.Fatal("equal temperaments at different addresses hash differently")
	}
	if mk(0.25).Hash() == mk(0.5).Hash() {
		t.Fatal("a changed Leap hashes the same")
	}
}
