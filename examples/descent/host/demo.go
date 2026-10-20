package host

import (
	"fmt"
	"math"
	"strings"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/letgo"
)

// The demo drivers (CLI and browser) differ only in where a turn's trace line
// goes and how the wait between turns happens. Everything that must not drift
// between them — which lg calls make a turn, where the tension accumulator
// lives — is here, once.

// DemoTurns returns the arc length. demo must already be loaded.
func DemoTurns(c *api.LetGo) (int, error) {
	return EvalInt(c, "(demo/turns)")
}

// DemoStep runs one turn of the arc: reads the target off the world, advances
// the envelope from prev, pushes parameters and the turn's one-shot triggers
// at the engine, and returns the new tension plus a formatted trace line.
//
// The tension accumulator lives in the CALLER, not in lg — the driver stands
// in for the browser audio module. lg supplies the pure step function
// (music/advance) and the host owns the one value that has to be remembered.
// That is the whole reason xsofy needs neither a world field nor a mutable
// global for tension: the memory belongs to the thing making the sound.
//
// Decay is applied once per parameter push, and pushes are turns, so the
// half-life is in game-clock units rather than seconds — idling does not
// relieve dread, taking turns does.
func DemoStep(c *api.LetGo, m *letgo.Music, i int, prev float64) (tension float64, line string, err error) {
	label, err := EvalString(c, fmt.Sprintf("(demo/label %d)", i))
	if err != nil {
		return prev, "", err
	}
	target, err := EvalFloat(c, fmt.Sprintf("(music/tension-target (demo/world-at %d))", i))
	if err != nil {
		return prev, "", err
	}
	// The envelope step is lg's, so the shape stays testable there.
	tension, err = EvalFloat(c, fmt.Sprintf("(music/advance %v %v music/default-decay)", prev, target))
	if err != nil {
		return prev, "", err
	}
	if _, err = c.Run(fmt.Sprintf(
		"(undertone/set-parameters (music/params-for (demo/world-at %d) %v))", i, tension)); err != nil {
		return tension, "", err
	}
	// The turn's one-shots ride the same boundary: lg derives the trigger
	// maps (sfx/for-events, pure), undertone/trigger fires them. With no synth
	// (a -dry run) they drop — the derivation still runs, which keeps the
	// dry demo an end-to-end check of the whole lg lane.
	if _, err = c.Run(fmt.Sprintf("(undertone/trigger (demo/triggers %d))", i)); err != nil {
		return tension, "", err
	}
	cue, err := EvalString(c, fmt.Sprintf("(demo/cue %d)", i))
	if err != nil {
		return tension, "", err
	}

	p := m.Params()
	line = fmt.Sprintf("%2d  %-24s target %s  tension %s  bpm %3.0f  pulse %.2f  %s",
		i+1, label, Meter(target), Meter(tension), p.BPM, p.PulseGain, cue)
	return tension, line, nil
}

// Meter renders a 0..1 value as a short bar, so the attack/release asymmetry
// is visible in the trace and not just audible.
func Meter(v float64) string {
	const w = 10
	full := int(math.Round(math.Min(math.Max(v, 0), 1) * w))
	return fmt.Sprintf("%s%s %.2f", strings.Repeat("#", full), strings.Repeat(".", w-full), v)
}

func EvalFloat(c *api.LetGo, expr string) (float64, error) {
	v, err := c.Run(expr)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", expr, err)
	}
	switch n := v.(type) {
	case vm.Float:
		return float64(n), nil
	case vm.Int:
		return float64(n), nil
	}
	return 0, fmt.Errorf("%s: got %T, want a number", expr, v)
}

func EvalInt(c *api.LetGo, expr string) (int, error) {
	f, err := EvalFloat(c, expr)
	return int(f), err
}

func EvalString(c *api.LetGo, expr string) (string, error) {
	v, err := c.Run(expr)
	if err != nil {
		return "", fmt.Errorf("%s: %w", expr, err)
	}
	s, ok := v.(vm.String)
	if !ok {
		return "", fmt.Errorf("%s: got %T, want a string", expr, v)
	}
	return string(s), nil
}
