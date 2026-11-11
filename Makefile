VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o ssh-menu ./cmd/ssh-menu

test:
	go vet ./...
	go test ./...

install: build
	./install.sh --binary ./ssh-menu

.PHONY: build test install
