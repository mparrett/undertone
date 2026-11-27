package letgo

import (
	"reflect"
	"testing"

	"github.com/nooga/let-go/pkg/api"

	"github.com/mparrett/undertone/engine"
)

// :bass on a theme map and in set-parameters names the bass lane's
// instrument; one that names no lead instrument is an error.
func TestBassFromLg(t *testing.T) {
	m := NewMusic(engine.DeriveDungeon(1, 0, 0))
	SetMusic(m)
	c, err := api.NewLetGo("bass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(`(undertone/play-theme {:grids [{:bass [1]}] :bass :reed})`); err != nil {
		t.Fatal(err)
	}
	if m.theme == nil || m.theme.Bass != "reed" {
		t.Errorf("theme :bass :reed read as %+v", m.theme)
	}
	for _, src := range []string{
		`(undertone/play-theme {:grids [{:bass [1]}] :bass :tuba})`,
		`(undertone/set-parameters {:bass :tuba})`,
		`(undertone/set-parameters {:bass {:out 0.1}})`,
		`(undertone/play-theme {:grids [{:bass [1]}] :bass 1})`,
	} {
		if _, err := c.Run(src); err == nil {
			t.Errorf("%s: no error", src)
		}
	}
	want := m.params
	engine.ApplyBass(&want, "reed")
	if _, err := c.Run(`(undertone/set-parameters {:bass :reed})`); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.params.Palette, want.Palette) {
		t.Error("set-parameters :bass :reed did not apply the reed as ApplyBass does")
	}
	// :mood in the same map is applied first, so the :bass is the one kept
	if _, err := c.Run(`(undertone/set-parameters {:bass :bell :mood :aurora})`); err != nil {
		t.Fatal(err)
	}
	aurora, _ := engine.DeriveMood("aurora", 1, 0, 0)
	want = aurora
	engine.ApplyBass(&want, "bell")
	if !reflect.DeepEqual(m.params.Palette.Bass, want.Palette.Bass) {
		t.Error("a :bass beside a :mood was replaced by the mood's bass")
	}
}
