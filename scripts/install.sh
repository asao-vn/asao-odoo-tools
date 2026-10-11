#!/bin/sh
# odoo-cli installer: downloads the latest released binary for this machine, checks it against the
# release's checksums, and installs it into /usr/local/bin (or $ODOO_CLI_INSTALL_DIR).
#   curl -fsSL https://raw.githubusercontent.com/asao-vn/asao-odoo-tools/main/scripts/install.sh | sh
set -eu

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) echo "Unsupported operating system: $(uname -s). On Windows, download odoo-cli_windows_amd64.exe from https://github.com/asao-vn/asao-odoo-tools/releases" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

NAME="odoo-cli_${OS}_${ARCH}"
BASE="https://github.com/asao-vn/asao-odoo-tools/releases/latest/download"
INSTALL_DIR="${ODOO_CLI_INSTALL_DIR:-/usr/local/bin}"
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading odoo-cli for ${OS}/${ARCH}..."
curl -fsSL "$BASE/$NAME" -o "$TMP_DIR/$NAME"
curl -fsSL "$BASE/checksums.txt" -o "$TMP_DIR/checksums.txt"
EXPECTED=$(awk -v name="$NAME" '$2 == name { print $1 }' "$TMP_DIR/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$TMP_DIR/$NAME" | awk '{ print $1 }')
else
  ACTUAL=$(shasum -a 256 "$TMP_DIR/$NAME" | awk '{ print $1 }')
fi
if [ -z "$EXPECTED" ] || [ "$EXPECTED" != "$ACTUAL" ]; then
  echo "Checksum mismatch for $NAME: refusing to install." >&2
  exit 1
fi

chmod +x "$TMP_DIR/$NAME"
if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP_DIR/$NAME" "$INSTALL_DIR/odoo-cli"
else
  sudo mv "$TMP_DIR/$NAME" "$INSTALL_DIR/odoo-cli"
fi
echo "Installed $INSTALL_DIR/odoo-cli. Start with: odoo-cli --help"
