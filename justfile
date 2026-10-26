staticcheck := "honnef.co/go/tools/cmd/staticcheck@2026.2.2"

test:
    go test ./...

# vet and staticcheck, native and for the wasm packages, as CI runs them.
# staticcheck is installed natively first: `go run` under GOOS=js would build
# the checker for js too.
lint:
    go vet ./...
    GOOS=js GOARCH=wasm go vet ./examples/designer ./webaudio
    GOBIN="$(go env GOPATH)/bin" go install {{staticcheck}}
    "$(go env GOPATH)/bin/staticcheck" ./...
    GOOS=js GOARCH=wasm "$(go env GOPATH)/bin/staticcheck" ./examples/designer ./webaudio

# build the mood designer and serve it on localhost:{{port}}
designer port="8373": designer-build
    python3 examples/serve.py {{port}} designer

designer-build:
    GOOS=js GOARCH=wasm go build -o examples/designer/main.wasm ./examples/designer
    cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/designer/
