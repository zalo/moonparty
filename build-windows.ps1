# PowerShell build script for Windows
# Requires: MSYS2 with MinGW-w64, CMake, and OpenSSL

$ErrorActionPreference = "Stop"

# Configuration
$BuildDir = "build"
$MoonlightDir = "moonlight-common-c"

# Check for required tools
function Check-Command {
    param($Command, $InstallHint)
    if (!(Get-Command $Command -ErrorAction SilentlyContinue)) {
        Write-Error "$Command not found. $InstallHint"
        exit 1
    }
}

Write-Host "Checking prerequisites..." -ForegroundColor Cyan

# Check for CMake
Check-Command "cmake" "Install from https://cmake.org/download/ or via 'winget install Kitware.CMake'"

# Check for GCC (MinGW)
Check-Command "gcc" "Install MSYS2 from https://www.msys2.org/ then run: pacman -S mingw-w64-x86_64-gcc"

# Check for Go
Check-Command "go" "Install from https://go.dev/dl/"

Write-Host "All prerequisites found!" -ForegroundColor Green

# Initialize submodules if needed
if (!(Test-Path "$MoonlightDir/src/Limelight.h")) {
    Write-Host "Initializing git submodules..." -ForegroundColor Cyan
    git submodule update --init --recursive
}

# Create build directory
if (!(Test-Path $BuildDir)) {
    New-Item -ItemType Directory -Path $BuildDir | Out-Null
}

# Build moonlight-common-c
Write-Host "Building moonlight-common-c..." -ForegroundColor Cyan
Push-Location $BuildDir

cmake "..\$MoonlightDir" `
    -G "MinGW Makefiles" `
    -DBUILD_SHARED_LIBS=OFF `
    -DCMAKE_POSITION_INDEPENDENT_CODE=ON `
    -DCMAKE_BUILD_TYPE=Release

if ($LASTEXITCODE -ne 0) {
    Pop-Location
    Write-Error "CMake configuration failed"
    exit 1
}

mingw32-make -j$env:NUMBER_OF_PROCESSORS

if ($LASTEXITCODE -ne 0) {
    Pop-Location
    Write-Error "Build failed"
    exit 1
}

Pop-Location
Write-Host "moonlight-common-c built successfully!" -ForegroundColor Green

# Build Go application
Write-Host "Building moonparty..." -ForegroundColor Cyan

$env:CGO_ENABLED = "1"
$env:CC = "gcc"

go build -o "$BuildDir\moonparty.exe" .\cmd\moonparty

if ($LASTEXITCODE -ne 0) {
    Write-Error "Go build failed"
    exit 1
}

Write-Host "Build complete: $BuildDir\moonparty.exe" -ForegroundColor Green
Write-Host ""
Write-Host "Usage:" -ForegroundColor Yellow
Write-Host "  .\$BuildDir\moonparty.exe -host <sunshine-ip>"
Write-Host "  .\$BuildDir\moonparty.exe -host <sunshine-ip> -no-limelight  # Use native Go backend"
