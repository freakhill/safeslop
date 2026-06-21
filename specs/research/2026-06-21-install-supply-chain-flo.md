# Install/update supply-chain hardening — decision (FLO-selected)

**Date:** 2026-06-21
**Method:** feedback-loop-optimization (FLO), premium K=2 cross-family. Workers = Opus subagents
(blind, parallel, 8 distinct security lenses → 1 synthesis); evaluators = Kimi K2.7 (criteria in order)
+ Gemini 3.1 Pro (criteria reversed), averaged, blind to lane/score. ZDR/subscription routes only.
**Resolves:** how the Installs-tab install/update flow should defend against supply-chain attacks
("protect against supply-chain as much as possible") without bricking the user's machine ("don't break
people's computers"). Grounded in a full map of the current `internal/engine/install` + `tools` +
Installs-tab code.

## Rubric (locked)

C1 supply-chain-attack resistance (35) · C2 don't-break-the-machine robustness (25) · C3 honesty /
informed-consent (20) · C4 implementability in safeslop (10) · C5 proportionate usability (10).
Anchors (10/7/4/1) at the bottom. Adversarial calibration: a frictionless "click→runs" install must
score LOW on C3; a curl|sh route with no verification must score LOW (1) on C1.

## Result

| Design | C1 | C2 | C3 | C4 | C5 | Weighted |
|----|----|----|----|----|----|----------|
| Baseline (today) | 1.5 | 2 | 1 | 9.5 | 4 | **~26** |
| Ship-first MVP (subset of the winner) | 8 | 9.5 | 9.75 | 10 | 9.75 | **~91** |
| **Maximal synthesis (catalog→store transactional installer)** | 10 | 10 | 10 | 10 | 10 | **~99** |

Cross-family agreement was strong: Kimi and Gemini independently scored the maximal design straight 10s
(each naming the SAME real implementation residuals, below — so this is convergence, not sycophancy),
the MVP C1=8 from both, and the baseline low across C1–C3 / high on C4 (it is, after all, the
already-implemented architecture). The maximal's literal weighted-100 is reported as ~99 under score
bounding — the residuals are accepted implementation cautions, not design defects.

### Load-bearing findings (cross-family consensus)

1. **The baseline's supply-chain floor is the `curl | sh` route (C1≈1).** The MAJORITY of the ~30-tool
   catalog installs via a raw `curl … | sh` piped to `/bin/sh -c` as the user, with ZERO verification
   — both families scored this the cardinal sin. The strong embedded-pin route (sha256 + minisign,
   fail-closed, compiled into the notarized binary) only covers ~2 tools today. **Closing curl|sh is the
   single highest-leverage change.**
2. **Verification and CONTAINMENT are orthogonal and both required.** Crypto-verification authenticates
   *artifacts*; it does nothing for `brew`'s Ruby or a `curl|sh` that *executes attacker code before any
   artifact exists*. Running every installer inside the existing Seatbelt/Tart boundary (credentials
   denied, egress allow-listed, writable surface = an empty stage dir) means even a maintainer-
   compromised package can't breach the machine — it wastes compute, not secrets. This is what lets the
   design defend C1 AND C2 at once.
3. **The verify→move TOCTOU and the destructive `.app` upgrade are the C2 floor.** Today verification
   runs on in-memory bytes then writes to a path (a same-uid process can swap post-verify), and an `.app`
   upgrade `RemoveAll`s the old app *before* the new one is in place (a failed rename loses the app).
   The fix is **descriptor discipline** (hash-through-fd → `fexecve`/`linkat` the *verified* fd) + a
   content-addressed store committed by an **atomic symlink flip** with N kept generations → instant
   rollback. Generations double as a supply-chain mitigation: detect-and-revert.
4. **`--insecure` is itself a supply-chain threat.** Behind a corporate WARP/Zscaler TLS-intercepting
   proxy, child toolchains (npm/pip/uv/cargo) ignore the keychain and fail opaquely → users globally
   disable TLS verification and leave it off. The design must wire the system-keychain CA into each
   toolchain's cert-env so nobody ever reaches for it — while keeping content sha+sig as the real anchor
   so the *sanctioned* MITM is moot.
5. **Time is the defense that sha+sig miss (the xz/liblzma class).** A faithfully-signed, correctly-
   checksummed release can still be malicious (compromised maintainer key / build runner). A freshness
   quarantine (refuse releases younger than ~10 days, the detection window) + a separately-keyed signed
   advisory/revocation feed (refuse/auto-rollback a known-bad sha network-wide without re-shipping
   safeslop, with a signed CVE fast-track to waive quarantine for genuine patches) is the only defense.

