package letgo

import (
	"io"
	"testing"
	"time"

	"github.com/mparrett/undertone/engine"
)

// queueTestOutput records SetQueue calls and clamps to max, as webaudio
// clamps to its ring.
type queueTestOutput struct {
	gainTestOutput
	max time.Duration
	got []time.Duration
}

func (o *queueTestOutput) SetQueue(d time.Duration) time.Duration {
	d = min(d, o.max)
	o.got = append(o.got, d)
	return d
}

func TestSetQueueBeforeStartAppliesAtStart(t *testing.T) {
	m := NewMusic(engine.DeriveDungeon(7, 3, 0.5))
	m.setQueue(150 * time.Millisecond)
	out := &queueTestOutput{max: time.Second}
	m.start(func(io.Reader) (output, error) { return out, nil })
	defer m.Stop()
	if len(out.got) != 1 || out.got[0] != 150*time.Millisecond {
		t.Fatalf("SetQueue calls %v, want [150ms]", out.got)
	}
}

func TestSetQueueWhilePlayingAppliesAtOnce(t *testing.T) {
	m := NewMusic(engine.DeriveDungeon(7, 3, 0.5))
	out := &queueTestOutput{max: time.Second}
	m.start(func(io.Reader) (output, error) { return out, nil })
	defer m.Stop()
	if len(out.got) != 0 {
		t.Fatalf("an unset queue reached the output: %v", out.got)
	}
	m.setQueue(90 * time.Millisecond)
	m.setQueue(5 * time.Second) // clamped by the output, reported on stderr
	if len(out.got) != 2 || out.got[0] != 90*time.Millisecond || out.got[1] != time.Second {
		t.Fatalf("SetQueue calls %v, want [90ms 1s]", out.got)
	}
}

func TestSetQueueIsANoOpOnAFixedOutput(t *testing.T) {
	m := NewMusic(engine.DeriveDungeon(7, 3, 0.5))
	m.start(func(io.Reader) (output, error) { return gainTestOutput{}, nil })
	defer m.Stop()
	m.setQueue(90 * time.Millisecond) // must not panic on an output without SetQueue
}
