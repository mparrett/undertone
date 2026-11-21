package letgo

import (
	"strings"
	"testing"

	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// set-parameters reads :lead-db, :pad-db, :bass-db and :mute; a key left out
// keeps its value, as every key does, and a misspelt layer is an error.
func TestTrimsAndMuteFromLg(t *testing.T) {
	c, m := lgRuntime(t)
	m.params = engine.DeriveDungeon(1, 0, 0)
	if _, err := c.Run("(undertone/set-parameters {:lead-db -3 :pad-db 2.5 :bass-db -6 :mute [:lead :drums]})"); err != nil {
		t.Fatal(err)
	}
	p := m.params
	if p.LeadDB != -3 || p.PadDB != 2.5 || p.BassDB != -6 || p.Mute != engine.LayerLead|engine.LayerDrums {
		t.Fatalf("got lead %v pad %v bass %v mute %b", p.LeadDB, p.PadDB, p.BassDB, p.Mute)
	}
	if _, err := c.Run("(undertone/set-parameters {:bpm 80})"); err != nil {
		t.Fatal(err)
	}
	if p := m.params; p.LeadDB != -3 || p.Mute != engine.LayerLead|engine.LayerDrums {
		t.Fatalf("a map without the keys changed them: lead %v mute %b", p.LeadDB, p.Mute)
	}
	if _, err := c.Run("(undertone/set-parameters {:mute []})"); err != nil {
		t.Fatal(err)
	}
	if m.params.Mute != 0 {
		t.Fatalf(":mute [] left %b muted", m.params.Mute)
	}
	if _, err := c.Run(`(undertone/set-parameters {:mute ["pad" :air]})`); err != nil || m.params.Mute != engine.LayerPad|engine.LayerAir {
		t.Fatalf(`:mute ["pad" :air]: err %v, mute %b`, err, m.params.Mute)
	}
	// a rejected map changes nothing, not even the keys before the bad one
	before := m.params
	_, err := c.Run("(undertone/set-parameters {:bpm 140 :lead-db -12 :mute [:leed]})")
	if err == nil || !strings.Contains(err.Error(), "unknown layer") {
		t.Fatalf(":mute [:leed]: err %v, want unknown layer", err)
	}
	if m.params.BPM != before.BPM || m.params.LeadDB != before.LeadDB || m.params.Mute != before.Mute {
		t.Fatalf("a rejected map left bpm %v lead-db %v mute %b behind", m.params.BPM, m.params.LeadDB, m.params.Mute)
	}
}

// An entry's :lead-db and :pad-db are the mood's trims: they read back from
// its EDN, derive-spec hands them to set-parameters as :mood-lead-db and
// :mood-pad-db, a game's own :lead-db survives a mood change, and a mood
// with no trims clears the last one's.
func TestMoodTrimsFromLg(t *testing.T) {
	c, m := lgRuntime(t)
	s, _ := engine.MoodByName("village")
	s.Name, s.Palette = "hot", ""
	s.LeadDB, s.PadDB = -6, 2.5
	if err := c.Def("hot-src", "{"+s.EDN()+"}"); err != nil {
		t.Fatal(err)
	}
	v, err := c.Run("(:hot (read-string hot-src))")
	if err != nil {
		t.Fatal(err)
	}
	got, err := moodSpec(v.(*vm.PersistentMap))
	if err != nil {
		t.Fatal(err)
	}
	if got.LeadDB != -6 || got.PadDB != 2.5 {
		t.Fatalf("the entry reads back with trims %v %v, want -6 2.5", got.LeadDB, got.PadDB)
	}
	m.params = engine.DeriveDungeon(1, 0, 0)
	if _, err := c.Run("(undertone/set-parameters {:lead-db -3})"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run("(ambience/score-spec! (:hot (read-string hot-src)) 7 3 0.5)"); err != nil {
		t.Fatal(err)
	}
	if p := m.params; p.MoodLeadDB != -6 || p.MoodPadDB != 2.5 || p.LeadDB != -3 {
		t.Fatalf("after the trimmed mood: mood trims %v %v, game lead %v; want -6 2.5, -3", p.MoodLeadDB, p.MoodPadDB, p.LeadDB)
	}
	plain, _ := engine.MoodByName("village")
	plain.Name, plain.Palette = "plain", ""
	if err := c.Def("plain-src", "{"+plain.EDN()+"}"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run("(ambience/score-spec! (:plain (read-string plain-src)) 7 3 0.5)"); err != nil {
		t.Fatal(err)
	}
	if p := m.params; p.MoodLeadDB != 0 || p.MoodPadDB != 0 || p.LeadDB != -3 {
		t.Fatalf("after an untrimmed mood: mood trims %v %v, game lead %v; want 0 0, -3", p.MoodLeadDB, p.MoodPadDB, p.LeadDB)
	}
	if _, err := c.Run(`(undertone/derive-spec (assoc (:hot (read-string hot-src)) :lead-db "loud") 1 0 0)`); err == nil ||
		!strings.Contains(err.Error(), ":lead-db must be a number") {
		t.Fatalf("a non-number :lead-db: err %v, want it named", err)
	}
}
