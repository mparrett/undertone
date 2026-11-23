package engine

import (
	"bytes"
	"testing"
)

// A grid's bass lane plays the instrument ApplyBass names, and the lead and
// everything else are left as they were.
func TestApplyBass(t *testing.T) {
	render := func(p Params, g Grid) []byte {
		s := NewSynth(p)
		s.SetGrid(&g)
		buf := make([]byte, 4*SampleRate)
		s.Read(buf)
		return buf
	}
	base := DeriveOverworld(1, 2, 0)
	bell := base
	ApplyBass(&bell, "bell")
	if bell.Palette.Bass == nil {
		t.Fatal("ApplyBass(bell) left the palette's bass unset")
	}
	if bell.LeadName != base.LeadName {
		t.Errorf("ApplyBass changed the lead: %q, was %q", bell.LeadName, base.LeadName)
	}

	bass := Grid{Bass: [Steps]int{1, 0, 0, 0, 5}}
	if bytes.Equal(render(base, bass), render(bell, bass)) {
		t.Error("a grid's bass lane sounds the same with a bell as with the default bass")
	}
	lead := Grid{Lead: [Steps]int{1, 0, 0, 0, 5}}
	if !bytes.Equal(render(base, lead), render(bell, lead)) {
		t.Error("ApplyBass changed a grid with no bass notes")
	}

	unknown := base
	ApplyBass(&unknown, "no-such-lead")
	if unknown.Palette.Bass != nil {
		t.Error("an unknown name set the palette's bass")
	}
}
