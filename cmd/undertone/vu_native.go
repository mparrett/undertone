//go:build !js

package main

import (
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/mparrett/undertone/engine"
)

const vuWidth = 12

// empty-track glyph; try "🮐" (legacy-computing checkerboard) if your
// terminal font carries it — Fairfax HD does, most terminal fonts don't
const vuEmpty = "·"

// eighth-width partial blocks for the bar tip, smooth sub-cell motion
var vuTips = []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

func vuColor(cell int) string {
	switch {
	case cell < 8:
		return "\x1b[32m" // green
	case cell < 10:
		return "\x1b[33m" // yellow
	default:
		return "\x1b[31m" // red
	}
}

// bar renders one band: colored zone fill with a fractional tip, dim empty
// track, and a peak-hold tick that decays slower than the bar.
func bar(b *strings.Builder, v, peak float64, color bool) {
	f := math.Min(math.Pow(v, 0.4)*vuWidth, vuWidth)
	full := int(f)
	tip := int((f - float64(full)) * 8)
	peakCell := int(math.Min(math.Pow(peak, 0.4)*vuWidth, vuWidth-1))
	for c := 0; c < vuWidth; c++ {
		if color {
			b.WriteString(vuColor(c))
		}
		switch {
		case c < full:
			b.WriteString("█")
		case c == full && tip > 0:
			b.WriteString(vuTips[tip])
		case c == peakCell && peakCell > full:
			b.WriteString("╹")
		default:
			if color {
				b.WriteString("\x1b[2m")
			}
			b.WriteString(vuEmpty)
		}
		if color {
			b.WriteString("\x1b[0m")
		}
	}
}

func isTTY() bool {
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// vertical eighth-blocks for the compact one-cell-per-band mode
var vuVert = []string{"", "\u2581", "\u2582", "\u2583", "\u2584", "\u2585", "\u2586", "\u2587", "\u2588"}

// compactCell renders one band in a single cell: 17 levels — blank + the 8
// vertical eighths at two ANSI intensities, interleaved (dim \u2581, bright \u2581,
// dim \u2582, ...). Unicode has no hashed eighths (\u2591\u2592\u2593 are full-cell only),
// so intensity supplies the extra bit. No-color terminals degrade to 9 levels.
func renderCell(b *strings.Builder, idx int, zone string, color bool) {
	if idx <= 0 {
		b.WriteString(" ")
		return
	}
	if color {
		b.WriteString(zone)
		if idx%2 == 1 {
			b.WriteString("\x1b[2m")
		}
	}
	b.WriteString(vuVert[(idx+1)/2])
	if color {
		b.WriteString("\x1b[0m")
	}
}

func compactCell(b *strings.Builder, v float64, color bool) {
	idx := int(math.Min(math.Pow(v, 0.4)*16, 16) + 0.5)
	zone := "\x1b[32m"
	switch {
	case idx >= 15:
		zone = "\x1b[31m"
	case idx >= 11:
		zone = "\x1b[33m"
	}
	renderCell(b, idx, zone, color)
}

// tallLines renders each band as two stacked cells: 33 glyph levels (2 x 16
// eighth-x-intensity steps + blank), labels on the bottom line.
func tallLines(levels [4]float64, names [4]string, color bool) (string, string) {
	var top, bot strings.Builder
	top.WriteString(" ")
	bot.WriteString(" ")
	for i, v := range levels {
		idx := int(math.Min(math.Pow(v, 0.4)*32, 32) + 0.5)
		zone := "\x1b[32m"
		switch {
		case idx >= 29:
			zone = "\x1b[31m"
		case idx >= 22:
			zone = "\x1b[33m"
		}
		top.WriteString(strings.Repeat(" ", len(names[i])+1))
		renderCell(&top, idx-16, zone, color)
		top.WriteString("  ")
		if color {
			bot.WriteString("\x1b[2m")
		}
		fmt.Fprintf(&bot, "%s ", names[i])
		if color {
			bot.WriteString("\x1b[0m")
		}
		renderCell(&bot, min(idx, 16), zone, color)
		bot.WriteString("  ")
	}
	return top.String(), bot.String()
}

// runMeterUI redraws a single carriage-return line of band meters ~30x/s.
// Never returns; ctrl-c restores the cursor and exits.
func runMeterUI(s *engine.Synth, desc, style string) {
	fmt.Printf("playing %s — ctrl-c to stop\n", desc)
	tty := isTTY()
	if tty {
		fmt.Print("\x1b[?25l")
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		go func() {
			<-sig
			fmt.Print("\x1b[?25h\n")
			os.Exit(0)
		}()
	}
	names := [4]string{"sub", "low", "mid", "hi"}
	if style == "tall" && tty {
		first := true
		for range time.Tick(33 * time.Millisecond) {
			top, bot := tallLines(s.Levels(), names, true)
			if first {
				fmt.Print(top + "\n" + bot)
				first = false
				continue
			}
			fmt.Print("\x1b[1A\r" + top + "\x1b[K\x1b[1B\r" + bot + "\x1b[K")
		}
	}
	if style == "tall" {
		style = "compact" // no cursor movement in a pipe
	}
	var peaks [4]float64
	for range time.Tick(33 * time.Millisecond) {
		var b strings.Builder
		b.WriteString("\r ")
		for i, v := range s.Levels() {
			if style == "compact" {
				if tty {
					b.WriteString("\x1b[2m")
				}
				fmt.Fprintf(&b, "%s ", names[i])
				if tty {
					b.WriteString("\x1b[0m")
				}
				compactCell(&b, v, tty)
				b.WriteString("  ")
				continue
			}
			peaks[i] = math.Max(v, peaks[i]*0.97)
			if tty {
				b.WriteString("\x1b[2m")
			}
			fmt.Fprintf(&b, "%-3s ", names[i])
			if tty {
				b.WriteString("\x1b[0m")
			}
			bar(&b, v, peaks[i], tty)
			b.WriteString("  ")
		}
		fmt.Print(b.String())
	}
}
