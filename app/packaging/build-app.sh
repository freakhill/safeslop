#!/usr/bin/env bash
# Build the cockpit + the engine and assemble a SELF-CONTAINED SafeSlop.app inside app/.build
# (SwiftPM only, never xcodebuild). The engine binary is shipped at Contents/MacOS/safeslop so a
# double-clicked app works without `safeslop` on PATH (EngineBinary.resolved finds it there).
set -euo pipefail
cd "$(dirname "$0")/.."          # -> app/
ROOT="$(pwd)"
REPO="$(cd "$ROOT/.." && pwd)"   # -> repo root
APP="$ROOT/.build/SafeSlop.app"

echo "▸ building engine (make build)"
make -C "$REPO" build >/dev/null
ENGINE="$REPO/safeslop"
[ -x "$ENGINE" ] || { echo "engine build failed: $ENGINE missing" >&2; exit 1; }

echo "▸ swift build -c release (cockpit)"
swift build -c release --disable-sandbox
BIN="$ROOT/.build/release/SafeSlopCockpit"
[ -x "$BIN" ] || { echo "cockpit build failed: $BIN missing" >&2; exit 1; }

[ -f "$ROOT/packaging/SafeSlop.icns" ] || { echo "▸ no icns yet — generating"; bash "$ROOT/packaging/make-icns.sh"; }

echo "▸ assembling $APP"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/SafeSlopCockpit"
cp "$ENGINE" "$APP/Contents/MacOS/safeslop"          # the engine ships inside the app
cp "$ROOT/packaging/Info.plist" "$APP/Contents/Info.plist"
cp "$ROOT/packaging/SafeSlop.icns" "$APP/Contents/Resources/SafeSlop.icns"
# ad-hoc sign the whole bundle (incl. the nested engine) for a stable app identity.
codesign --force --deep --sign - "$APP" 2>/dev/null && echo "✓ ad-hoc signed" || echo "⚠ codesign skipped"

echo "✓ built $APP (self-contained: engine bundled)"
echo "  run: open '$APP'"
