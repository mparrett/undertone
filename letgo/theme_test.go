package letgo

import (
	"io"
	"testing"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// A theme sent from lg before music-start waits and starts with the music,
// as a title theme must in a browser, where audio unlocks on a gesture; then
// it plays out and theme-playing? turns false.
func TestPlayThemeFromLgStartsWithTheMusic(t *testing.T) {
	m := NewMusic(engine.DeriveDungeon(1, 0, 0))
	SetMusic(m)
	c, err := api.NewLetGo("themetest")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(`(undertone/play-theme
	                      {:grids [{:lead [1 0 3 0 5] :bass [1] :pulse [true]
	                                :drums ["X...x..." "" "x.x.x.x."] :bed false}]
	                       :then :silence})`); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Run(`(undertone/theme-playing?)`); v != vm.TRUE {
		t.Fatalf("theme-playing? before music-start = %v, want true: the theme is waiting", v)
	}
	m.start(func(io.Reader) (output, error) { return gainTestOutput{}, nil })
	defer m.Stop()
	if !m.synth.ThemePlaying() {
		t.Fatal("the waiting theme did not start with the music")
	}
	for range 8 { // one pattern is about 7.3 s at dungeon's 66 bpm
		m.synth.Read(make([]byte, 4*engine.SampleRate))
	}
	if v, _ := c.Run(`(undertone/theme-playing?)`); v != vm.FALSE {
		t.Errorf("theme-playing? after the theme = %v, want false", v)
	}
}

// Bad themes are errors, since one is a bug in the game; nil stops a theme.
func TestPlayThemeRejectsBadGrids(t *testing.T) {
	SetMusic(NewMusic(engine.DeriveDungeon(1, 0, 0)))
	c, err := api.NewLetGo("themebad")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`(undertone/play-theme [{:lead [1]}])`,
		`(undertone/play-theme {:grids {:lead [1]}})`,
		`(undertone/play-theme {:grids [{:lead [9]}]})`,
		`(undertone/play-theme {:grids [{:lead [1 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1]}]})`,
		`(undertone/play-theme {:grids [{:drums [1]}]})`,
		`(undertone/play-theme {:grids [{}] :then :fade})`,
	} {
		if _, err := c.Run(bad); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
	if _, err := c.Run(`(undertone/play-theme nil)`); err != nil {
		t.Errorf("nil should stop a theme: %v", err)
	}
	if v, _ := c.Run(`(undertone/theme-playing?)`); v != vm.FALSE {
		t.Errorf("theme-playing? after nil = %v, want false", v)
	}
}
