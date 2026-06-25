# 0048 — host egress approval UX (premium-FLO follow-up)

## Context

During specs/0047 (graduate fish), the legacy host-side Envoy/CoreDNS/notifier
approval flow was deliberately deleted with the rest of the legacy toolkit. The
Go tiers model supersedes it for core isolation:

- `host` — no boundary
- `sandbox` — macOS Seatbelt mistake-guard
- `container` — proxy/topology egress allowlist
- `vm` — disposable VM boundary

The old interactive host egress approval UX is not a graduation blocker. A
Go-native replacement should be designed separately rather than recreated as a
straight port.

## Goal

Use FLO to design a premium Go-native host egress approval experience that fits
safeslop's current control-plane and tier model.

## Non-goals

- Do not revive deleted legacy implementation paths.
- Do not weaken the default `network: "deny"` posture.
- Do not make host-tier egress approval a prerequisite for specs/0047 deletion.

## Starting questions

- Should approval live in the cockpit, CLI, Network Extension, or container/VM
  proxy surface?
- What can be enforced topologically versus merely prompted?
- How should approvals be recorded, audited, expired, and tied to a profile?
- How should the UI avoid implying stronger isolation than the tier provides?
