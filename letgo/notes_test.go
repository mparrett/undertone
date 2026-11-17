package letgo

import (
	"testing"
	"time"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

func TestNoteEventsFromLg(t *testing.T) {
	p, _ := engine.DeriveMood("forge", 7, 4, 1)
	m := NewMusic(p)
	SetMusic(m)
	c, err := api.NewLetGo("notestest")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := c.Run(`(undertone/note-events 0)`); err != nil || v != vm.NIL {
		t.Fatalf("note-events before music-start = %v, %v; want nil", v, err)
	}
	startRendering(t, m, gainTestOutput{}, rowsOf(p, 4))
	v, err := c.Run(`(let [r (undertone/note-events nil)
	                       evs (:events r)
	                       again (undertone/note-events (:next r))]
	                   [(= (:next r) (count evs)) (:dropped r) (:heard? r) (pos? (:now r))
	                    (every? #(contains? % :lane) (filter #(= :drums (:layer %)) evs))
	                    (not-any? #(contains? % :lane) (remove #(= :drums (:layer %)) evs))
	                    (boolean (some #(= [:drone :note 0 0] [(:layer %) (:kind %) (:row %) (:pattern %)]) evs))
	                    (count (:events again))])`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), "[true 0 false true true true true 0]"; got != want {
		t.Errorf("note-events = %s, want %s", got, want)
	}
	if _, err := c.Run(`(undertone/note-events "x")`); err == nil {
		t.Error("a string cursor was accepted")
	}
}

// With an output that reports its queue, :now is the frame at the speaker.
func TestNoteEventsNowIsHeard(t *testing.T) {
	p := engine.DeriveDungeon(1, 2, 0)
	m := NewMusic(p)
	startRendering(t, m, queuedTestOutput{queued: 100 * time.Millisecond}, engine.SampleRate)
	_, _, _, now, heard, ok := m.NoteEvents(0, nil)
	if want := int64(engine.SampleRate - engine.SampleRate/10); !ok || !heard || now != want {
		t.Errorf("now %d heard %v ok %v; want %d, heard", now, heard, ok, want)
	}
}
