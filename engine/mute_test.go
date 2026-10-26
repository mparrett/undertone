package engine

import (
	"bytes"
	"fmt"
	"testing"
)

// Mute silences layers at the mix and changes nothing upstream: a synth with
// pad, lead and pulse muted builds the same patterns and drums, fills
// included, as one hearing everything, and a synth with every layer muted
// is silent.
func TestMuteSilencesAtTheMix(t *testing.T) {
	p, _ := DeriveMood("village", 7, 4, 0.8)
	full, some, none := p, p, p
	some.Mute = LayerPad | LayerLead | LayerPulse
	none.Mute = LayerDrone | LayerFifth | LayerPad | LayerLead | LayerAir | LayerPulse | LayerDrums
	a, b, c := NewSynth(full), NewSynth(some), NewSynth(none)
	bufA, bufB, bufC := make([]byte, 4096), make([]byte, 4096), make([]byte, 4096)
	silent := make([]byte, 4096)
	differ := false
	for i := 0; i < 600; i++ { // about 14 s: several patterns at village's tempo
		a.Read(bufA)
		b.Read(bufB)
		c.Read(bufC)
		differ = differ || !bytes.Equal(bufA, bufB)
		if !bytes.Equal(bufC, silent) {
			t.Fatalf("buffer %d: a synth with every layer muted made sound", i)
		}
		// A muted voice runs unheard, so every voice holds the same state.
		if i%10 == 0 && fmt.Sprint(a.pats, a.drums, a.voices, a.dvoices) != fmt.Sprint(b.pats, b.drums, b.voices, b.dvoices) {
			t.Fatalf("buffer %d (pattern %d): muting changed what plays", i, a.pat)
		}
	}
	if !differ {
		t.Error("muting pad, lead and pulse changed nothing heard")
	}
}
