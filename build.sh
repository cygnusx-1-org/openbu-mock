#!/usr/bin/env bash
set -euo pipefail

VERSION=$(grep -E '^const Version = ' main.go | sed -E 's/.*"([^"]+)".*/\1/')
OUT_DIR="dist"
NAME="openbu-mock"

mkdir -p "$OUT_DIR"

targets=(
    "linux   amd64 ${NAME}-${VERSION}-linux-amd64"
    "linux   arm64 ${NAME}-${VERSION}-linux-arm64"
    "darwin  amd64 ${NAME}-${VERSION}-darwin-amd64"
    "darwin  arm64 ${NAME}-${VERSION}-darwin-arm64"
    "windows amd64 ${NAME}-${VERSION}-windows-amd64.exe"
    "windows arm64 ${NAME}-${VERSION}-windows-arm64.exe"
)

for t in "${targets[@]}"; do
    read -r goos goarch outname <<< "$t"
    echo "Building $outname"
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
        go build -trimpath -ldflags="-s -w" -o "$OUT_DIR/$outname" .
done

echo
echo "Built artifacts in $OUT_DIR/:"
ls -lh "$OUT_DIR"
