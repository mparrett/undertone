package engine

import "testing"

// The bass once played on the fifth's track, so a theme over the bed lost
// the fifth. With a track of its own, the fifth stays, as quiet as the rest
// of the bed under a grid.
func TestThemeKeepsTheFifth(t *testing.T) {
	p := DeriveOverworld(1, 2, 0)
	gen := buildPatterns(p, 0)
	row := -1
	for r := range rows {
		if gen[trackFifth][r].note != 0 {
			row = r
			break
		}
	}
	if row < 0 {
		t.Fatal("no fifth note in the pattern: the test checked nothing")
	}
	for _, bed := range []bool{true, false} {
		g := Grid{Bed: bed}
		g.Bass[row] = 1
		s := NewSynth(p)
		s.pats = buildPatterns(p, 0)
		s.applyGrid(&g, row)
		fifth, bass := s.pats[trackFifth][row], s.pats[trackBass][row]
		if bass.note == 0 || bass.gain != gridBassGain {
			t.Errorf("bed %v: no grid bass on its track: %+v", bed, bass)
		}
		switch {
		case bed && (fifth.note != gen[trackFifth][row].note || fifth.gain != gen[trackFifth][row].gain*gridBedScale):
			t.Errorf("bed on: fifth %+v, want the generated one at gridBedScale", fifth)
		case !bed && fifth.note != 0:
			t.Errorf("bed off: fifth %+v, want it silent like the rest of the bed", fifth)
		}
	}
}

// bassOnly plays a grid of nothing but bass notes, with the bed off.
func bassOnly(p Params) *Synth {
	s := NewSynth(p)
	g := Grid{}
	for r := 0; r < Steps; r += 4 {
		g.Bass[r] = 1
	}
	s.SetGrid(&g)
	return s
}

// The grid's bass is layer 7, not track 6's bit (the drums'): its meter,
// its mute and its note events all say bass.
func TestGridBassIsItsOwnLayer(t *testing.T) {
	p := DeriveOverworld(1, 2, 0)
	buf := make([]byte, patternBytes(p))

	s := bassOnly(p)
	s.Read(buf) // the generated bed before the grid dies away
	s.Read(buf)
	if v := rms(buf); v < 1e-3 {
		t.Fatalf("a grid of bass notes is silent, rms %.5f", v)
	}
	if v := s.LayerLevels()[7]; v < 1e-3 {
		t.Errorf("bass meter %g while only the bass plays", v)
	}
	evs, _, _ := s.NoteEvents(0, nil)
	sawBass := false
	for _, e := range evs {
		if e.Layer == layerDrums {
			t.Errorf("a drum event with no drums in the grid: %+v", e)
		}
		sawBass = sawBass || LayerNames()[e.Layer] == "bass"
	}
	if !sawBass {
		t.Error("no note event on the bass layer")
	}

	p.Mute = LayerBass
	s = bassOnly(p)
	s.Read(buf)
	s.Read(buf)
	if v := rms(buf); v > 1e-3 {
		t.Errorf("bass muted, rms %.5f, want silence", v)
	}
}

func TestTrackLayers(t *testing.T) {
	want := [nTracks]string{"drone", "fifth", "pad", "lead", "air", "pulse", "bass"}
	names := LayerNames()
	seen := map[int]bool{}
	for tr, l := range trackLayer {
		if names[l] != want[tr] {
			t.Errorf("track %d is layer %d %q, want %q", tr, l, names[l], want[tr])
		}
		if l == DrumsLayer || seen[l] {
			t.Errorf("track %d shares layer %d", tr, l)
		}
		seen[l] = true
	}
	if names[DrumsLayer] != "drums" || Layers(1)<<DrumsLayer != LayerDrums || Layers(1)<<7 != LayerBass {
		t.Error("DrumsLayer, LayerDrums and LayerBass disagree with LayerNames")
	}
}
