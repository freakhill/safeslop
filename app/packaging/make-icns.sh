#!/usr/bin/env bash
# Regenerate app/packaging/SafeSlop.icns from make-icon.swift (all required sizes).
# Run whenever the icon design changes; build-app.sh copies the committed .icns into the bundle.
set -euo pipefail
cd "$(dirname "$0")"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

swiftc make-icon.swift -o "$TMP/make-icon"
"$TMP/make-icon" "$TMP/icon-1024.png"

ICONSET="$TMP/SafeSlop.iconset"; mkdir -p "$ICONSET"
for spec in 16:16x16 32:16x16@2x 32:32x32 64:32x32@2x 128:128x128 256:128x128@2x 256:256x256 512:256x256@2x 512:512x512 1024:512x512@2x; do
  px="${spec%%:*}"; name="${spec##*:}"
  sips -z "$px" "$px" "$TMP/icon-1024.png" --out "$ICONSET/icon_${name}.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o SafeSlop.icns
echo "✓ wrote app/packaging/SafeSlop.icns ($(du -h SafeSlop.icns | cut -f1))"
