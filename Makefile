BINARY_NAME := poly
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

.PHONY: build run test lint clean docker proto fmt vet mod-download check

build:
	go build -ldflags="-s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)" \
		-o bin/$(BINARY_NAME) ./cmd/$(BINARY_NAME)

run: build
	STAGE=local	./bin/$(BINARY_NAME)

test:
	go test -cover -coverprofile coverage.out ./...
	go tool cover -func coverage.out

check : lint fmt vet

lint:
	golangci-lint run --timeout 5m

fmt:
	go fmt ./...

vet:
	go vet ./...

mod-download:
	go mod download
	go mod tidy

clean:
	rm -rf bin/ coverage.out


install-protoc:
	@which protoc > /dev/null || (echo "protoc not found, install: https://github.com/protocolbuffers/protobuf/releases" && exit 1)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

proto: install-protoc
	@rm -rf pkg/grpc/$(BINARY_NAME)
	@SHARED_INCLUDES=$$(find api/proto/shared -maxdepth 1 -type d -name 'v*' | sort | while read dir; do echo "-I $$dir -I $$dir/deps"; done | tr '\n' ' '); \
	for version in $$(find api/proto -maxdepth 1 -type d -name 'v*' | sort); do \
		ver=$$(basename $$version); \
		echo "Generating proto for $$ver..."; \
		mkdir -p pkg/grpc/$(BINARY_NAME)/$$ver; \
		protoc -I api/proto $$SHARED_INCLUDES \
			--go_out=pkg/grpc/$(BINARY_NAME) --go_opt=paths=source_relative \
			--go-grpc_out=pkg/grpc/$(BINARY_NAME) --go-grpc_opt=paths=source_relative \
			api/proto/$$ver/*.proto; \
	done
	@rm -rf pkg/grpc/$(BINARY_NAME)/shared
	@go mod tidy
