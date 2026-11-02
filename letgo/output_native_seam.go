//go:build audio_oto && !js

package letgo

import (
	"io"

	"github.com/nooga/let-go/pkg/audio"
)

// A host built with -tags audio_oto has let-go's oto device, so the music
// joins its mix instead of opening a second oto context. Its latency is the
// device's: audio.Default().Configure, or LETGO_AUDIO_QUEUE and
// LETGO_AUDIO_DEVICE_FRAMES; nativeaudio's options and UNDERTONE_* variables
// don't apply.
func openOutput(src io.Reader) (output, error) {
	return streamTo(audio.Default(), src)
}
