# Building Moonparty

Moonparty uses CGO to integrate with moonlight-common-c for proper Moonlight protocol support. This requires a C compiler and some dependencies.

## Prerequisites

### All Platforms
- **Go 1.21+**: https://go.dev/dl/
- **Git**: For cloning submodules
- **CMake 3.10+**: For building moonlight-common-c

### Linux (Ubuntu/Debian)
```bash
sudo apt-get update
sudo apt-get install -y build-essential cmake libssl-dev pkg-config
```

### Linux (Fedora/RHEL)
```bash
sudo dnf install gcc cmake openssl-devel pkgconfig
```

### macOS
```bash
# Install Xcode Command Line Tools
xcode-select --install

# Install dependencies via Homebrew
brew install cmake openssl pkg-config

# Set OpenSSL paths for CGO
export PKG_CONFIG_PATH="/opt/homebrew/opt/openssl@3/lib/pkgconfig:$PKG_CONFIG_PATH"
```

### Windows

Windows requires MinGW-w64 for CGO compilation.

#### Option 1: MSYS2 (Recommended)

1. **Install MSYS2** from https://www.msys2.org/

2. **Open MSYS2 MINGW64 terminal** and install packages:
   ```bash
   pacman -Syu
   pacman -S mingw-w64-x86_64-gcc mingw-w64-x86_64-cmake mingw-w64-x86_64-openssl mingw-w64-x86_64-pkg-config make
   ```

3. **Add MinGW to your PATH**:
   - Add `C:\msys64\mingw64\bin` to your Windows PATH
   - Or run builds from the MSYS2 MINGW64 terminal

4. **Install Go** from https://go.dev/dl/

#### Option 2: Chocolatey
```powershell
choco install mingw cmake golang
```

## Building

### Linux / macOS

```bash
# Clone with submodules
git clone --recursive https://github.com/zalo/moonparty.git
cd moonparty

# Or if already cloned:
git submodule update --init --recursive

# Build everything
make build

# Binary will be at: build/moonparty
```

### Windows (PowerShell)

```powershell
# Clone with submodules
git clone --recursive https://github.com/zalo/moonparty.git
cd moonparty

# Run build script
.\build-windows.ps1

# Or use the batch file
.\build-windows.bat

# Binary will be at: build\moonparty.exe
```

### Windows (MSYS2 Terminal)

```bash
# From MSYS2 MINGW64 terminal
git clone --recursive https://github.com/zalo/moonparty.git
cd moonparty

# Build moonlight-common-c
mkdir -p build && cd build
cmake ../moonlight-common-c -G "MinGW Makefiles" -DBUILD_SHARED_LIBS=OFF -DCMAKE_BUILD_TYPE=Release
mingw32-make -j$(nproc)
cd ..

# Build Go application
CGO_ENABLED=1 CC=gcc go build -o build/moonparty.exe ./cmd/moonparty
```

## Running

```bash
# Linux/macOS
./build/moonparty -host <sunshine-ip>

# Windows
.\build\moonparty.exe -host <sunshine-ip>
```

### Command Line Options

| Flag | Default | Description |
|------|---------|-------------|
| `-host` | `localhost` | Sunshine server address |
| `-port` | `47989` | Sunshine Moonlight API port |
| `-listen` | `:8080` | Web server listen address |
| `-limelight` | `true` | Use moonlight-common-c backend |
| `-no-limelight` | `false` | Use native Go backend instead |
| `-new-identity` | `false` | Regenerate client identity |

## Troubleshooting

### "gcc not found" on Windows
Make sure MinGW-w64 bin directory is in your PATH:
```powershell
$env:PATH = "C:\msys64\mingw64\bin;$env:PATH"
```

### "cannot find -lssl" or "-lcrypto"
OpenSSL development files are missing:
- **Linux**: `sudo apt-get install libssl-dev`
- **macOS**: `brew install openssl` and set PKG_CONFIG_PATH
- **Windows (MSYS2)**: `pacman -S mingw-w64-x86_64-openssl`

### Submodule not initialized
```bash
git submodule update --init --recursive
```

### CMake can't find compiler on Windows
Ensure you're using the correct generator:
```bash
cmake .. -G "MinGW Makefiles"
```

## Development

### Rebuilding moonlight-common-c only
```bash
cd build && make
```

### Quick Go rebuild (skip C library)
```bash
make dev
```

### Clean build
```bash
make clean
make build
```
