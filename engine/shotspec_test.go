package engine

import (
	"bytes"
	"math"
	"reflect"
	"strings"
	"testing"
)

// Every built-in shot taken through ShotSpec builds the same patch, and
// registered under another name it plays the same samples.
func TestShotSpecRoundTrip(t *testing.T) {
	for name, d := range shotLib {
		s, ok := ShotByName(name)
		if !ok {
			t.Fatalf("%s: no spec", name)
		}
		s.Name = "copy-of-" + name
		p, err := s.patch()
		if err != nil || !reflect.DeepEqual(p, d.patch) || s.GateMS != d.gateMS {
			t.Fatalf("%s: the spec builds a different patch (%v)", name, err)
		}
		if err := RegisterShot(s); err != nil {
			t.Fatal(err)
		}
		a, b := NewSynth(silentParams), NewSynth(silentParams)
		a.Trigger(name, 52, 1, 0.3, 0, 9)
		b.Trigger(s.Name, 52, 1, 0.3, 0, 9)
		if !bytes.Equal(render(a, SampleRate/2), render(b, SampleRate/2)) {
			t.Errorf("%s: the registered copy sounds different", name)
		}
	}
}

// A spec the synth could not run safely is refused with a reason, and
// nothing is registered.
func TestRegisterShotRefusesUnsafePatches(t *testing.T) {
	env := UnitSpec{Op: "env", Args: []float64{0.001, 0.1, 0, 0.05}}
	osc := UnitSpec{Op: "osc", Shape: "sine", Args: []float64{0, 0, 1}}
	mul, out := UnitSpec{Op: "mul"}, UnitSpec{Op: "out", Args: []float64{0.5, 0.5}}
	ok := []UnitSpec{env, osc, mul, out}
	if err := RegisterShot(ShotSpec{Name: "beep", GateMS: 10, Units: ok}); err != nil {
		t.Fatalf("a plain patch was refused: %v", err)
	}
	with := func(i int, u UnitSpec) []UnitSpec {
		us := append([]UnitSpec(nil), ok...)
		us[i] = u
		return us
	}
	nine := []UnitSpec{env}
	for i := 0; i < 8; i++ {
		nine = append(nine, osc)
	}
	for _, c := range []struct {
		why  string
		spec ShotSpec
		says string
	}{
		{"a built-in's name", ShotSpec{Name: "hit", Units: ok}, "built-in"},
		{"no name", ShotSpec{Units: ok}, "name"},
		{"a name EDN cannot read back", ShotSpec{Name: "big hit", GateMS: 10, Units: ok}, "name"},
		{"a release of a minute", ShotSpec{Name: "x", GateMS: 10, Units: with(0, UnitSpec{Op: "env", Args: []float64{0, 0.1, 0, 60}})}, "release"},
		{"a pluck four octaves down", ShotSpec{Name: "x", GateMS: 10, Units: with(1, UnitSpec{Op: "pluck", Args: []float64{-48, 0.9}})}, "transpose"},
		{"an unknown op", ShotSpec{Name: "x", GateMS: 10, Units: with(2, UnitSpec{Op: "add"})}, "unknown op"},
		{"an unknown shape", ShotSpec{Name: "x", GateMS: 10, Units: with(1, UnitSpec{Op: "osc", Shape: "square", Args: []float64{0, 0, 1}})}, "shape"},
		{"too few args", ShotSpec{Name: "x", GateMS: 10, Units: with(3, UnitSpec{Op: "out", Args: []float64{0.5}})}, "args"},
		{"a zero decay", ShotSpec{Name: "x", GateMS: 10, Units: with(0, UnitSpec{Op: "env", Args: []float64{0, 0, 0, 0.05}})}, "decay"},
		{"a NaN", ShotSpec{Name: "x", GateMS: 10, Units: with(3, UnitSpec{Op: "out", Args: []float64{0.5, nan()}})}, "finite"},
		{"a pluck that rings forever", ShotSpec{Name: "x", GateMS: 10, Units: with(1, UnitSpec{Op: "pluck", Args: []float64{0, 1}})}, "feedback"},
		{"a mul on one value", ShotSpec{Name: "x", GateMS: 10, Units: []UnitSpec{env, mul, out}}, "run dry"},
		{"nine on the stack", ShotSpec{Name: "x", GateMS: 10, Units: nine}, "overflow"},
		{"no out", ShotSpec{Name: "x", GateMS: 10, Units: []UnitSpec{env}}, "out"},
		{"a gate that never closes", ShotSpec{Name: "x", GateMS: 0, Units: ok}, "gate"},
		{"DC on the signal", ShotSpec{Name: "x", GateMS: 10, Units: []UnitSpec{env, osc, mul, {Op: "scale", Args: []float64{1, 0.7}}, out}}, "DC"},
		{"a release of hours", ShotSpec{Name: "x", GateMS: 10, Units: with(0, UnitSpec{Op: "env", Args: []float64{0, 0.1, 0, 3600}})}, "release"},
	} {
		err := RegisterShot(c.spec)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: got %v, want an error saying %q", c.why, err, c.says)
		}
	}
	if _, ok := lookupShot("x"); ok {
		t.Error("a refused spec was registered")
	}
	if !NewSynth(silentParams).Trigger("beep", 60, 1, 0.5, 0, 1) {
		t.Error("a registered shot cannot be triggered")
	}
}

func nan() float64 { var z float64; return z / z }

