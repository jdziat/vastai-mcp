VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
export CGO_ENABLED=0

.PHONY: build install test vet lint vulncheck check clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/vastai-mcp ./cmd/vastai-mcp

install:
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/vastai-mcp

test:
	go test -race -cover ./...

vet:
	go vet ./...

lint:
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./...
	go run github.com/securego/gosec/v2/cmd/gosec@latest -exclude-generated ./...

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

check: vet test vulncheck

clean:
	rm -rf bin dist
