#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND_ARCHIVE="${1:-${FRONTEND_ARCHIVE:-}}"
SERVICE_NAME="${SERVICE_NAME:-yihi-api}"
BINARY_NAME="${BINARY_NAME:-yihi-api}"
SKIP_GIT_PULL="${SKIP_GIT_PULL:-false}"

cd "$ROOT_DIR"

if [[ "$SKIP_GIT_PULL" != "true" ]]; then
  echo "Updating repository..."
  git pull --ff-only
fi

if [[ -n "$FRONTEND_ARCHIVE" ]]; then
  if [[ ! -f "$FRONTEND_ARCHIVE" ]]; then
    echo "Missing frontend archive: $ROOT_DIR/$FRONTEND_ARCHIVE" >&2
    echo "Upload it first, for example: scp frontend-dist.tgz root@server:$ROOT_DIR/" >&2
    exit 1
  fi

  echo "Extracting frontend assets from $FRONTEND_ARCHIVE..."
  tar -xzf "$FRONTEND_ARCHIVE"
else
  echo "Skipping frontend asset extraction. Pass an archive path to update embedded frontend assets."
fi

if [[ -f "$BINARY_NAME" ]]; then
  BACKUP_NAME="${BINARY_NAME}.bak.$(date +%Y%m%d%H%M%S)"
  echo "Backing up existing binary to $BACKUP_NAME..."
  cp "$BINARY_NAME" "$BACKUP_NAME"
fi

APP_VERSION="${APP_VERSION:-$(git rev-parse --short HEAD)}"
echo "Building Go service with APP_VERSION=${APP_VERSION}..."
go build -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=${APP_VERSION}'" -o "$BINARY_NAME"
chmod +x "$BINARY_NAME"

echo "Restarting systemd service: $SERVICE_NAME..."
systemctl restart "$SERVICE_NAME"

echo "Service status:"
systemctl status "$SERVICE_NAME" --no-pager
