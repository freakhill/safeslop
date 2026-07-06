# 0079 — Squid IP-literal deny (M6)

**Status:** implemented  
**Date:** 2026-07-06

## Source

Implements `specs/0070-security-review.md` M6: Squid `dstdomain` allowlisting may try reverse DNS for IP-based URLs, so a public IP literal with an attacker-controlled PTR can match an allowlisted domain.

Squid docs for `acl` say `dstdomain`/`dstdom_regex` try reverse lookup for IP-based URLs unless `-n` disables lookups/conversions.

## Decision

For `network: deny` container runs, reject numeric IP-literal destinations before the domain allowlist and render the allowlist with `dstdomain -n` so it cannot authorize a numeric destination via PTR.

Keep `network: allow` semantics open except for the existing metadata/private denies; the vulnerability is in the deny-tier domain allowlist.

## Tasks

- [x] Add tests for deny-tier public IPv4/IPv6 literals and Squid ACL ordering.
- [x] Add strict-mode IP-literal deny ACL and `dstdomain -n` allowlist rendering.
- [x] Update source docs and mark M6 implemented in `specs/0070-security-review.md`.
- [x] Verify `make check` and `make build`.
