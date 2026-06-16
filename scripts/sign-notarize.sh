#!/usr/bin/env bash
# Codesign + notarize the slop binaries for distribution.
#
# This is the SP1 signing pipeline (specs/0001 §8). It is intentionally NOT run
# in CI — it needs an Apple Developer identity and is the maintainer's gate. Run
# it locally (or in a credentialed release job) after `make dist`.
#
# Required environment:
#   SLOP_SIGN_IDENTITY   Developer ID Application identity, e.g.
#                        "Developer ID Application: Your Name (TEAMID)"
#   SLOP_NOTARY_PROFILE  notarytool keychain profile name, created once via:
#                          xcrun notarytool store-credentials SLOP_NOTARY_PROFILE \
#                            --apple-id <you@example.com> --team-id <TEAMID> \
#                            --password <app-specific-password>
#
# Usage: scripts/sign-notarize.sh dist/slop-darwin-arm64 [dist/slop-darwin-amd64 ...]
#
# Note on stapling: a bare CLI executable cannot be stapled (stapling targets
# .app/.dmg/.pkg). The notarized zip below is what you publish (Homebrew/GitHub
# Releases); Gatekeeper verifies the notarized signature. A stapled .pkg/.dmg
# wrapper is a later distribution-format decision, tracked in specs/0001.
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <binary> [binary...]" >&2
  exit 2
fi

: "${SLOP_SIGN_IDENTITY:?set SLOP_SIGN_IDENTITY to your Developer ID Application identity}"
: "${SLOP_NOTARY_PROFILE:?set SLOP_NOTARY_PROFILE to your notarytool keychain profile}"

for bin in "$@"; do
  [[ -f "$bin" ]] || { echo "missing: $bin" >&2; exit 1; }

  echo ">> codesign $bin"
  codesign --force --timestamp --options runtime \
    --sign "$SLOP_SIGN_IDENTITY" "$bin"
  codesign --verify --strict --verbose=2 "$bin"

  zip="${bin}.zip"
  echo ">> zip + notarize $zip"
  ditto -c -k --keepParent "$bin" "$zip"
  xcrun notarytool submit "$zip" --keychain-profile "$SLOP_NOTARY_PROFILE" --wait
  rm -f "$zip"

  echo ">> done: $bin (signed + notarized)"
done
