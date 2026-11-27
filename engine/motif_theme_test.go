package engine

import (
	"fmt"
	"slices"
	"testing"
)

// Spike: what the arc does under a theme. The spike was written before
// themes, so this is a look at how they meet, and it logs what it finds: a
// theme's own lanes should play note for note with the arc on, and the arc
// should still reach the bed under them.
func TestArcUnderAThemeSpike(t *testing.T) {
	play := func(arc bool) (lanes, bed []string, state MotifState) {
		p, _ := DeriveMood("dungeon", 7, 1, 0.3)
		if arc {
			tm, ok := TemperamentFor("dungeon")
			if !ok {
				t.Fatal("the dungeon has no temperament")
			}
			p.Phrasing, p.Memory = &tm.Phrasing, &tm.Memory
		}
		s := NewSynth(p)
		a, b := Grid{Bed: true}, Grid{Bed: true}
		for i := 0; i < Steps; i += 4 {
			a.Lead[i], b.Lead[i] = 1+i/4, 5-i/4
			a.Bass[i], b.Bass[i] = 1, 3
		}
		a.Drums[0], b.Drums[0] = "x.......x.......x.......x.......", "x..|"
		s.PlayTheme(&Theme{Grids: []Grid{a, b}, Then: ThenLoop})
		var cursor uint64
		for sec := 0; sec < 90; sec++ {
			playedRMS(s, 1)
			var evs []NoteEvent
			evs, cursor, _ = s.NoteEvents(cursor, nil)
			for _, e := range evs {
				if e.Kind != NoteOn && e.Kind != DrumOn {
					continue
				}
				line := fmt.Sprintf("%d.%02d %s/%d %d", e.Pattern, e.Row, LayerNames()[e.Layer], e.Lane, e.Note)
				// the layers a grid writes: its lead, bass and drums
				if e.Layer == 3 || e.Layer == 7 || e.Layer == layerDrums {
					lanes = append(lanes, line)
				} else {
					bed = append(bed, line)
				}
			}
		}
		return lanes, bed, s.MotifTrace()
	}
	plainLanes, plainBed, _ := play(false)
	arcLanes, arcBed, st := play(true)
	t.Logf("lanes: %d notes plain, %d with the arc; bed: %d plain, %d with the arc", len(plainLanes), len(arcLanes), len(plainBed), len(arcBed))
	t.Logf("the arc after 90s under the theme: %+v", st)
	if !slices.Equal(plainLanes, arcLanes) {
		n := 0
		for n < len(plainLanes) && n < len(arcLanes) && plainLanes[n] == arcLanes[n] {
			n++
		}
		t.Errorf("the theme's lanes differ with the arc on, first at note %d: plain %v, arc %v", n, at(plainLanes, n), at(arcLanes, n))
	}
	if slices.Equal(plainBed, arcBed) {
		t.Errorf("the bed under the theme is the same with the arc on: it does not reach it")
	}
}

func at(a []string, i int) string {
	if i < len(a) {
		return a[i]
	}
	return "(none)"
}
