@echo off
REM Windows build script for moonparty
REM Requires: MSYS2 with MinGW-w64 in PATH, CMake, Go

setlocal enabledelayedexpansion

set BUILD_DIR=build
set MOONLIGHT_DIR=moonlight-common-c

echo Checking prerequisites...

where cmake >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo ERROR: cmake not found
    echo Install from https://cmake.org/download/ or: winget install Kitware.CMake
    exit /b 1
)

where gcc >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo ERROR: gcc not found
    echo Install MSYS2 from https://www.msys2.org/
    echo Then run: pacman -S mingw-w64-x86_64-gcc mingw-w64-x86_64-openssl
    echo Add C:\msys64\mingw64\bin to your PATH
    exit /b 1
)

where go >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo ERROR: go not found
    echo Install from https://go.dev/dl/
    exit /b 1
)

echo All prerequisites found!

REM Initialize submodules
if not exist "%MOONLIGHT_DIR%\src\Limelight.h" (
    echo Initializing git submodules...
    git submodule update --init --recursive
)

REM Create build directory
if not exist "%BUILD_DIR%" mkdir "%BUILD_DIR%"

REM Build moonlight-common-c
echo Building moonlight-common-c...
pushd "%BUILD_DIR%"

cmake "..\%MOONLIGHT_DIR%" -G "MinGW Makefiles" -DBUILD_SHARED_LIBS=OFF -DCMAKE_POSITION_INDEPENDENT_CODE=ON -DCMAKE_BUILD_TYPE=Release
if %ERRORLEVEL% neq 0 (
    popd
    echo CMake configuration failed
    exit /b 1
)

mingw32-make -j%NUMBER_OF_PROCESSORS%
if %ERRORLEVEL% neq 0 (
    popd
    echo Build failed
    exit /b 1
)

popd
echo moonlight-common-c built successfully!

REM Build Go application
echo Building moonparty...
set CGO_ENABLED=1
set CC=gcc

go build -o "%BUILD_DIR%\moonparty.exe" .\cmd\moonparty
if %ERRORLEVEL% neq 0 (
    echo Go build failed
    exit /b 1
)

echo.
echo Build complete: %BUILD_DIR%\moonparty.exe
echo.
echo Usage:
echo   %BUILD_DIR%\moonparty.exe -host ^<sunshine-ip^>
echo   %BUILD_DIR%\moonparty.exe -host ^<sunshine-ip^> -no-limelight
