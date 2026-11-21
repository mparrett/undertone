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
	groove, lead := vm.Keyword("none"), vm.Keyword("none")
	if p.Groove != "" {
		groove = vm.Keyword(p.Groove)
	}
	if p.LeadName != "" {
		lead = vm.Keyword(p.LeadName)
	}
	return vm.NewPersistentMap([]vm.Value{
		vm.Keyword("seed"), vm.Int(int64(p.Seed)),
		vm.Keyword("bpm"), vm.Float(p.BPM),
		vm.Keyword("root"), vm.Int(p.Root),
		vm.Keyword("scale"), vm.NewArrayVector(scale),
		vm.Keyword("drone-gain"), vm.Float(p.DroneGain),
		vm.Keyword("fifth-gain"), vm.Float(p.FifthGain),
		vm.Keyword("pad-density"), vm.Float(p.PadDensity),
		vm.Keyword("lead-density"), vm.Float(p.LeadDensity),
		vm.Keyword("air-gain"), vm.Float(p.AirGain),
		vm.Keyword("pulse-gain"), vm.Float(p.PulseGain),
		vm.Keyword("grit"), vm.Float(p.Grit),
		vm.Keyword("open"), vm.Float(p.Open),
		vm.Keyword("room-size"), vm.Float(p.Room.Size),
		vm.Keyword("room-damp"), vm.Float(p.Room.Damp),
		vm.Keyword("fifth-offset"), vm.Int(p.FifthOffset),
		vm.Keyword("lead-lift"), vm.Int(p.LeadLift),
		vm.Keyword("lead"), lead,
		vm.Keyword("groove"), groove,
		vm.Keyword("drum-gain"), vm.Float(p.DrumGain),
		vm.Keyword("drum-busy"), vm.Float(p.DrumBusy),
		vm.Keyword("swing"), vm.Float(p.Swing),
		// always present, so a mood with no trims clears the last mood's
		vm.Keyword("mood-lead-db"), vm.Float(p.MoodLeadDB),
		vm.Keyword("mood-pad-db"), vm.Float(p.MoodPadDB),
		// the entry's own, read above, so set-parameters reads what moodSpec
		// checked; nil for a mood with none, which clears the last mood's
		vm.Keyword("mood-phrasing"), at(m, "phrasing"),
		vm.Keyword("mood-memory"), at(m, "memory"),
		vm.Keyword("palette"), palette,
	}), nil
}

// moodSpec reads a mood entry. It is strict, unlike set-parameters: an
// entry is authored data, so a misspelt key is a bug to report, not a field
// to leave at zero.
func moodSpec(m *vm.PersistentMap) (engine.MoodSpec, error) {
	var s engine.MoodSpec
	// first, or an old entry would fail on the missing :lead-density instead
	if err := retiredKey(m, "bell", "bell-lift"); err != nil {
		return s, err
	}
	known := map[string]bool{"palette": true, "leads": true, "grooves": true, "scales": true, "room": true,
		"phrasing": true, "memory": true}
	ints := []struct {
		k   string
		dst *int
	}{{"root-base", &s.RootBase}, {"root-step", &s.RootStep}, {"calm", &s.Calm}, {"lead-lift", &s.LeadLift}}
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
		{"pad", &s.Pad, true}, {"lead-density", &s.LeadDensity, true}, {"air", &s.Air, true},
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
	}{{"swing", &s.Swing}, {"tense-drums", &s.TenseDrums}, {"lead-db", &s.LeadDB}, {"pad-db", &s.PadDB}} {
		known[f.k] = true
		if at(m, f.k) != vm.NIL && !flt(m, f.k, f.dst) {
			return s, fmt.Errorf(":%s must be a number", f.k)
		}
	}
	// An absent room, or an absent field in one, is the default: Room is
	// offsets from the room every mood had before.
	if v := at(m, "room"); v != vm.NIL {
		r, ok := v.(*vm.PersistentMap)
		if !ok {
			return s, fmt.Errorf(":room must be a map, {:size s :damp d}")
		}
		for _, f := range []struct {
			k   string
			dst *float64
		}{{"size", &s.Room.Size}, {"damp", &s.Room.Damp}} {
			if at(r, f.k) != vm.NIL && !flt(r, f.k, f.dst) {
				return s, fmt.Errorf(":room's :%s must be a number", f.k)
			}
		}
		if err := onlyKeys(r, map[string]bool{"size": true, "damp": true}); err != nil {
			return s, fmt.Errorf(":room: %w", err)
		}
	}
	var err error
	if s.Phrasing, err = phrasingOf(at(m, "phrasing")); err != nil {
		return s, err
	}
	if s.Memory, err = memoryOf(at(m, "memory")); err != nil {
		return s, err
	}
	if v := at(m, "palette"); v != vm.NIL {
		name, ok := kwStr(m, "palette")
		if !ok {
			return s, fmt.Errorf(":palette must be a name")
		}
		s.Palette = name
	}
	if s.Leads, err = names(m, "leads"); err != nil {
		return s, err
	}
	if at(m, "grooves") != vm.NIL {
		if s.Grooves, err = names(m, "grooves"); err != nil {
			return s, err
		}
	}
	scales, ok := asVec(at(m, "scales"))
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

// retired maps keys the lead rename (#108) retired to what replaced them.
// Each is an error rather than an alias: they had no users outside the repo
// to keep working, and a silent no-op in a partial map is the worse failure.
var retired = map[string]string{
	"bell-density": "it is :lead-density",
	"bell-lift":    "it is :lead-lift",
	"bell":         "the line is :lead-density",
}

