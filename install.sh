#!/usr/bin/env bash
# ACP installer — fetches a public release archive and installs the binaries.
#
#   curl -fsSL https://raw.githubusercontent.com/ab0t-com/acp/main/install.sh | bash
#
# Env vars:
#   ACP_REPO=ab0t-com/acp
#   ACP_INSTALL_DIR=$HOME/.local/bin
#   ACP_VERSION=latest|vX.Y.Z
#   ACP_BINS="acp coordd acp-mcp"     # which binaries to install (default: all)
#   ACP_CHECKSUMS=1                    # verify sha256 when checksums.txt is available
#   ACP_DRY_RUN=0|1
#   ACP_RELEASE_BASE_URL=https://github.com/<repo>/releases/download
#   ACP_MIRROR_BASE_URL=https://raw.githubusercontent.com/<repo>/main/releases/downloads
#   ACP_LATEST_URL=https://raw.githubusercontent.com/<repo>/main/releases/latest.txt
#
# Resolution: prefers a LOCAL release archive in this checkout
# (./releases/downloads/acp_<ver>_<os>_<arch>.tar.gz), else GitHub Releases, else the
# repo-mirrored releases/downloads/. Verifies checksums when present. Never auto-sudo.
set -euo pipefail

REPO="${ACP_REPO:-ab0t-com/acp}"
INSTALL_DIR="${ACP_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${ACP_VERSION:-latest}"
BINS="${ACP_BINS:-acp coordd acp-mcp}"
CHECKSUMS="${ACP_CHECKSUMS:-1}"
DRY_RUN="${ACP_DRY_RUN:-0}"
RELEASE_BASE="${ACP_RELEASE_BASE_URL:-https://github.com/${REPO}/releases/download}"
MIRROR_BASE="${ACP_MIRROR_BASE_URL:-https://raw.githubusercontent.com/${REPO}/main/releases/downloads}"
LATEST_URL="${ACP_LATEST_URL:-https://raw.githubusercontent.com/${REPO}/main/releases/latest.txt}"

fail() { echo "install.sh: $*" >&2; exit 1; }
fetch() { # url out
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2" 2>/dev/null
  elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1" 2>/dev/null
  else fail "need curl or wget"; fi
}

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"; case "$arch" in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; *) fail "unsupported arch $arch";; esac
script_dir="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT

# Resolve version.
if [ "$VERSION" = "latest" ]; then
  if [ -f "$script_dir/releases/latest.txt" ]; then VERSION="$(tr -d '[:space:]' < "$script_dir/releases/latest.txt")"
  else fetch "$LATEST_URL" "$tmp/latest" && VERSION="$(tr -d '[:space:]' < "$tmp/latest")" || fail "cannot resolve latest version"; fi
fi
archive="acp_${VERSION}_${os}_${arch}.tar.gz"
echo "ACP install: ${archive} -> ${INSTALL_DIR}"

# Acquire the archive: local checkout, then GitHub Releases, then raw mirror.
got=""
for cand in "$script_dir/releases/downloads/$archive" "./releases/downloads/$archive"; do
  [ -f "$cand" ] && cp "$cand" "$tmp/$archive" && got="local:$cand" && break
done
if [ -z "$got" ]; then
  if fetch "${RELEASE_BASE}/${VERSION}/${archive}" "$tmp/$archive"; then got="release"
  elif fetch "${MIRROR_BASE}/${archive}" "$tmp/$archive"; then got="mirror"
  else fail "could not find $archive (local/release/mirror). Build locally: ./rebuild.sh (private repo)"; fi
fi
echo "  source: $got"

# Verify checksum if available.
if [ "$CHECKSUMS" = "1" ]; then
  sumsrc=""
  for cand in "$script_dir/releases/downloads/checksums.txt" "$tmp/checksums.txt"; do
    [ -f "$cand" ] && sumsrc="$cand" && break
  done
  [ -z "$sumsrc" ] && fetch "${MIRROR_BASE}/checksums.txt" "$tmp/checksums.txt" && sumsrc="$tmp/checksums.txt" || true
  if [ -n "$sumsrc" ] && command -v sha256sum >/dev/null 2>&1; then
    want="$(grep "  ${archive}\$" "$sumsrc" | awk '{print $1}' || true)"
    if [ -n "$want" ]; then
      have="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
      [ "$want" = "$have" ] || fail "checksum mismatch for $archive"
      echo "  checksum: ok"
    fi
  fi
fi

tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$INSTALL_DIR"
for b in $BINS; do
  src="$tmp/$b"; [ -f "$src" ] || { echo "  (skip $b: not in archive)"; continue; }
  if [ "$DRY_RUN" = "1" ]; then echo "  would install $b -> $INSTALL_DIR/$b"; continue; fi
  install -m 0755 "$src" "$INSTALL_DIR/$b"; echo "  installed $b"
done

case ":$PATH:" in *":$INSTALL_DIR:"*) : ;; *) echo "NOTE: add to PATH: export PATH=\"$INSTALL_DIR:\$PATH\"";; esac
echo "done — try: acp version && acp config init"
