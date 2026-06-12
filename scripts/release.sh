#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:?Usage: scripts/release.sh <version>  (e.g. v0.3.1)}"
REPO="DhilipBinny/claudeorch"
OUTDIR="$(mktemp -d)"
PLATFORMS=(
  "linux   amd64"
  "linux   arm64"
  "darwin  amd64"
  "darwin  arm64"
)

COMMIT="$(git rev-parse --short HEAD)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

echo "==> Building ${VERSION} for ${#PLATFORMS[@]} targets..."

for entry in "${PLATFORMS[@]}"; do
  read -r os arch <<< "$entry"
  name="claudeorch-${os}-${arch}"
  echo "    ${name}"
  GOOS="$os" GOARCH="$arch" go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
    -o "${OUTDIR}/${name}" \
    ./cmd/claudeorch
done

echo "==> Generating SHA256SUMS..."
(cd "$OUTDIR" && sha256sum claudeorch-* > SHA256SUMS)

echo "==> Uploading to release ${VERSION}..."
gh release upload "$VERSION" \
  "${OUTDIR}"/claudeorch-* \
  "${OUTDIR}/SHA256SUMS" \
  --repo "$REPO"

echo "==> Done. Assets uploaded to https://github.com/${REPO}/releases/tag/${VERSION}"
rm -rf "$OUTDIR"
