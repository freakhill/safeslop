# Prior-art research — safeslop for a Rust/TS/Java startup on MacBooks → AWS/GCP

Date: 2026-06-17
Method: cross-model `ayo` pass (3 blind lanes: Anthropic host subagent / Google Gemini 3.1 Pro via
ai-router (ZDR enforced) / Moonshot Kimi K2.7). Each lane mined external prior art against the same
brief + 7 named project surfaces; the orchestrator (host) compiled, de-duped, and triaged. See the
method footer.

---

## Headline (the two load-bearing insights)

1. **The AWS/GCP credential story is the biggest hole, and it inverts the cleanup model.** All three
   lanes independently said the same thing: safeslop mints GitHub/Forgejo/npm tokens but leaves the
   *expensive* secret — cloud creds — unaddressed for the exact users we target. And the right model
   isn't "revoke on exit": **short TTL must be the primary control, revoke-on-exit only best-effort**,
   because `SIGKILL`/force-quit means our on-exit hooks frequently never run. Design for credential
   *decay*, not revocation.

2. **For Rust/TS/Java, the dangerous code runs at install/build/IDE-import time, with ambient
   authority — so "default = Seatbelt sandbox" is the wrong default for these languages.** `build.rs`
   + proc-macros (run even on `cargo check`), npm/pnpm `postinstall`, and Gradle config-phase scripts
   are all arbitrary code execution *before* "run the tests," and Seatbelt is an undocumented,
   Apple-deprecated boundary with no egress topology. These languages should default to the
   container/VM tier, and egress — not filesystem confinement — is the load-bearing control.

---

## Triaged lessons

Tags: **[C]** = cross-family consensus (≥2 lanes, higher confidence) · **[U:family]** = single-lane,
higher novelty. Priority: **HIGH** (act) · **MED** (design work / secondary) · **DEFER** (note only).

### A. Install-time RCE for Rust/TS/Java (the language-specific threat)

