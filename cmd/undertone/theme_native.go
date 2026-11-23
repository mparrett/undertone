//go:build !js

package main

import (
	"errors"
	"os"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/internal/themejson"
)

// loadTheme reads -theme's file.
func loadTheme(path string) (*engine.Theme, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	t, err := themejson.Parse(string(b))
	if err != nil {
		return nil, err
	}
	if len(t.Grids) == 0 {
		return nil, errors.New("no grids")
	}
	return t, nil
}
