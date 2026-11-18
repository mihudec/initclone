.PHONY: build test check clean

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(BUILD_DATE)

build:
	mkdir -p dist/macos/amd64 dist/macos/arm64 dist/linux/amd64 dist/linux/arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath \
		-ldflags '$(LDFLAGS)' -o dist/macos/amd64/initclone .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
		-ldflags '$(LDFLAGS)' -o dist/macos/arm64/initclone .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
		-ldflags '$(LDFLAGS)' -o dist/linux/amd64/initclone .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
		-ldflags '$(LDFLAGS)' -o dist/linux/arm64/initclone .

test:
	go test ./...

check:
	go vet ./...
	go test -race ./...

clean:
	rm -rf dist
