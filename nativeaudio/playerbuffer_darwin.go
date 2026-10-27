//go:build darwin && !ios

package nativeaudio

import "time"

// playerBuffer on macOS (not iOS, which the darwin suffix alone would take): see latency.go. Ear-tested clean in tesserae on a
// MacBook Air, 2026-10-26.
const playerBuffer = 24 * time.Millisecond
