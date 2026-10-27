//go:build !js && !(darwin && !ios)

package nativeaudio

import "time"

// playerBuffer off macOS, iOS included: see latency.go. Not yet heard below 40 ms here.
const playerBuffer = 40 * time.Millisecond
