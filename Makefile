# Makefile for moonparty - Pure Go build

BUILD_DIR := build

.PHONY: all clean build run test dev fmt lint help

all: build

# Build the Go application
build:
	@echo "Building moonparty..."
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/moonparty ./cmd/moonparty
	@echo "Build complete: $(BUILD_DIR)/moonparty"

# Run the application
run: build
	./$(BUILD_DIR)/moonparty

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)
	go clean

# Development build
dev:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/moonparty ./cmd/moonparty

# Test the build
test:
	go test ./...

# Format Go code
fmt:
	go fmt ./...

# Lint Go code
lint:
	golangci-lint run ./...

help:
	@echo "moonparty Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  all              - Build everything (default)"
	@echo "  build            - Build moonparty binary"
	@echo "  run              - Build and run moonparty"
	@echo "  dev              - Quick development build"
	@echo "  test             - Run tests"
	@echo "  clean            - Remove build artifacts"
	@echo "  fmt              - Format Go code"
	@echo "  lint             - Lint Go code"
	@echo "  help             - Show this help"
