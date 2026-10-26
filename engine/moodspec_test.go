package engine

import (
	"reflect"
	"strings"
	"testing"
)

// A built-in mood taken through MoodSpec derives exactly what DeriveMood
// does, so a designer starting from one hears it unchanged.
func TestMoodSpecRoundTrip(t *testing.T) {
	for _, name := range MoodNames() {
		s, ok := MoodByName(name)
		if !ok || s.Palette == "" {
			t.Fatalf("%s: spec %v, palette %q", name, ok, s.Palette)
		}
		for _, seed := range []uint64{1, 7, 42} {
			for _, level := range []int{-2, 0, 1, 3, 8} {
				for _, tension := range []float64{0, 0.5, 1} {
					want, _ := DeriveMood(name, seed, level, tension)
					got, err := s.Derive(seed, level, tension)
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("%s seed %d level %d tension %v: spec derives differently (%v)", name, seed, level, tension, err)
					}
				}
			}
		}
	}
}

// A spec owns its slices: editing one in place leaves the built-in mood, and
// what the spec already derived, alone.
func TestMoodSpecDoesNotAliasTheTable(t *testing.T) {
	want, _ := DeriveMood("village", 1, 3, 0)
	s, _ := MoodByName("village")
	got, _ := s.Derive(1, 3, 0)
	for _, sc := range s.Scales {
		for i := range sc {
			sc[i] = 99
		}
	}
	for i := range s.Leads {
		s.Leads[i] = "zzz"
	}
	for i := range s.Grooves {
		s.Grooves[i] = "zzz"
	}
	if fresh, _ := MoodByName("village"); fresh.Leads[0] == "zzz" || fresh.Grooves[0] == "zzz" || fresh.Scales[0][0] == 99 {
		t.Error("editing a spec changed the mood table")
	}
	if after, _ := DeriveMood("village", 1, 3, 0); !reflect.DeepEqual(after, want) {
		t.Error("editing a spec changed the built-in mood")
	}
	if !reflect.DeepEqual(got, want) {
		t.Error("editing a spec changed what it had already derived")
	}
}

func TestMoodSpecRejectsWhatTheEngineLacks(t *testing.T) {
	base, _ := MoodByName("village")
	for _, c := range []struct {
		name string
		edit func(*MoodSpec)
	}{
		{"palette", func(s *MoodSpec) { s.Palette = "nope" }},
		{"lead", func(s *MoodSpec) { s.Leads = []string{"theremin"} }},
		{"groove", func(s *MoodSpec) { s.Grooves = []string{"polka"} }},
		{"scale", func(s *MoodSpec) { s.Scales = [][]int{{0, 2, 4, 7}} }},
	} {
		s := base
		c.edit(&s)
		if _, err := s.Derive(1, 0, 0); err == nil {
			t.Errorf("a spec with a bad %s derived without error", c.name)
		}
	}
}

func TestMoodSpecEDN(t *testing.T) {
	s, _ := MoodByName("village")
	e := s.EDN()
	for _, want := range []string{":village\n", ":bpm       {:base 100.0 :step 2.0 :hi 112.0}", ":grooves   [:amble :amble :skip :stomp]", ":swing     0.1}"} {
		if !strings.Contains(e, want) {
			t.Errorf("EDN lacks %q:\n%s", want, e)
		}
	}
	a, _ := MoodByName("aurora")
	if strings.Contains(a.EDN(), ":grooves") {
		t.Error("a mood without drums exports drum keys")
	}
}

// An open line that only a clamp moves still exports, and reads back as
// the same mood.
func TestOpenClampExports(t *testing.T) {
	s, _ := MoodByName("dungeon")
	if strings.Contains(s.EDN(), ":open") {
		t.Error("a mood with no open line exports one")
	}
	s.Open.Lo = 0.3
	if !strings.Contains(s.EDN(), ":open      {:base 0.0 :step 0.0 :lo 0.3}") {
		t.Errorf("a clamped open line was dropped:\n%s", s.EDN())
	}
}

// A room exports only when it is not the default, and derives as itself.
func TestRoomExports(t *testing.T) {
	s, _ := MoodByName("aurora")
	if strings.Contains(s.EDN(), ":room") {
		t.Error("a mood with the default room exports one")
	}
	s.Room = Room{Size: 0.5, Damp: -0.25}
	if !strings.Contains(s.EDN(), " :room      {:size 0.5 :damp -0.25}\n") {
		t.Errorf("a room was dropped:\n%s", s.EDN())
	}
	if p, _ := s.Derive(7, 3, 0.5); p.Room != s.Room {
		t.Errorf("derived room %+v, want %+v", p.Room, s.Room)
	}
}
