#!/usr/bin/env bash
# Visual smoke for the cockpit — the one piece that needs real pixels. Builds safeslop + the app,
# seeds a throwaway repo, starts the engine FROM that repo, launches the cockpit NON-blocking, waits
# for it to render + connect, screenshots it to a PNG, then tears the app + engine down. Unlike
# run-cockpit-test.sh (interactive — you Cmd-Q to quit), nobody has to click: the PNG can be inspected
# afterwards by a human or by Claude (Read the file). Needs a logged-in GUI (Aqua) session.
#
#   bash app/screenshot-cockpit.sh                 # -> /tmp/safeslop-cockpit.png
#   COCKPIT_SHOT=/tmp/x.png bash app/screenshot-cockpit.sh
#
# (or: `make cockpit-shot`)
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"      # repo root (this script lives in app/)
TESTREPO="${COCKPIT_TEST_DIR:-/tmp/safeslop-cockpit-test}"
OUT="${COCKPIT_SHOT:-/tmp/safeslop-cockpit.png}"
SETTLE="${COCKPIT_SETTLE:-6}"                                 # seconds to let SwiftUI render + connect

echo "==> building safeslop + cockpit"
make -C "$REPO" build >/dev/null
export PATH="$REPO:$PATH"
swift build --package-path "$REPO/app" >/dev/null
APP_BIN="$(swift build --package-path "$REPO/app" --show-bin-path)/SafeSlopCockpit"
[ -x "$APP_BIN" ] || { echo "!! cockpit binary not found at $APP_BIN"; exit 1; }

echo "==> seeding test repo: $TESTREPO"
mkdir -p "$TESTREPO"
cat > "$TESTREPO/safeslop.cue" <<'CUE'
package safeslop
safeslop: {
	version: 1
	profiles: {
		safe:   {agent: "shell", environment: "sandbox",   network: "deny"}   // green chrome
		net:    {agent: "shell", environment: "sandbox",   network: "allow"}  // red chrome (open egress)
		risky:  {agent: "shell", environment: "host"}                          // host tier -> Touch ID
		box:    {agent: "shell", environment: "container", network: "deny"}   // egress-allowlisted (amber)
		boxnet: {agent: "shell", environment: "container", network: "allow"}  // open egress (red)
	}
}
CUE

echo "==> starting the engine (safeslop serve) from the test repo"
pkill -f 'safeslop serve' 2>/dev/null || true
sleep 0.3
( cd "$TESTREPO" && exec safeslop serve ) &

APP_PID=""
cleanup() {
  echo "==> tearing down"
  [ -n "$APP_PID" ] && kill "$APP_PID" 2>/dev/null || true
  pkill -f 'SafeSlopCockpit' 2>/dev/null || true
  pkill -f 'safeslop serve' 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# wait for the control socket
for _ in $(seq 1 60); do [ -S "$HOME/.safeslop/s.sock" ] && break; sleep 0.1; done
[ -S "$HOME/.safeslop/s.sock" ] || { echo "!! engine socket never appeared at ~/.safeslop/s.sock"; exit 1; }

echo "==> launching cockpit (non-blocking)"
( cd "$TESTREPO" && exec "$APP_BIN" ) &
APP_PID=$!

echo "==> letting it render (${SETTLE}s)"
sleep "$SETTLE"
# best-effort: bring it frontmost so the window isn't occluded
osascript -e 'tell application "System Events" to set frontmost of (first process whose name is "SafeSlopCockpit") to true' 2>/dev/null || true
sleep 1

echo "==> capturing -> $OUT"
# Prefer a tight window-region capture (needs Accessibility for System Events); fall back to full screen.
if bounds=$(osascript -e 'tell application "System Events" to tell process "SafeSlopCockpit" to get {position, size} of window 1' 2>/dev/null); then
  IFS=', ' read -r x y w h <<<"$bounds"
  if [[ "$x" =~ ^-?[0-9]+$ && "$w" =~ ^[0-9]+$ && "$w" -gt 0 ]]; then
    screencapture -x -R"${x},${y},${w},${h}" "$OUT" || screencapture -x "$OUT"
  else
    screencapture -x "$OUT"
  fi
else
  screencapture -x "$OUT"
fi

[ -s "$OUT" ] && echo "==> wrote $(du -h "$OUT" | cut -f1) screenshot: $OUT" || { echo "!! screenshot empty/failed"; exit 1; }
