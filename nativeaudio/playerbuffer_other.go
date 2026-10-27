//go:build !js && !darwin

package nativeaudio

import "time"

// playerBuffer off macOS: see latency.go. Not yet heard below 40 ms here.
const playerBuffer = 40 * time.Millisecond
