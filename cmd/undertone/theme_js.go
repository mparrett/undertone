//go:build js

package main

import (
	"errors"

	"github.com/mparrett/undertone/engine"
)

// loadTheme has no file to read in a browser, and leaving the JSON reader
// out keeps it from the page's download.
func loadTheme(string) (*engine.Theme, error) {
	return nil, errors.New("a theme file cannot be read in the browser")
}
