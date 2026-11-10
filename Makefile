VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/h4ux/homeowl/internal/config.Version=$(VERSION)

.PHONY: build build-lite run test lint tidy clean

# Full build (needs network access to fetch tailscale.com once)
build:
	CGO_ENABLED=1 go build -trimpath -ldflags "$(LDFLAGS)" -o homeowl ./cmd/homeowl

# Without built-in Tailscale (smaller, fewer dependencies)
build-lite:
	CGO_ENABLED=1 go build -tags notsnet -trimpath -ldflags "$(LDFLAGS)" -o homeowl ./cmd/homeowl

run: build-lite
	./homeowl run --data ./.devdata

test:
	go test -tags notsnet ./...

lint:
	gofmt -l . | (! grep .)
	go vet -tags notsnet ./...

tidy:
	go get tailscale.com/tsnet@latest
	go mod tidy

clean:
	rm -rf homeowl homeowl.exe dist .devdata
