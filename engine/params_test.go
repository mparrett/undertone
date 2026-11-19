package engine

import (
	"slices"
	"testing"
)

func rooted(p Params, root int) Params { p.Root = root; return p }

// A boundary change made after an immediate one is newer, and wins even when
// both arrive before the first frame, where they are consumed together; in
// the other order the immediate change supersedes the staged one.
func TestParameterChangesApplyInCallOrderAtStartup(t *testing.T) {
	p := DeriveDungeon(7, 2, 0)
	s := NewSynth(p)
	s.SetParametersNow(rooted(p, 60))
	s.SetParameters(rooted(p, 72))
	render(s, 1)
	if s.params.Root != 72 {
		t.Errorf("now(60) then staged(72): root %d, want 72", s.params.Root)
	}

	s = NewSynth(p)
	s.SetParameters(rooted(p, 72))
	s.SetParametersNow(rooted(p, 60))
	render(s, 1)
	if s.params.Root != 60 || s.staged() != nil {
		t.Errorf("staged(72) then now(60): root %d, staged %v; want 60 and none", s.params.Root, s.staged() != nil)
	}
	render(s, rows*s.samplesPerRow+nowBlock)
	if s.params.Root != 60 {
		t.Errorf("staged(72) then now(60): the older staged change landed at the boundary: root %d", s.params.Root)
	}
}

// The same pair one frame before a running pattern boundary: the boundary
// steps before the immediate change's block comes round, and the older
// immediate change must not then undo the newer staged one.
func TestOlderImmediateChangeDoesNotUndoBoundaryChange(t *testing.T) {
	p := DeriveDungeon(7, 2, 0)
	s := NewSynth(rooted(p, 50))
	render(s, 1)
	for s.row != 0 || s.rowLeft != 1 {
		render(s, 1)
	}
	s.SetParametersNow(rooted(p, 60))
	s.SetParameters(rooted(p, 72))
	render(s, 1+nowBlock)
	if s.params.Root != 72 {
		t.Errorf("root %d after the boundary, want the newer staged 72", s.params.Root)
	}
}

// A staged change that an immediate one has since replaced does not play at
// the boundary, not even for the frames before the immediate change's block.
func TestReplacedStagedChangeSkipsBoundary(t *testing.T) {
	p := DeriveDungeon(7, 2, 0)
	s := NewSynth(rooted(p, 50))
	render(s, 1)
	for s.row != 0 || s.rowLeft != 1 || (s.nframe+1)%nowBlock == 0 {
		render(s, 1)
	}
	s.SetParameters(rooted(p, 72))
	s.SetParametersNow(rooted(p, 60))
	for range nowBlock {
		render(s, 1)
		if s.params.Root == 72 {
			t.Fatalf("frame %d: the replaced staged change is playing", s.nframe)
		}
	}
	if s.params.Root != 60 {
		t.Errorf("root %d, want the immediate 60", s.params.Root)
	}
}

// A degree shot is pitched against the newest change, whether it waits for
// the boundary or is an immediate one not yet rendered; an older staged
// change superseded by an immediate one no longer counts.
func TestDegreeFollowsNewestParameterChange(t *testing.T) {
	p := DeriveDungeon(7, 2, 0)
	s := NewSynth(rooted(p, 60))
	render(s, 1)
	s.SetParameters(rooted(p, 61))
	if got := s.resolveDegree(0, 0); got != 61 {
		t.Errorf("staged 61: degree 0 = %d, want 61", got)
	}
	s.SetParametersNow(rooted(p, 62))
	if got := s.resolveDegree(0, 0); got != 62 {
		t.Errorf("immediate 62 before render: degree 0 = %d, want 62", got)
	}
	render(s, nowBlock)
	if got := s.resolveDegree(0, 0); got != 62 || s.staged() != nil {
		t.Errorf("after render: degree 0 = %d, staged %v; want 62 and none", got, s.staged() != nil)
	}
}

// Editing a derived scale reaches neither a later derivation nor a synth
// already given it, at construction or through either setter.
func TestCallerScaleEditsDoNotReachSynth(t *testing.T) {
	want := slices.Clone(DeriveDungeon(7, 2, 0).Scale)
	for _, set := range []struct {
		name string
		fn   func(*Synth, Params)
	}{
		{"NewSynth", nil},
		{"SetParameters", (*Synth).SetParameters},
		{"SetParametersNow", (*Synth).SetParametersNow},
	} {
		p := DeriveDungeon(7, 2, 0)
		s := NewSynth(DeriveDungeon(7, 2, 0))
		if set.fn == nil {
			s = NewSynth(p)
		} else {
			set.fn(s, p)
		}
		render(s, 1)
		p.Scale[0] = 99
		if got := DeriveDungeon(7, 2, 0).Scale; !slices.Equal(got, want) {
			t.Errorf("%s: a new derivation's scale is %v, want %v", set.name, got, want)
		}
		if !slices.Equal(s.params.Scale, want) {
			t.Errorf("%s: the synth's scale is %v, want %v", set.name, s.params.Scale, want)
		}
	}
}
