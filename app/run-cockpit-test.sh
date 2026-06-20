#!/usr/bin/env bash
# Click-test the cockpit with zero manual setup. Builds safeslop, seeds a throwaway repo with a few
# profiles (green deny / red open-egress / host), starts the engine FROM that repo, launches the
# SwiftUI cockpit, and tears the engine down when you quit the app. You only deal with the GUI.
#
#   bash app/run-cockpit-test.sh            # build + serve + run the cockpit
#   bash app/run-cockpit-test.sh --fresh    # also wipe the trust store, so all profiles start "not trusted"
#
# (or: `make cockpit` / `make cockpit-fresh`)
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"      # repo root (this script lives in app/)
TESTREPO="${COCKPIT_TEST_DIR:-/tmp/safeslop-cockpit-test}"

if [ "${1:-}" = "--fresh" ]; then
  echo "==> --fresh: clearing the trust store (~/.config/safeslop/trust.json)"
  rm -f "$HOME/.config/safeslop/trust.json"
fi

echo "==> building safeslop"
make -C "$REPO" build >/dev/null
export PATH="$REPO:$PATH"

echo "==> seeding test repo: $TESTREPO"
mkdir -p "$TESTREPO"
cat > "$TESTREPO/safeslop.cue" <<'CUE'
package safeslop
safeslop: {
	version: 1
	profiles: {
		safe:  {agent: "shell", environment: "sandbox", network: "deny"}   // green chrome
		net:   {agent: "shell", environment: "sandbox", network: "allow"}  // red chrome (open egress)
		risky: {agent: "shell", environment: "host"}                        // host tier -> Touch ID
	}
}
CUE

echo "==> starting the engine (safeslop serve) from the test repo"
pkill -f 'safeslop serve' 2>/dev/null || true
sleep 0.3
( cd "$TESTREPO" && exec safeslop serve ) &
trap 'echo; echo "==> stopping engine"; pkill -f "safeslop serve" 2>/dev/null || true' EXIT INT TERM

# wait for the control socket
for _ in $(seq 1 60); do [ -S "$HOME/.safeslop/s.sock" ] && break; sleep 0.1; done
if [ ! -S "$HOME/.safeslop/s.sock" ]; then
  echo "!! engine socket never appeared at ~/.safeslop/s.sock"; exit 1
fi
echo "==> engine up. Launching the cockpit — quit the app (Cmd-Q) to tear everything down."
echo

cd "$REPO/app"
swift run
