#!/usr/bin/env bash
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

expected=$(printf '%s\n' \
  internal/engine/container/projection.go \
  internal/engine/creds/pi.go)
actual=$(rg -l 'github\.com/freakhill/safeslop/internal/engine/hostpath' cmd internal \
  --glob '*.go' --glob '!internal/engine/hostpath/**' | sort)

if ! diff -u <(printf '%s\n' "$expected") <(printf '%s\n' "$actual"); then
  echo >&2 'hostpath import denylist: only typed container projection and Pi credential facades may import hostpath'
  exit 1
fi

if rg -n 'hostpath\.(Root|Path|Options?|Open|Resolve|Walk|Proof|Capability|Descriptor)' \
  internal/engine/container/projection.go internal/engine/creds/pi.go; then
  echo >&2 'hostpath import denylist: generic host path capability escaped the package'
  exit 1
fi

go test ./internal/engine/hostpath -run '^TestHostPathExportedAPI$' -count=1
