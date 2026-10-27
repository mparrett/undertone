//go:build !js

package nativeaudio

import (
	"testing"
	"time"
)

// oto turns our device buffer duration back into bytes by truncating integer
// arithmetic, then hands bytes/4/channelCount to createScriptProcessor on
// browsers without AudioWorklet — an API that throws unless that number is a
// power of two. So the round trip, not just the intent, has to land on one.
// This reproduces oto's own arithmetic (context.go / driver_js.go).
func TestDeviceBufferSurvivesOtosRoundTripAsAPowerOfTwo(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		deviceRoundTrip(t, rate)
	}
}

func deviceRoundTrip(t *testing.T, rate int) {
	const channelCount = 2
	bytesPerSecond := rate * otoBytesPerFrame

	b := int(int64(deviceBuffer(rate, deviceFrames)) * int64(bytesPerSecond) / int64(time.Second))
	b = b / otoBytesPerFrame * otoBytesPerFrame // oto's alignment step
	frames := b / 4 / channelCount

	if frames&(frames-1) != 0 || frames == 0 {
		t.Errorf("at %d Hz the device buffer round-trips to %d frames, which is not a power of two; "+
			"ScriptProcessorNode would throw IndexSizeError and audio would die on "+
			"browsers without AudioWorklet", rate, frames)
	}
	if int64(frames) != deviceFrames {
		t.Errorf("at %d Hz the device buffer round-trips to %d frames, want %d", rate, frames, deviceFrames)
	}
}

func TestPlayerBufferIsWellFormedAndSmallerThanOtosDefault(t *testing.T) {
	const rate = 44100
	got := playerBufferBytes(rate, playerBuffer)
	if got%bytesPerFrame != 0 {
		t.Errorf("player buffer %d bytes is not a whole number of frames", got)
	}
	// oto's default is half a second, which is the latency this exists to cut.
	otoDefault := rate * bytesPerFrame / 2
	if got >= otoDefault {
		t.Errorf("player buffer %d >= oto's default %d — the point is to be smaller", got, otoDefault)
	}
	if playerBuffer < deviceBuffer(rate, deviceFrames) {
		t.Errorf("player buffer (%v) below device buffer (%v): the device drains in whole "+
			"buffers, so read-ahead shorter than one cannot help", playerBuffer, deviceBuffer(rate, deviceFrames))
	}
}

func TestOverridesReadTheEnvironment(t *testing.T) {
	t.Setenv("UNDERTONE_PLAYER_BUFFER", "20ms")
	t.Setenv("UNDERTONE_DEVICE_FRAMES", "256")
	q, f, err := overrides(44100, 500*time.Millisecond)
	if err != nil || q != 20*time.Millisecond || f != 256 {
		t.Fatalf("overrides = %v, %d, %v; want 20ms, 256, nil", q, f, err)
	}
	// Every override still survives oto's round trip as a power of two.
	for n := int64(64); n <= 16384; n *= 2 {
		for _, rate := range []int{44100, 48000} {
			b := int(int64(deviceBuffer(rate, n)) * int64(rate*otoBytesPerFrame) / int64(time.Second))
			if got := int64(b / otoBytesPerFrame * otoBytesPerFrame / 4 / 2); got != n {
				t.Errorf("%d frames at %d Hz round-trips to %d", n, rate, got)
			}
		}
	}
}

func TestOverridesRejectBadValues(t *testing.T) {
	for _, c := range []struct{ env, val string }{
		{"UNDERTONE_PLAYER_BUFFER", "fast"},
		{"UNDERTONE_PLAYER_BUFFER", "-5ms"},
		{"UNDERTONE_PLAYER_BUFFER", "10us"}, // 0 bytes: oto would read it as its 500 ms default
		{"UNDERTONE_PLAYER_BUFFER", "5ms"},  // shorter than the 512-frame device buffer
		{"UNDERTONE_DEVICE_FRAMES", "300"},
		{"UNDERTONE_DEVICE_FRAMES", "32"},
		{"UNDERTONE_DEVICE_FRAMES", "32768"},
	} {
		t.Setenv("UNDERTONE_PLAYER_BUFFER", "")
		t.Setenv("UNDERTONE_DEVICE_FRAMES", "")
		t.Setenv(c.env, c.val)
		if _, _, err := overrides(44100, 40*time.Millisecond); err == nil {
			t.Errorf("%s=%s: want an error", c.env, c.val)
		}
	}
}

func TestOverridesDefaultToTheCaller(t *testing.T) {
	t.Setenv("UNDERTONE_PLAYER_BUFFER", "")
	t.Setenv("UNDERTONE_DEVICE_FRAMES", "")
	q, f, err := overrides(44100, 500*time.Millisecond)
	if err != nil || q != 500*time.Millisecond || f != deviceFrames {
		t.Fatalf("overrides = %v, %d, %v; want the caller's 500ms and %d", q, f, err, deviceFrames)
	}
}

// 512 frames at 44.1 kHz hold 11.6 ms, so 12 ms of read-ahead is legal and
// 11 ms is not.
func TestOverridesBoundReadAheadByTheDeviceBuffersPlayTime(t *testing.T) {
	t.Setenv("UNDERTONE_DEVICE_FRAMES", "512")
	for v, ok := range map[string]bool{"12ms": true, "11ms": false} {
		t.Setenv("UNDERTONE_PLAYER_BUFFER", v)
		if _, _, err := overrides(44100, 40*time.Millisecond); (err == nil) != ok {
			t.Errorf("UNDERTONE_PLAYER_BUFFER=%s with 512 frames: err = %v, want ok=%v", v, err, ok)
		}
	}
}
