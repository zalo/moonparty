# Building Moonparty

Moonparty is a pure Go application with no CGO dependencies, making it easy to build on any platform.

## Prerequisites

- **Go 1.21+**: https://go.dev/dl/

That's it! No C compiler, CMake, or other dependencies required.

## Building

### Linux / macOS / Windows

```bash
# Clone the repository
git clone https://github.com/zalo/moonparty.git
cd moonparty

# Build
go build -o moonparty ./cmd/moonparty

# Or use make
make build
# Binary will be at: build/moonparty
```

### Cross-Compilation

Since there's no CGO, cross-compilation is straightforward:

```bash
# Build for Windows from Linux/macOS
GOOS=windows GOARCH=amd64 go build -o moonparty.exe ./cmd/moonparty

# Build for Linux from Windows/macOS
GOOS=linux GOARCH=amd64 go build -o moonparty ./cmd/moonparty

# Build for macOS (Intel) from Linux/Windows
GOOS=darwin GOARCH=amd64 go build -o moonparty ./cmd/moonparty

# Build for macOS (Apple Silicon) from Linux/Windows
GOOS=darwin GOARCH=arm64 go build -o moonparty ./cmd/moonparty
```

## Running

```bash
# Linux/macOS
./moonparty -host <sunshine-ip>

# Windows
moonparty.exe -host <sunshine-ip>
```

### Command Line Options

| Flag | Default | Description |
|------|---------|-------------|
| `-host` | `localhost` | Sunshine server address |
| `-port` | `47989` | Sunshine Moonlight API port |
| `-listen` | `:8080` | Web server listen address |
| `-limelight` | `true` | Use moonlight-common-go backend |
| `-no-limelight` | `false` | Use basic streaming backend instead |
| `-new-identity` | `false` | Regenerate client identity |

## Development

### Quick rebuild
```bash
make dev
```

### Run tests
```bash
make test
```

### Clean build
```bash
make clean
make build
```

### Format code
```bash
make fmt
```
