#!/bin/sh
# safeslop container entrypoint: load secrets into env at runtime, then exec the agent.
# Secrets ride a 0600 file (sourced here) rather than `docker -e`/`--env-file`, so the
# values never appear in `ps` (no argv) or `docker inspect` (Config.Env stays clean).
set -a
[ -f /safeslop/runtime/secrets.env ] && . /safeslop/runtime/secrets.env
set +a
exec "$@"
