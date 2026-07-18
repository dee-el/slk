.PHONY: build build-macos test lint run clean

BINARY=slk
BUILD_DIR=bin

build:
	go build -ldflags="-s -w" -trimpath -o $(BUILD_DIR)/$(BINARY) ./cmd/slk

build-macos:
	@test "$$(go env GOOS)" = "darwin" || (echo "build-macos requires macOS" && exit 1)
	CGO_ENABLED=1 go build -ldflags="-s -w" -trimpath -o $(BUILD_DIR)/$(BINARY) ./cmd/slk
	@otool -L $(BUILD_DIR)/$(BINARY) | grep -q 'AppKit.framework' || \
		(echo "macOS clipboard framework missing" && exit 1)

test:
	go test ./... -v -race

lint:
	golangci-lint run ./...

run: build
	./$(BUILD_DIR)/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)
