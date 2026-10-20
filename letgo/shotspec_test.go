package letgo

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/nooga/let-go/pkg/api"

	"github.com/mparrett/undertone/engine"
)

// A shot registered from lg, in the map ShotSpec.EDN writes, is the shot Go
// would register: every built-in, renamed and read back, gives the same spec,
// and a trigger from lg plays it as the built-in plays.
func TestRegisterShotFromLg(t *testing.T) {
	m := NewMusic(engine.DeriveDungeon(1, 0, 0))
	SetMusic(m)
	c, err := api.NewLetGo("shotspectest")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range engine.ShotNames() {
		want, _ := engine.ShotByName(name)
		want.Name = "lg-" + name
		if _, err := c.Run("(undertone/register-shot " + want.EDN() + ")"); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, want.EDN())
		}
		if got, _ := engine.ShotByName(want.Name); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: lg registered\n%+v\nwant\n%+v", name, got, want)
		}
	}

	silent := engine.Params{BPM: 120, Seed: 1, Scale: []int{0, 2, 3, 5, 7, 9, 10}}
	m.mu.Lock()
	m.synth = engine.NewSynth(silent)
	m.mu.Unlock()
	if _, err := c.Run(`(undertone/trigger {:shot :lg-hit :note 50 :gain 1.0 :pan 0.3 :seed "4"})`); err != nil {
		t.Fatal(err)
	}
	ref := engine.NewSynth(silent)
	ref.Trigger("hit", 50, 1, 0.3, 0, 4)
	got, want := make([]byte, engine.SampleRate), make([]byte, engine.SampleRate)
	m.synth.Read(got)
	ref.Read(want)
	if !bytes.Equal(got, want) {
		t.Error("a shot registered from lg plays differently from the built-in it copies")
	}

	for _, c2 := range []struct{ patch, says string }{
		{"[[:env 0 0.1 0 0.1] [:mul] [:out 1 0.5]]", "run dry"},
		{"[[:env :sine 0 0.1 0 0.1] [:osc 0 :sine 0 1] [:mul] [:out 1 0.5]]", "only an osc"},
		{"[[:env 0 0.1 0 0.1] [:osc 0 :sine :saw 0 1] [:mul] [:out 1 0.5]]", "only an osc"},
	} {
		_, err := c.Run(`(undertone/register-shot {:name :bad :gate-ms 10 :patch ` + c2.patch + `})`)
		if err == nil || !strings.Contains(err.Error(), c2.says) {
			t.Errorf("%s: got %v, want an error saying %q", c2.patch, err, c2.says)
		}
	}
}
