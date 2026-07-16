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

# specs/0113: an opted-in Pi OAuth bearer enters through the read-only runtime
# mount, then is copied once into the ephemeral tmpfs home before Pi starts.
# Fail closed on any unexpected filesystem shape; never print file contents.
pi_stage_root=/safeslop/runtime/pi
if [ -e "$pi_stage_root" ] || [ -L "$pi_stage_root" ]; then
  pi_provider_dir="$pi_stage_root/openai-codex"
  pi_stage_auth="$pi_provider_dir/auth.json"
  stat_perm() { stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1" 2>/dev/null; }
  stat_owner() { stat -c '%u' "$1" 2>/dev/null || stat -f '%u' "$1" 2>/dev/null; }
  stat_links() { stat -c '%h' "$1" 2>/dev/null || stat -f '%l' "$1" 2>/dev/null; }
  pi_auth_fail() {
    printf '%s\n' 'safeslop: staged Pi OAuth snapshot failed safety checks' >&2
    exit 78
  }

  [ -n "$home" ] || pi_auth_fail
  [ -d "$pi_stage_root" ] && [ ! -L "$pi_stage_root" ] || pi_auth_fail
  [ -d "$pi_provider_dir" ] && [ ! -L "$pi_provider_dir" ] || pi_auth_fail
  [ -f "$pi_stage_auth" ] && [ ! -L "$pi_stage_auth" ] || pi_auth_fail
  [ "$(stat_perm "$pi_stage_root")" = 700 ] || pi_auth_fail
  [ "$(stat_perm "$pi_provider_dir")" = 700 ] || pi_auth_fail
  [ "$(stat_perm "$pi_stage_auth")" = 600 ] || pi_auth_fail
  [ "$(stat_owner "$pi_stage_root")" = "$(id -u)" ] || pi_auth_fail
  [ "$(stat_owner "$pi_provider_dir")" = "$(id -u)" ] || pi_auth_fail
  [ "$(stat_owner "$pi_stage_auth")" = "$(id -u)" ] || pi_auth_fail
  [ "$(stat_links "$pi_stage_auth")" = 1 ] || pi_auth_fail
  [ "$(find "$pi_stage_root" -print 2>/dev/null | wc -l | tr -d ' ')" = 3 ] || pi_auth_fail

  mkdir -p "$home/.pi/agent" || pi_auth_fail
  chmod 700 "$home/.pi" "$home/.pi/agent" || pi_auth_fail
  pi_tmp_auth="$(mktemp "$home/.pi/agent/.auth.json.safeslop.XXXXXX")" || pi_auth_fail
  chmod 600 "$pi_tmp_auth" || { rm -f "$pi_tmp_auth"; pi_auth_fail; }
  if ! cat "$pi_stage_auth" > "$pi_tmp_auth"; then
    rm -f "$pi_tmp_auth"
    pi_auth_fail
  fi
  if ! mv -f "$pi_tmp_auth" "$home/.pi/agent/auth.json"; then
    rm -f "$pi_tmp_auth"
    pi_auth_fail
  fi
  chmod 600 "$home/.pi/agent/auth.json" || pi_auth_fail
  unset pi_tmp_auth
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
