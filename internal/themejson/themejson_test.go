package themejson

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mparrett/undertone/engine"
)

// write is Parse's inverse, for the round trip: a theme as the JSON Parse reads.
func write(t *engine.Theme) string {
	type grid struct {
		Lead  string   `json:"lead"`
		Bass  string   `json:"bass"`
		Pulse string   `json:"pulse"`
		Drums []string `json:"drums"`
		Bed   bool     `json:"bed"`
	}
	out := struct {
		Grids []grid `json:"grids"`
		Then  string `json:"then"`
	}{Then: [...]string{"generate", "silence", "loop"}[t.Then]}
	for _, g := range t.Grids {
		var j grid
		for i := range engine.Steps {
			j.Lead += string(rune('0' + g.Lead[i]))
			j.Bass += string(rune('0' + g.Bass[i]))
			if g.Pulse[i] {
				j.Pulse += "1"
			} else {
				j.Pulse += "0"
			}
		}
		j.Drums, j.Bed = g.Drums[:], g.Bed
		out.Grids = append(out.Grids, j)
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func TestParseRoundTrips(t *testing.T) {
	want := &engine.Theme{Then: engine.ThenSilence, Grids: []engine.Grid{
		{Lead: [engine.Steps]int{1, 0, 3, 0, 5, 0, 0, 8}, Bass: [engine.Steps]int{1, 0, 0, 0, 5}, Bed: true,
			Pulse: [engine.Steps]bool{true, false, false, false, true}, Drums: [engine.DrumLanes]string{"x...x...", "", "..x."}},
		{Lead: [engine.Steps]int{8, 7, 6, 5}},
	}}
	got, err := Parse(write(want))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back\n%+v\nwant\n%+v", got, want)
	}
}

// A bare vector of grids hands back to the generator; short lanes rest for
// the steps they leave out, and a grid may leave any lane out.
func TestParseForms(t *testing.T) {
	th, err := Parse(` [{"lead": "12"}, {}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Grids) != 2 || th.Then != engine.ThenGenerate || th.Grids[0].Lead != [engine.Steps]int{1, 2} || th.Grids[0].Bed {
		t.Errorf("a vector read as %+v", th)
	}
	for then, want := range map[string]engine.ThemeEnd{"generate": engine.ThenGenerate, "silence": engine.ThenSilence, "loop": engine.ThenLoop} {
		th, err := Parse(`{"grids": [{}], "then": "` + then + `"}`)
		if err != nil || th.Then != want {
			t.Errorf("then %s: %v, %v", then, th, err)
		}
	}
}

func TestParseErrors(t *testing.T) {
	long := strings.Repeat("1", engine.Steps+1)
	for bad, say := range map[string]string{
		`{"grids": [{}], "then": "stop"}`:   "then is",
		`[{"lead": "9"}]`:                   "not a degree",
		`[{}, {"bass": "x"}]`:               "grid 1",
		`[{"lead": "` + long + `"}]`:        "at most",
		`[{"pulse": "2"}]`:                  "not 0 or 1",
		`[{"pulse": "` + long + `"}]`:       "at most",
		`[{"drums": ["x", "x", "x", "x"]}]`: "drum lanes",
		`[{"leed": "1"}]`:                   "leed",
		`{"grids": [{}], "than": "loop"}`:   "than",
		`[{}] [{}]`:                         "more after",
		`{"grids": 3}`:                      "",
		`nope`:                              "",
	} {
		if _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), say) {
			t.Errorf("%s: error %v, want one saying %q", bad, err, say)
		}
	}
}
