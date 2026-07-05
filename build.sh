#!/usr/bin/env bash
# Cross-compile timpi-cise for every supported platform into ./dist.
# Requires only the Go toolchain — no C compiler, no per-target environment.
set -euo pipefail

cd "$(dirname "$0")"
mkdir -p dist

VERSION="${1:-dev}"
PKG="./cmd/timpicise"
LDFLAGS="-s -w"   # strip symbols for smaller binaries

build() {
  local goos="$1" goarch="$2" goarm="${3:-}" out="$4"
  echo ">> $out  ($goos/$goarch${goarm:+ v$goarm})"
  GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" CGO_ENABLED=0 \
    go build -trimpath -ldflags "$LDFLAGS" -o "dist/$out" "$PKG"
}

# Windows
build windows amd64 ""  "timpicise-windows-amd64.exe"
build windows arm64 ""  "timpicise-windows-arm64.exe"

# Linux desktop/server
build linux   amd64 ""  "timpicise-linux-amd64"
build linux   arm64 ""  "timpicise-linux-arm64"      # Raspberry Pi OS 64-bit (Pi 3/4/5)
build linux   arm   "7" "timpicise-linux-armv7"       # Raspberry Pi OS 32-bit
build linux   arm   "6" "timpicise-linux-armv6"       # Pi Zero / Pi 1

# macOS
build darwin  amd64 ""  "timpicise-darwin-amd64"
build darwin  arm64 ""  "timpicise-darwin-arm64"

echo
echo "Done. Artifacts in ./dist:"
ls -la dist
