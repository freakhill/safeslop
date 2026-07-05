#!/usr/bin/env bash
set -euo pipefail

# specs/0075: security-critical host helpers must be resolved/executed through
# internal/engine/hostexec, not as bare names against the raw process PATH.
helpers='op|aws|gcloud|gke-gcloud-auth-plugin|git|ssh-keygen|ssh-keyscan|docker|podman|lima|mise|nix'

paths=(
  -- 'cmd/**' 'internal/**'
  ':!internal/engine/hostexec/**'
  ':!internal/engine/hostenv/**'
  ':!**/*_test.go'
)

if git grep -nE "(exec|osexec)\.LookPath\(\"(${helpers})\"\)" "${paths[@]}"; then
  echo >&2 "host-helper exec denylist: raw LookPath for a protected helper; use hostexec"
  exit 1
fi

if git grep -nE "(exec|osexec)\.Command(Context)?\([^\n]*\"(${helpers})\"" "${paths[@]}"; then
  echo >&2 "host-helper exec denylist: raw Command/CommandContext for a protected helper; use hostexec"
  exit 1
fi
