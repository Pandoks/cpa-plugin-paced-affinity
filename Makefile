EXT := $(if $(filter Darwin,$(shell uname -s)),dylib,so)
VERSION ?= $(shell git describe --tags 2>/dev/null || echo dev)

.PHONY: build test

build:
	go build -buildmode=c-shared -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/paced-affinity.$(EXT) .
	rm -f dist/paced-affinity.h

test:
	go vet ./...
	go test -race ./...
