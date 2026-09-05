#!/bin/sh
# Installs the latest bubblegit release binary for this OS and CPU.
#
#   curl -fsSL https://raw.githubusercontent.com/f3xp/bubblegit/main/install.sh | sh
#
# BUBBLEGIT_VERSION=v0.1.0 pins a release. BIN_DIR picks the install directory;
# the default is /usr/local/bin when writable, otherwise ~/.local/bin.
set -eu

repo=f3xp/bubblegit

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $os in
  darwin|linux) ;;
  *) echo "install.sh: unsupported OS $os; download from https://github.com/$repo/releases" >&2; exit 1 ;;
esac

case $(uname -m) in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "install.sh: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac

# Newest release, prereleases included; /releases/latest skips those.
version=${BUBBLEGIT_VERSION:-$(curl -fsSL "https://api.github.com/repos/$repo/releases" \
  | grep -m1 '"tag_name"' | sed 's/.*"\(v[^"]*\)".*/\1/')}
[ -n "$version" ] || { echo "install.sh: could not find a release" >&2; exit 1; }

asset="bubblegit_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"

echo "Downloading bubblegit $version for $os/$arch"
curl -fsSL -o "$asset" "$base/$asset"
curl -fsSL -o checksums.txt "$base/checksums.txt"

if command -v sha256sum >/dev/null 2>&1; then sum=$(sha256sum "$asset"); else sum=$(shasum -a 256 "$asset"); fi
grep -q "^${sum%% *}  $asset\$" checksums.txt || { echo "install.sh: checksum mismatch for $asset" >&2; exit 1; }

tar xzf "$asset"

if [ -z "${BIN_DIR:-}" ]; then
  if [ -w /usr/local/bin ]; then BIN_DIR=/usr/local/bin; else BIN_DIR=$HOME/.local/bin; fi
fi
mkdir -p "$BIN_DIR"
install -m 755 "bubblegit_${os}_${arch}/bubblegit" "$BIN_DIR/bubblegit"

echo "Installed $BIN_DIR/bubblegit"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Add $BIN_DIR to your PATH." ;;
esac
