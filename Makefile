MODULE  := github.com/eftakhairul/uc
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X '$(MODULE)/internal/commands.Version=$(VERSION)'
BIN     := dist/uc

.PHONY: build build-mac install test test-e2e vet fmt clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/uc

# Universal macOS binary (Intel + Apple Silicon) via lipo.
build-mac:
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/uc-arm64 ./cmd/uc
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/uc-amd64 ./cmd/uc
	lipo -create -output $(BIN) dist/uc-arm64 dist/uc-amd64
	rm -f dist/uc-arm64 dist/uc-amd64
	lipo -info $(BIN)

install: build-mac
	mkdir -p $(HOME)/bin
	cp $(BIN) $(HOME)/bin/uc
	@echo "installed $(HOME)/bin/uc ($(VERSION))"

test:
	go test ./...

# Full CLI surface against the compiled binary, in a Linux container via
# testcontainers-go. Requires a running Docker daemon; not part of `test`
# since it's slower and has an external dependency.
test-e2e:
	go test -tags e2e ./e2e/...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -rf dist
