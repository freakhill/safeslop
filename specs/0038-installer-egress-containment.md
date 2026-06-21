# Installer Egress Containment — Feasibility + the Achievable Slice (specs/0036 Task 6, 2nd layer)

**Context:** Task 6 made the script-only installers (rustup, nix) **verified** — safeslop pins +
sha256-checks the installer binary before running it. The asked-for "second layer" was to also contain
each installer's OWN network egress (a squid allowlist of the hosts it may reach while running).

## Feasibility finding: true host-tier egress allowlisting is NOT achievable here

1. **Seatbelt can't host-allowlist.** `internal/engine/sandbox` is explicit: sandbox-exec network policy
   is coarse deny/allow — "sandbox-exec cannot do a URL allowlist; that is the container's job." So the
   host tier cannot restrict an installer to `static.rust-lang.org` / `cache.nixos.org`.
2. **No host-tier squid.** The squid allowlist lives in the **container** tier
   (`internal/engine/container`), as a Docker sidecar. There is no host-resident squid an installer
   subprocess could be forced through, and standing one up + forcing the installer through it (HTTPS_PROXY
   + Seatbelt-deny-except-localhost) is a large new subsystem.
3. **Container tier installs to the wrong place.** Running rustup/nix INSIDE a squid-guarded container
   would install the toolchain in the container, not on the host — defeating the purpose.
4. **nix needs root.** The Determinate nix-installer creates `/nix`, installs a daemon, and `sudo`s —
   it cannot run under a user Seatbelt confinement at all.

**Conclusion:** the literal squid-egress layer is infeasible at the host tier and is NOT recommended.
And its marginal value is now low: the installers are verified AND self-verify their own downloads
(rustup checks signed manifests; Determinate verifies its components), so the residual threat the egress
allowlist would address is small.

## The achievable, worthwhile slice: secret-read confinement via Seatbelt

What IS achievable and worth doing is bounding a confinable installer's filesystem blast radius. The
sandbox already auto-denies a curated credential set (`secretDenyTargets`: SSH private keys, GPG, AWS/GCP/
Azure creds, vault/op/lpass, db password files, shell history, netrc) whenever a profile grants extra
file scope. So running a confinable installer under `sandbox.WrapArgv(argv, $HOME, "allow",
Scope{Write:["~"]})` lets the install work (broad home write, system read, temp, network) while a latent
bug or a compromised download **cannot read or exfiltrate the user's secrets**. Network stays coarse-allow
(the documented Seatbelt limit).

- **rustup** → `Confine: true`. Writes ~/.rustup, ~/.cargo, shell profiles (all under home) — works under
  the profile; the secret-deny set is paths rustup never touches.
- **nix** → `Confine: false`. Needs root + system-wide changes; runs unconfined (flagged in the consent
  precaution as admin-level + unconfined).

This is defense-in-depth on top of the (primary) verification, fail-safe (a too-tight profile makes the
install error out, leaving the existing tool intact), and reuses existing sandbox machinery.

## Implemented
- `tools.VerifiedInstaller.Confine` + `installerRunArgv` wrapping confinable installers under Seatbelt
  with the secret-deny scope; nix unconfined. Precaution text differentiates confined vs unconfined-admin.
- Unit test asserts a confinable installer's run argv is sandbox-exec-wrapped and the profile denies an
  SSH private key, while nix's is not wrapped.

## Deferred / not recommended
- The full squid egress allowlist (infeasible at host tier, low marginal value — see above).
- Tightening installer WRITE scope below "~" (fragile: a missed profile path silently breaks PATH setup;
  broad-home-write + secret-deny is the proportionate choice for verified installers).
