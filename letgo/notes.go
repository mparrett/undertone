package letgo

import (
	"time"

	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// NoteEvents passes engine.Synth.NoteEvents through: the notes started
// after cursor since, the cursor to pass next, and how many were missed.
// now is the frame at the speaker when the output reports its queue
// (heard), else the last frame rendered, on the same clock as the events'
// frames. ok is false while stopped. A Stop and Start replaces the synth;
// a cursor from the old one reads the new one from its first event.
func (m *Music) NoteEvents(since uint64, dst []engine.NoteEvent) (evs []engine.NoteEvent, next, dropped uint64, now int64, heard, ok bool) {
	syn, _ := m.playing()
	if syn == nil {
		return dst, since, 0, 0, false, false
	}
	evs, next, dropped = syn.NoteEvents(since, dst)
	now = syn.Frames()
	if q, isQ := m.queued(); isQ {
		now -= int64(q) * engine.SampleRate / int64(time.Second)
		heard = true
	}
	return evs, next, dropped, now, heard, true
}

// noteEvents is undertone/note-events: {:next :dropped :now :heard?
// :events [...]} for the events after cursor, or nil while stopped.
func (m *Music) noteEvents(cursor vm.Value) (vm.Value, error) {
	var since uint64
	if cursor != vm.NIL {
		c, err := numArg("note-events", "cursor", cursor)
		if err != nil {
			return vm.NIL, err
		}
		since = uint64(max(0, c))
	}
	evs, next, dropped, now, heard, ok := m.NoteEvents(since, nil)
	if !ok {
		return vm.NIL, nil
	}
	layers, lanes := engine.LayerNames(), engine.LaneNames()
	out := make([]vm.Value, len(evs))
	for i, e := range evs {
		kv := []vm.Value{
			vm.Keyword("layer"), vm.Keyword(layers[e.Layer]),
			vm.Keyword("kind"), vm.Keyword(e.Kind.String()),
			vm.Keyword("note"), vm.Int(e.Note),
			vm.Keyword("gain"), vm.Float(e.Gain),
			vm.Keyword("row"), vm.Int(e.Row),
			vm.Keyword("pattern"), vm.Int(e.Pattern),
			vm.Keyword("frame"), vm.Int(e.Frame),
		}
		if layers[e.Layer] == "drums" {
			kv = append(kv, vm.Keyword("lane"), vm.Keyword(lanes[e.Lane]))
		}
		out[i] = vm.NewPersistentMap(kv)
	}
	return vm.NewPersistentMap([]vm.Value{
		vm.Keyword("next"), vm.Int(next),
		vm.Keyword("dropped"), vm.Int(dropped),
		vm.Keyword("now"), vm.Int(now),
		vm.Keyword("heard?"), vm.Boolean(heard),
		vm.Keyword("events"), vm.NewArrayVector(out),
	}), nil
}
