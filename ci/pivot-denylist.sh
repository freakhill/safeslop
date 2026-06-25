#!/usr/bin/env bash
set -euo pipefail

# Keep the specs/0049 pivot honest in live code/docs. Historical specs are
# archival design records and intentionally excluded.
pattern='opencode|open-code|vscode|vs code|visual studio code|SafeSlopCockpit|cockpit|Package\.swift|xcodebuild|swiftc|control\.proto|grpc|proto-sync|sign-notarize'

if git grep -nEi "$pattern" -- . \
  ':!specs/**' \
  ':!internal/cli/agentseed_test.go' \
  ':!internal/cli/cli_agentargv_test.go' \
  ':!ci/pivot-denylist.sh'; then
  echo >&2 "pivot denylist matched forbidden legacy surface"
  exit 1
fi