func TestShotSpecEDN(t *testing.T) {
	s, _ := ShotByName("hit")
	want := "{:name :hit :gate-ms 25.0\n :patch [[:env 0.001 0.11 0.0 0.05]\n         [:osc 0.0 :sine 26.0 0.03]"
	if e := s.EDN(); !strings.HasPrefix(e, want) {
		t.Errorf("EDN:\n%s\nwant it to start:\n%s", e, want)
	}
}

// Whatever RegisterShot accepts, the synth runs: random patches that pass
// validation render without a panic and stay finite.
func TestAcceptedShotsAreSafe(t *testing.T) {
	r := rng{12345}
	pick := func(xs []float64) float64 { return xs[r.n(len(xs))] }
	vals := []float64{-60, -48, -1, 0, 1e-9, 0.001, 0.3, 0.99, 1, 2, 30, 900, 22050, 30000}
	usual := []float64{1e-9, 0.001, 0.3, 0.5, 0.99, 1}
	arg := func() float64 {
		if r.n(10) == 0 {
			return pick(vals)
		}
		return pick(usual)
	}
	accepted, swept, gritty, bent := 0, 0, 0, 0
	for i := 0; i < 10000; i++ {
		// patch-shaped chains (env, source, mul, filters, out) with random
		// parameters, so that some pass and the edges of each range are hit
		unit := func(op string) UnitSpec {
			k := opArgs[indexOf(opNames, op)]
			u := UnitSpec{Op: op, Shape: shapeNames[r.n(len(shapeNames))]}
			for a := 0; a < k; a++ {
				u.Args = append(u.Args, arg())
			}
			return u
		}
		var units []UnitSpec
		for c := 0; c < 1+r.n(3); c++ {
			if r.n(3) == 0 {
				// vibrato: an lfo bending the oscs after it
				l, pt := unit("lfo"), unit("pitch")
				l.Args[1] = float64(r.n(3))
				pt.Args[0] = pick([]float64{-13, -12, 0.3, 2, 12, 13})
				units = append(units, l, pt)
			}
			units = append(units, unit("env"), unit([]string{"osc", "pluck"}[r.n(2)]), unit("mul"))
			for _, op := range []string{"lpf", "hpf", "send", "verb"} {
				if r.n(2) == 0 {
					units = append(units, unit(op))
				}
			}
			if r.n(3) == 0 {
				// tremolo: an lfo scaled into a range and multiplied in
				l, sc := unit("lfo"), unit("scale")
				l.Args[1] = float64(r.n(3))
				sc.Args = []float64{pick([]float64{-3, -2, 0.3, 1, 2}), pick([]float64{-2, 0, 0.7, 2, 3})}
				units = append(units, l, sc, unit("mul"))
			}
			if r.n(2) == 0 {
				// grit: drive and crush, at the edges of their ranges
				d, c := unit("drive"), unit("crush")
				d.Args = []float64{pick([]float64{0.05, 0.1, 1, 20, 21}), pick([]float64{-1.5, -1, 0, 0.3, 1})}
				c.Args = []float64{pick([]float64{40, 50, 900, 22050, 44100, 50000}), pick([]float64{0, 1, 1.5, 8, 16, 17})}
				units = append(units, d, c)
			}
			if r.n(2) == 0 {
				// a resonant filter, swept by an lfo or an env half the time
				f := unit("svf")
				f.Args[1], f.Args[2] = pick([]float64{0, 0.5, 0.95, 1, 1.2}), float64(r.n(4))
				f.Args[3] = pick([]float64{0, 0, -8, -2, 3, 8, 9})
				if f.Args[3] != 0 {
					m := unit([]string{"lfo", "env"}[r.n(2)])
					if m.Op == "lfo" {
						m.Args[1] = float64(r.n(4))
					}
					units = append(units, m)
				}
				units = append(units, f)
			}
			units = append(units, unit("out"))
		}
		s := ShotSpec{Name: "fuzz", GateMS: pick(vals), Units: units}
		if RegisterShot(s) != nil {
			continue
		}
		accepted++
		for _, u := range units {
			if u.Op == "svf" && u.Args[3] != 0 {
				swept++
				break
			}
		}
		for _, u := range units {
			if u.Op == "crush" {
				gritty++
				break
			}
		}
		for _, u := range units {
			if u.Op == "pitch" {
				bent++
				break
			}
		}
		q := silentParams
		q.Grit = float64(i % 2) // grit at both ends
		syn := NewSynth(q)
		syn.Trigger("fuzz", 20+r.n(90), 1, 0.5, 0, uint64(i))
		for range 4 {
			var l, rr float64
			for f := 0; f < SampleRate/20; f++ {
				syn.runShots(&l, &rr)
				// the sends too: a shot's verb must not break the room
				vl, _ := syn.verb.process(syn.bus.verb)
				syn.bus = sends{}
				l += vl
			}
			if math.IsNaN(l) || math.IsInf(l, 0) {
				t.Fatalf("an accepted patch rendered %v: %+v", l, units)
			}
		}
	}
	if accepted < 50 || swept < 10 || gritty < 10 || bent < 10 {
		t.Fatalf("only %d of 10000 random patches were accepted, %d with a swept filter, %d with drive and crush, %d bent; the test proves little", accepted, swept, gritty, bent)
	}
	t.Logf("%d random patches accepted and rendered, %d with a swept filter, %d with drive and crush, %d bent", accepted, swept, gritty, bent)
}
