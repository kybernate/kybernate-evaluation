#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
BIN_DIR="$ROOT_DIR/bin"
CMD_DIR="$ROOT_DIR/cmd/kybernate-restore-pod"

mkdir -p "$BIN_DIR"

echo "Building kybernate-restore-pod..."
cd "$CMD_DIR"
go build -o "$BIN_DIR/kybernate-restore-pod" .

echo "Build complete: $BIN_DIR/kybernate-restore-pod"
echo "Usage: $BIN_DIR/kybernate-restore-pod --checkpoint <path> --image <image> [options]"
