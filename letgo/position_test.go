package letgo

import (
	"io"
	"testing"
	"time"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// queuedTestOutput reports a fixed queue, as webaudio reports the audio it
// holds ahead of the speaker.
type queuedTestOutput struct {
	gainTestOutput
	queued time.Duration
}

func (o queuedTestOutput) Queued() time.Duration { return o.queued }

// rowsOf is the frames in n rows of p.
func rowsOf(p engine.Params, n int) int {
	return n * int(engine.SampleRate*60/(p.BPM*2))
}

// startRendering starts m on out and renders frames through it, as the
// output's first reads would.
func startRendering(t *testing.T, m *Music, out output, frames int) {
	t.Helper()
	m.start(func(src io.Reader) (output, error) {
		if _, err := io.ReadFull(src, make([]byte, 4*frames)); err != nil {
			t.Fatal(err)
		}
		return out, nil
	})
	t.Cleanup(func() { m.Stop() })
}

func TestPositionIsTheRenderedRowWithoutAQueue(t *testing.T) {
	p := engine.DeriveDungeon(1, 1, 0)
	m := NewMusic(p)
	if _, _, ok := m.Position(); ok {
		t.Fatal("Position before music-start: ok, want not")
	}
	startRendering(t, m, gainTestOutput{}, rowsOf(p, 5)+1)
	got, heard, ok := m.Position()
	if !ok || heard || got.Row != 5 {
		t.Errorf("Position = %+v heard %v ok %v; want row 5, not heard", got, heard, ok)
	}
	m.Stop()
	if _, _, ok := m.Position(); ok {
		t.Error("Position after music-stop: ok, want not")
	}
}

// With an output that reports its queue, the position is the row playing
// at the speaker: the queue's worth of frames behind the rendered one.
func TestPositionIsTheHeardRowWithAQueue(t *testing.T) {
	p := engine.DeriveDungeon(1, 1, 0)
	m := NewMusic(p)
	row := time.Duration(rowsOf(p, 1)) * time.Second / engine.SampleRate
	startRendering(t, m, queuedTestOutput{queued: 3*row + row/2}, rowsOf(p, 10)+1)
	got, heard, ok := m.Position()
	if !ok || !heard || got.Row != 6 {
		t.Errorf("Position = %+v heard %v ok %v; want row 6, heard", got, heard, ok)
	}
}

func TestPositionFromLg(t *testing.T) {
	p := engine.DeriveDungeon(1, 1, 0)
	m := NewMusic(p)
	SetMusic(m)
	c, err := api.NewLetGo("positiontest")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := c.Run(`(undertone/position)`); err != nil || v != vm.NIL {
		t.Fatalf("position before music-start = %v, %v; want nil", v, err)
	}
	startRendering(t, m, gainTestOutput{}, rowsOf(p, 17)+1)
	v, err := c.Run(`(let [p (undertone/position)]
	                   [(:row p) (:pattern p) (:pending? p) (:theme-pending? p) (:heard? p) (pos? (:frame p))])`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), "[1 1 false false false true]"; got != want {
		t.Errorf("position = %s, want %s", got, want)
	}
	if v, _ := c.Run(`(do (undertone/set-parameters {:bpm 70}) (:pending? (undertone/position)))`); v != vm.TRUE {
		t.Errorf("pending? after set-parameters = %v, want true", v)
	}
}

// A change made while the output opened still lands, and shows as pending
// until it does; with none, nothing is pending after music-start.
func TestPositionPendingAfterStart(t *testing.T) {
	for _, moved := range []bool{false, true} {
		p := engine.DeriveDungeon(1, 1, 0)
		m := NewMusic(p)
		m.start(func(src io.Reader) (output, error) {
			if moved {
				m.SetParams(engine.DeriveDungeon(2, 1, 0))
			}
			io.ReadFull(src, make([]byte, 4*(rowsOf(p, 2)+1)))
			return gainTestOutput{}, nil
		})
		got, _, ok := m.Position()
		m.Stop()
		if !ok || got.Pending != moved {
			t.Errorf("params moved during the wait %v: pending %v, want %v", moved, got.Pending, moved)
		}
	}
}
