# Progressive runtime readiness — live root cause and repair constraints

Date: 2026-07-16 · Status: confirmed live defect

## Reproduction

A fresh container/deny Pi session reported `status: running`, but its agent could not resolve `proxy`, `session egress observations` returned no rows, and Docker showed only the agent container. Compose showed:

```text
proxy  ubuntu/squid:5.2-22.04_beta  Exited (1)
FATAL: Cannot open '/dev/stdout' for writing.
The parent directory must be writeable by the user 'proxy',
which is the cache_effective_user set in squid.conf.
```

The generated config used:

```text
access_log stdio:/dev/stdout safeslop_observation
```

The Ubuntu image entrypoint already tails `/var/log/squid/access.log` to stdout. Replacing only the ephemeral runtime target with `stdio:/var/log/squid/access.log` and restarting the unchanged proxy service made it stay up. The same session then proved:

```text
deny request -> observation chatgpt.com:443 (count 5)
exact session grant -> Pi openai-codex/gpt-5.6-luna response
revoke revision 2 -> request denied again
stop --revoke-credentials -> credentials_revoked true
```

No source-tree patch was present during this experiment; the runtime directory was removed with the test session.

## Root causes

1. The Squid log destination conflicts with the image's privilege drop. The value-free format is sound; the destination is not.
2. `container.Up` treats successful `compose up -d proxy` as readiness. Compose success means the container was started, not that Squid stayed alive or accepted reconfigure/check commands.
3. The detached supervisor can therefore remain `running` while the required proxy has exited. This is fail-more-restrictive for network authority, but operationally false and prevents observations/grants.
4. Session finish preserves non-projection errors as raw `last_error`; a proxy startup failure needs the same bounded engine-owned structured contract projection failures already use.

## Repair constraints

- Keep the existing value-free log format; write to the image-owned tailed log file.
- Probe the real Squid process (`squid -k check`) after compose-up, with a bounded retry.
- On exhaustion, tear the compose stack down before returning and never construct/start the agent command.
- Persist only `network_proxy_unavailable`, fixed summary, and fixed action; never persist compose/Squid output.
- The smoke test must prove the original live sequence without patching runtime files.

## Pi activation pin

The signed catalog previously pinned Pi `0.80.2`, whose `--list-models luna` returns no match. Host Pi `0.80.7` lists `openai-codex/gpt-5.6-luna` and answered a no-tools marker prompt.

Catalog review for `0.80.7`:

- npm registry source: `@earendil-works/pi-coding-agent`;
- published: `2026-07-14T16:41:45.925Z`;
- registry latest at review: `0.80.7`, not deprecated, integrity metadata present;
- magnitude: same-minor patch (`0.80.2 → 0.80.7`);
- `catalog propose-version`: newest non-yanked patch, no human-confirm flag;
- `catalog bump`: default lane, `soakRequired:false`, `soakSatisfied:true`, `self-computed-WEAK`;
- reverse package-requires closure: leaf `pi` only; affected bundles/images include `pi` and `personal`;
- npm caveat remains: the top-level version is pinned but transitive dependencies are not locked by a committed integrity map.

The locked policy describes a patch soak as illustrative/tunable while the shipped tool does not require one for this patch. This activation fixes a demonstrated signed-image capability gap and was explicitly approved in the user-directed repair, but it is **not** labeled a security bump and did not use `--security`.
