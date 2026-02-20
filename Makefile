BINARY=madari

.PHONY: build run test

build:
	go build -o bin/$(BINARY) ./cmd/madari

run:
	go run ./cmd/madari

test:
	go test ./...
