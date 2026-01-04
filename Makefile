# Makefile for moonparty with moonlight-common-c integration

BUILD_DIR := build
MOONLIGHT_DIR := moonlight-common-c

.PHONY: all clean deps moonlight-common-c build run

all: deps build

# Build moonlight-common-c as a static library
deps: moonlight-common-c

moonlight-common-c:
	@echo "Building moonlight-common-c..."
	@mkdir -p $(BUILD_DIR)
	@cd $(BUILD_DIR) && cmake ../$(MOONLIGHT_DIR) \
		-DBUILD_SHARED_LIBS=OFF \
		-DCMAKE_POSITION_INDEPENDENT_CODE=ON \
		-DCMAKE_BUILD_TYPE=Release
	@cd $(BUILD_DIR) && make -j$$(nproc)
	@echo "moonlight-common-c built successfully"

# Build the Go application
build: moonlight-common-c
	@echo "Building moonparty..."
	PATH="/usr/local/go/bin:$$PATH" CGO_ENABLED=1 go build -o $(BUILD_DIR)/moonparty ./cmd/moonparty
	@echo "Build complete: $(BUILD_DIR)/moonparty"

# Run the application
run: build
	./$(BUILD_DIR)/moonparty

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)
	go clean

# Development build (faster, no CGO optimization)
dev:
	@mkdir -p $(BUILD_DIR)
	@if [ ! -f $(BUILD_DIR)/libmoonlight-common-c.a ]; then \
		$(MAKE) moonlight-common-c; \
	fi
	CGO_ENABLED=1 go build -o $(BUILD_DIR)/moonparty ./cmd/moonparty

# Test the build
test: deps
	CGO_ENABLED=1 go test ./...

# Install dependencies (system packages)
install-deps:
	@echo "Installing system dependencies..."
	@if command -v apt-get >/dev/null 2>&1; then \
		sudo apt-get update && sudo apt-get install -y \
			build-essential \
			cmake \
			libssl-dev \
			pkg-config; \
	elif command -v dnf >/dev/null 2>&1; then \
		sudo dnf install -y \
			gcc \
			cmake \
			openssl-devel \
			pkgconfig; \
	elif command -v brew >/dev/null 2>&1; then \
		brew install cmake openssl pkg-config; \
	else \
		echo "Please install: cmake, openssl-dev, build-essential"; \
	fi

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
	@echo "  deps             - Build moonlight-common-c library"
	@echo "  build            - Build moonparty binary"
	@echo "  run              - Build and run moonparty"
	@echo "  dev              - Quick development build"
	@echo "  test             - Run tests"
	@echo "  clean            - Remove build artifacts"
	@echo "  install-deps     - Install system dependencies"
	@echo "  fmt              - Format Go code"
	@echo "  lint             - Lint Go code"
	@echo "  help             - Show this help"
