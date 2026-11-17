package engine

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/derive.golden from the current derivation")

// goldenLine prints every Params field with floats as exact hex bits, so a
// change in the last place of any gain shows up. Params rather than rendered
// PCM: the synth is a pure function of Params, and PCM differs across
// architectures (Go fuses multiply-adds on arm64) while the derivation, which
// guards against fusion, does not.
func goldenLine(name string, p Params) string {
	h := func(x float64) string { return strconv.FormatFloat(x, 'x', -1, 64) }
	return fmt.Sprintf("%s bpm=%s root=%d scale=%v drone=%s fifth=%s pad=%s lead-density=%s air=%s pulse=%s off=%d lift=%d lead=%s/%d seed=%d palette=%v",
		name, h(p.BPM), p.Root, p.Scale, h(p.DroneGain), h(p.FifthGain), h(p.PadDensity), h(p.LeadDensity),
		h(p.AirGain), h(p.PulseGain), p.FifthOffset, p.LeadLift, p.LeadName, p.LeadGate, p.Seed, p.Palette)
}

// The mood table must reproduce the moods that existed before it byte for
// byte: a saved game's seed and depth keep sounding the same.
func TestDerivationGolden(t *testing.T) {
	var b strings.Builder
	for _, mood := range []string{"dungeon", "overworld"} {
		derive := DeriveDungeon
		if mood == "overworld" {
			derive = DeriveOverworld
		}
		for _, seed := range []uint64{0, 1, 7, 42, 0x5eed1234, 1<<63 + 5} {
			for level := 0; level <= 9; level++ {
				for _, tension := range []float64{0, 0.3, 0.4, 0.6, 0.75, 1, 1.5} {
					fmt.Fprintln(&b, goldenLine(fmt.Sprintf("%s/%d/%d/%v", mood, seed, level, tension), derive(seed, level, tension)))
				}
			}
		}
	}
	const path = "testdata/derive.golden"
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(b.String(), "\n")
	wantLines := strings.Split(string(want), "\n")
	if len(got) != len(wantLines) {
		t.Fatalf("derived %d lines, golden has %d; run with -update if the change is intended", len(got), len(wantLines))
	}
	for i, w := range wantLines {
		if i >= len(got) || got[i] != w {
			t.Fatalf("line %d differs:\n got %s\nwant %s", i+1, got[min(i, len(got)-1)], w)
		}
	}
}
