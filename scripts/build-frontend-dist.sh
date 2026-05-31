#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_FILE="${1:-frontend-dist.tgz}"
APP_VERSION="${APP_VERSION:-$(cd "$ROOT_DIR" && git rev-parse --short HEAD)}"

echo "Building frontend assets with APP_VERSION=${APP_VERSION}"

cd "$ROOT_DIR/web/default"
bun install --frozen-lockfile
VITE_REACT_APP_VERSION="$APP_VERSION" bun run build

cd "$ROOT_DIR/web/classic"
bun install --frozen-lockfile
VITE_REACT_APP_VERSION="$APP_VERSION" bun run build

cd "$ROOT_DIR"
tar -czf "$OUT_FILE" web/default/dist web/classic/dist

echo "Created $ROOT_DIR/$OUT_FILE"
