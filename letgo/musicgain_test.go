package letgo

import (
	"bytes"
	"io"
	"testing"

	"github.com/mparrett/undertone/engine"
)

type gainTestOutput struct{}

func (gainTestOutput) Close()  {}
func (gainTestOutput) Pause()  {}
func (gainTestOutput) Resume() {}

func TestMusicGainAtStartup(t *testing.T) {
	for _, tc := range []struct {
		name string
		gain float64
	}{
		{"muted", 0},
		{"attenuated", 0.25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := engine.DeriveDungeon(7, 3, 0.5)
			m := NewMusic(p)
			m.setMusicGain(tc.gain)
			ref := engine.NewSynth(p)
			ref.SetMusicGain(tc.gain)
			want, got := make([]byte, 512*4), make([]byte, 512*4)
			ref.Read(want)

			// Outputs can prime their buffers before opening returns.
			m.start(func(src io.Reader) (output, error) {
				if _, err := io.ReadFull(src, got); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Error("initial output ignored the saved music gain")
				}
				m.setMusicGain(0.5)
				return gainTestOutput{}, nil
			})
			defer m.Stop()

			ref.SetMusicGain(0.5)
			ref.Read(want)
			m.synth.Read(got)
			if !bytes.Equal(got, want) {
				t.Error("music gain changed during startup was not applied")
			}
		})
	}
}
