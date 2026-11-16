package letgo

import (
	"fmt"

	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// paletteSpec reads an lg palette map, in the shape PaletteSpec.EDN writes,
// into a spec; RegisterPalette judges it. An absent :bell or :kit is left
// zero, the built-in bell and the default kit.
func paletteSpec(v vm.Value) (engine.PaletteSpec, error) {
	var s engine.PaletteSpec
	m, ok := v.(*vm.PersistentMap)
	if !ok {
		return s, fmt.Errorf("want a map, got %s", v.Type().Name())
	}
	if s.Name, ok = kwStr(m, "name"); !ok {
		return s, fmt.Errorf("want :name as a keyword or string")
	}
	for _, t := range []struct {
		key string
		ps  *engine.PatchSpec
	}{
		{"drone", &s.Drone}, {"fifth", &s.Fifth}, {"pad", &s.Pad},
		{"bell", &s.Bell}, {"air", &s.Air}, {"pulse", &s.Pulse},
	} {
		pv := at(m, t.key)
		if pv == vm.NIL {
			continue // RegisterPalette says which a palette can't leave out
		}
		ps, err := patchSpec(pv)
		if err != nil {
			return s, fmt.Errorf(":%s: %w", t.key, err)
		}
		*t.ps = ps
	}
	kv := at(m, "kit")
	if kv == vm.NIL {
		return s, nil
	}
	lanes, ok := asVec(kv)
	if !ok || len(lanes) != len(s.Kit) {
		return s, fmt.Errorf(":kit: want a vector of %d lanes, kick, clap and shaker", len(s.Kit))
	}
	for i, lv := range lanes {
		if lv == vm.Keyword("default") {
			s.Kit[i].Default = true
			continue
		}
		lm, ok := lv.(*vm.PersistentMap)
		if !ok {
			return s, fmt.Errorf(":kit lane %d: want :default or {:note n :patch p}", i)
		}
		if !integer(lm, "note", &s.Kit[i].Note) {
			return s, fmt.Errorf(":kit lane %d: want :note as an integer", i)
		}
		ps, err := patchSpec(at(lm, "patch"))
		if err != nil {
			return s, fmt.Errorf(":kit lane %d: %w", i, err)
		}
		s.Kit[i].PatchSpec = ps
	}
	return s, nil
}

// patchSpec reads a palette's patch: a built-in by its keyword (:pad), a
// vector of units, or a map {:name :x :patch [units]} naming them.
func patchSpec(v vm.Value) (engine.PatchSpec, error) {
	switch v := v.(type) {
	case vm.Keyword:
		return engine.PatchSpec{Name: string(v)}, nil
	case *vm.PersistentMap:
		name, ok := kwStr(v, "name")
		if !ok {
			return engine.PatchSpec{}, fmt.Errorf("want :name as a keyword or string")
		}
		units, err := unitSpecs(at(v, "patch"))
		return engine.PatchSpec{Name: name, Units: units}, err
	}
	units, err := unitSpecs(v)
	return engine.PatchSpec{Units: units}, err
}

// leadSpec reads an lg lead map, in the shape LeadSpec.EDN writes, into a
// spec; RegisterLead judges it.
func leadSpec(v vm.Value) (engine.LeadSpec, error) {
	var s engine.LeadSpec
	m, ok := v.(*vm.PersistentMap)
	if !ok {
		return s, fmt.Errorf("want a map, got %s", v.Type().Name())
	}
	if s.Name, ok = kwStr(m, "name"); !ok {
		return s, fmt.Errorf("want :name as a keyword or string")
	}
	if !integer(m, "gate", &s.Gate) {
		return s, fmt.Errorf("want :gate as an integer, in rows")
	}
	var err error
	s.Units, err = unitSpecs(at(m, "patch"))
	return s, err
}
