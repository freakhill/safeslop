#!/usr/bin/env bash
# Hermetic contract gate for the reviewed multi-platform Squid OCI lock.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
for file in proxy-image.lock.json proxy-image.index.json; do
  cmp "library/layer/container/$file" "internal/engine/container/assets/$file" >/dev/null || {
    echo "drift: embedded $file (run 'make sync-container-assets')" >&2
    exit 1
  }
done

go test ./internal/engine/container -run 'ProxyImageLock' -count=1
