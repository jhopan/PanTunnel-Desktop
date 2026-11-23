#!/usr/bin/env bash
# Bangun core sing-box untuk PanTunnel Desktop dari source resmi SagerNet.
#
#   ./scripts/build-core.sh                 # versi default, host saat ini
#   ./scripts/build-core.sh 1.11.15 windows amd64
#   ./scripts/build-core.sh 1.11.15 darwin arm64
#
# Hasil dinamai sesuai yang dicari aplikasi:
#   windows/amd64 → bin/PanTunnel-Core-windows-amd64.exe
#   linux/amd64   → bin/PanTunnel-Core-linux-amd64
#   linux/arm64   → bin/PanTunnel-Core-linux-arm64
#   darwin/amd64  → bin/PanTunnel-Core-macos-intel
#   darwin/arm64  → bin/PanTunnel-Core-macos-apple
#
# Tag build wajib `with_gvisor` (stack TUN yang dipakai config PanTunnel) dan
# `with_clash_api` supaya sama dengan core yang dipakai aplikasi Android.
set -euo pipefail

VER="${1:-1.11.15}"
TARGET_OS="${2:-$(go env GOOS)}"
TARGET_ARCH="${3:-$(go env GOARCH)}"

case "$TARGET_OS/$TARGET_ARCH" in
  windows/amd64) NAME="windows-amd64.exe" ;;
  linux/amd64)   NAME="linux-amd64" ;;
  linux/arm64)   NAME="linux-arm64" ;;
  darwin/amd64)  NAME="macos-intel" ;;
  darwin/arm64)  NAME="macos-apple" ;;
  *)             NAME="$TARGET_OS-$TARGET_ARCH" ;;
esac

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$ROOT/bin"
OUT="$ROOT/bin/PanTunnel-Core-$NAME"

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
sha256sum "$OUT" 2>/dev/null || shasum -a 256 "$OUT"