package host

import (
	"fmt"
	"math"
	"testing"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/letgo"
)

// These tests are about the tension MODEL, not the plumbing. The model exists
// to answer one question — "how tense is this moment?" — and the ways it can
// be wrong are all about which situations it under-reads. So the cases below
// are chosen adversarially: each one is a moment a plausible-looking formula
// gets audibly wrong.

func musicRuntime(t *testing.T) *api.LetGo {
	t.Helper()
	m := letgo.NewMusic(engine.DeriveDungeon(1, 0, 0))
	letgo.SetMusic(m)
	c, err := api.NewLetGo("musictest")
	if err != nil {
		t.Fatal(err)
	}
	if err := letgo.LoadEmbedded(c); err != nil {
		t.Fatal(err)
	}
	c.SetLoadPath([]string{"../lg"})
	if _, err := c.Run("(require 'music)"); err != nil {
		t.Fatal(err)
	}
	// Worlds are built once and referred to by name. `hostile` takes an ai
	// state and whether the tile is in view, so each case reads as the
	// situation it describes.
	setup := `(do
      (def base {:seed-input 42 :depth 3 :fov #{}
                 :entities {:player {:id :player :body {:hp 20 :max-hp 20}}}})
      (defn at-hp [w hp] (assoc-in w [:entities :player :body :hp] hp))
      (defn with-mobs [w & states]
        (let [ids (map (fn [i] (keyword (str "m" i))) (range (count states)))
              poss (map (fn [i] [i i]) (range (count states)))]
          (assoc w
            :fov (set poss)
            :entities (merge (:entities w)
                        (zipmap ids (map (fn [id st pos]
                                           {:id id :ai {:state st} :pos pos})
                                         ids states poss))))))
      (defn unseen-mobs [w & states]
        (let [ids (map (fn [i] (keyword (str "u" i))) (range (count states)))]
          (assoc w
            :fov #{}
            :entities (merge (:entities w)
                        (zipmap ids (map (fn [id st i] {:id id :ai {:state st} :pos [i i]})
                                         ids states (range (count states)))))))))`
	if _, err := c.Run(setup); err != nil {
		t.Fatal(err)
	}
	return c
}

func evalNum(t *testing.T, c *api.LetGo, expr string) float64 {
	t.Helper()
	v, err := c.Run(expr)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	switch n := v.(type) {
	case vm.Float:
		return float64(n)
	case vm.Int:
		return float64(n)
	}
	t.Fatalf("%s: got %T (%v), want a number", expr, v, v)
	return 0
}

// The design's whole justification: a product or geometric mean is an AND, so
// it zeroes out when either source does. Both situations below would then be
// scored as perfectly calm, and they are the two tensest moments the genre
// has. Each source must raise tension on its own.
func TestEitherSourceAloneRaisesTension(t *testing.T) {
	c := musicRuntime(t)

	untouchedButSwarmed := evalNum(t, c, `(music/tension-target (with-mobs base :hunt :hunt :hunt))`)
	if untouchedButSwarmed <= 0 {
		t.Errorf("full health, three hunting mobs in view: tension %v — a product/geomean "+
			"scores this 0; each source must count alone", untouchedButSwarmed)
	}

	nearlyDeadButAlone := evalNum(t, c, `(music/tension-target (at-hp base 2))`)
	if nearlyDeadButAlone <= 0 {
		t.Errorf("2/20 hp, nothing in view: tension %v — likewise must not be 0", nearlyDeadButAlone)
	}

	// And together they must exceed either alone, without exceeding 1.
	both := evalNum(t, c, `(music/tension-target (at-hp (with-mobs base :hunt :hunt :hunt) 2))`)
	if both <= untouchedButSwarmed || both <= nearlyDeadButAlone {
		t.Errorf("both = %v, want > swarmed (%v) and > dying (%v)", both, untouchedButSwarmed, nearlyDeadButAlone)
	}
	if both > 1 {
		t.Errorf("both = %v, must stay within 0..1 with no clamp", both)
	}
}

