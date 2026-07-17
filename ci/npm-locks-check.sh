#!/usr/bin/env bash
# Hermetic contract gate for the reviewed per-package npm lock projects.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
source_dir=library/layer/container/npm-locks
asset_dir=internal/engine/container/assets/npm-locks
expected=$'claude-code\npi\npnpm'

for root in "$source_dir" "$asset_dir"; do
  [[ -d "$root" ]] || { echo "missing npm lock root: $root" >&2; exit 1; }
  actual="$(find "$root" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | LC_ALL=C sort)"
  [[ "$actual" == "$expected" ]] || { echo "unreviewed npm lock project set in $root" >&2; exit 1; }
  if find "$root" -mindepth 1 ! -type d ! -type f -print -quit | grep -q .; then
    echo "foreign npm lock filesystem entry in $root" >&2
    exit 1
  fi
  for project in claude-code pi pnpm; do
    files="$(find "$root/$project" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; | LC_ALL=C sort)"
    [[ "$files" == $'package-lock.json\npackage.json' ]] || { echo "foreign file in $root/$project" >&2; exit 1; }
  done
done

diff -qr "$source_dir" "$asset_dir" >/dev/null || {
  echo "drift: embedded npm locks (run 'make sync-container-assets')" >&2
  exit 1
}

go test ./internal/engine/container -run 'NPMTool|BuildContext' -count=1
