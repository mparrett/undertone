package letgo

import (
	"time"

	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// queueReporter is an output that knows how much audio it holds ahead of
// the speaker: webaudio's. The native outputs don't say, so their position
// is the rendered one.
type queueReporter interface {
	Queued() time.Duration
}

// Position is where the music is, for a playhead. heard is true when p is
// the row the output is playing now, found by taking the output's queue off
// the frames rendered; otherwise p is the last row rendered, ahead of the
// speaker by the output's latency. ok is false while the music is stopped.
func (m *Music) Position() (p engine.Position, heard, ok bool) {
	m.mu.Lock()
	syn, out := m.synth, m.out
	m.mu.Unlock()
	if out == nil {
		return p, false, false
	}
	if q, isQ := out.(queueReporter); isQ {
		frame := syn.Frames() - int64(q.Queued())*engine.SampleRate/int64(time.Second)
		if p, ok = syn.PositionAt(frame); ok {
			return p, true, true
		}
	}
	p, ok = syn.Position()
	return p, false, ok
}

// position is undertone/position: the map for Position, or nil while the
// music is stopped.
func (m *Music) position() vm.Value {
	p, heard, ok := m.Position()
	if !ok {
		return vm.NIL
	}
	return vm.NewPersistentMap([]vm.Value{
		vm.Keyword("row"), vm.Int(p.Row),
		vm.Keyword("pattern"), vm.Int(p.Pattern),
		vm.Keyword("frame"), vm.Int(p.Frame),
		vm.Keyword("pending?"), vm.Boolean(p.Pending),
		vm.Keyword("theme-pending?"), vm.Boolean(p.ThemePending),
		vm.Keyword("heard?"), vm.Boolean(heard),
	})
}
