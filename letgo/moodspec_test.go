package letgo

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mparrett/undertone/engine"
)

// ambience/moods is the built-in table, and each entry derives as that mood.
func TestAmbienceMoodsIsTheBuiltInTable(t *testing.T) {
	c, _ := lgRuntime(t)
	want := slices.Sorted(slices.Values(engine.MoodNames()))
	v, err := c.Run(`(= (sort (map name (keys ambience/moods))) ["` + strings.Join(want, `" "`) + `"])`)
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "true" {
		t.Errorf("ambience/moods does not hold exactly %v", want)
	}
}

// An entry with a misspelt, missing or mistyped key is an error rather than
// a mood that quietly derives zeros: it is authored data, so a slip is a bug.
func TestDeriveSpecRejectsBadEntries(t *testing.T) {
	c, _ := lgRuntime(t)
	for _, bad := range []string{
		`(assoc (:village ambience/moods) :swng 0.2)`,
		`(dissoc (:village ambience/moods) :calm)`,
		`(dissoc (:village ambience/moods) :bpm)`,
		`(assoc (:village ambience/moods) :drums {:base 0.5})`,
		`(assoc (:village ambience/moods) :root-base 62.5)`,
		`(assoc (:village ambience/moods) :leads [:kazoo])`,
		`(assoc (:village ambience/moods) :scales [[0 2 4]])`,
		`(assoc (:village ambience/moods) :palette :nope)`,
		`[:not :a :map]`,
	} {
		if _, err := c.Run("(ambience/derive-spec " + bad + " 7 2 0.5)"); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
	if _, err := c.Run("(ambience/derive-spec (:village ambience/moods) 7 2.5 0.5)"); err == nil {
		t.Error("a fractional level was accepted")
	}
}

// An export under a name the host has never seen still plays as designed:
// derive-spec takes the entry itself, and its :palette picks the patches that
// the host would otherwise pick by mood name.
func TestDeriveSpecMatchesMoodSpec(t *testing.T) {
	c, m := lgRuntime(t)
	s, _ := engine.MoodByName("village")
	s.Name, s.Palette = "cavern", "aurora"
	s.BPM.Base -= 9
	s.Grooves = engine.GrooveNames()[:2]
	if err := c.Def("cavern-src", "{"+s.EDN()+"}"); err != nil {
		t.Fatal(err)
	}
	for _, seed := range []uint64{1, 7, 42} {
		for _, level := range []int{0, 3, 8} {
			for _, tension := range []float64{0, 0.6} {
				want, err := s.Derive(seed, level, tension)
				if err != nil {
					t.Fatal(err)
				}
				name := fmt.Sprintf("cavern/seed%d/level%d/tension%.1f", seed, level, tension)
				m.params = engine.DeriveDungeon(1, 0, 0)
				expr := fmt.Sprintf("(ambience/score-spec! (:cavern (read-string cavern-src)) %d %d %v)", seed, level, tension)
				if _, err := c.Run(expr); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				checkParams(t, name, m.params, want)
			}
		}
	}
}

// A palette the host lacks is an error, as it is for MoodSpec.Derive, and
// leaves the current patches alone; an export with none still reads back.
func TestSpecPaletteMustExist(t *testing.T) {
	c, m := lgRuntime(t)
	m.params = engine.DeriveDungeon(1, 0, 0)
	before := m.params.Palette
	if _, err := c.Run("(undertone/set-parameters {:palette :auroa :bpm 61})"); err == nil {
		t.Error("an unknown :palette was accepted")
	}
	if m.params.BPM == 61 || !reflect.DeepEqual(m.params.Palette, before) {
		t.Error("a rejected map still changed the music")
	}
	s, _ := engine.MoodByName("village")
	s.Palette = ""
	if _, err := c.Run("(get {" + s.EDN() + "} :village)"); err != nil {
		t.Errorf("an export without a palette does not read: %v\n%s", err, s.EDN())
	}
}

// Drums at zero gain stay silent under tension through lg too: the tension
// term turns on only for drums that are playing.
func TestDeriveSpecLeavesSilentDrumsSilent(t *testing.T) {
	c, m := lgRuntime(t)
	s, _ := engine.MoodByName("village")
	s.Name = "hush"
	s.Drums = engine.Line{Base: 0, Step: 0, Lo: math.Inf(-1), Hi: math.Inf(1)}
	if err := c.Def("hush-src", "{"+s.EDN()+"}"); err != nil {
		t.Fatal(err)
	}
	for _, level := range []int{0, 3} {
		want, _ := s.Derive(7, level, 1)
		m.params = engine.DeriveDungeon(1, 0, 0)
		if _, err := c.Run(fmt.Sprintf("(ambience/score-spec! (:hush (read-string hush-src)) 7 %d 1.0)", level)); err != nil {
			t.Fatal(err)
		}
		checkParams(t, fmt.Sprintf("hush/level%d", level), m.params, want)
		if m.params.DrumGain != 0 {
			t.Errorf("level %d: silent drums play at %v", level, m.params.DrumGain)
		}
	}
}
