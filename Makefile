.PHONY: check build test lint fmt

BINARY := bin/korai

build:
	go build -o $(BINARY) ./cmd/korai

check: fmt lint build test

fmt:
	@echo "--- gofmt"
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi
	@echo "--- goimports"
	@out=$$(goimports -l .); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

lint:
	@echo "--- go vet"
	go vet ./...
	@echo "--- golangci-lint"
	golangci-lint run

test:
	@echo "--- go test -race"
	go test -race ./...
