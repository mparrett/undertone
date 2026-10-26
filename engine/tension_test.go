package engine

import (
	"reflect"
	"testing"
)

func TestTensionZeroIsInert(t *testing.T) {
	p := DeriveDungeon(1, 5, 0)
	if p.PulseGain != 0 {
		t.Fatalf("tension 0 must silence the pulse track, got gain %v", p.PulseGain)
	}
	if p.FifthOffset != -5 {
		t.Fatalf("tension 0 must keep the perfect fifth, got offset %d", p.FifthOffset)
	}
	pats := buildPatterns(p, 0)
	for row, tr := range pats[5] {
		if tr.note != 0 {
			t.Fatalf("tension 0 placed a pulse note at row %d", row)
		}
	}
}

func TestTensionOneAgitates(t *testing.T) {
	calm := DeriveDungeon(1, 5, 0)
	hot := DeriveDungeon(1, 5, 1)
	if hot.BPM <= calm.BPM {
		t.Fatalf("tension must raise tempo: %v <= %v", hot.BPM, calm.BPM)
	}
	if hot.FifthOffset != -11 {
		t.Fatalf("full tension should reach the minor-ninth offset, got %d", hot.FifthOffset)
	}
	pats := buildPatterns(hot, 0)
	pulses := 0
	for _, tr := range pats[5] {
		if tr.note != 0 {
			pulses++
		}
	}
	if pulses < 2 {
		t.Fatalf("full tension should place at least the 0/8 heartbeat, got %d notes", pulses)
	}
	if hot.BellDensity >= calm.BellDensity || hot.PadDensity >= calm.PadDensity {
		t.Fatalf("tension should thin the calm layers: bells %v>=%v or pads %v>=%v",
			hot.BellDensity, calm.BellDensity, hot.PadDensity, calm.PadDensity)
	}
}

func TestOverworldIsBrighter(t *testing.T) {
	dun := DeriveDungeon(1, 3, 0)
	over := DeriveOverworld(1, 3, 0)
	if over.Root <= dun.Root {
		t.Fatalf("overworld root should sit higher: %d <= %d", over.Root, dun.Root)
	}
	if over.FifthOffset != -8 {
		t.Fatalf("calm overworld wants the major third, got %d", over.FifthOffset)
	}
	if over.BellDensity <= dun.BellDensity || over.BellLift != 12 {
		t.Fatalf("overworld bells should be denser and lifted: %v vs %v, lift %d",
			over.BellDensity, dun.BellDensity, over.BellLift)
	}
	if over.DroneGain >= dun.DroneGain {
		t.Fatalf("overworld drone is a bed, not a weight: %v >= %v", over.DroneGain, dun.DroneGain)
	}
	if DeriveOverworld(1, 3, 1).FifthOffset != -11 {
		t.Fatal("overworld tension should reach the same dread interval")
	}
}

func TestLeadLibrary(t *testing.T) {
	p := DeriveDungeon(1, 3, 0)
	if _, ok := leadLib[p.LeadName]; !ok {
		t.Fatalf("derive picked an unknown lead %q", p.LeadName)
	}
	ApplyLead(&p, "reed")
	if p.LeadName != "reed" || p.LeadGate != 3 {
		t.Fatalf("override failed: %q gate %d", p.LeadName, p.LeadGate)
	}
	seen := map[string]bool{}
	for seed := uint64(1); seed <= 12; seed++ {
		seen[DeriveDungeon(seed, 3, 0).LeadName] = true
	}
	if len(seen) < 3 {
		t.Fatalf("12 seeds should audition at least 3 leads, got %v", seen)
	}
}

func TestSetParametersAppliesAtPatternBoundary(t *testing.T) {
	s := NewSynth(DeriveDungeon(1, 3, 0))
	buf := make([]byte, s.samplesPerRow*4*4) // advance 4 rows into pattern 0
	s.Read(buf)
	before := s.params.BPM
	next := DeriveDungeon(1, 3, 1)
	s.SetParameters(next)
	if s.params.BPM != before {
		t.Fatal("staged parameters must not apply mid-pattern")
	}
	rest := make([]byte, (s.samplesPerRow*(rows-4)+s.samplesPerRow)*4)
	s.Read(rest) // cross the pattern boundary
	if s.params.BPM != next.BPM {
		t.Fatalf("staged parameters should apply at the boundary: got bpm %v, want %v",
			s.params.BPM, next.BPM)
	}
}

func TestEveryLeadHasATrim(t *testing.T) {
	for name := range leadLib {
		if leadTrim[name] <= 0 {
			t.Errorf("lead %q has no leadTrim entry, so the generator would play it silent", name)
		}
	}
}

// Meadow is the bright end: at every level it sits at or above the
// overworld's register, with less drone under it and more bells over it.
func TestMeadowIsBrightest(t *testing.T) {
	for level := 0; level <= 5; level++ {
		over := DeriveOverworld(1, level, 0)
		mead, ok := DeriveMood("meadow", 1, level, 0)
		if !ok {
			t.Fatal("meadow is not in the mood table")
		}
		if mead.Root < over.Root {
			t.Errorf("level %d: meadow root %d under overworld %d", level, mead.Root, over.Root)
		}
		if mead.DroneGain >= over.DroneGain {
			t.Errorf("level %d: meadow drone %v not under overworld %v", level, mead.DroneGain, over.DroneGain)
		}
		if mead.BellDensity <= over.BellDensity {
			t.Errorf("level %d: meadow bells %v not denser than overworld %v", level, mead.BellDensity, over.BellDensity)
		}
	}
	if p, _ := DeriveMood("meadow", 1, 2, 0); p.FifthOffset != -3 {
		t.Fatalf("meadow's calm second voice at %d; want -3, a major sixth over the low drone", p.FifthOffset)
	}
}

