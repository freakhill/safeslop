#!/bin/sh
# safeslop container entrypoint: load secrets into env at runtime, then exec the agent.
# Secrets ride a 0600 file (sourced here) rather than `docker -e`/`--env-file`, so the
# values never appear in `ps` (no argv) or `docker inspect` (Config.Env stays clean).
set -a
[ -f /safeslop/runtime/secrets.env ] && . /safeslop/runtime/secrets.env
set +a
# $HOME is a fresh tmpfs (the rootfs is read-only; specs/0064): pre-create the
# state trees agents assume exist. pi's session-store mkdir is non-recursive,
# so a missing parent chain kills it at startup with ENOENT. HOME can be unset
# under docker; fall back to the passwd entry, as the agent runtimes do.
home="${HOME:-$(getent passwd "$(id -u)" | cut -d: -f6)}"
if [ -n "$home" ]; then
  mkdir -p "$home/.pi/agent/sessions" "$home/.claude" "$home/.config" \
    "$home/.cache" "$home/.local/state" 2>/dev/null || true
fi
exec "$@"
