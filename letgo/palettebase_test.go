package letgo

import (
	"reflect"
	"testing"

	"github.com/mparrett/undertone/engine"
)

// register-palette's :base fills what the map leaves out from that palette,
// and a based spec's EDN registers as the spec does.
func TestRegisterPaletteBaseFromLg(t *testing.T) {
	c, _ := lgRuntime(t)
	if _, err := c.Run(`(undertone/register-palette {:name :lg-aurora-soft :base :aurora :bass :reed})`); err != nil {
		t.Fatal(err)
	}
	want, _ := engine.PaletteSpecByName("aurora")
	reed, _ := engine.PaletteSpecByName("lg-aurora-soft")
	want.Name, want.Bass = "lg-aurora-soft", reed.Bass
	if reed.Bass.Name != "reed" {
		t.Errorf(":bass :reed read back as %q", reed.Bass.Name)
	}
	if !reflect.DeepEqual(reed, want) {
		t.Errorf("based on aurora, read back\n%+v\nwant\n%+v", reed, want)
	}
	// the README's softer dungeon bass, under a name that shadows nothing
	if _, err := c.Run(`(undertone/register-palette
	                      {:name :lg-dungeon-sine :base :dungeon
	                       :bass [[:env 0.01 0.4 0.6 0.3] [:osc 0 :sine 0 1] [:mul] [:out 0.1 0.5]]})`); err != nil {
		t.Errorf("the README's example: %v", err)
	}
	spec := engine.PaletteSpec{Name: "lg-aurora-edn", Base: "aurora", Bass: engine.PatchSpec{Name: "reed"}}
	if _, err := c.Run("(undertone/register-palette " + spec.EDN() + ")"); err != nil {
		t.Fatal(err)
	}
	got, _ := engine.PaletteSpecByName("lg-aurora-edn")
	want.Name = "lg-aurora-edn"
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a based spec's EDN registered as\n%+v", got)
	}
}
