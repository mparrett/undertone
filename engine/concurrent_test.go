package engine

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Every method documented as safe from any goroutine, called from its own
// goroutine while another renders: the shape of a game thread, a page's
// meter and an audio callback sharing one Synth. It asserts little by
// itself; its job is to give the race detector something to watch, and CI
// runs the tests named Concurrent under -race (`just race`). A new method
// that other goroutines may call belongs in the list.
func TestConcurrentControlWhileRendering(t *testing.T) {
	moods := MoodNames()
	params := make([]Params, len(moods))
	for i, name := range moods {
		params[i], _ = DeriveMood(name, uint64(i), i, float64(i%3)/2)
	}
	s := NewSynth(params[0])
	theme := &Theme{Grids: []Grid{SeedGrid(params[0]), SeedGrid(params[1])}, Then: ThenLoop}
	if err := RegisterShot(ShotSpec{Name: "concurrent-ping", GateMS: 20, Units: []UnitSpec{
		{Op: "env", Args: []float64{0.001, 0.1, 0, 0.05}},
		{Op: "osc", Shape: "sine", Args: []float64{0, 0, 1}},
		{Op: "mul"},
		{Op: "out", Args: []float64{0.5, 0.5}},
	}}); err != nil {
		t.Fatal(err)
	}
	shots := ShotNames()
	// The registry is the process's and has no way to unregister, so the
	// test takes its shots back out by hand: left in, every later test that
	// ranges over ShotNames would play them too.
	t.Cleanup(func() {
		shotsMu.Lock()
		defer shotsMu.Unlock()
		for name := range shotReg {
			if strings.HasPrefix(name, "concurrent-") {
				delete(shotReg, name)
			}
		}
	})
	forgetRegistered(t, "concurrent-")

	controls := []func(i int){
		func(i int) { s.SetParameters(params[i%len(params)]) },
		func(i int) { s.SetParametersNow(params[i%len(params)]) },
		func(i int) {
			if i%3 == 0 {
				s.SetGrid(nil)
				return
			}
			g := SeedGrid(params[i%len(params)])
			s.SetGrid(&g)
		},
		func(i int) {
			if i%4 == 0 {
				s.PlayTheme(nil)
			} else {
				s.PlayTheme(theme)
			}
			s.ThemePlaying()
		},
		func(i int) {
			s.SetMusicGain(float64(i%5) / 4)
			s.GlideMusicGain(float64(i%3)/2, 0.01)
			s.SetSfxGain(float64(i%4) / 3)
			s.GlideSfxGain(float64(i%2), 0.01)
		},
		func(i int) { s.Trigger(shots[i%len(shots)], 40+i%40, 1, 0.5, float64(i%30), uint64(i)) },
		// a degree is resolved against the parameters playing, read here
		// while the renderer and the two setters above replace them
		func(i int) {
			s.TriggerShot(Shot{Name: shots[i%len(shots)], Gain: 1, Pan: 0.5, Degree: i % 9, Octave: i % 3, HasDegree: true})
		},
		func(i int) {
			s.Levels()
			s.RowAt(s.Frames())
			s.Position()
			s.PositionAt(s.Frames() - 4096)
			s.RenderStats()
			s.LayerLevels()
		},
		// the shot registry is shared by every synth in the process
		func(i int) {
			RegisterShot(ShotSpec{Name: fmt.Sprintf("concurrent-%d", i%4), GateMS: 20, Units: []UnitSpec{
				{Op: "env", Args: []float64{0.001, 0.1, 0, 0.05}},
				{Op: "osc", Shape: "noise", Args: []float64{0, 0, 1}},
				{Op: "mul"},
				{Op: "out", Args: []float64{0.5, 0.5}},
			}})
			ShotNames()
			HasShot("concurrent-ping")
		},
		// so are the palette and lead registries, which a game writes while
		// it derives moods that read them
		func(i int) {
			pal, _ := PaletteSpecByName(PaletteNames()[i%6])
			pal.Name = fmt.Sprintf("concurrent-%d", i%3)
			RegisterPalette(pal)
			lead, _ := LeadSpecByName("chime")
			lead.Name = pal.Name
			RegisterLead(lead)
			m, _ := MoodByName(moods[i%len(moods)])
			m.Palette, m.Leads = pal.Name, []string{pal.Name}
			if p, err := m.Derive(uint64(i), i%5, 0); err == nil {
				s.SetParameters(p)
			}
			LeadNames()
		},
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	for _, control := range controls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; !stop.Load(); i++ {
				control(i)
			}
		}()
	}
	buf := make([]byte, 512*4)
	for i := 0; i < 2*SampleRate/512; i++ { // two seconds, a slice at a time as an output reads
		s.Read(buf)
	}
	stop.Store(true)
	wg.Wait()
}
