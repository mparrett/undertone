package nativeaudio

import "time"

// playerBuffer on macOS: see latency.go. Ear-tested clean in tesserae on a
// MacBook Air, 2026-10-26.
const playerBuffer = 24 * time.Millisecond
