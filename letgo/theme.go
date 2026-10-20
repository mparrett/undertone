package letgo

import (
	"fmt"

	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// playTheme starts an authored theme, given as
// {:grids [grid ...] :then :generate|:silence|:loop}, where each grid is
// {:lead [degrees] :bass [degrees] :pulse [bools] :drums ["X.x." ...] :bed true}
// and :then defaults to :generate. nil or no grids stops a theme. Before
// music-start the theme waits and starts with the music, since a title theme
// is wanted before the gesture that unlocks audio.
func (m *Music) playTheme(v vm.Value) (vm.Value, error) {
	t, err := theme(v)
	if err != nil {
		return vm.NIL, fmt.Errorf("undertone/play-theme: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.out == nil { // not started, waiting for the gesture, or stopped
		m.theme = t
		return vm.NIL, nil
	}
	m.synth.PlayTheme(t)
	return vm.NIL, nil
}

// themePlaying reports whether the last theme has still to finish, counting
// one waiting for music-start.
func (m *Music) themePlaying() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.out == nil {
		return m.theme != nil
	}
	return m.synth.ThemePlaying()
}

func theme(v vm.Value) (*engine.Theme, error) {
	if v == vm.NIL {
		return nil, nil
	}
	opts, ok := v.(*vm.PersistentMap)
	if !ok {
		return nil, fmt.Errorf("expected a map {:grids [...] :then ...} or nil")
	}
	var grids vm.ArrayVector
	if g := at(opts, "grids"); g != vm.NIL {
		if grids, ok = g.(vm.ArrayVector); !ok {
			return nil, fmt.Errorf(":grids must be a vector of maps")
		}
	}
	if len(grids) == 0 {
		return nil, nil
	}
	t := &engine.Theme{}
	for i, v := range grids {
		g, err := grid(v)
		if err != nil {
			return nil, fmt.Errorf("grid %d: %w", i, err)
		}
		t.Grids = append(t.Grids, g)
	}
	if then, ok := kwStr(opts, "then"); ok {
		switch then {
		case "generate":
			t.Then = engine.ThenGenerate
		case "silence":
			t.Then = engine.ThenSilence
		case "loop":
			t.Then = engine.ThenLoop
		default:
			return nil, fmt.Errorf(":then must be :generate, :silence or :loop, got %q", then)
		}
	}
	return t, nil
}

// grid reads one grid map. A lane shorter than the grid rests for the rest.
func grid(v vm.Value) (engine.Grid, error) {
	var g engine.Grid
	m, ok := v.(*vm.PersistentMap)
	if !ok {
		return g, fmt.Errorf("not a map")
	}
	for _, lane := range []struct {
		key string
		dst *[engine.Steps]int
	}{{"lead", &g.Lead}, {"bass", &g.Bass}} {
		if at(m, lane.key) == vm.NIL {
			continue
		}
		ds, ok := ints(m, lane.key)
		if !ok || len(ds) > engine.Steps {
			return g, fmt.Errorf(":%s must be a vector of at most %d degrees", lane.key, engine.Steps)
		}
		for i, d := range ds {
			if d < 0 || d > engine.Degrees {
				return g, fmt.Errorf(":%s step %d is degree %d, outside 0..%d", lane.key, i, d, engine.Degrees)
			}
			lane.dst[i] = d
		}
	}
	if p := at(m, "pulse"); p != vm.NIL {
		vec, ok := p.(vm.ArrayVector)
		if !ok || len(vec) > engine.Steps {
			return g, fmt.Errorf(":pulse must be a vector of at most %d booleans", engine.Steps)
		}
		for i, e := range vec {
			g.Pulse[i] = vm.IsTruthy(e)
		}
	}
	if d := at(m, "drums"); d != vm.NIL {
		vec, ok := d.(vm.ArrayVector)
		if !ok || len(vec) > engine.DrumLanes {
			return g, fmt.Errorf(":drums must be a vector of at most %d lanes", engine.DrumLanes)
		}
		for i, e := range vec {
			s, ok := e.(vm.String)
			if !ok {
				return g, fmt.Errorf(":drums lane %d must be a string", i)
			}
			g.Drums[i] = string(s)
		}
	}
	g.Bed = vm.IsTruthy(at(m, "bed"))
	return g, nil
}
