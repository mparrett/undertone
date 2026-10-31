package letgo

import (
	"io"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// queueTestOutput records SetQueue calls, flooring to whole frames and
// clamping to max as webaudio does to its ring.
type queueTestOutput struct {
	gainTestOutput
	max time.Duration
	got []time.Duration
}

func (o *queueTestOutput) SetQueue(d time.Duration) time.Duration {
	f := int64(min(d, o.max)) * engine.SampleRate / int64(time.Second)
	d = time.Duration(f) * time.Second / engine.SampleRate
	o.got = append(o.got, d)
	return d
}

// stderrOf returns what f writes to os.Stderr.
func stderrOf(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	f()
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
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
	if len(out.got) != 1 || out.got[0] != 90*time.Millisecond {
		t.Fatalf("SetQueue calls %v, want [90ms]", out.got)
	}
}

func TestSetQueueReportsAClampButNotFrameRounding(t *testing.T) {
	m := NewMusic(engine.DeriveDungeon(7, 3, 0.5))
	out := &queueTestOutput{max: time.Second}
	m.start(func(io.Reader) (output, error) { return out, nil })
	defer m.Stop()
	// 15 ms is 661.5 frames at 44.1 kHz, floored to 661.
	if s := stderrOf(t, func() { m.setQueue(15 * time.Millisecond) }); s != "" {
		t.Errorf("frame rounding reported as a clamp: %q", s)
	}
	if s := stderrOf(t, func() { m.setQueue(5 * time.Second) }); !strings.Contains(s, "clamped to 1s") {
		t.Errorf("a clamp to the ring was not reported: %q", s)
	}
}

func TestSetQueueOnAFixedOutputIsKeptAndIgnored(t *testing.T) {
	m := NewMusic(engine.DeriveDungeon(7, 3, 0.5))
	m.start(func(io.Reader) (output, error) { return gainTestOutput{}, nil })
	defer m.Stop()
	if s := stderrOf(t, func() { m.setQueue(90 * time.Millisecond) }); s != "" {
		t.Errorf("a native output reported something: %q", s)
	}
	if m.queue != 90*time.Millisecond {
		t.Errorf("queue %v not kept for a later output", m.queue)
	}
}

func TestQueueArg(t *testing.T) {
	for _, c := range []struct {
		in   vm.Value
		want time.Duration
	}{
		{vm.Int(120), 120 * time.Millisecond},
		{vm.Float(90.5), 90500 * time.Microsecond},
		{vm.Float(math.Inf(1)), time.Minute},
		{vm.Int(1e9), time.Minute},
	} {
		if got, err := queueArg(c.in); err != nil || got != c.want {
			t.Errorf("queueArg(%v) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, in := range []vm.Value{vm.Int(0), vm.Int(-5), vm.Float(math.NaN()), vm.String("fast"), vm.NIL} {
		if _, err := queueArg(in); err == nil {
			t.Errorf("queueArg(%v) accepted", in)
		}
	}
}
