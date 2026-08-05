#!/bin/sh
# octo-cli installer — downloads the latest release from GitHub
# Usage: curl -fsSL https://raw.githubusercontent.com/Mininglamp-OSS/octo-cli/main/install.sh | sh

set -e

REPO="Mininglamp-OSS/octo-cli"
BINARY="octo-cli"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  linux)  OS="linux" ;;
  darwin) OS="darwin" ;;
  *)      echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)             echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Get latest version
echo "Fetching latest release..."
VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')
if [ -z "$VERSION" ]; then
  echo "Failed to fetch latest version"
  exit 1
fi
echo "Latest version: v${VERSION}"

# Download
FILENAME="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${FILENAME}"

echo "Downloading ${URL}..."
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

curl -fsSL "$URL" -o "${TMP_DIR}/${FILENAME}"

# Verify checksum
CHECKSUM_URL="https://github.com/${REPO}/releases/download/v${VERSION}/checksums.txt"
curl -fsSL "$CHECKSUM_URL" -o "${TMP_DIR}/checksums.txt"
EXPECTED=$(grep "$FILENAME" "${TMP_DIR}/checksums.txt" | awk '{print $1}')
if [ -n "$EXPECTED" ]; then
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL=$(sha256sum "${TMP_DIR}/${FILENAME}" | awk '{print $1}')
  else
    ACTUAL=$(shasum -a 256 "${TMP_DIR}/${FILENAME}" | awk '{print $1}')
  fi
  if [ "$EXPECTED" != "$ACTUAL" ]; then
    echo "Checksum mismatch! Expected: ${EXPECTED}, Got: ${ACTUAL}"
    exit 1
  fi
  echo "Checksum verified ✓"
fi

# Extract and install
tar -xzf "${TMP_DIR}/${FILENAME}" -C "${TMP_DIR}"

if [ -w "$INSTALL_DIR" ]; then
  mv "${TMP_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mv "${TMP_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
fi

chmod +x "${INSTALL_DIR}/${BINARY}"

echo ""
echo "✅ octo-cli v${VERSION} installed to ${INSTALL_DIR}/${BINARY}"
echo ""
echo "Quick start:"
echo "  export OCTO_BOT_TOKEN=\"your-bot-token\""
echo "  # OCTO_API_BASE_URL defaults to https://im.deepminer.com.cn"
echo "  octo-cli matter list"
