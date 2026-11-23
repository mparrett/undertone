package engine

import (
	"bytes"
	"math"
	"testing"
)

// Each voice ApplyBass can name plays the bass lane about as loud as the
// string it replaces: at the string's gain the louder ones drown the lead.
func TestApplyBassLevel(t *testing.T) {
	level := func(name string) float64 {
		p := DeriveOverworld(1, 2, 0)
		p.BPM = 110
		if name != "" {
			ApplyBass(&p, name)
		}
		s := NewSynth(p)
		s.SetGrid(&Grid{Bass: [Steps]int{1, 0, 0, 0, 5, 0, 0, 0, 1, 0, 0, 0, 5}})
		buf := make([]byte, 4*SampleRate*8)
		s.Read(buf)
		var sum float64
		var n int
		for i := 0; i+1 < len(buf); i += 2 {
			v := float64(int16(uint16(buf[i])|uint16(buf[i+1])<<8)) / 32768
			if math.Abs(v) > 0.001 { // while sounding
				sum += v * v
				n++
			}
		}
		return 10 * math.Log10(sum/float64(n))
	}
	want := level("")
	for _, d := range leadLib {
		if _, ok := gridBassVoiceGain[d.name]; !ok {
			t.Errorf("%s: no bass gain measured; it plays at the string's", d.name)
		}
		if got := level(d.name); math.Abs(got-want) > 1.5 {
			t.Errorf("%s as the bass: %.1f dB, the string is %.1f", d.name, got, want)
		}
	}
}

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
