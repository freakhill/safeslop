#!/usr/bin/env bash
# Opt-in live Docker smoke for the container-deny progressive egress lifecycle.
# Uses an isolated state/workspace and removes only the session it creates.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${SAFESLOP_BIN:-$ROOT/safeslop}"
DOCKER="${DOCKER:-$(command -v docker)}"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/safeslop-egress-smoke.XXXXXX")"
export SAFESLOP_STATE_DIR="$TMP/state"
SID=""
GRANT_ID=""

json_field() {
  python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(d[sys.argv[1]])' "$1"
}

cleanup() {
  local rc=$?
  set +e
  if [[ -n "$SID" ]]; then
    "$BIN" session stop --session-id "$SID" --revoke-credentials --output json >/dev/null 2>&1
    "$BIN" session rm --session-id "$SID" --output json >/dev/null 2>&1
    while read -r cid; do
      [[ -n "$cid" ]] && "$DOCKER" rm -f "$cid" >/dev/null 2>&1
    done < <("$DOCKER" ps -a --filter 'label=safeslop.managed=true' --format '{{.ID}} {{.Label "safeslop.session"}}' | awk -v id="$SID" '$2 ~ ("^" id "-") {print $1}')
  fi
  rm -rf "$TMP"
  exit "$rc"
}
trap cleanup EXIT INT TERM

mkdir -p "$TMP/workspace"
create="$("$BIN" session create --agent fish --environment container --workspace "$TMP/workspace" --name progressive-egress-smoke --output json)"
SID="$(printf '%s' "$create" | json_field session_id)"

"$BIN" session run --session-id "$SID" --detach >/dev/null

CID=""
for _ in $(seq 1 180); do
  CID="$("$DOCKER" ps --filter 'label=safeslop.managed=true' --format '{{.ID}} {{.Label "safeslop.session"}} {{.Label "com.docker.compose.service"}}' | awk -v id="$SID" '$2 ~ ("^" id "-") && $3 == "agent" {print $1; exit}')"
  [[ -n "$CID" ]] && break
  status="$("$BIN" session status --session-id "$SID" --output json | json_field status)"
  [[ "$status" != stopped ]] || { echo "smoke session stopped before agent readiness" >&2; exit 1; }
  sleep 1
done
[[ -n "$CID" ]] || { echo "smoke agent container did not become ready" >&2; exit 1; }

probe=("$DOCKER" exec -u 1000 "$CID" sh -lc 'curl -fsS --max-time 10 https://example.com >/dev/null')
if "${probe[@]}"; then
  echo "deny probe unexpectedly succeeded" >&2
  exit 1
fi

observed=0
for _ in $(seq 1 40); do
  observations="$("$BIN" session egress observations --session-id "$SID" --output json)"
  if printf '%s' "$observations" | python3 -c 'import json,sys; rows=json.load(sys.stdin)["data"].get("observations",[]); raise SystemExit(0 if any(r.get("host")=="example.com" and r.get("port")==443 for r in rows) else 1)'; then
    observed=1
    break
  fi
  sleep 0.25
done
[[ "$observed" == 1 ]] || { echo "denied destination was not observed" >&2; exit 1; }

grant="$("$BIN" session egress grant --session-id "$SID" --host example.com --port 443 --output json)"
GRANT_ID="$(printf '%s' "$grant" | python3 -c 'import json,sys; rows=json.load(sys.stdin)["data"]["egress_grants"]; print(next(r["id"] for r in rows if r["host"]=="example.com" and r["port"]==443))')"
"${probe[@]}"

"$BIN" session egress revoke --session-id "$SID" --grant-id "$GRANT_ID" --output json >/dev/null
if "${probe[@]}"; then
  echo "revoked probe unexpectedly succeeded" >&2
  exit 1
fi

"$BIN" session stop --session-id "$SID" --revoke-credentials --output json | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; assert d["status"]=="stopped" and not d.get("egress_grants",[])'
"$BIN" session rm --session-id "$SID" --output json >/dev/null
SID=""
echo "progressive-egress-smoke: PASS"
