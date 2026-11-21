package letgo

import (
	"strings"
	"testing"

	"github.com/mparrett/undertone/engine"
)

// A built-in mood's entry carries its :phrasing and :memory, derive-spec
// hands them to set-parameters as the mood's, and they play only once the
// game says :phrasing true or :memory true. The game's switches outlast a
// mood change, and a mood with none clears the last one's.
func TestTemperamentFromLg(t *testing.T) {
	c, m := lgRuntime(t)
	m.params = engine.DeriveDungeon(1, 0, 0)
	if _, err := c.Run("(ambience/score-mood! :rust 7 2 0.3)"); err != nil {
		t.Fatal(err)
	}
	tm, _ := engine.TemperamentFor("rust")
	p := m.params
	if p.MoodPhrasing == nil || p.MoodMemory == nil || *p.MoodMemory != tm.Memory || p.MoodPhrasing.BreakChance != tm.Phrasing.BreakChance {
		t.Fatalf("rust's temperament did not arrive: %+v %+v", p.MoodPhrasing, p.MoodMemory)
	}
	if p.NoPhrasing || p.NoMemory {
		t.Fatal("a mood turned its own temperament off")
	}
	if _, err := c.Run("(undertone/set-parameters {:phrasing false :memory false})"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run("(ambience/score-mood! :dungeon 7 2 0.3)"); err != nil {
		t.Fatal(err)
	}
	dt, _ := engine.TemperamentFor("dungeon")
	if p := m.params; !p.NoPhrasing || !p.NoMemory || p.MoodMemory == nil || *p.MoodMemory != dt.Memory {
		t.Fatalf("a mood change lost the switches or kept rust's memory: %v %v %+v", p.NoPhrasing, p.NoMemory, p.MoodMemory)
	}
	if _, err := c.Run("(undertone/set-parameters {:memory true})"); err != nil {
		t.Fatal(err)
	}
	if p := m.params; !p.NoPhrasing || p.NoMemory {
		t.Fatalf(":memory true: phrasing off %v memory off %v", p.NoPhrasing, p.NoMemory)
	}
	// an entry without them, as every entry before this was
	if _, err := c.Run("(ambience/score-spec! (dissoc (:village ambience/moods) :phrasing :memory) 7 2 0.3)"); err != nil {
		t.Fatal(err)
	}
	if p := m.params; p.MoodPhrasing != nil || p.MoodMemory != nil || !p.NoPhrasing {
		t.Fatalf("a mood with none kept the last one's, or lost the switch: %+v %+v %v", p.MoodPhrasing, p.MoodMemory, p.NoPhrasing)
	}
	// :mood brings its temperament with its palette, which plays by default;
	// an explicit :mood-memory in the same map wins
	if _, err := c.Run("(undertone/set-parameters {:mood :forge})"); err != nil {
		t.Fatal(err)
	}
	ft, _ := engine.TemperamentFor("forge")
	if p := m.params; p.MoodMemory == nil || *p.MoodMemory != ft.Memory || p.MoodPhrasing == nil || p.MoodPhrasing.BreakChance != ft.Phrasing.BreakChance {
		t.Fatalf(":mood :forge kept another mood's temperament: %+v", p.MoodMemory)
	}
	if _, err := c.Run("(undertone/set-parameters {:mood :forge :mood-memory {:restlessness 0.1}})"); err != nil {
		t.Fatal(err)
	}
	if got, want := m.params.MoodMemory, engine.NewMemory(0.1); got == nil || *got != want {
		t.Fatalf("an explicit :mood-memory lost to :mood: %+v", got)
	}
	// a partial memory takes the defaults for the rest
	if _, err := c.Run("(ambience/score-spec! (assoc (:village ambience/moods) :memory {:restlessness 0.5 :leap 0.9}) 7 2 0.3)"); err != nil {
		t.Fatal(err)
	}
	want := engine.NewMemory(0.5)
	want.Leap = 0.9
	if got := m.params.MoodMemory; got == nil || *got != want {
		t.Fatalf("a partial :memory read as %+v, want %+v", got, want)
	}
}

// A temperament is authored data, so a slip in one is an error; and a
// switch that isn't a boolean is one too, with nothing left behind.
func TestTemperamentRejectsBadInput(t *testing.T) {
	c, m := lgRuntime(t)
	for _, bad := range []struct{ code, want string }{
		{`(assoc (:rust ambience/moods) :memory {:restless 0.5})`, "unknown key"},
		{`(assoc (:rust ambience/moods) :memory {:leap "lots"})`, ":leap must be a number"},
		{`(assoc-in (:rust ambience/moods) [:phrasing :drop] [:leed])`, "unknown layer"},
		{`(assoc-in (:rust ambience/moods) [:phrasing :shade :hlod] {:open 1})`, "unknown key"},
		{`(assoc-in (:rust ambience/moods) [:phrasing :shade :hold :lift] 0.5)`, ":lift must be an integer"},
		{`(update (:rust ambience/moods) :phrasing dissoc :phrase-max)`, ":phrase-max must be integers"},
	} {
		_, err := c.Run("(ambience/derive-spec " + bad.code + " 7 2 0.3)")
		if err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%s: err %v, want %q", bad.code, err, bad.want)
		}
	}
	m.params = engine.DeriveDungeon(1, 0, 0)
	before := m.params
	_, err := c.Run("(undertone/set-parameters {:bpm 140 :phrasing 1})")
	if err == nil || !strings.Contains(err.Error(), ":phrasing must be true or false") {
		t.Fatalf(":phrasing 1: err %v", err)
	}
	if m.params.BPM != before.BPM || m.params.NoPhrasing {
		t.Fatal("a rejected map left changes behind")
	}
}
