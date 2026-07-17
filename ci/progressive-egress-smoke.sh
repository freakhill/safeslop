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
TUNNEL_READY="/tmp/safeslop-old-tunnel.ready"
TUNNEL_STATUS="/tmp/safeslop-old-tunnel.status"

json_field() {
  python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(d[sys.argv[1]])' "$1"
}

proxy_id() {
  "$DOCKER" ps --filter 'label=safeslop.managed=true' --format '{{.ID}} {{.Label "safeslop.session"}} {{.Label "com.docker.compose.service"}}' | awk -v id="$SID" '$2 == id && $3 == "proxy" {print $1; exit}'
}

assert_proxy_generation() {
  local expected_revision="$1" cid revision expected_hash actual_hash
  cid="$(proxy_id)"
  [[ -n "$cid" ]] || { echo "proxy container is not running" >&2; return 1; }
  revision="$("$DOCKER" inspect --format '{{ index .Config.Labels "safeslop.egress-revision" }}' "$cid")"
  expected_hash="$("$DOCKER" inspect --format '{{ index .Config.Labels "safeslop.egress-hash" }}' "$cid")"
  actual_hash="$("$DOCKER" exec "$cid" sha256sum /etc/squid/safeslop.d/session-grants.conf | awk '{print $1}')"
  [[ "$revision" == "$expected_revision" && -n "$expected_hash" && "$expected_hash" == "$actual_hash" ]] || {
    echo "proxy generation ACK mismatch: revision=$revision expected_revision=$expected_revision label=$expected_hash file=$actual_hash" >&2
    return 1
  }
}

cleanup() {
  local rc=$?
  set +e
  if [[ -n "$SID" ]]; then
    "$BIN" session stop --session-id "$SID" --revoke-credentials --output json >/dev/null 2>&1
    "$BIN" session rm --session-id "$SID" --output json >/dev/null 2>&1
    while read -r cid; do
      [[ -n "$cid" ]] && "$DOCKER" rm -f "$cid" >/dev/null 2>&1
    done < <("$DOCKER" ps -a --filter 'label=safeslop.managed=true' --format '{{.ID}} {{.Label "safeslop.session"}}' | awk -v id="$SID" '$2 == id {print $1}')
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
  CID="$("$DOCKER" ps --filter 'label=safeslop.managed=true' --format '{{.ID}} {{.Label "safeslop.session"}} {{.Label "com.docker.compose.service"}}' | awk -v id="$SID" '$2 == id && $3 == "agent" {print $1; exit}')"
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
assert_proxy_generation 0

proxy_before_grant="$(proxy_id)"
grant="$("$BIN" session egress grant --session-id "$SID" --host example.com --port 443 --output json)"
GRANT_ID="$(printf '%s' "$grant" | python3 -c 'import json,sys; rows=json.load(sys.stdin)["data"]["egress_grants"]; print(next(r["id"] for r in rows if r["host"]=="example.com" and r["port"]==443))')"
proxy_after_grant="$(proxy_id)"
[[ -n "$proxy_after_grant" && "$proxy_after_grant" != "$proxy_before_grant" ]] || { echo "grant did not replace the proxy boundary" >&2; exit 1; }
assert_proxy_generation 1
"${probe[@]}"

# Keep a proven CONNECT tunnel open across revoke without completing TLS. The
# probe blocks on the established socket and can write "closed" only after the
# old proxy disappears.
"$DOCKER" exec -u 1000 "$CID" rm -f "$TUNNEL_READY" "$TUNNEL_STATUS" /tmp/safeslop-old-tunnel.sh
"$DOCKER" exec -i -u 1000 "$CID" sh -c 'cat >/tmp/safeslop-old-tunnel.sh' <<'EOF'
#!/usr/bin/env bash
set -u
ready=/tmp/safeslop-old-tunnel.ready
status=/tmp/safeslop-old-tunnel.status
exec 3<>/dev/tcp/proxy/3128 || { printf 'connect-failed\n' >"$status"; exit; }
printf 'CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n' >&3
IFS= read -r response <&3 || { printf 'response-failed\n' >"$status"; exit; }
while IFS= read -r line <&3; do
  [[ "$line" == $'\r' ]] && break
done
[[ "$response" == *" 200 "* ]] || { printf 'rejected\n' >"$status"; exit; }
printf 'ready\n' >"$ready"
if IFS= read -r _ <&3; then
  printf 'unexpected-data\n' >"$status"
else
  printf 'closed\n' >"$status"
fi
EOF
"$DOCKER" exec -d -u 1000 "$CID" bash /tmp/safeslop-old-tunnel.sh
tunnel_established=0
for _ in $(seq 1 80); do
  if "$DOCKER" exec -u 1000 "$CID" test -f "$TUNNEL_READY"; then
    tunnel_established=1
    break
  fi
  if "$DOCKER" exec -u 1000 "$CID" test -f "$TUNNEL_STATUS"; then
    echo "old-tunnel probe exited before revoke" >&2
    exit 1
  fi
  sleep 0.25
done
[[ "$tunnel_established" == 1 ]] || { echo "old-tunnel probe did not establish CONNECT" >&2; exit 1; }
if "$DOCKER" exec -u 1000 "$CID" test -f "$TUNNEL_STATUS"; then
  echo "old-tunnel probe was not live before revoke" >&2
  exit 1
fi

"$BIN" session egress revoke --session-id "$SID" --grant-id "$GRANT_ID" --output json >/dev/null
proxy_after_revoke="$(proxy_id)"
[[ -n "$proxy_after_revoke" && "$proxy_after_revoke" != "$proxy_after_grant" ]] || { echo "revoke did not replace the proxy boundary" >&2; exit 1; }
assert_proxy_generation 2
old_tunnel_terminated=0
for _ in $(seq 1 80); do
  if "$DOCKER" exec -u 1000 "$CID" test -f "$TUNNEL_STATUS"; then
    old_tunnel_terminated=1
    break
  fi
  sleep 0.25
done
[[ "$old_tunnel_terminated" == 1 ]] || { echo "established tunnel survived proxy replacement" >&2; exit 1; }
tunnel_result="$("$DOCKER" exec -u 1000 "$CID" cat "$TUNNEL_STATUS")"
[[ "$tunnel_result" == closed ]] || {
  echo "old-tunnel probe did not close after revoke" >&2
  exit 1
}
if "${probe[@]}"; then
  echo "revoked probe unexpectedly succeeded" >&2
  exit 1
fi

"$BIN" session stop --session-id "$SID" --revoke-credentials --output json | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; assert d["status"]=="stopped" and not d.get("egress_grants",[])'
"$BIN" session rm --session-id "$SID" --output json >/dev/null
SID=""
echo "progressive-egress-smoke: PASS"
