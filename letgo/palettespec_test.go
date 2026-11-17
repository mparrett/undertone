package letgo

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mparrett/undertone/engine"
)

// A palette registered from lg, in the map PaletteSpec.EDN writes, is the
// palette Go would register: every built-in, renamed, reads back the same,
// with its patches by name and with them as units. A :palette naming it in
// set-parameters plays its patches.
func TestRegisterPaletteFromLg(t *testing.T) {
	c, m := lgRuntime(t)
	for _, name := range []string{"dungeon", "overworld", "aurora", "rust", "void", "stratosphere"} {
		want, _ := engine.PaletteSpecByName(name)
		want.Name = "lg-" + name
		if _, err := c.Run("(undertone/register-palette " + want.EDN() + ")"); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, want.EDN())
		}
		if got, _ := engine.PaletteSpecByName(want.Name); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: lg registered\n%+v\nwant\n%+v", name, got, want)
		}

		// units rather than names: an unnamed patch is written as a vector
		want.Name = "lg-units-" + name
		want.Drone.Name, want.Pad.Name = "", "my-pad"
		if !want.Kit[0].Default { // a default lane is written as :default
			want.Kit[0].Name = ""
		}
		edn := want.EDN()
		if !strings.Contains(edn, ":drone [[:env") || !strings.Contains(edn, ":name :my-pad") {
			t.Fatalf("%s: the drone and pad are not written as units\n%s", name, edn)
		}
		if _, err := c.Run("(undertone/register-palette " + edn + ")"); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, edn)
		}
		if got, _ := engine.PaletteSpecByName(want.Name); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: lg registered\n%+v\nwant\n%+v", name, got, want)
		}
	}

	m.params = engine.DeriveDungeon(1, 0, 0)
	if _, err := c.Run("(undertone/set-parameters {:palette :lg-rust :lead :clang})"); err != nil {
		t.Fatal(err)
	}
	want, _ := engine.PaletteByName("rust")
	want.Lead = m.params.Palette.Lead // the lead's, both ways
	if !reflect.DeepEqual(m.params.Palette, want) {
		t.Error("a registered :palette plays other patches than the palette it copies")
	}

	for _, c2 := range []struct{ palette, says string }{
		{"{:name :lg-bad :drone :nope :fifth :pad :pad :pad :air :air :pulse :pulse}", "built-in patch"},
		{"{:name :lg-bad :fifth :pad :pad :pad :air :air :pulse :pulse}", "drone"},
		{"{:name :lg-bad :drone :drone :fifth :pad :pad :pad :air :air :pulse :pulse :kit [:default]}", "3 lanes"},
		{"{:name :lg-bad :drone :drone :fifth :pad :pad :pad :air :air :pulse :pulse :kit [:default {:patch :kick} :default]}", ":note"},
		{"{:name :dungeon :drone :drone :fifth :pad :pad :pad :air :air :pulse :pulse}", "built-in palette"},
		{"{:name :lg-bad :drone [[:env 0 0.1 0 0.1] [:mul] [:out 1 0.5]] :fifth :pad :pad :pad :air :air :pulse :pulse}", "run dry"},
	} {
		_, err := c.Run("(undertone/register-palette " + c2.palette + ")")
		if err == nil || !strings.Contains(err.Error(), c2.says) {
			t.Errorf("%s: got %v, want an error saying %q", c2.palette, err, c2.says)
		}
	}
	if _, ok := engine.PaletteByName("lg-bad"); ok {
		t.Error("a refused palette was registered")
	}
}

// A lead registered from lg reads back as Go would register it, and a :lead
// naming it puts its patch on the lead track.
func TestRegisterLeadFromLg(t *testing.T) {
	c, m := lgRuntime(t)
	want, _ := engine.LeadSpecByName("reed")
	want.Name = "lg-reed"
	if _, err := c.Run("(undertone/register-lead " + want.EDN() + ")"); err != nil {
		t.Fatalf("%v\n%s", err, want.EDN())
	}
	if got, _ := engine.LeadSpecByName(want.Name); !reflect.DeepEqual(got, want) {
		t.Errorf("lg registered\n%+v\nwant\n%+v", got, want)
	}
	m.params = engine.DeriveDungeon(1, 0, 0)
	if _, err := c.Run("(undertone/set-parameters {:lead :lg-reed})"); err != nil {
		t.Fatal(err)
	}
	reed := m.params
	engine.ApplyLead(&reed, "reed")
	if m.params.LeadName != "lg-reed" || m.params.LeadGate != want.Gate || !reflect.DeepEqual(m.params.Palette.Lead, reed.Palette.Lead) {
		t.Errorf("a registered :lead gave %q, gate %d", m.params.LeadName, m.params.LeadGate)
	}
	if _, err := c.Run("(undertone/register-lead {:name :lg-x :patch [[:env 0 0.1 0 0.1] [:osc 0 :sine 0 1] [:mul] [:out 1 0.5]]})"); err == nil || !strings.Contains(err.Error(), ":gate") {
		t.Errorf("a lead with no gate: got %v", err)
	}
}
