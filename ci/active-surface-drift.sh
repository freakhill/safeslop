#!/usr/bin/env bash
# Reject removed runtime/image surfaces in active docs and CI (historical specs excluded).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
active=(.github/workflows .woodpecker CONTRIBUTING.md README.md skills library/README.md library/layer/container Makefile)
forbidden="test-integration|internal/engine/vm|sandbox-exec|agent-sandbox-tools|local/agent-sandbox:latest|ENABLE_(CREWAI|PYDANTIC|AG2)|CREWAI_VERSION|PYDANTIC_AI_VERSION|AG2_VERSION|CLAUDE_CODE_NPM_PACKAGE|PI_NPM_PACKAGE|PNPM_NPM_PACKAGE|DOCKER_BUILDKIT:[[:space:]]*['\"]?0|COMPOSE_DOCKER_CLI_BUILD"

if LC_ALL=C rg -n --color=never -e "$forbidden" "${active[@]}"; then
  echo "removed VM or image-build surface remains active" >&2
  exit 1
fi

for file in .github/workflows/container-images.yml .woodpecker/container-images.yml; do
  [[ -f "$file" ]] || { echo "missing current container image workflow: $file" >&2; exit 1; }
  grep -q 'test-container-images' "$file" || { echo "container image workflow has no real target: $file" >&2; exit 1; }
  grep -q 'check-npm-locks' "$file" || { echo "container image workflow skips npm locks: $file" >&2; exit 1; }
  grep -q 'check-proxy-image-lock' "$file" || { echo "container image workflow skips proxy lock: $file" >&2; exit 1; }
done

make -n test-container-images >/dev/null