// Aurora's second voice floats above the chord, and tension sours it in
// place rather than dropping it more than an octave.
func TestAuroraTensionStaysAbove(t *testing.T) {
	for _, c := range []struct {
		tension float64
		want    int
	}{{0, 7}, {0.5, 6}, {1, 1}} {
		if p, _ := DeriveMood("aurora", 1, 2, c.tension); p.FifthOffset != c.want {
			t.Errorf("tension %.1f: aurora's second voice at %d, want %d", c.tension, p.FifthOffset, c.want)
		}
	}
}

// Aurora is bright by being thin: nothing under it, fewer bells than the
// meadow, slower than any mood, and the veil sending into the delay.
func TestAuroraIsThin(t *testing.T) {
	for level := 0; level <= 5; level++ {
		mead, _ := DeriveMood("meadow", 1, level, 0)
		aur, ok := DeriveMood("aurora", 1, level, 0)
		if !ok {
			t.Fatal("aurora is not in the mood table")
		}
		if aur.DroneGain != 0 {
			t.Errorf("level %d: aurora drone %v, want silent", level, aur.DroneGain)
		}
		if aur.BellDensity >= mead.BellDensity || aur.BPM >= mead.BPM {
			t.Errorf("level %d: aurora bells/bpm %v/%v not under meadow %v/%v",
				level, aur.BellDensity, aur.BPM, mead.BellDensity, mead.BPM)
		}
		if aur.Root < mead.Root {
			t.Errorf("level %d: aurora root %d under meadow %d", level, aur.Root, mead.Root)
		}
	}
	if p, _ := DeriveMood("aurora", 1, 0, 0); p.Palette.Pad[3].op != opSend {
		t.Fatal("aurora's pad should send into the delay")
	}
}

// Stratosphere has no lead: it derives no lead name, and across levels,
// tensions and patterns the lead track strikes nothing, so tension's
// agitation cannot bring one in either.
func TestStratosphereLeadStaysSilent(t *testing.T) {
	for _, seed := range []uint64{1, 7, 42} {
		for _, level := range []int{0, 5, 20} {
			for _, tension := range []float64{0, 0.5, 1} {
				p, ok := DeriveMood("stratosphere", seed, level, tension)
				if !ok {
					t.Fatal("stratosphere is not in the mood table")
				}
				if p.LeadName != "" {
					t.Fatalf("stratosphere picked lead %q", p.LeadName)
				}
				for pat := range 8 {
					for _, r := range buildPatterns(p, pat)[3] {
						if r.patch != nil {
							t.Fatalf("seed %d level %d tension %v pattern %d: the lead track plays %+v", seed, level, tension, pat, r)
						}
					}
				}
			}
		}
	}
	aur, _ := DeriveMood("aurora", 1, 3, 0)
	str, _ := DeriveMood("stratosphere", 1, 3, 0)
	if str.Root <= aur.Root || str.PadDensity >= aur.PadDensity || str.DroneGain != 0 {
		t.Errorf("stratosphere root/pad/drone %d/%v/%v, want above aurora's %d, thinner than %v, and no drone",
			str.Root, str.PadDensity, str.DroneGain, aur.Root, aur.PadDensity)
	}
}

// A mood without leads keeps a bell line's walk but strikes nothing, and the
// walk's draws keep the rest of the pattern as it was with a lead.
func TestLeadlessSpecSilencesOnlyTheLead(t *testing.T) {
	s, _ := MoodByName("dungeon")
	with, _ := s.Derive(7, 2, 1)
	s.Leads = nil
	without, err := s.Derive(7, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if without.BellDensity == 0 {
		t.Fatal("the test needs a bell line that plays")
	}
	// A grid's lead is authored, so it still plays, on the palette's bell.
	syn := NewSynth(without)
	syn.SetGrid(&Grid{Lead: [Steps]int{1}})
	syn.Read(make([]byte, 4*syn.samplesPerRow))
	if !samePatch(syn.pats[trackLead][0].patch, bell) {
		t.Error("a grid's lead does not play on the palette's bell in a mood without a lead")
	}
	for pat := range 8 {
		a, b := buildPatterns(with, pat), buildPatterns(without, pat)
		if !reflect.DeepEqual(b[3], [rows]trackRow{}) {
			t.Fatalf("pattern %d: the lead track plays without a lead", pat)
		}
		a[3] = b[3]
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("pattern %d: dropping the lead changed the other tracks", pat)
		}
	}
}

// Void is past the dungeon: lower and slower at every level and tension.
func TestVoidIsBelowTheDungeon(t *testing.T) {
	for _, seed := range []uint64{1, 7, 42} {
		for level := 0; level <= 20; level++ {
			for _, tension := range []float64{0, 0.5, 1} {
				dun := DeriveDungeon(seed, level, tension)
				void, ok := DeriveMood("void", seed, level, tension)
				if !ok {
					t.Fatal("void is not in the mood table")
				}
				if void.Root >= dun.Root || void.BPM >= dun.BPM {
					t.Errorf("seed %d level %d tension %v: void root/bpm %d/%v, dungeon %d/%v",
						seed, level, tension, void.Root, void.BPM, dun.Root, dun.BPM)
				}
			}
		}
	}
	if p, _ := DeriveMood("void", 1, 0, 0); p.Room == (Room{}) {
		t.Error("void plays in the default room")
	}
}
