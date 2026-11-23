// Package themejson reads a theme written as JSON, the form the studio page
// plays and cmd/undertone's -theme takes. It is apart from the engine so that
// the engine, and what embeds it, does not carry encoding/json.
package themejson

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mparrett/undertone/engine"
)

// gridJSON is one grid of a theme: lead and bass are 16 digits, a scale
// degree each (0 rests), pulse 16 digits with 1 for a step that sounds, drums
// up to DrumLanes groove lanes.
type gridJSON struct {
	Lead, Bass, Pulse string
	Drums             []string
	Bed               bool
}

// Parse reads a vector of grids, which hands back to the generator when it
// ends, or {grids, then}, where then is what undertone/play-theme's :then
// is: generate (the default), silence or loop.
func Parse(data string) (*engine.Theme, error) {
	var in struct {
		Grids []gridJSON
		Then  string
	}
	// A key that is not a theme's is an error: a misspelled lane would
	// otherwise play as a rest, and a misspelled then as generate.
	dec := json.NewDecoder(strings.NewReader(data))
	dec.DisallowUnknownFields()
	var err error
	if strings.HasPrefix(strings.TrimSpace(data), "[") {
		err = dec.Decode(&in.Grids)
	} else {
		err = dec.Decode(&in)
	}
	if err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("more after the theme")
	}
	t := &engine.Theme{}
	switch in.Then {
	case "", "generate":
		t.Then = engine.ThenGenerate
	case "silence":
		t.Then = engine.ThenSilence
	case "loop":
		t.Then = engine.ThenLoop
	default:
		return nil, fmt.Errorf("then is generate, silence or loop, not %q", in.Then)
	}
	for i, g := range in.Grids {
		var out engine.Grid
		for lane, src := range map[*[engine.Steps]int]string{&out.Lead: g.Lead, &out.Bass: g.Bass} {
			if len(src) > engine.Steps {
				return nil, fmt.Errorf("grid %d: a lane is %d steps, at most %d", i, len(src), engine.Steps)
			}
			for j, c := range src {
				if c < '0' || c > '0'+engine.Degrees {
					return nil, fmt.Errorf("grid %d: %q is not a degree 0-%d", i, c, engine.Degrees)
				}
				lane[j] = int(c - '0')
			}
		}
		if len(g.Pulse) > engine.Steps {
			return nil, fmt.Errorf("grid %d: the pulse is %d steps, at most %d", i, len(g.Pulse), engine.Steps)
		}
		for j, c := range g.Pulse {
			if c != '0' && c != '1' {
				return nil, fmt.Errorf("grid %d: pulse %q is not 0 or 1", i, c)
			}
			out.Pulse[j] = c == '1'
		}
		if len(g.Drums) > engine.DrumLanes {
			return nil, fmt.Errorf("grid %d: %d drum lanes, at most %d", i, len(g.Drums), engine.DrumLanes)
		}
		copy(out.Drums[:], g.Drums)
		out.Bed = g.Bed
		t.Grids = append(t.Grids, out)
	}
	return t, nil
}
