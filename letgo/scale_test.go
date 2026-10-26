package letgo

import (
	"fmt"
	"slices"
	"testing"

	"github.com/mparrett/undertone/engine"
)

// A five-note scale passes the strict entry reader and set-parameters alike.
func TestFiveNoteScalesReachTheEngine(t *testing.T) {
	c, m := lgRuntime(t)
	s, _ := engine.MoodByName("village")
	s.Name = "pentavillage"
	s.Scales = [][]int{{0, 2, 4, 7, 9}, {0, 3, 5, 7, 10}}
	if err := c.Def("penta-src", "{"+s.EDN()+"}"); err != nil {
		t.Fatal(err)
	}
	for _, level := range []int{0, 3} {
		want, err := s.Derive(7, level, 0.5)
		if err != nil {
			t.Fatal(err)
		}
		expr := fmt.Sprintf("(ambience/score-spec! (:pentavillage (read-string penta-src)) 7 %d 0.5)", level)
		if _, err := c.Run(expr); err != nil {
			t.Fatalf("level %d: %v", level, err)
		}
		checkParams(t, fmt.Sprintf("pentavillage/level%d", level), m.params, want)
	}
	if _, err := c.Run("(undertone/set-parameters {:scale [0 3 5 7 10]})"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.params.Scale, []int{0, 3, 5, 7, 10}) {
		t.Errorf("set-parameters left scale %v", m.params.Scale)
	}
}
