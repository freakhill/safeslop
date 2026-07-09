#!/usr/bin/env bash
# Run local Emacs UI key-resolution compatibility slots without installing
# packages or loading private config unless the caller explicitly opts in.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EMACS_BIN="${EMACS:-emacs}"
PROBE="$ROOT/emacs/test/safeslop-ui-probe.el"
status=0

declare -a EVIL_LOAD_PATHS=()
declare -a EVIL_LOAD_ARGS=()
EVIL_SOURCE=""

mark_fail() {
  status=1
}

run_slot() {
  local name="$1"
  shift
  printf '\n== safeslop Emacs UI slot: %s ==\n' "$name"
  if "$@"; then
    printf '== safeslop Emacs UI slot: %s PASS ==\n' "$name"
  else
    printf '== safeslop Emacs UI slot: %s FAIL ==\n' "$name"
    mark_fail
  fi
}

run_probe() {
  local slot="$1"
  local evil="$2"
  local doom_shim="$3"
  shift 3
  env \
    SAFESLOP_UI_PROBE_SLOT="$slot" \
    SAFESLOP_UI_PROBE_EVIL="$evil" \
    SAFESLOP_UI_PROBE_DOOM_SHIM="$doom_shim" \
    "$EMACS_BIN" -Q --batch -L "$ROOT/emacs" "$@" \
    -l ert -l "$PROBE" -f ert-run-tests-batch-and-exit
}

split_configured_evil_paths() {
  local configured="${SAFESLOP_EVIL_LOAD_PATH:-}"
  local path
  [[ -n "$configured" ]] || return 1
  IFS=':' read -r -a EVIL_LOAD_PATHS <<<"$configured"
  for path in "${EVIL_LOAD_PATHS[@]}"; do
    if [[ -z "$path" || ! -d "$path" ]]; then
      printf 'SAFESLOP_EVIL_LOAD_PATH contains a missing directory: %s\n' "$path" >&2
      return 2
    fi
  done
  EVIL_SOURCE="SAFESLOP_EVIL_LOAD_PATH"
  return 0
}

try_evil_root() {
  local root="$1"
  local dep
  for dep in compat goto-chg undo-fu evil; do
    [[ -d "$root/$dep" ]] || return 1
  done
  EVIL_LOAD_PATHS=("$root/compat" "$root/goto-chg" "$root/undo-fu" "$root/evil")
  EVIL_SOURCE="$root"
  return 0
}

detect_evil_paths() {
  local result root
  if split_configured_evil_paths; then
    return 0
  fi
  result=$?
  [[ "$result" -ne 2 ]] || return 2

  for root in \
    "$HOME"/.emacs.d/.local/straight/build* \
    "$HOME"/.config/emacs/.local/straight/build* \
    "$HOME"/.emacs.d/straight/build* \
    "$HOME"/.config/emacs/straight/build* \
    "$HOME"/.emacs.d/elpaca/builds \
    "$HOME"/.config/emacs/elpaca/builds; do
    [[ -d "$root" ]] || continue
    if try_evil_root "$root"; then
      return 0
    fi
  done
  return 1
}

prepare_evil_args() {
  local path
  EVIL_LOAD_ARGS=()
  for path in "${EVIL_LOAD_PATHS[@]}"; do
    EVIL_LOAD_ARGS+=("-L" "$path")
  done
}

run_personal_slot() {
  local cmd="${SAFESLOP_UI_PERSONAL_CMD:-}"
  local require_personal="${SAFESLOP_UI_REQUIRE_PERSONAL:-0}"
  local -a suffix=(--batch -L "$ROOT/emacs" -l ert -l "$PROBE" -f ert-run-tests-batch-and-exit)
  local arg quoted

  if [[ -z "$cmd" ]]; then
    if [[ "$require_personal" == "1" || "$require_personal" == "true" || "$require_personal" == "yes" ]]; then
      printf '\n== safeslop Emacs UI slot: personal FAIL ==\n' >&2
      printf 'SAFESLOP_UI_REQUIRE_PERSONAL=1 requires SAFESLOP_UI_PERSONAL_CMD.\n' >&2
      printf 'Example: SAFESLOP_UI_PERSONAL_CMD="emacs --batch -l ~/.emacs.d/init.el" make test-emacs-ui-matrix\n' >&2
      mark_fail
    else
      printf '\n== safeslop Emacs UI slot: personal SKIP (set SAFESLOP_UI_PERSONAL_CMD to opt in) ==\n'
    fi
    return 0
  fi

  printf '\n== safeslop Emacs UI slot: personal ==\n'
  printf 'personal command provided; appending repository probe args (command redacted)\n'
  for arg in "${suffix[@]}"; do
    printf -v quoted '%q' "$arg"
    cmd+=" $quoted"
  done

  if env \
      SAFESLOP_UI_PROBE_SLOT="personal" \
      SAFESLOP_UI_PROBE_PERSONAL="1" \
      bash -c "$cmd"; then
    printf '== safeslop Emacs UI slot: personal PASS ==\n'
  else
    printf '== safeslop Emacs UI slot: personal FAIL ==\n'
    mark_fail
  fi
}

run_slot raw run_probe raw 0 0
run_slot doom-shim run_probe doom-shim 0 1

if detect_evil_paths; then
  prepare_evil_args
  printf '\nUsing local Evil load paths from %s\n' "$EVIL_SOURCE"
  run_slot evil run_probe evil 1 0 "${EVIL_LOAD_ARGS[@]}"
  run_slot doom-evil run_probe doom-evil 1 1 "${EVIL_LOAD_ARGS[@]}"
else
  result=$?
  if [[ "$result" -eq 2 ]]; then
    mark_fail
  else
    printf '\n== safeslop Emacs UI slot: evil SKIP (set SAFESLOP_EVIL_LOAD_PATH to opt in) ==\n'
    printf '== safeslop Emacs UI slot: doom-evil SKIP (set SAFESLOP_EVIL_LOAD_PATH to opt in) ==\n'
  fi
fi

run_personal_slot

exit "$status"
