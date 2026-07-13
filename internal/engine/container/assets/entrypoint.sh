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
# specs/0096: copy projected host config (read-only staging under /safeslop/projected/<id>)
# into the ephemeral home. The TSV is engine-generated (projection.tsv): one line per present
# file, "<staging>\t</home/agent/target>". We COPY only — never source/. /eval/execute projected
# content: shell/pi-skill config is readable instruction/code authority that runs only if the
# agent or shell later chooses to invoke it. Copy failures are recorded but never block launch;
# the resolver already failed closed on required/absent sources before the container started.
if [ -n "$home" ] && [ -f /safeslop/runtime/projection.tsv ]; then
  mkdir -p "$home/.safeslop" 2>/dev/null || true
  : > "$home/.safeslop/projection-status"
  while IFS="$(printf '\t')" read -r staging target; do
    [ -n "$staging" ] || continue
    if [ -r "$staging" ]; then
      mkdir -p "$(dirname "$target")" 2>/dev/null || true
      if cp -- "$staging" "$target" 2>/dev/null; then
        printf '%s\tok\n' "$target" >> "$home/.safeslop/projection-status"
      else
        printf '%s\tcopy-failed\n' "$target" >> "$home/.safeslop/projection-status"
      fi
    else
      printf '%s\truntime-unreadable\n' "$target" >> "$home/.safeslop/projection-status"
    fi
  done < /safeslop/runtime/projection.tsv
fi
exec "$@"
