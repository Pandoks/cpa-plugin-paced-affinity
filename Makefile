EXT := $(if $(filter Darwin,$(shell uname -s)),dylib,so)
VERSION ?= $(shell git describe --tags 2>/dev/null || echo dev)

.PHONY: build test sim

build:
	go build -buildmode=c-shared -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/paced-affinity.$(EXT) .
	rm -f dist/paced-affinity.h

test:
	go vet ./...
	go test -race ./...

sim: # replay your own Claude Code and Codex logs; see docs/benchmarks.md
	python3 -I testdata/extract.py testdata/work.tsv
	go test -tags sim -run TestSim -count=1 -v .
