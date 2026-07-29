#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")"
os="$(go env GOOS)"
arch="$(go env GOARCH)"
case "$os" in
  windows) ext=dll ;;
  darwin) ext=dylib ;;
  *) ext=so ;;
esac
out="dist/$os/$arch"
mkdir -p "$out"
go test ./...
go build -buildmode=c-shared -o "$out/antigravity-model-sync.$ext" .
rm -f "$out/antigravity-model-sync.h"
