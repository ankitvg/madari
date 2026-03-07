BINARY=madari

.PHONY: build run test

build:
	go build $(if $(VERSION),-ldflags "-X main.version=$(VERSION)") -o bin/$(BINARY) ./cmd/madari

run:
	go run ./cmd/madari

test:
	go test ./...
