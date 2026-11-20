//go:build !js && !(darwin && cgo)

package main

import "errors"

func openVirtualSource(string) (midiSink, error) {
	return nil, errors.New("-midi-live needs macOS and a cgo build")
}
