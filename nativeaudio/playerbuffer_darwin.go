//go:build darwin && !ios

package nativeaudio

import "time"

// playerBuffer on macOS: see latency.go. 24 ms played clean in tesserae on a
// MacBook Air (2026-10-26) but crackled in xsofy, a heavier lg process, which
// 40 ms fixed (2026-10-31); a host that can afford the stall budget sets
// Options.Queue or UNDERTONE_PLAYER_BUFFER lower.
const playerBuffer = 40 * time.Millisecond
