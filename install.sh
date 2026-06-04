#!/bin/bash
set -euo pipefail

REPO="donbader/agent-sandbox"
BINARY="agent-sandbox"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux) OS="linux" ;;
  darwin) OS="darwin" ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect arch
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Get latest version
echo "Fetching latest release..."
RELEASE_JSON=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest")
VERSION=$(echo "$RELEASE_JSON" | jq -r .tag_name 2>/dev/null || echo "$RELEASE_JSON" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$VERSION" ]; then
  echo "Failed to fetch latest version"
  exit 1
fi
echo "Latest version: $VERSION"

# Download
FILENAME="${BINARY}_${VERSION#v}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"

echo "Downloading ${FILENAME}..."
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "$URL" -o "$TMP/$FILENAME"

# Verify checksum
CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  echo "Verifying checksum..."
  if curl -fsSL "$CHECKSUMS_URL" -o "$TMP/checksums.txt"; then
    EXPECTED=$(grep "$FILENAME" "$TMP/checksums.txt" | awk '{print $1}')
    if [ -z "$EXPECTED" ]; then
      echo "Error: checksum for $FILENAME not found in checksums.txt"
      exit 1
    fi
    ACTUAL=$(sha256sum "$TMP/$FILENAME" | awk '{print $1}')
    if [ "$EXPECTED" != "$ACTUAL" ]; then
      echo "Error: checksum verification failed!"
      echo "  Expected: $EXPECTED"
      echo "  Actual:   $ACTUAL"
      exit 1
    fi
    echo "Checksum verified."
  else
    echo "Warning: could not download checksums.txt, skipping verification"
  fi
else
  echo "Warning: sha256sum not available, skipping checksum verification"
fi

tar xzf "$TMP/$FILENAME" -C "$TMP"

# Install
if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"
else
  echo "Installing to $INSTALL_DIR (requires sudo)..."
  sudo mv "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"
fi

chmod +x "$INSTALL_DIR/$BINARY"
echo "Installed $BINARY $VERSION to $INSTALL_DIR/$BINARY"
