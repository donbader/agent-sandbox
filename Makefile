.PHONY: build test lint check clean sync-gateway

BINARY := agent-sandbox
GO := go

build: sync-gateway
	cd cmd/agent-sandbox && $(GO) build -o ../../$(BINARY) .

test:
	$(GO) test ./...

lint:
	golangci-lint run ./...

check: lint test

clean:
	rm -f $(BINARY)
	rm -rf .build/

# Sync gateway source into internal/generate/_gateway-src/ for go:embed.
# Run this after modifying gateway/ source files.
sync-gateway:
	rm -rf internal/generate/_gateway-src
	mkdir -p internal/generate/_gateway-src
	cp -r gateway/cmd gateway/internal internal/generate/_gateway-src/
	cp gateway/go.mod internal/generate/_gateway-src/go.mod.embed
	cp gateway/go.sum internal/generate/_gateway-src/go.sum.embed