// Only creatures that are BOTH visible and aware should count. A sleeping mob
// two rooms away is the common case on any floor, and counting it would peg
// tension permanently — the signal would carry no information.
func TestOnlyVisibleAndEngagedHostilesCount(t *testing.T) {
	c := musicRuntime(t)
	cases := []struct {
		expr string
		want float64
		why  string
	}{
		{`(music/engaged-hostiles base)`, 0, "empty floor"},
		{`(music/engaged-hostiles (with-mobs base :hunt :hunt))`, 2, "two hunting, in view"},
		{`(music/engaged-hostiles (with-mobs base :hunt :wander :sleep))`, 1, "wander and sleep are not engaged"},
		{`(music/engaged-hostiles (with-mobs base :investigate :flee))`, 2, "investigate and flee are engaged"},
		{`(music/engaged-hostiles (unseen-mobs base :hunt :hunt :hunt))`, 0, "hunting but out of view"},
	}
	for _, tc := range cases {
		if got := evalNum(t, c, tc.expr); got != tc.want {
			t.Errorf("%s: got %v, want %v (%s)", tc.expr, got, tc.want, tc.why)
		}
	}
}

// Monotonic in both inputs, and the curves have the shapes the comments claim:
// half health is a murmur, and the first hostile is the biggest single jump.
func TestCurveShapes(t *testing.T) {
	c := musicRuntime(t)

	half := evalNum(t, c, `(music/hurt (at-hp base 10))`)
	if half > 0.3 {
		t.Errorf("hurt at half health = %v; squaring is meant to keep this a murmur (~0.25), "+
			"not half panic", half)
	}
	prev := -1.0
	for _, hp := range []int{20, 15, 10, 5, 1} {
		got := evalNum(t, c, fmt.Sprintf(`(music/hurt (at-hp base %d))`, hp))
		if got < prev {
			t.Errorf("hurt not monotonic as hp falls: hp %d gave %v after %v", hp, got, prev)
		}
		prev = got
	}

	// First hostile must be the largest step.
	var steps []float64
	last := 0.0
	for n := 1; n <= 4; n++ {
		args := ""
		for i := 0; i < n; i++ {
			args += " :hunt"
		}
		got := evalNum(t, c, fmt.Sprintf(`(music/pressed (with-mobs base%s))`, args))
		steps = append(steps, got-last)
		last = got
	}
	for i := 1; i < len(steps); i++ {
		if steps[i] >= steps[0] {
			t.Errorf("step %d (%v) >= first step (%v): the alone -> not-alone transition "+
				"should be the biggest jump", i+1, steps[i], steps[0])
		}
	}
	if last >= 1 {
		t.Errorf("pressed saturated to %v at 4 hostiles; it should approach 1 without reaching it", last)
	}
}

// Death: the player entity is gone, so every lookup falls through to its
// default. That must land at maximum tension by construction, not by a
// special case someone can forget to write.
func TestMissingPlayerPinsTensionHigh(t *testing.T) {
	c := musicRuntime(t)
	got := evalNum(t, c, `(music/tension-target (assoc base :entities {}))`)
	if got < 0.99 {
		t.Errorf("no player entity: tension %v, want ~1.0 to fall out of the hp default", got)
	}
}

