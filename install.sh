#!/usr/bin/env sh
# claudeorch installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/DhilipBinny/claudeorch/main/install.sh | sh
#
# Environment variables:
#   CLAUDEORCH_VERSION  - version tag to install (default: latest release)
#   CLAUDEORCH_BINDIR   - install destination (default: $HOME/.local/bin,
#                         or /usr/local/bin if running as root)
#
# This script:
#   1. Detects your OS and CPU architecture
#   2. Downloads the matching binary from GitHub Releases
#   3. Verifies its SHA-256 against the release's SHA256SUMS
#   4. Installs it to the destination dir

set -eu

REPO="DhilipBinny/claudeorch"
BINARY="claudeorch"

# ---- Helpers ---------------------------------------------------------------

info()  { printf '  %s\n'       "$*" >&2; }
warn()  { printf '! %s\n'       "$*" >&2; }
fatal() { printf '\nError: %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# ---- Platform detection ----------------------------------------------------

detect_os() {
    case "$(uname -s)" in
        Linux)  echo linux ;;
        Darwin) echo darwin ;;
        *)      fatal "unsupported OS: $(uname -s). Supported: Linux, macOS." ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  echo amd64 ;;
        arm64|aarch64) echo arm64 ;;
        *)             fatal "unsupported CPU: $(uname -m). Supported: x86_64, arm64." ;;
    esac
}

# ---- Version resolution ----------------------------------------------------

resolve_version() {
    if [ -n "${CLAUDEORCH_VERSION:-}" ]; then
        echo "$CLAUDEORCH_VERSION"
        return
    fi
    # Most recent release tag — uses /releases (not /releases/latest) so
    # pre-releases are included. This matters while claudeorch is still
    # on -rc builds: /releases/latest returns 404 when only pre-releases
    # exist.
    API_URL="https://api.github.com/repos/$REPO/releases"
    if have curl; then
        BODY=$(curl -fsSL "$API_URL")
    elif have wget; then
        BODY=$(wget -qO- "$API_URL")
    else
        fatal "neither curl nor wget found"
    fi
    echo "$BODY" \
        | grep -E '"tag_name"' \
        | head -1 \
        | sed -E 's/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/'
}

# ---- Install location ------------------------------------------------------

pick_bindir() {
    if [ -n "${CLAUDEORCH_BINDIR:-}" ]; then
        echo "$CLAUDEORCH_BINDIR"
        return
    fi
    if [ "$(id -u)" = "0" ]; then
        echo /usr/local/bin
    else
        echo "$HOME/.local/bin"
    fi
}

# ---- Download ---------------------------------------------------------------

download() {
    URL="$1"; OUT="$2"
    if have curl; then
        curl -fsSL "$URL" -o "$OUT"
    else
        wget -qO "$OUT" "$URL"
    fi
}

verify_sha256() {
    EXPECTED="$1"; FILE="$2"
    if have sha256sum; then
        ACTUAL=$(sha256sum "$FILE" | awk '{print $1}')
    elif have shasum; then
        ACTUAL=$(shasum -a 256 "$FILE" | awk '{print $1}')
    else
        warn "no sha256sum or shasum command — skipping checksum verification"
        return 0
    fi
    [ "$EXPECTED" = "$ACTUAL" ] || fatal "checksum mismatch: expected $EXPECTED, got $ACTUAL"
}

# ---- Main -------------------------------------------------------------------

OS=$(detect_os)
ARCH=$(detect_arch)
VERSION=$(resolve_version)
BINDIR=$(pick_bindir)

[ -n "$VERSION" ] || fatal "could not determine release version"

ASSET="${BINARY}-${OS}-${ARCH}"
BASE_URL="https://github.com/$REPO/releases/download/$VERSION"
SUMS_URL="$BASE_URL/SHA256SUMS"
BIN_URL="$BASE_URL/$ASSET"

printf '\nclaudeorch installer\n'
info "version: $VERSION"
info "target:  ${OS}/${ARCH}"
info "bindir:  $BINDIR"
printf '\n'

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

info "downloading $ASSET..."
download "$BIN_URL"  "$TMPDIR/$ASSET"  || fatal "download failed: $BIN_URL"
download "$SUMS_URL" "$TMPDIR/SHA256SUMS" || fatal "download failed: $SUMS_URL"

EXPECTED=$(grep " $ASSET$" "$TMPDIR/SHA256SUMS" | awk '{print $1}')
[ -n "$EXPECTED" ] || fatal "no checksum for $ASSET in SHA256SUMS"

info "verifying checksum..."
verify_sha256 "$EXPECTED" "$TMPDIR/$ASSET"

mkdir -p "$BINDIR"
install -m 0755 "$TMPDIR/$ASSET" "$BINDIR/$BINARY"

printf '\n  installed: %s/%s\n' "$BINDIR" "$BINARY"

if ! echo "$PATH" | tr ':' '\n' | grep -Fxq "$BINDIR"; then
    printf '\nNote: %s is not on your $PATH. Add it with:\n' "$BINDIR"
    printf '  export PATH="%s:$PATH"\n' "$BINDIR"
fi

printf '\nVerify with: %s --version\n' "$BINARY"
