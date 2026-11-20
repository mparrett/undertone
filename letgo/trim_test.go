package letgo

import (
	"strings"
	"testing"

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
