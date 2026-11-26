package letgo

import (
	"io"
	"testing"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// In a render, a revision of a theme with itself changes nothing, so it
// neither cuts the pattern nor starts the theme over; one with other notes
// changes what follows, and when it lands changes that again.
func TestRenderWAVRevisesATheme(t *testing.T) {
	c := renderLg(t, "renderrevise")
	const a = `{:grids [{:lead [1 0 3 0 5 0 3 0 1 0 3 0 5 0 3 0]} {:lead [2 0 4 0 2 0 4 0]}] :then :loop}`
	const b = `{:grids [{:lead [5 0 5 0 5 0 5 0 5 0 5 0 5 0 5 0]} {:lead [6 6 6 6 6 6 6 6]}] :then :loop}`
	run := func(steps string) vm.Value {
		t.Helper()
		v, err := c.Run(`(undertone/render-wav nil 20.0 [[0.0 :play-theme ` + a + `]` + steps + `])`)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	plain := run(``)
	if same := run(`[1.3 :revise-theme [` + a + ` :row]]`); same != plain {
		t.Error("revising a theme with itself changed the render")
	}
	row, grid, loop := run(`[1.3 :revise-theme [`+b+` :row]]`), run(`[1.3 :revise-theme [`+b+` :grid]]`), run(`[1.3 :revise-theme [`+b+` :loop]]`)
	if row == plain || grid == plain || loop == plain {
		t.Error("a revision with other notes left the render as it was")
	}
	if row == grid || grid == loop || row == loop {
		t.Errorf("when the revision lands made no difference: :row %v, :grid %v, :loop %v", row, grid, loop)
	}
}

// A bad revise-theme is an error, and so is one with no theme in it.
func TestReviseThemeRejectsBadArgs(t *testing.T) {
	SetMusic(NewMusic(engine.DeriveDungeon(1, 0, 0)))
	c, err := api.NewLetGo("revisebad")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`(undertone/revise-theme nil :row)`,
		`(undertone/revise-theme {:grids []} :row)`,
		`(undertone/revise-theme {:grids [{:lead [9]}]} :row)`,
		`(undertone/revise-theme {:grids [{:lead [1]}]} :now)`,
		`(undertone/revise-theme {:grids [{:lead [1]}]} nil)`,
		`(undertone/revise-theme {:grids [{:lead [1]}]})`,
		`(undertone/render-wav nil 2.0 [[0.0 :revise-theme {:grids [{:lead [1]}]}]])`,
		`(undertone/render-wav nil 2.0 [[0.0 :revise-theme [{:grids [{:lead [1]}]} :soon]]])`,
	} {
		if _, err := c.Run(bad); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

// A revision sent while a theme waits on music-start replaces the waiting
// theme; with none waiting it starts nothing.
func TestReviseThemeBeforeMusicStart(t *testing.T) {
	m := NewMusic(engine.DeriveDungeon(1, 0, 0))
	SetMusic(m)
	c, err := api.NewLetGo("revisewait")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(`(undertone/revise-theme {:grids [{:lead [5]}] :then :loop} :loop)`); err != nil {
		t.Fatal(err)
	}
	if m.theme != nil {
		t.Fatal("a revision with no theme waiting left one waiting")
	}
	if _, err := c.Run(`(do (undertone/play-theme {:grids [{:lead [1]}] :then :loop})
	                        (undertone/revise-theme {:grids [{:lead [5]} {:lead [6]}] :then :loop} :loop))`); err != nil {
		t.Fatal(err)
	}
	if m.theme == nil || len(m.theme.Grids) != 2 || m.theme.Grids[0].Lead[0] != 5 {
		t.Fatalf("the waiting theme is %+v, want the revision", m.theme)
	}
	m.start(func(io.Reader) (output, error) { return gainTestOutput{}, nil })
	defer m.Stop()
	if !m.synth.ThemePlaying() {
		t.Error("the revised theme did not start with the music")
	}
}

// position names the theme's grid and pass, and neither outside a theme.
func TestPositionThemeGridFromLg(t *testing.T) {
	p := engine.DeriveDungeon(1, 1, 0)
	m := NewMusic(p)
	SetMusic(m)
	c, err := api.NewLetGo("positiontheme")
	if err != nil {
		t.Fatal(err)
	}
	startRendering(t, m, gainTestOutput{}, rowsOf(p, 3)+1)
	if v, _ := c.Run(`(let [p (undertone/position)] [(:theme-grid p) (:theme-pass p)])`); v.String() != "[nil nil]" {
		t.Errorf("outside a theme: %s, want [nil nil]", v)
	}
	if _, err := c.Run(`(undertone/play-theme {:grids [{:lead [1]} {:lead [2]}] :then :loop})`); err != nil {
		t.Fatal(err)
	}
	// the theme starts within a block; 16 rows on is its second grid, and
	// 32 the first of its second pass
	for _, want := range []string{"[1 0]", "[0 1]"} {
		m.synth.Read(make([]byte, 4*(rowsOf(p, 16)+200)))
		if v, _ := c.Run(`(let [p (undertone/position)] [(:theme-grid p) (:theme-pass p)])`); v.String() != want {
			t.Errorf("position's grid and pass: %s, want %s", v, want)
		}
	}
}

// While the output opens, the synth may already be rendering the theme it
// was started with. A revision sent then goes to that synth and keeps the
// theme's place, where replacing the waiting theme would start it over.
func TestReviseThemeWhileTheOutputOpens(t *testing.T) {
	p := engine.DeriveDungeon(1, 0, 0)
	m := NewMusic(p)
	SetMusic(m)
	c, err := api.NewLetGo("reviseopening")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(`(undertone/play-theme {:grids [{:lead [1]} {:lead [2]}] :then :loop})`); err != nil {
		t.Fatal(err)
	}
	m.start(func(src io.Reader) (output, error) {
		io.ReadFull(src, make([]byte, 4*(rowsOf(p, 20))))
		if _, err := c.Run(`(undertone/revise-theme {:grids [{:lead [5]} {:lead [6]}] :then :loop} :row)`); err != nil {
			t.Error(err)
		}
		io.ReadFull(src, make([]byte, 4*(rowsOf(p, 2))))
		return gainTestOutput{}, nil
	})
	defer m.Stop()
	got, _ := m.synth.Position()
	if !got.Theme || got.Grid != 1 || got.Row != 5 {
		t.Errorf("after opening: %+v, want row 5 of the theme's second grid, its place kept", got)
	}
}
