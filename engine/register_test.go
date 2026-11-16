package engine

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// forgetRegistered takes a test's palettes and leads back out of the
// registries when it ends. The registries are the process's and have no way
// to unregister, and a name left in would reach every later test that ranges
// over PaletteNames or LeadNames.
func forgetRegistered(t *testing.T, prefix string) {
	t.Cleanup(func() {
		regMu.Lock()
		defer regMu.Unlock()
		for n := range paletteReg {
			if strings.HasPrefix(n, prefix) {
				delete(paletteReg, n)
			}
		}
		for n := range leadReg {
			if strings.HasPrefix(n, prefix) {
				delete(leadReg, n)
			}
		}
	})
}

// renderMood derives a mood spec and renders two seconds of it.
func renderMood(t *testing.T, m MoodSpec) []byte {
	t.Helper()
	p, err := m.Derive(3, 4, 0.4)
	if err != nil {
		t.Fatal(err)
	}
	return render(NewSynth(p), 2*SampleRate)
}

// Every built-in mood, its palette registered under another name, plays the
// same samples: as the spec reads (built-in patches by name), and with every
// name cleared, so each patch is built afresh from its units.
func TestRegisteredPalettePlaysAsBuiltIn(t *testing.T) {
	forgetRegistered(t, "reg-")
	for _, mood := range MoodNames() {
		m, _ := MoodByName(mood)
		want := renderMood(t, m)
		s, ok := PaletteSpecByName(m.Palette)
		if !ok {
			t.Fatalf("%s: palette %q has no spec", mood, m.Palette)
		}
		s.Name = "reg-" + m.Palette
		if err := RegisterPalette(s); err != nil {
			t.Fatalf("%s: %v", mood, err)
		}
		if got, _ := PaletteSpecByName(s.Name); !reflect.DeepEqual(got, s) {
			t.Errorf("%s: reads back\n%+v\nwant\n%+v", s.Name, got, s)
		}
		m.Palette = s.Name
		if !bytes.Equal(renderMood(t, m), want) {
			t.Errorf("%s: plays differently on %s", mood, s.Name)
		}

		s.Name = "reg-fresh-" + s.Name
		for _, ps := range s.tracks() {
			ps.Name = ""
		}
		for i := range s.Kit {
			if !s.Kit[i].Default {
				s.Kit[i].Name = ""
			}
		}
		if err := RegisterPalette(s); err != nil {
			t.Fatalf("%s: %v", mood, err)
		}
		m.Palette = s.Name
		if !bytes.Equal(renderMood(t, m), want) {
			t.Errorf("%s: plays differently on %s", mood, s.Name)
		}
	}
	if names := PaletteNames(); !slices.Contains(names, "reg-fresh-reg-rust") {
		t.Errorf("PaletteNames %v: no registered palette", names)
	}
}

// A lead registered under another name plays as the built-in it copies. The
// pluck is left out: its measured trim is the built-in's alone, and a
// registered lead plays untrimmed.
func TestRegisteredLeadPlaysAsBuiltIn(t *testing.T) {
	forgetRegistered(t, "reg-")
	m, _ := MoodByName("dungeon")
	for _, lead := range []string{"bell", "chime", "clang", "kalimba", "reed"} {
		m.Leads = []string{lead}
		want := renderMood(t, m)
		s, _ := LeadSpecByName(lead)
		s.Name = "reg-" + lead
		if err := RegisterLead(s); err != nil {
			t.Fatal(err)
		}
		if got, _ := LeadSpecByName(s.Name); !reflect.DeepEqual(got, s) {
			t.Errorf("%s: reads back %+v, want %+v", s.Name, got, s)
		}
		m.Leads = []string{s.Name}
		if !bytes.Equal(renderMood(t, m), want) {
			t.Errorf("%s: plays differently from %s", s.Name, lead)
		}
	}
	if names := LeadNames(); !slices.Contains(names, "reg-reed") {
		t.Errorf("LeadNames %v: no registered lead", names)
	}
}

