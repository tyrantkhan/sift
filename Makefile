.PHONY: build install test lint clean setup screenshots

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
LDFLAGS := -ldflags "-X github.com/tyrantkhan/sift/internal/cli.Version=$(VERSION)"

setup:
	mise install
	lefthook install

build:
	go build $(LDFLAGS) -o bin/sift ./cmd/sift

install:
	go install $(LDFLAGS) ./cmd/sift

test:
	go test ./...

lint:
	golangci-lint run

clean:
	rm -rf bin

# Redraws the README's gif from docs/tapes. vhs needs ttyd and ffmpeg; on macOS, brew install vhs.
screenshots:
	for tape in docs/tapes/*.tape; do vhs "$$tape"; done
