# 0080 — Deny-tier Docker DNS exfil guard (M7)

**Status:** implemented  
**Date:** 2026-07-06

SCOPE: Close `specs/0070` M7 for the container `network: deny` topology by preventing Docker's embedded resolver from forwarding arbitrary external DNS lookups while preserving service-name resolution for the local proxy.  
OFF-LIMITS: No new runtime sidecars/dependencies, no host iptables/pf manipulation, no `CAP_NET_ADMIN`, no changes to `network: allow`, and no broadening of HTTP(S) egress allowlists.  
WORKTREE: `.worktrees/0080-dns-exfil-deny/`

## Design

Problem: a deny-tier agent can query Docker's embedded resolver (`127.0.0.11`) even with no default route, creating a DNS exfil caveat if the embedded resolver forwards to the host resolver.

Chosen approach: render the agent service in deny-tier with Docker/Compose DNS set to `127.0.0.1`. On user-defined networks Docker still exposes `127.0.0.11` for service discovery, but per-container DNS settings become the embedded resolver's external forwarders; `127.0.0.1` means the container's own loopback, so off-network DNS forwarding fails while internal names like `proxy` remain resolvable. `network: allow` keeps normal DNS.

Rejected alternatives:

- Add CoreDNS/dnsmasq sidecar: stronger policy surface, but a new runtime dependency and operational burden for this targeted fix.
- Host firewall/iptables port-53 drop: non-portable across Docker Desktop/OrbStack/podman/lima and outside the signed binary.
- Remove Docker DNS entirely: breaks `HTTP_PROXY=http://proxy:3128` service discovery unless static IP/IPAM is introduced, which is more brittle.

## Tasks

- [x] Add deny-tier DNS pin regression tests
  FILE:     `internal/engine/container/compose_test.go`
  CHANGE:   Add a test proving rendered deny-tier compose includes `dns: [127.0.0.1]` (or the equivalent YAML block) for the agent, and rendered open-egress compose omits it.
  VERIFY:   `go test ./internal/engine/container/ -run TestComposeDenyTierPinsDNSLoopback -count=1 -v`
  EXPECTED: Initially fails before the template change; passes after implementation.

- [x] Render deny-tier agent DNS as container-loopback
  FILE:     `internal/engine/container/assets/compose.yml.tmpl`
  CHANGE:   In the agent service, emit a deny-tier-only `dns: 127.0.0.1` stanza with a why-focused comment explaining Docker embedded DNS external forwarding.
  VERIFY:   `go test ./internal/engine/container/ -run 'TestComposeDenyTierPinsDNSLoopback|TestComposeOpenEgressJoinsAgentToBridge|TestComposeIsNetworkEnforcedAndLeakFree' -count=1 -v`
  EXPECTED: Deny-tier render has loopback DNS and remains internal-only; open-egress render has no DNS pin and joins egress.

- [x] Keep legacy/example container layer aligned
  FILE:     `library/layer/container/docker-compose.yml`
  CHANGE:   Add the same DNS loopback pin to the legacy `agent` and `agent-tools` services that are on `sandbox_internal` only.
  VERIFY:   `rg -n "dns:|127\.0\.0\.1" library/layer/container/docker-compose.yml`
  EXPECTED: Both internal-only services carry the loopback DNS pin.

- [x] Update docs/spec status
  FILE:     `README.md`, `skills/agent-sandbox-ops/SKILL.md`, `specs/0070-security-review.md`, `specs/0005-sp3-container-environment.md`, `specs/0080-dns-exfil-deny.md`
  CHANGE:   Document deny-tier DNS forwarding is pinned to container loopback; mark M7 implemented and tick this plan.
  VERIFY:   `rg -n "DNS|127\.0\.0\.1|M7" README.md skills/agent-sandbox-ops/SKILL.md specs/0070-security-review.md specs/0080-dns-exfil-deny.md`
  EXPECTED: User docs and security-review spec state the new DNS behavior.

- [x] Final verification
  FILE:     repository
  CHANGE:   Run the required full checks after implementation.
  VERIFY:   `make check && make build`
  EXPECTED: Both commands exit 0.
