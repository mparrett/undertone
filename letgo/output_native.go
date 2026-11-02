//go:build !audio_oto && !js

package letgo

import (
	"io"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/nativeaudio"
)

// Without let-go's oto device (-tags audio_oto, output_native_seam.go), the
// music opens its own player.
func openOutput(src io.Reader) (output, error) {
	return nativeaudio.Open(src, nativeaudio.Options{SampleRate: engine.SampleRate})
}
