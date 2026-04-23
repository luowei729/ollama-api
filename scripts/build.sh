#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_NAME="ollama-openai-proxy"
VERSION="${VERSION:-dev}"
DIST_DIR="$ROOT_DIR/dist"
LDFLAGS="-s -w -X main.version=${VERSION}"

mkdir -p "$DIST_DIR"

echo "building Windows amd64 binary..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -trimpath -ldflags "$LDFLAGS" \
  -o "$DIST_DIR/${APP_NAME}-windows-amd64.exe" \
  ./cmd/ollama-openai-proxy

echo "building Linux amd64 binary..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "$LDFLAGS" \
  -o "$DIST_DIR/${APP_NAME}-linux-amd64" \
  ./cmd/ollama-openai-proxy

echo "artifacts written to $DIST_DIR"

