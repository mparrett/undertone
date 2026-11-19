staticcheck := "honnef.co/go/tools/cmd/staticcheck@2026.2.2"

# Runs a go test under node as js/wasm. go_js_wasm_exec hands the whole
# environment to the wasm program, which fails outright when it is large
# ("total length of command line and environment variables exceeds limit"),
# so it gets only what go needs.
wasm := 'env -i PATH="$PATH" HOME="$HOME" GOPATH="$(go env GOPATH)" GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)" GOOS=js GOARCH=wasm go test -exec "$(go env GOROOT)/lib/wasm/go_js_wasm_exec"'

test:
    go test ./...
    go test -tags audio_oto ./letgo
    {{wasm}} -run SeamDevice ./letgo

# The engine and binding suites as a browser runs them: js/wasm under node, one
# thread that switches goroutines only when one blocks. CI runs only the seam
# test under wasm, so run this before a PR (just ready does). -short trims the
# widest test matrices, which wasm runs about ten times slower than native.
test-wasm:
    {{wasm}} -short ./engine ./letgo

# Everything to run before opening a PR.
ready: test race lint test-wasm

# The tests that share a synth between goroutines, under the race detector,
# as CI runs them. Only those: the detector slows rendering many times over,
# and the rest of the suite is single-threaded, where it has nothing to find.
race:
    go test -race -run Concurrent ./engine ./letgo

# Render speed per mood as a multiple of real time, and allocations per
# slice. `just bench Shots` runs one benchmark.
bench pattern=".":
    go test -run '^$' -bench '{{pattern}}' ./engine

# The same as wasm under node: what a browser runs, several times slower.
bench-wasm pattern=".":
    {{wasm}} -run '^$' -bench '{{pattern}}' ./engine

# vet and staticcheck, native and for the wasm packages, as CI runs them.
# staticcheck is installed natively first: `go run` under GOOS=js would build
# the checker for js too.
lint:
    go vet ./...
    go vet -tags audio_oto ./...
    GOOS=js GOARCH=wasm go vet ./examples/designer ./examples/studio ./webaudio ./letgo
    GOBIN="$(go env GOPATH)/bin" go install {{staticcheck}}
    "$(go env GOPATH)/bin/staticcheck" ./...
    "$(go env GOPATH)/bin/staticcheck" -tags audio_oto ./...
    GOOS=js GOARCH=wasm "$(go env GOPATH)/bin/staticcheck" ./examples/designer ./examples/studio ./webaudio ./letgo

# build the mood designer and serve it on localhost:{{port}}
designer port="8373": designer-build
    python3 examples/serve.py {{port}} designer

# build the sound studio and serve it on localhost:{{port}}
studio port="8374": studio-build
    python3 examples/serve.py {{port}} studio

studio-build:
    GOOS=js GOARCH=wasm go build -o examples/studio/main.wasm ./examples/studio
    cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/studio/

designer-build:
    GOOS=js GOARCH=wasm go build -o examples/designer/main.wasm ./examples/designer
    cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/designer/