// retiredKey reports the first of keys that m still uses.
func retiredKey(m *vm.PersistentMap, keys ...string) error {
	for _, k := range keys {
		if at(m, k) != vm.NIL {
			return fmt.Errorf(":%s was renamed: %s", k, retired[k])
		}
	}
	return nil
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

// names reads a vector of keywords or strings. An empty one reads as nil,
// as MoodByName exports a mood without leads.
func names(m *vm.PersistentMap, k string) ([]string, error) {
	vec, ok := asVec(at(m, k))
	if !ok {
		return nil, fmt.Errorf(":%s must be a vector of names", k)
	}
	if len(vec) == 0 {
		return nil, nil
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
	vec, ok := asVec(v)
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

// phrasingOf reads a mood's :phrasing; nil is none. Strict like the rest of
// an entry: an unknown key, layer or section is an error.
func phrasingOf(v vm.Value) (*engine.Phrasing, error) {
	if v == vm.NIL {
		return nil, nil
	}
	m, ok := v.(*vm.PersistentMap)
	if !ok {
		return nil, fmt.Errorf(":phrasing must be a map")
	}
	var t engine.Phrasing
	if !integer(m, "phrase-min", &t.PhraseMin) || !integer(m, "phrase-max", &t.PhraseMax) {
		return nil, fmt.Errorf(":phrasing's :phrase-min and :phrase-max must be integers")
	}
	if at(m, "break-chance") != vm.NIL && !flt(m, "break-chance", &t.BreakChance) {
		return nil, fmt.Errorf(":phrasing's :break-chance must be a number")
	}
	if v := at(m, "drop"); v != vm.NIL {
		vec, ok := asVec(v)
		if !ok {
			return nil, fmt.Errorf(":phrasing's :drop must be a vector of layer names")
		}
		for _, e := range vec {
			k, _ := e.(vm.Keyword)
			l, ok := muteNames[string(k)]
			if !ok {
				return nil, fmt.Errorf(":phrasing's :drop: unknown layer %v (drone fifth pad lead air pulse drums)", e)
			}
			t.Drop = append(t.Drop, l)
		}
	}
	if v := at(m, "shade"); v != vm.NIL {
		sm, ok := v.(*vm.PersistentMap)
		if !ok {
			return nil, fmt.Errorf(":phrasing's :shade must be a map of sections")
		}
		known := map[string]bool{}
		for i, name := range engine.SectionNames() {
			known[name] = true
			if err := shadeOf(at(sm, name), &t.Shade[i]); err != nil {
				return nil, fmt.Errorf(":phrasing's :shade :%s: %w", name, err)
			}
		}
		if err := onlyKeys(sm, known); err != nil {
			return nil, fmt.Errorf(":phrasing's :shade: %w", err)
		}
	}
	if err := onlyKeys(m, map[string]bool{"phrase-min": true, "phrase-max": true, "break-chance": true, "drop": true, "shade": true}); err != nil {
		return nil, fmt.Errorf(":phrasing: %w", err)
	}
	return &t, nil
}

// shadeOf reads one section's colour; an absent field is no change.
func shadeOf(v vm.Value, sh *engine.Shade) error {
	if v == vm.NIL {
		return nil
	}
	m, ok := v.(*vm.PersistentMap)
	if !ok {
		return fmt.Errorf("must be a map")
	}
	known := map[string]bool{"lift": true, "pad": true, "fill": true}
	for _, f := range []struct {
		k   string
		dst *float64
	}{{"open", &sh.Open}, {"grit", &sh.Grit}, {"dark", &sh.Dark}, {"wash", &sh.Wash}, {"room", &sh.Room}, {"echo", &sh.Echo}, {"busy", &sh.Busy}} {
		known[f.k] = true
		if at(m, f.k) != vm.NIL && !flt(m, f.k, f.dst) {
			return fmt.Errorf(":%s must be a number", f.k)
		}
	}
	for _, f := range []struct {
		k   string
		dst *int
	}{{"lift", &sh.Lift}, {"pad", &sh.Pad}} {
		if at(m, f.k) != vm.NIL && !integer(m, f.k, f.dst) {
			return fmt.Errorf(":%s must be an integer", f.k)
		}
	}
	switch at(m, "fill") {
	case vm.NIL, vm.FALSE:
	case vm.TRUE:
		sh.Fill = true
	default:
		return fmt.Errorf(":fill must be true or false")
	}
	return onlyKeys(m, known)
}

// memoryOf reads a mood's :memory; nil is none. An absent knob takes
// engine.NewMemory's default for the restlessness given.
func memoryOf(v vm.Value) (*engine.Memory, error) {
	if v == vm.NIL {
		return nil, nil
	}
	m, ok := v.(*vm.PersistentMap)
	if !ok {
		return nil, fmt.Errorf(":memory must be a map")
	}
	var r float64
	if at(m, "restlessness") != vm.NIL && !flt(m, "restlessness", &r) {
		return nil, fmt.Errorf(":memory's :restlessness must be a number")
	}
	mem := engine.NewMemory(r)
	for _, f := range []struct {
		k   string
		dst *float64
	}{{"leap", &mem.Leap}, {"return-drift", &mem.ReturnDrift}, {"cycle-churn", &mem.CycleChurn}} {
		if at(m, f.k) != vm.NIL && !flt(m, f.k, f.dst) {
			return nil, fmt.Errorf(":memory's :%s must be a number", f.k)
		}
	}
	if err := onlyKeys(m, map[string]bool{"restlessness": true, "leap": true, "return-drift": true, "cycle-churn": true}); err != nil {
		return nil, fmt.Errorf(":memory: %w", err)
	}
	return &mem, nil
}
