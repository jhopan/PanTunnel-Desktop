#!/usr/bin/env bash
# Bangun core sing-box untuk PanTunnel Desktop dari source resmi SagerNet.
#
#   ./scripts/build-core.sh                 # versi default, host saat ini
#   ./scripts/build-core.sh 1.11.15 windows amd64
#
# Hasil: bin/PanTunnel-Core-<goos>-<goarch>[.exe]
#
# Tag build wajib `with_gvisor` (stack TUN yang dipakai config PanTunnel) dan
# `with_clash_api` supaya sama dengan core yang dipakai aplikasi Android.
set -euo pipefail

VER="${1:-1.11.15}"
TARGET_OS="${2:-$(go env GOOS)}"
TARGET_ARCH="${3:-$(go env GOARCH)}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$ROOT/bin"
OUT="$ROOT/bin/PanTunnel-Core-$TARGET_OS-$TARGET_ARCH"
if [ "$TARGET_OS" = "windows" ]; then OUT="$OUT.exe"; fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

git clone --depth 1 --branch "v$VER" https://github.com/SagerNet/sing-box.git "$WORK/sing-box"
cd "$WORK/sing-box"
CGO_ENABLED=0 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" go build -trimpath \
  -tags "with_gvisor,with_clash_api" \
  -ldflags "-s -w -X github.com/sagernet/sing-box/constant.Version=v$VER" \
  -o "$OUT" ./cmd/sing-box

echo "core : $OUT"
if [ "$TARGET_OS" = "$(go env GOOS)" ]; then
  "$OUT" version | head -4
fi
sha256sum "$OUT"