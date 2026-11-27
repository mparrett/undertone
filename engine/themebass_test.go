package engine

import (
	"bytes"
	"testing"
)

// A theme's Bass plays its bass lane as ApplyBass would, keeps playing it
// across a mood change, which replaces the palette, and ends with the theme.
func TestThemeBass(t *testing.T) {
	g := Grid{Bass: [Steps]int{1, 0, 0, 0, 5, 0, 0, 0, 1, 0, 0, 0, 5}}
	render := func(p Params, th Theme, then func(*Synth)) []byte {
		s := NewSynth(p)
		s.PlayTheme(&th)
		buf := make([]byte, 4*SampleRate*2)
		s.Read(buf[:len(buf)/2])
		if then != nil {
			then(s)
		}
		s.Read(buf[len(buf)/2:])
		return buf
	}
	p := DeriveOverworld(1, 2, 0)
	applied := p
	ApplyBass(&applied, "reed")
	want := render(applied, Theme{Grids: []Grid{g}, Then: ThenLoop}, nil)
	if got := render(p, Theme{Grids: []Grid{g}, Then: ThenLoop, Bass: "reed"}, nil); !bytes.Equal(got, want) {
		t.Error("a theme's Bass plays unlike ApplyBass of the same instrument")
	}
	if got := render(p, Theme{Grids: []Grid{g}, Then: ThenLoop}, nil); bytes.Equal(got, want) {
		t.Error("no Bass plays the reed too: the test cannot tell them apart")
	}
	if got, plain := render(p, Theme{Grids: []Grid{g}, Bass: "no-such-lead"}, nil), render(p, Theme{Grids: []Grid{g}}, nil); !bytes.Equal(got, plain) {
		t.Error("a Bass no lead has changes the bass")
	}

	// A mood change mid-theme: the theme's reed holds, where ApplyBass's
	// would go with the old palette.
	themed := render(p, Theme{Grids: []Grid{g}, Then: ThenLoop, Bass: "reed"},
		func(s *Synth) { s.SetParametersNow(DeriveDungeon(1, 2, 0)) })
	d := DeriveDungeon(1, 2, 0)
	ApplyBass(&d, "reed")
	ref := render(applied, Theme{Grids: []Grid{g}, Then: ThenLoop}, func(s *Synth) { s.SetParametersNow(d) })
	if !bytes.Equal(themed, ref) {
		t.Error("a theme's Bass did not hold across a mood change")
	}
}

// A palette replaced after ApplyBass plays its own bass at the string's level,
// not at the level ApplyBass matched to the instrument it chose.
func TestNewPaletteResetsBassLevel(t *testing.T) {
	g := Grid{Bass: [Steps]int{1, 0, 0, 0, 5}}
	render := func(p Params) []byte {
		s := NewSynth(p)
		s.SetGrid(&g)
		buf := make([]byte, 4*SampleRate)
		s.Read(buf)
		return buf
	}
	p := DeriveOverworld(1, 2, 0)
	want := render(p)
	q := p
	ApplyBass(&q, "bell")
	q.Palette = p.Palette
	if !bytes.Equal(render(q), want) {
		t.Error("a palette set after ApplyBass kept the bell's bass level")
	}
}
