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

// A browser waiting for the unlock gesture renders nothing while it opens.
// A set to 0 and a fade-in asked for in that wait must still fade in from
// 0 once it plays, as a synth given both before its first frame would.
func TestFadeInAskedForWhileOpeningStartsFromItsSetLevel(t *testing.T) {
	p := engine.DeriveDungeon(7, 3, 0.5)
	m := NewMusic(p)
	m.setGain(musicBus, 0.25) // the game's level, set before music-start
	m.start(func(io.Reader) (output, error) {
		m.setGain(musicBus, 0)
		m.glideGain(musicBus, 0.25, 1.5)
		return gainTestOutput{}, nil
	})
	defer m.Stop()

	ref := engine.NewSynth(p)
	ref.SetMusicGain(0)
	ref.GlideMusicGain(0.25, 1.5)
	want, got := make([]byte, engine.SampleRate/4*4), make([]byte, engine.SampleRate/4*4)
	ref.Read(want)
	m.synth.Read(got)
	if !bytes.Equal(got, want) {
		t.Error("a fade-in asked for while opening did not start from its set level")
	}
}

// A fade asked for before music-start plays when the music starts, and a
// later music-start resumes at the level it reached rather than fading in
// again.
func TestFadeInPlaysOnceAcrossRestarts(t *testing.T) {
	p := engine.DeriveDungeon(7, 3, 0.5)
	m := NewMusic(p)
	m.setGain(musicBus, 0)
	m.glideGain(musicBus, 0.5, 0.2)
	open := func(io.Reader) (output, error) { return gainTestOutput{}, nil }
	frames := func() []byte { return make([]byte, engine.SampleRate/4*4) }

	m.start(open)
	ref := engine.NewSynth(p)
	ref.SetMusicGain(0)
	ref.GlideMusicGain(0.5, 0.2)
	want, got := frames(), frames()
	ref.Read(want)
	m.synth.Read(got)
	if !bytes.Equal(got, want) {
		t.Error("a fade asked for before music-start did not play")
	}

	m.Stop()
	m.start(open)
	defer m.Stop()
	ref = engine.NewSynth(p)
	ref.SetMusicGain(0.5)
	want, got = frames(), frames()
	ref.Read(want)
	m.synth.Read(got)
	if !bytes.Equal(got, want) {
		t.Error("a restart faded in again instead of resuming at 0.5")
	}
}
