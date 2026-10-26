// Package lgmutate runs the grid example's pattern transforms, which are
// written in lg (mutate.lg), on an embedded let-go runtime.
package lgmutate

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

//go:embed mutate.lg
var source string

// Mutator holds a let-go runtime with the grid namespace loaded.
type Mutator struct{ c *api.LetGo }

// New boots let-go and loads mutate.lg. It takes a moment, so a page does it
// once, ahead of the first press.
func New() (*Mutator, error) {
	c, err := api.NewLetGo("grid-mutate")
	if err != nil {
		return nil, err
	}
	if err := c.Def("grid-src", source); err != nil {
		return nil, err
	}
	if _, err := c.Run("(load-string grid-src)"); err != nil {
		return nil, fmt.Errorf("mutate.lg: %w", err)
	}
	return &Mutator{c}, nil
}

// Mutate returns lg's variation of g for seed. The bed setting is kept.
func (m *Mutator) Mutate(g engine.Grid, seed int) (engine.Grid, error) {
	lit := func(xs []string) string { return "[" + strings.Join(xs, " ") + "]" }
	var lead, bass, pulse []string
	for i := 0; i < engine.Steps; i++ {
		lead = append(lead, fmt.Sprint(g.Lead[i]))
		bass = append(bass, fmt.Sprint(g.Bass[i]))
		pulse = append(pulse, fmt.Sprint(g.Pulse[i]))
	}
	v, err := m.c.Run(fmt.Sprintf("(grid/mutate {:lead %s :bass %s :pulse %s} %d)", lit(lead), lit(bass), lit(pulse), seed))
	if err != nil {
		return g, err
	}
	s, ok := v.(vm.String)
	parts := strings.Split(string(s), "|")
	if !ok || len(parts) != 3 || len(parts[0]) != engine.Steps || len(parts[1]) != engine.Steps || len(parts[2]) != engine.Steps {
		return g, fmt.Errorf("grid/mutate returned %v", v)
	}
	out := engine.Grid{Bed: g.Bed, Drums: g.Drums} // lg varies the melodic lanes only
	for i := 0; i < engine.Steps; i++ {
		out.Lead[i] = int(parts[0][i] - '0')
		out.Bass[i] = int(parts[1][i] - '0')
		out.Pulse[i] = parts[2][i] == '1'
	}
	return out, nil
}
