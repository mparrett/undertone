package letgo

import (
	"fmt"
	"math"
	"strings"

	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// moodsEDN is the built-in mood table as one EDN map, in the shape
// undertone.ambience's moods table has always had: entries carry no
// :palette, since the host picks a built-in mood's patches by its name.
func moodsEDN() string {
	var b strings.Builder
	b.WriteString("{")
	for _, name := range engine.MoodNames() {
		s, _ := engine.MoodByName(name)
		s.Palette = ""
		b.WriteString(s.EDN())
		b.WriteString("\n")
	}
	return b.String() + "}"
}

// deriveSpec derives parameters from a mood entry, the map shape of the
// moods table, through engine.MoodSpec: Go is the one derivation (#49). The
// result is a set-parameters map with :palette from the entry, or nil, so a
// palette-less entry leaves the host's patches alone.
func deriveSpec(a []vm.Value) (vm.Value, error) {
	m, ok := a[0].(*vm.PersistentMap)
	if !ok {
		return vm.NIL, fmt.Errorf("undertone/derive-spec: wants a mood map, got %s", a[0].Type().Name())
	}
	s, err := moodSpec(m)
	if err != nil {
		return vm.NIL, fmt.Errorf("undertone/derive-spec: %w", err)
	}
	seed, ok1 := a[1].(vm.Int)
	level, ok2 := a[2].(vm.Int)
	tension, ok3 := vm.ToFloat(a[3])
	if !ok1 || !ok2 || !ok3 {
		return vm.NIL, fmt.Errorf("undertone/derive-spec: wants a mood map, an integer seed and level, and a number for tension")
	}
	palette := vm.Value(vm.NIL)
	if s.Palette != "" {
		palette = vm.Keyword(s.Palette)
	} else {
		s.Palette = "dungeon" // only Params.Palette reads it, and the map does not carry that
	}
	p, err := s.Derive(uint64(seed), int(level), tension)
	if err != nil {
		return vm.NIL, fmt.Errorf("undertone/derive-spec: %w", err)
	}
	scale := make([]vm.Value, len(p.Scale))
	for i, d := range p.Scale {
		scale[i] = vm.Int(d)
	}
	groove := vm.Keyword("none")
	if p.Groove != "" {
		groove = vm.Keyword(p.Groove)
	}
	return vm.NewPersistentMap([]vm.Value{
		vm.Keyword("seed"), vm.Int(int64(p.Seed)),
		vm.Keyword("bpm"), vm.Float(p.BPM),
		vm.Keyword("root"), vm.Int(p.Root),
		vm.Keyword("scale"), vm.NewArrayVector(scale),
		vm.Keyword("drone-gain"), vm.Float(p.DroneGain),
		vm.Keyword("fifth-gain"), vm.Float(p.FifthGain),
		vm.Keyword("pad-density"), vm.Float(p.PadDensity),
		vm.Keyword("bell-density"), vm.Float(p.BellDensity),
		vm.Keyword("air-gain"), vm.Float(p.AirGain),
		vm.Keyword("pulse-gain"), vm.Float(p.PulseGain),
		vm.Keyword("grit"), vm.Float(p.Grit),
		vm.Keyword("open"), vm.Float(p.Open),
		vm.Keyword("fifth-offset"), vm.Int(p.FifthOffset),
		vm.Keyword("bell-lift"), vm.Int(p.BellLift),
		vm.Keyword("lead"), vm.Keyword(p.LeadName),
		vm.Keyword("groove"), groove,
		vm.Keyword("drum-gain"), vm.Float(p.DrumGain),
		vm.Keyword("drum-busy"), vm.Float(p.DrumBusy),
		vm.Keyword("swing"), vm.Float(p.Swing),
		vm.Keyword("palette"), palette,
	}), nil
}

// moodSpec reads a mood entry. It is strict, unlike set-parameters: an
// entry is authored data, so a misspelt key is a bug to report, not a field
// to leave at zero.
func moodSpec(m *vm.PersistentMap) (engine.MoodSpec, error) {
	var s engine.MoodSpec
	known := map[string]bool{"palette": true, "leads": true, "grooves": true, "scales": true}
	ints := []struct {
		k   string
		dst *int
	}{{"root-base", &s.RootBase}, {"root-step", &s.RootStep}, {"calm", &s.Calm}, {"bell-lift", &s.BellLift}}
	for _, f := range ints {
		known[f.k] = true
		if !integer(m, f.k, f.dst) {
			return s, fmt.Errorf(":%s must be an integer", f.k)
		}
	}
	lines := []struct {
		k        string
		dst      *engine.Line
		required bool
	}{
		{"bpm", &s.BPM, true}, {"drone", &s.Drone, true}, {"fifth", &s.Fifth, true},
		{"pad", &s.Pad, true}, {"bell", &s.Bell, true}, {"air", &s.Air, true},
		{"drums", &s.Drums, false}, {"busy", &s.Busy, false}, {"open", &s.Open, false},
	}
	for _, f := range lines {
		known[f.k] = true
		// An absent line is the zero line, unclamped, as MoodByName exports it.
		*f.dst = engine.Line{Lo: math.Inf(-1), Hi: math.Inf(1)}
		v := at(m, f.k)
		if v == vm.NIL && !f.required {
			continue
		}
		l, ok := v.(*vm.PersistentMap)
		if !ok {
			return s, fmt.Errorf(":%s must be a line, {:base b :step s}", f.k)
		}
		if !flt(l, "base", &f.dst.Base) || !flt(l, "step", &f.dst.Step) {
			return s, fmt.Errorf(":%s needs a :base and a :step", f.k)
		}
		for _, k := range []string{"lo", "hi"} {
			dst := &f.dst.Lo
			if k == "hi" {
				dst = &f.dst.Hi
			}
			if l.Contains(vm.Keyword(k)) == vm.TRUE && !flt(l, k, dst) {
				return s, fmt.Errorf(":%s's :%s must be a number", f.k, k)
			}
		}
		if err := onlyKeys(l, map[string]bool{"base": true, "step": true, "lo": true, "hi": true}); err != nil {
			return s, fmt.Errorf(":%s: %w", f.k, err)
		}
	}
	for _, f := range []struct {
		k   string
		dst *float64
	}{{"swing", &s.Swing}, {"tense-drums", &s.TenseDrums}} {
		known[f.k] = true
		if at(m, f.k) != vm.NIL && !flt(m, f.k, f.dst) {
			return s, fmt.Errorf(":%s must be a number", f.k)
		}
	}
	if v := at(m, "palette"); v != vm.NIL {
		name, ok := kwStr(m, "palette")
		if !ok {
			return s, fmt.Errorf(":palette must be a name")
		}
		s.Palette = name
	}
	var err error
	if s.Leads, err = names(m, "leads"); err != nil {
		return s, err
	}
	if at(m, "grooves") != vm.NIL {
		if s.Grooves, err = names(m, "grooves"); err != nil {
			return s, err
		}
	}
	scales, ok := at(m, "scales").(vm.ArrayVector)
	if !ok {
		return s, fmt.Errorf(":scales must be a vector of scales")
	}
	for _, e := range scales {
		sc, ok := ints1(e)
		if !ok {
			return s, fmt.Errorf(":scales must be a vector of vectors of integers")
		}
		s.Scales = append(s.Scales, sc)
	}
	return s, onlyKeys(m, known)
}

// onlyKeys reports the first key of m that known does not hold.
func onlyKeys(m *vm.PersistentMap, known map[string]bool) error {
	for seq := m.Seq(); seq != nil && seq != vm.EmptyList; seq = seq.Next() {
		e, _ := seq.First().(vm.MapEntry)
		if k, _ := e.Key.(vm.Keyword); !known[string(k)] {
			return fmt.Errorf("unknown key %v", e.Key)
		}
	}
	return nil
}

// names reads a vector of keywords or strings.
func names(m *vm.PersistentMap, k string) ([]string, error) {
	vec, ok := at(m, k).(vm.ArrayVector)
	if !ok {
		return nil, fmt.Errorf(":%s must be a vector of names", k)
	}
	out := make([]string, len(vec))
	for i, e := range vec {
		switch v := e.(type) {
		case vm.Keyword:
			out[i] = string(v)
		case vm.String:
			out[i] = string(v)
		default:
			return nil, fmt.Errorf(":%s must be a vector of names", k)
		}
	}
	return out, nil
}

func ints1(v vm.Value) ([]int, bool) {
	vec, ok := v.(vm.ArrayVector)
	if !ok {
		return nil, false
	}
	out := make([]int, len(vec))
	for i, e := range vec {
		n, ok := e.(vm.Int)
		if !ok {
			return nil, false
		}
		out[i] = int(n)
	}
	return out, true
}