### Accepted residuals (named by BOTH evaluators — implementation cautions, not design flaws)

- **Static-URL prefetch for `curl|sh`:** running a vendored installer offline against pre-fetched pinned
  downloads assumes its download URLs are statically determinable. Scripts that dynamically construct
  paths or query an API at runtime won't reduce to pinned bytes → they fall to the **MANUAL** path
  (shown, never auto-run), which is the honest fail-closed outcome.
- **Tart-VM overhead:** reserving a throwaway VM for heavy/unpinnable installers costs disk/boot/memory;
  keep it for the few that need it — Seatbelt is the proportionate default.
- **Rekor/transparency-metadata sourcing:** offline inclusion proofs require the proof to travel with the
  artifact or be curated in the catalog overlay — labor that scales with the catalog. (Deferred in the
  MVP; the spine doesn't depend on it.)

---

## The selected design — catalog→store transactional installer

**Spine.** One transactional installer fronted by a signed, content-addressed **catalog** and backed by
a content-addressed **store**. Every tool is an `#Artifact{url, sha256, minisign(2-of-3 maintainer
co-sign), spki, provenance, rekorRoot, route}` resolved from `catalog.cue` — **embedded in the notarized
binary** as the root of trust, plus an **epoch-gated, advisory-key-signed fetchable overlay** for
between-release pin bumps (embedded epoch = monotonic floor; a lower-epoch or bad-sig fetched catalog is
refused, fail-closed). The binary never runs remote code: it **fetches bytes, verifies bytes, promotes
bytes.** Verification → containment → commit are three fixed stages around every route; only the
per-route *front* differs.

**Per-route verify → contain → commit.**
- **A — pinned artifact (default, the whole catalog migrates here).** Fetch to a same-FS stage opened
  `O_NOFOLLOW`; **hash THROUGH the fd** (`fstat` + read-fd, never the path) → minisign-verify the
  maintainer-signed `SHASUMS` → SLSA/in-toto provenance → **Rekor inclusion proof verified OFFLINE**
  against a log root pinned in the notarized binary → spki-pin the TLS cert → freshness gate. Commit by
  `linkat`/`renameat` the *same fd* into the store. One click.
- **B — brew/cask, RE-VERIFIED not delegated.** Commit-pinned tap; the bottle sha256 from the pinned tap
  AND from brew's API must **agree** (two-source consensus) before fetch; re-verify the downloaded bottle
  fd; no formula eval runs outside the sandbox. One ack.
- **C — `curl|sh` / unpinnable, NEVER piped.** Vendor the script at a pinned sha, pre-fetch every URL it
  reaches to pinned-sha downloads, run it **offline inside the sandbox** (network denied, or allow only
  the artifact host via squid). Not reducible to pinned bytes → **MANUAL** (shown, never auto-run).
  npm-delivered tools → `npm ci --offline` against integrity hashes. Comprehension gate + Touch ID.

Every route's installer runs inside **Seatbelt `sandbox-exec`**: network `deny` → allow-only-the-
artifact-host (squid), `~/.ssh ~/.aws keychain op` auto-denied, writable surface = the empty stage dir
only. Heavy/unpinnable installers → a **throwaway Tart VM**. Only the verified artifact is promoted out.

**Robustness.** `~/.safeslop/store/<tool>/<gen>/` immutable; `current/<tool>` symlink. TOCTOU defeated by
descriptor discipline (above). Commit = atomic symlink flip; keep **N=3 generations** for instant
rollback. Each txn journaled `PREPARE→VERIFIED→COMMITTED`, so a killed install replays clean (no half
state). `.app` upgrades become generations — never `RemoveAll`-then-rename.

**Time dimension.** `Pin.PublishedAt` from the Rekor inclusion time (not the attacker-mutable GitHub
API); refuse releases younger than a ~10-day quarantine; a separately-keyed append-only **signed
Advisory Feed** (monotonic, offline advisory key) lists revoked shas + min-version + `fast_track`
waivers → a known-bad pinned version is refused/auto-rolled-back network-wide **without re-shipping
safeslop**; a signed CVE fast-track waives quarantine for genuine patches (resolves freshness-vs-urgency).

**Consent + transport.** Clicking **Install** opens a **plan sheet** (never an action): version diff,
exact origin URL, verification method with the **literal sha/sig or the word UNVERIFIED**, runs-as-YOU-
vs-contained + blast radius, exact argv. Proportionate gate — verified-pin one click, brew one ack,
`curl|sh` the reused host-launch **comprehension gate** (match an engine-authored risk sentence + Touch
ID). Reuses the cockpit non-color danger channel; no false-urgency counter, no green-while-unverified,
no "remember" on risky routes. Export the system-keychain CA to `~/.safeslop/ca/system-roots.pem`
(read-only, scoped to install child-envs only, NEVER the binary's pin store) and wire
`SSL_CERT_FILE / NODE_EXTRA_CA_CERTS / PIP_CERT / UV_NATIVE_TLS=1 / CARGO_HTTP_CAINFO / GIT_SSL_CAINFO`
so toolchains work behind WARP and nobody reaches for `--insecure`.

**Implementability.** Reuses `internal/engine/install/verify.go` (minisign chain, extends with
provenance+Rekor+spki), the `Pin`/`Action` `Format`+`Sig` manifest (gains `route/spki/provenance/
rekorRoot`), `slop-pinning` (now also gates catalog epoch + bans any `url` lacking a sibling `sha256`),
Seatbelt + squid (egress-guardrail 0008) to wrap installers, the Tart-VM tier for heavy, hostenv's
keychain-CA export for child cert-env, the cockpit consent gates (specs/0030/0031) for the plan sheet +
danger channel, and the existing gRPC `InstallPlan`/`InstallApply` stream. No new infra — it generalizes
Route A and deletes Route-B-trust and Route C.

## Ship-first MVP (validated ~91; build this first)

The cheapest subset that closes the worst gaps on machinery safeslop already has — each item additive,
none re-architecting the spine:
1. **Route C — fetch-pin-sandbox.** Replace every `curl|sh` with fetch → sha256 → embedded-pin-check →
   show-the-script → execute the LOCAL verified file inside Seatbelt + a per-script squid egress
   allowlist. Unpinned/new tools render UNVERIFIED + sandboxed + explicit consent, never silently piped.
   *(closes the C1 floor — highest leverage)*
2. **Route A — atomic install + rollback.** stage → verify → atomic `rename()` (kills the verify→move
   TOCTOU); keep N=1 prior version with `--rollback`; `.app` upgrades use the staged-rename, never
   destructive `RemoveAll`. *(closes the C2 floor)*
3. **Route B — pin + re-verify the brew bottle sha** (single-source first), fail-closed on drift.
4. **Cockpit consent+preview gate** before any remote fetch/run (exact URL, VERIFIED-sha+sig vs
   UNVERIFIED, the literal command; risky routes reuse the host-launch comprehension gate). *(closes C3)*
5. **Freshness-floor warning** (binary carries its pin-set date; a stale pin surfaces "update safeslop").

**Deferred from the MVP (additive):** Rekor offline proofs, 2-of-3 co-sign, two-source brew consensus,
the signed Advisory Feed + CVE fast-track, the Tart-VM heavy path, N=3 journaled generations, full
per-toolchain WARP cert-env wiring.

## Net

Build the catalog→store transactional installer: a signed content-addressed catalog (embedded as the
root of trust) replaces curl|sh and trusted-brew with pinned, re-verified, sandbox-executed,
fd-anchored, atomically-committed, generation-rollback-able installs; freshness quarantine + a signed
revocation/fast-track feed add the time dimension; a plan-sheet + route-proportionate consent gate keep
it honest; keychain-CA cert-env keeps WARP users off `--insecure`. Ship the 5-item MVP first
(curl|sh→fetch-pin-sandbox + atomic-rollback + brew-reverify + consent gate + freshness warning), then
layer the deferred items — each is additive on the same spine.

## Method footer

FLO premium K=2: workers Opus (8 blind lenses — verification-maximalist, eliminate-remote-code,
sandbox-the-installer, transactional-robustness, informed-consent, freshness/revocation, WARP/transport,
pragmatic-MVP — + 1 synthesis) · evaluators Kimi K2.7 (order) + Gemini 3.1 Pro (reversed), averaged,
blind · 2 generations (8 lenses → synthesis), 3 finalists scored (baseline/MVP/maximal), 6 K=2
evaluations · ZDR/subscription routes only. **Process caveat:** Kimi K2.7 (a reasoning model) returned
reasoning that hit the orchestrator's token cap before a clean final-score line on several evals; its
in-reasoning scores were explicit and matched Gemini's clean scores, and the cross-family ORDERING
(baseline ≪ MVP ≪ maximal) is unambiguous — but the exact baseline C2–C5 Kimi numbers are
Gemini-anchored. Re-run with a higher cap + reasoning suppressed if a tighter number is needed.