**HIGH [C, all 3] — `cargo`/`pnpm`/`gradle` are adversarial code execution; enter the boundary first, with no ambient creds.**
- EVIDENCE: crates.io `build.rs` + proc-macros run arbitrary native code at *compile* time (so even an editor's `cargo check` is RCE); npm/pnpm lifecycle scripts are the dominant real-world JS attack vector and run with full user env on install; Gradle runs arbitrary Groovy/Kotlin at *configuration* time. The payload fires at install/build/import, long before tests, and runs as you.
- RELEVANCE: Surface #1+#4. Constraint: boundary must be entered *before* the first `cargo`/`pnpm`/`gradle`; #3 creds must NOT be staged in unless policy explicitly grants — else the sandbox is just a clean room with your keys in it.

**HIGH [C, Kimi+Host] — Default Rust/TS/Java install+build steps to container/VM, not Seatbelt; add a per-language `build-scripts: allow|deny` knob.**
- EVIDENCE: Seatbelt has no egress topology and is best-effort; `--ignore-scripts` breaks legit packages (esbuild, protobuf) and is bypassed by monorepo tools; cargo has no script kill-switch at all (build.rs always runs) — so only the *boundary* contains the blast radius, not a flag.
- RELEVANCE: Surface #1+#4. `slop.cue` needs a `build-scripts` knob; the "evaluate this dep's script in a VM" escape hatch belongs in the safe-installer (#5).

**MED [C, Gemini+Kimi] — IDE auto-build blindspot: the host IntelliJ/VSCode that opens the agent-written repo will auto-execute `build.gradle`/`build.rs` on the host, un-sandboxed.**
- EVIDENCE: IntelliJ Gradle sync + language servers run the config phase on project import; VSCode rust-analyzer triggers `cargo check`. Sandboxing the agent shell misses this entirely — the developer's own IDE is the escape.
- RELEVANCE: Surface #1. **Largely outside safeslop's control** (we can't sandbox the user's host IDE) → DEFER to *documentation* + the cache-poisoning mitigation below; do not over-build for it.

**MED [U:Gemini] — Mount shared package caches (`~/.cargo/registry`, `~/.npm`, `~/.gradle/caches`) read-only or per-session ephemeral.**
- EVIDENCE: malicious build/postinstall scripts poison the *shared host cache*; the host then executes the poisoned dep on its next un-sandboxed build — isolation defeated after the fact.
- RELEVANCE: Surface #1+#4. Ties into "provision from clean home" (E below).

### B. Cloud credentials (the #1 gap) + egress

**HIGH [C, all 3] — Ship an AWS/GCP provider now: aws-vault-style keychain-backed short STS + gcloud ADC, never mounting `~/.aws` / `~/.config/gcloud`.**
- EVIDENCE: most cloud breaches start with a long-lived key in `~/.aws/credentials`; aws-vault keeps the long-term key in the macOS Keychain and vends short STS sessions; our "ephemeral, minimal-scope" philosophy maps perfectly onto STS/SSO — adopt, don't invent.
- RELEVANCE: Surface #3. Mirror the existing SSH staging filter + its `id_ed25519` decoy test — add `~/.aws/credentials` + `application_default_credentials.json` decoy assertions.

**HIGH [U:Kimi] — Strip the `refresh_token` from gcloud ADC json before injecting; leave only the short-lived `access_token`.**
- EVIDENCE: `gcloud auth application-default login` writes a long-lived OAuth refresh token; one stolen file = persistent GCP access until manual revoke.
- RELEVANCE: Surface #3. Concrete GCP hardening; cheap.

**HIGH [U:Kimi] — Make short TTL the primary control (5–15 min STS, single-use deploy keys); treat revoke-on-exit as best-effort.**
- EVIDENCE: `SIGKILL` can't be caught and force-quit/`launchctl` kills skip cleanup — aws-vault mitigates by *design* (short lifetimes), not by revocation hooks. safeslop's current model leans on on-exit revoke, which dies dirty.
- RELEVANCE: Surface #3. Reframes the credential lifecycle: decay-first, revoke-second.

**HIGH [C, all 3] — Deny the metadata endpoint (169.254.169.254 + `metadata.google.internal` + the whole 169.254.0.0/16 link-local) at EVERY boundary as a non-removable baseline, not a user-editable allowlist entry.**
- EVIDENCE: SSRF-to-IMDS is the canonical cloud-cred theft path (Capital One); domain allowlists silently miss it because it's a raw link-local IP, not DNS.
- RELEVANCE: Surface #2+#3. Baseline deny rule in both squid and the future NetworkExtension.

**HIGH [C, Gemini+Kimi] — Force DNS through a controlled resolver and drop raw port 53; a domain allowlist alone is defeated by DNS tunneling.**
- EVIDENCE: encode stolen creds into subdomains (`aws-key.attacker.com`) — `nslookup` bypasses HTTP proxies entirely (iodine/dnscat2). The squid sidecar must also BE the DNS resolver for the internal net.
- RELEVANCE: Surface #2. Topology must block direct external 53.

**HIGH [C, all 3] — Egress (not filesystem) is the load-bearing control for credentialed agents; HTTP_PROXY is advisory only.**
- EVIDENCE: prompt-injection-driven exfil is demonstrated against Cursor/Devin/Claude Code (malicious README/issue/test-output tells the agent to POST your env to evil.com); an agent that can set its own env trivially unsets HTTP_PROXY. safeslop already got the *container* topology right; the gap is that **Seatbelt has no topology equivalent** — which is exactly what SP8 must fix to make the *default* boundary egress-safe.
- RELEVANCE: Surface #2+#1. Sequence SP8 accordingly.

**HIGH [U:Kimi] — Even with no cloud creds in the container, block host loopback + host-gateway (host.docker.internal / 127.0.0.1) — the host often exposes creds via locally-bound proxies/IMDS mocks.**
- EVIDENCE: Docker Desktop host-gateway and `--add-host` reach the host loopback; localstack/Cloud9/credential-helper daemons listen there.
- RELEVANCE: Surface #2. (See OrbStack item in D.)

**MED [U:Kimi] — Scope git creds to single-repo read-only deploy keys (not PATs) as the *compensating control* for exfil-to-an-allowed-domain.**
- EVIDENCE: an allowlist can't stop `https://github.com/search?q=LEAKED`; a deploy key bound to one repo is useless org-wide even if leaked. safeslop already uses deploy keys — this validates the choice and explains *why* it's load-bearing.
- RELEVANCE: Surface #3+#2.

### C. The TLS/MITM reality for an HTTPS allowlist (non-obvious, high-value)

**HIGH [U:Gemini] — A URL-*path* allowlist needs TLS termination (MITM), and Rust/Node/Java each use their OWN trust store — so either filter by SNI/domain (no MITM) or have the toolchain inject a CA per-language.**
- EVIDENCE: cargo (`CARGO_HTTP_CAINFO`), node (`NODE_EXTRA_CA_CERTS`), and the JVM keystore all ignore the system trust store; a MITMing squid breaks their handshakes with obscure errors unless the CA is injected explicitly.
- RELEVANCE: Surface #2+#4. Decide squid filtering granularity (SNI/domain vs URL-path) with this in mind; if URL-path, the `toolchain:` provisioner must wire each language's CA var.

### D. Isolation boundary mechanics

**HIGH [C, all 3] — Treat Seatbelt as a deprecation-risk best-effort layer: minimal tested profiles + a per-macOS-version regression CI job; steer high-trust workloads to container/VM.**
- EVIDENCE: SBPL is Apple-internal, deprecated since ~2017, and rules (e.g. `network-outbound`) change without docs across releases; Chromium/Bazel use only a tiny tested subset. Don't build your *strongest* guarantee on it.
- RELEVANCE: Surface #1. Pin a CI job that re-validates the `.sb` profiles on each macOS major (they break silently).

**HIGH [U:Gemini] — Disable OrbStack's default host-loopback bridging when building the container+squid topology.**
- EVIDENCE: OrbStack seamlessly bridges container nets to the macOS host (`localhost` reachable) for convenience — a compromised container can then hit unauthenticated host-bound dev servers/admin panels. Topology enforcement fails if the runtime bridges internal nets to host loopback.
- RELEVANCE: Surface #1+#2.

**HIGH [U:Kimi] — Harden the Tart VM tier: read-only VirtioFS sharing + route the VM's vNIC through the same squid allowlist; don't let VM mode be an fs/egress loophole.**
- EVIDENCE: Tart defaults to host-fs sharing via VirtioFS (agent writes malware the host later runs) and NAT egress (unrestricted). The disposable-VM tier must not be *weaker* than the container tier on egress.
- RELEVANCE: Surface #1+#2.

**MED [C, Host+Gemini] — Per-task fresh disposable VM, destroy-on-exit non-skippable; pre-warm from a base snapshot to hide boot latency.**
- EVIDENCE: Firecracker/gVisor/e2b exist because per-invocation disposable microVMs are the only defensible boundary; reuse accumulates trust and becomes lateral-movement surface. Tart cold-boot latency must be hidden (snapshot/suspend) or users disable the VM tier.
- RELEVANCE: Surface #1. Make destroy-on-exit non-skippable (like the snapshot-state hoist).

### E. Toolchain provisioning & safe-installer

**HIGH [C, Host+Kimi+Gemini] — Provision toolchains INSIDE the boundary from a pinned+checksummed manifest, from a CLEAN home; never bind-mount host tool dirs (they carry `~/.npmrc`/`~/.cargo/credentials` tokens). mise has NO built-in checksum verify — add a `sha256` field to the `toolchain:` CUE and fail closed.**
- EVIDENCE: devcontainers/Nix learned reproducibility requires building from a declarative spec within the target; `~/.npmrc` and `~/.cargo/credentials` carry auth tokens, so bind-mounting host tool dirs leaks secrets *and* breaks reproducibility. mise trusts TLS but doesn't verify a checksum manifest — a CDN substitution poisons the compiler the agent then runs.
- RELEVANCE: Surface #4+#5+#3.

**MED [U:Kimi] — Use Nix for the VM/container reproducibility tier and mise for the host/ergonomic tier; don't treat them as interchangeable. Nix's own macOS derivation sandbox is unreliable — Nix = provisioning, not runtime isolation.**
- EVIDENCE: Nix on macOS uses Seatbelt for its sandbox, frequently disabled by users on macOS 13+; the community treats macOS sandboxing as best-effort. Forcing Nix on everyone causes revolt; forcing mise everywhere loses VM-tier reproducibility.
- RELEVANCE: Surface #4+#1. Let `toolchain:` provider be chosen per-environment.

**HIGH [C, Host+Gemini] — Safe-installer must VM-evaluate by *observing behavior* (fs + network diff) during a dry install, offline after the initial download; checksum proves provenance, not honesty.**
- EVIDENCE: a correctly-checksummed artifact can still be malicious (xz, SolarWinds were build-time compromises); behavioral eval catches "does it phone home / write outside prefix / read ~/.aws"; block the installer VM's internet post-download to stop secondary payloads.
- RELEVANCE: Surface #5+#1+#2. Reuses the disposable-VM tier + egress logger from #2/#3 — build those first; SP7 composes them.

### F. Distribution, control plane, GUI ergonomics

**HIGH [C, Host+Gemini] — Authenticate the gRPC Unix-socket peer (LOCAL_PEERCRED uid/pid + code-signature), not just 0700 file perms; any user process can `connect()` to a user-owned socket.**
- EVIDENCE: recurring local-privesc bug class (Docker socket history); robust pattern checks the peer's audit token / code-signing identity. Matters MORE once the control plane can trigger root ops (VM/sysext).
- RELEVANCE: Surface #7. Design peer-auth into the gRPC handshake from the start.

**HIGH [U:Kimi] — Put the socket at a SHORT path (`~/.slop/s.sock`); macOS `sun_path` is 104 bytes — `Application Support` + a long username silently overflows → `bind: invalid argument`.**
- EVIDENCE: `sockaddr_un.sun_path` = 104 bytes incl. null; long paths truncate/fail confusingly.
- RELEVANCE: Surface #7. Correctness fix, free.

**HIGH [U:Kimi] — GUI ergonomics decide adoption: a one-click Fast(sandbox)/Deep(container/VM) trust toggle + a visible egress audit log. Friction kills these tools.**
- EVIDENCE: Qubes is architecturally sound but users disable it for friction; Little Snitch wins by prompting once and remembering. A 30s VM boot for every `git status` → engineers globally disable safeslop within a week.
- RELEVANCE: Surface #7 (the SP7 portal). Boundary selection must be one click, not config-file archaeology. (Contradicts a pure "default = sandbox" stance: default should carry a trust dimension.)

**MED [C, Host+Gemini] — Sparkle auto-update with EdDSA-signed appcasts over HTTPS; two channels (Homebrew tap for `slop`, Sparkle for the app) = two trust roots to protect. Never let the Go binary self-update outside the notarized channel.**
- EVIDENCE: Sparkle's early MITM CVEs came from unsigned/HTTP appcasts; an auto-updater is a deliberate RCE channel, so its signature verification IS the product's security.
- RELEVANCE: Surface #6.

**MED [contested] — Daemon privilege model: unprivileged user LaunchAgent vs SMAppService privileged helper.** Resolved by phase: keep the SP7 control plane **unprivileged** (LaunchAgent / app-spawned, embed the notarized `slop` in the bundle); defer privilege to **SP8**, where the NetworkExtension *must* be hosted by the app as a system extension (apply for the entitlement EARLY — lead time is real).
- RELEVANCE: Surface #6+#7+#2.

---

## Contested decision → FLO hand-off

**1Password SSH-agent socket pass-through.** The current design (specs/0001 §7.1) bind-mounts the
1Password SSH-agent socket into the container (and allowlists it in the sandbox `.sb`). **Kimi [U]
flatly contradicts this**: never pass the agent socket into the boundary — after the first Touch-ID
approval, subsequent requests from the same process are often silent, so any code in the boundary can
sign/authenticate with *any* configured key. Its alternative: use `op` to mint short-lived, single-use
SSH key material, inject as a file, delete after the git op.
→ **This is a genuine, security-load-bearing contested decision.** Recommend a `feedback-loop-optimization`
pass scoring: (a) keep socket pass-through (+ require per-use confirmation, a 1Password setting), vs
(b) mint ephemeral single-use key material via `op`, vs (c) per-profile choice. Default leaning (b) for
untrusted profiles. Do NOT silently keep (a).

---

## Actionables (numbered → project surface)

1. **Add an AWS/GCP credential provider** (Surface #3): aws-vault-style keychain + short STS; gcloud
   ADC with `refresh_token` stripped; reserve a `federation`/`oidc` provider shape in the `#Credentials`
   CUE schema now. Add `~/.aws/credentials` + `application_default_credentials.json` staging-filter
   decoy tests.
2. **Reframe the credential lifecycle to decay-first** (Surface #3): short TTL (5–15 min) as the primary
   control; revoke-on-exit explicitly best-effort (document that `SIGKILL` defeats it).
3. **Non-removable egress baseline** (Surface #2): deny 169.254.0.0/16 + metadata hostnames + host
   loopback/host-gateway; force DNS through the squid resolver and drop raw :53. Bake as baseline rules
   in squid *and* the SP8 filter — not user-editable allowlist entries.
4. **Decide squid filtering granularity with the TLS-trust-store reality** (Surface #2+#4): SNI/domain
   (no MITM) vs URL-path (+ per-language CA injection: `CARGO_HTTP_CAINFO`, `NODE_EXTRA_CA_CERTS`, JVM
   keystore via the `toolchain:` provisioner).
5. **Trust-tiered default + `build-scripts` knob** (Surface #1+#4): default Rust/TS/Java install+build to
   container/VM; add `build-scripts: allow|deny` to `slop.cue`; per-macOS-version Seatbelt regression CI.
6. **Toolchain provisioning hardening** (Surface #4+#5+#3): add `sha256` to the `toolchain:` CUE (mise has
   no checksum verify, fail closed); provision inside the boundary from a clean home; exclude/RO host
   `~/.npmrc`, `~/.cargo/credentials`, `~/.config/gcloud`, and shared package caches.
7. **VM-tier + OrbStack hardening** (Surface #1+#2): Tart VirtioFS read-only + VM egress routed through
   squid + non-skippable destroy-on-exit + snapshot pre-warm; disable OrbStack host-loopback bridging.
8. **Control-plane security** (Surface #7+#6): gRPC peer-auth (LOCAL_PEERCRED + code-signature), short
   `~/.slop/s.sock` path, unprivileged LaunchAgent for SP7, defer privilege/sysext to SP8 (apply for the
   NetworkExtension entitlement early).
9. **SP7 portal ergonomics** (Surface #7): one-click Fast/Deep trust toggle + visible egress audit log;
   boundary selection must not be config archaeology.
10. **Safe-installer = behavioral VM-eval** (Surface #5): fs+network diff during a dry install, offline
    after initial download; checksum is necessary-not-sufficient. Composes #3/#7 machinery.
11. **FLO the 1Password SSH-agent socket decision** (Surface #3) — see above.

---

## Net

The cross-model pass converges on a clear re-prioritization: safeslop has built excellent *git/npm*
credential hygiene and a genuinely correct *container* egress topology, but for a Rust/TS/Java startup
shipping to AWS/GCP the two things that actually matter most are **(a) a cloud-credential story that
doesn't yet exist and must be decay-first, and (b) recognizing that these languages execute attacker
code at install/build/IDE-import time, which makes egress the load-bearing control and makes
"default = Seatbelt" the wrong default for them.** Almost every HIGH actionable composes machinery
safeslop already has (the SSH staging filter, the squid bridge, the disposable VM, the toolchain
provisioner) — the work is mostly *extending* proven patterns to cloud creds, the metadata/DNS egress
holes, and the VM/OrbStack loopholes, plus hardening the new gRPC control plane. One design decision
(1Password agent-socket pass-through) is genuinely contested and should go through FLO, not be decided
by fiat.

## Method footer

- **Lanes (3, blind, same brief):** Anthropic (host `claude` subagent, 18 lessons) · Google **Gemini
  3.1 Pro** via `ai-router` `or_ask`, **ZDR enforced** (15 lessons) · Moonshot **Kimi K2.7** via
  `ai-router` `kimi_ask` (18 lessons). All three available; no lane dropped.
- **Routing/privacy:** Gemini via ai-router (no-training route); Kimi via its subscription key (never
  routed through OpenRouter); host lane on the Anthropic family. Within jojo's privacy hard lines.
- **Orchestration:** host compiled + de-duped + triaged (lanes proposed only; never self-triaged).
- **Pairs with:** `feedback-loop-optimization` (the 1Password decision) and `writing-plans` (turning
  actionables 1–10 into SP plans).