// A spec read back from the registry is a copy: editing it leaves the
// registered palette alone.
func TestRegisteredPaletteSpecIsACopy(t *testing.T) {
	forgetRegistered(t, "reg-")
	s, _ := PaletteSpecByName("dungeon")
	s.Name = "reg-copy"
	if err := RegisterPalette(s); err != nil {
		t.Fatal(err)
	}
	a, _ := PaletteSpecByName(s.Name)
	a.Drone.Units[0].Args[3] = 9
	if b, _ := PaletteSpecByName(s.Name); !reflect.DeepEqual(b, s) {
		t.Error("editing a read-back spec changed the registered one")
	}
}

// Left zero, the bell is the built-in bell and a kit lane the default kit's.
func TestRegisterPaletteDefaults(t *testing.T) {
	forgetRegistered(t, "reg-")
	full, _ := PaletteSpecByName("dungeon")
	s := full
	s.Name, s.Bell, s.Kit = "reg-defaults", PatchSpec{}, [nDrums]DrumSpec{}
	if err := RegisterPalette(s); err != nil {
		t.Fatal(err)
	}
	got, _ := PaletteSpecByName(s.Name)
	full.Name = s.Name
	if !reflect.DeepEqual(got, full) {
		t.Errorf("reads back\n%+v\nwant\n%+v", got, full)
	}
}

// A palette or lead the synth could not run safely is refused with a
// reason, and nothing is registered; a bed's long release is not refused.
func TestRegisterRefusesUnsafe(t *testing.T) {
	forgetRegistered(t, "reg-")
	base, _ := PaletteSpecByName("dungeon")
	base.Name = "reg-bad"
	drone := func(release float64) PatchSpec {
		ps := base.Drone.clone()
		ps.Name = ""
		ps.Units[0].Args[3] = release
		return ps
	}
	long := base
	long.Name, long.Drone = "reg-long", drone(8)
	if err := RegisterPalette(long); err != nil {
		t.Fatalf("an 8 s release, past a shot's, was refused: %v", err)
	}

	dry := PatchSpec{Units: []UnitSpec{{Op: "env", Args: []float64{0, 0.1, 0, 0.1}}, {Op: "mul"}, {Op: "out", Args: []float64{1, 0.5}}}}
	edited := drone(5)
	edited.Name = "drone"
	for _, c := range []struct {
		why  string
		edit func(*PaletteSpec)
		says string
	}{
		{"a built-in's name", func(s *PaletteSpec) { s.Name = "rust" }, "built-in palette"},
		{"a name EDN can't read back", func(s *PaletteSpec) { s.Name = "reg bad" }, "name"},
		{"a release of a minute", func(s *PaletteSpec) { s.Drone = drone(60) }, "release"},
		{"a built-in's name on other units", func(s *PaletteSpec) { s.Drone = edited }, "differ"},
		{"no units and no built-in", func(s *PaletteSpec) { s.Pad = PatchSpec{Name: "nope"} }, "built-in patch"},
		{"a pad with no units", func(s *PaletteSpec) { s.Pad = PatchSpec{} }, "built-in patch"},
		{"a stack run dry", func(s *PaletteSpec) { s.Air = dry }, "run dry"},
		{"a kick out of range", func(s *PaletteSpec) {
			s.Kit[0] = DrumSpec{PatchSpec: PatchSpec{Name: "kick"}, Note: 500}
		}, "note"},
	} {
		s := base.clone()
		c.edit(&s)
		err := RegisterPalette(s)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: got %v, want an error saying %q", c.why, err, c.says)
		}
		if _, ok := PaletteByName(s.Name); ok && s.Name != "rust" {
			t.Errorf("%s: registered anyway", c.why)
		}
	}

	bell, _ := LeadSpecByName("bell")
	for _, c := range []struct {
		why  string
		spec LeadSpec
		says string
	}{
		{"a built-in's name", bell, "built-in lead"},
		{"no gate", LeadSpec{PatchSpec{"reg-x", bell.Units}, 0}, "gate"},
		{"a gate past the pattern", LeadSpec{PatchSpec{"reg-x", bell.Units}, rows + 1}, "gate"},
		{"no units", LeadSpec{PatchSpec{Name: "reg-x"}, 1}, "units"},
	} {
		err := RegisterLead(c.spec)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("lead, %s: got %v, want an error saying %q", c.why, err, c.says)
		}
	}
	if _, ok := LeadSpecByName("reg-x"); ok {
		t.Error("a refused lead was registered")
	}
}
