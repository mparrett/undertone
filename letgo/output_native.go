//go:build !js

package letgo

import (
	"io"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/nativeaudio"
)

func openOutput(src io.Reader) (output, error) {
	return nativeaudio.Open(src, nativeaudio.Options{SampleRate: engine.SampleRate})
}