// The envelope: instant attack, exponential release. The attack must be
// immediate (danger lands now) and the release must never undershoot the
// current target (a rising threat cannot be smoothed away).
func TestEnvelopeAttacksInstantlyAndReleasesSlowly(t *testing.T) {
	c := musicRuntime(t)

	if got := evalNum(t, c, `(music/advance 0.0 0.9 music/default-decay)`); got != 0.9 {
		t.Errorf("attack = %v, want 0.9 reached in one step", got)
	}

	// Release from 1.0 with no threat: must decay geometrically, not drop.
	v := 1.0
	turns := 0
	for v > 0.5 {
		v = evalNum(t, c, fmt.Sprintf(`(music/advance %v 0.0 music/default-decay)`, v))
		turns++
		if turns > 50 {
			t.Fatalf("release never reached half from 1.0 in 50 turns (at %v)", v)
		}
	}
	if turns < 4 || turns > 12 {
		t.Errorf("half-life %d turns; the intent is a handful of turns — long enough that "+
			"dread outlasts the fight, short enough to notice relief", turns)
	}

	// A target above the decayed value wins immediately.
	if got := evalNum(t, c, `(music/advance 0.2 0.7 music/default-decay)`); got != 0.7 {
		t.Errorf("rising target = %v, want 0.7: the envelope must never smooth away a "+
			"threat that is getting worse", got)
	}
}

// The music seed must be stable for a whole floor. (:seed world) advances every
// turn, so binding the music to it would re-pick the lead instrument
// constantly — the failure this test exists to prevent.
func TestMusicSeedStablePerFloorAndIgnoresRngHead(t *testing.T) {
	c := musicRuntime(t)

	base := evalNum(t, c, `(music/music-seed base)`)
	for _, variant := range []string{
		`(at-hp base 3)`,               // took damage
		`(with-mobs base :hunt :hunt)`, // fight started
		`(assoc base :turn 900)`,       // time passed
		`(assoc base :seed 123456789)`, // RNG head advanced
	} {
		if got := evalNum(t, c, fmt.Sprintf(`(music/music-seed %s)`, variant)); got != base {
			t.Errorf("music-seed changed for %s: %v != %v — it must be stable across a floor",
				variant, got, base)
		}
	}
	if got := evalNum(t, c, `(music/music-seed (assoc base :depth 4))`); got == base {
		t.Error("music-seed identical on a different depth; each floor should sound like itself")
	}
}

// End to end: the map music.lg builds must be one the engine actually accepts,
// and rising tension must move the parameters the tension axis is defined to
// move. Guards against the pipeline type-checking but landing nowhere.
func TestParamsForDrivesTheEngine(t *testing.T) {
	m := letgo.NewMusic(engine.DeriveDungeon(1, 0, 0))
	letgo.SetMusic(m)
	c, err := api.NewLetGo("musicdrive")
	if err != nil {
		t.Fatal(err)
	}
	if err := letgo.LoadEmbedded(c); err != nil {
		t.Fatal(err)
	}
	c.SetLoadPath([]string{"../lg"})
	if _, err := c.Run("(require 'music)"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(`(def w {:seed-input 42 :depth 3 :fov #{}
                               :entities {:player {:id :player :body {:hp 20 :max-hp 20}}}})`); err != nil {
		t.Fatal(err)
	}

	read := func(tension string) engine.Params {
		if _, err := c.Run(fmt.Sprintf(`(undertone/set-parameters (music/params-for w %s))`, tension)); err != nil {
			t.Fatalf("tension %s: %v", tension, err)
		}
		return m.Params()
	}
	calm, tense := read("0.0"), read("1.0")

	if !(tense.BPM > calm.BPM) {
		t.Errorf("bpm %v -> %v; tension is defined to lift tempo", calm.BPM, tense.BPM)
	}
	if !(tense.PulseGain > calm.PulseGain) || calm.PulseGain != 0 {
		t.Errorf("pulse %v -> %v; the pulse track should be silent when calm and fade in", calm.PulseGain, tense.PulseGain)
	}
	if !(tense.PadDensity < calm.PadDensity) {
		t.Errorf("pad density %v -> %v; calm layers should thin out", calm.PadDensity, tense.PadDensity)
	}
	if tense.FifthOffset == calm.FifthOffset {
		t.Errorf("fifth offset stayed %d; the harmony should sour", calm.FifthOffset)
	}
	if calm.Scale == nil || calm.Root == 0 || math.IsNaN(calm.BPM) {
		t.Errorf("params look unpopulated: %+v", calm)
	}
}
