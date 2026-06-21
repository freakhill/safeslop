#!/usr/bin/env bash
# Build the cockpit and assemble SafeSlop.app inside app/.build (SwiftPM only, never xcodebuild).
set -euo pipefail
cd "$(dirname "$0")/.."          # -> app/
ROOT="$(pwd)"
APP="$ROOT/.build/SafeSlop.app"

echo "▸ swift build -c release"
swift build -c release --disable-sandbox

BIN="$ROOT/.build/release/SafeSlopCockpit"
[ -x "$BIN" ] || { echo "build failed: $BIN missing" >&2; exit 1; }

[ -f "$ROOT/packaging/SafeSlop.icns" ] || { echo "▸ no icns yet — generating"; bash "$ROOT/packaging/make-icns.sh"; }

echo "▸ assembling $APP"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/SafeSlopCockpit"
cp "$ROOT/packaging/Info.plist" "$APP/Contents/Info.plist"
cp "$ROOT/packaging/SafeSlop.icns" "$APP/Contents/Resources/SafeSlop.icns"
# ad-hoc sign so macOS treats it as a stable app identity (TouchID/keychain prompts, dock icon).
codesign --force --deep --sign - "$APP" 2>/dev/null && echo "✓ ad-hoc signed" || echo "⚠ codesign skipped"

echo "✓ built $APP"
echo "  run: open '$APP'"
