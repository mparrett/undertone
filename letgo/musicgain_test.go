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
			m.setGain(musicBus, tc.gain)
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
				m.setGain(musicBus, 0.5)
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

// A glide asked for while the output opens (a browser waiting for the unlock
// gesture) keeps its length: the output may have rendered by then, so the
// level must glide as asked rather than switch over the default 50 ms.
func TestGlideDuringStartupKeepsItsLength(t *testing.T) {
	p := engine.DeriveDungeon(7, 3, 0.5)
	m := NewMusic(p)
	ref := engine.NewSynth(p)
	ref.Trigger("hit", 60, 1, 0.5, 0, 11)
	want, got := make([]byte, 512*4), make([]byte, 512*4)
	ref.Read(want)
	m.start(func(src io.Reader) (output, error) {
		src.(*engine.Synth).Trigger("hit", 60, 1, 0.5, 0, 11)
		if _, err := io.ReadFull(src, got); err != nil {
			t.Fatal(err)
		}
		m.glideGain(sfxBus, 0, 1.0)
		return gainTestOutput{}, nil
	})
	defer m.Stop()

	ref.GlideSfxGain(0, 1.0)
	want, got = make([]byte, engine.SampleRate/4*4), make([]byte, engine.SampleRate/4*4)
	ref.Read(want)
	m.synth.Read(got)
	if !bytes.Equal(got, want) {
		t.Error("a glide asked for during startup did not keep its length")
	}
}
