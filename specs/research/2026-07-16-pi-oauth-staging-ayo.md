# Pi OAuth staging prior-art lessons

Date: 2026-07-16 · Status: compiled ayo input to the security decision

## Question

How should an exact-byte-trusted safeslop profile make one existing host Pi OAuth identity usable inside one container session without projecting host auth state, exposing refresh authority, creating rotation races, or adding a sandbox-facing broker?

Named project surfaces: policy Credentials/evaluation, `stageProfile`, private `stageDir`, container tmpfs home/entrypoint, session credential scopes/failures/teardown, and Pi provider/model selection.

## Method

Blind Kimi, Gemini, GPT, and GLM lanes received one canonical source packet. The host compiled and checked their lessons against current Pi `AuthStorage`/Codex code and current safeslop lifecycle. Sources:

- Kubernetes short-lived projected service-account tokens and projected volumes: <https://kubernetes.io/docs/concepts/security/service-accounts/>, <https://kubernetes.io/docs/concepts/storage/projected-volumes/>
- OAuth 2.0 Security BCP, especially bearer replay and refresh rotation: <https://www.rfc-editor.org/rfc/rfc9700.html>
- Git/Docker credential-helper separation: <https://git-scm.com/docs/api-credentials>, <https://docs.docker.com/reference/cli/docker/login/>
- VS Code Dev Containers credential forwarding as a live-capability counterexample: <https://code.visualstudio.com/remote/advancedcontainers/sharing-git-credentials>
- Pi `docs/providers.md`, `docs/models.md`, `AuthStorage`, and OpenAI Codex provider code.

All four lanes were available. GPT returned its lessons inline rather than writing its lane file; they were included in compilation.

## High-confidence lessons

1. **Require explicit trusted policy authority.** Never infer host OAuth from `agent:"pi"`, a builtin, environment presence, or ordinary projection. Kubernetes' ambient service-account history and VS Code's convenience forwarding show how entering a workload can silently become credential authority.
2. **Extract one access snapshot, not a store.** A synthetic provider-only artifact prevents unrelated provider keys/account metadata/refresh credentials from crossing. Never copy `auth.json` wholesale.
3. **Never duplicate refresh authority.** RFC 9700 refresh rotation and Pi's proper-lockfile serialization make host/container refresh consumers a split-brain invalidation race. Host Pi remains the sole writer/rotator.
4. **Describe authority honestly.** The Codex access token is a replayable provider-default bearer. Safeslop cannot retrofit audience, session, model, spend, or account downscoping. Client model selection and exact egress are compensating controls, not cryptographic scope.
5. **Publish a fixed file through existing custody.** Build a canonical 0600 synthetic auth file under the private 0700 stage, mount the stage read-only, and copy only that file into the container's tmpfs home before Pi starts. Keep values out of argv/env/Compose/inspect/logs/status/workspace.
6. **Read the mutable source consistently.** Pi writes in place while holding `auth.json.lock`; safeslop should use an approved-root descriptor, reject symlink/type/owner/mode/link-count hazards, sandwich a bounded read with lock/stat/identity checks, and retry briefly without taking/removing Pi's lock.
7. **Fail before launch.** Missing/unsafe/busy/malformed/wrong-provider/wrong-type/expired/near-expiry source must yield fixed value-free failure classes and executable remediation—not raw OS/JSON/path output.
8. **Use a non-refreshing Pi representation.** Current Pi resolves an `api_key` auth entry before OAuth refresh logic. Representing the already-issued Codex access bearer as `{"type":"api_key","key":...}` prevents a sandbox refresh attempt; the source expiry remains host-side validation metadata.
9. **Select provider/model in engine-owned argv.** `pi --provider openai-codex --model gpt-5.6-luna` removes settings/bootstrap ambiguity. The signed image must pin a Pi release containing that model.
10. **Wipe locally without claiming revocation.** Stop/reconcile/remove destroy stage and tmpfs copies, but a copied bearer remains valid until issuer expiry/revocation. Status must not set an issuer-revoked claim merely because local files disappeared.

## Contested lessons resolved

- **Audience/session/model-bound token:** desirable in Kubernetes because the issuer supports it; impossible for an already-issued Codex bearer. Rejected as a false claim.
- **Live host helper/broker or re-snapshot renewal:** useful for rotation, but creates a callable host capability and Pi has no reliable live auth reload contract. Deferred.
- **Expose a generic secret-file or startup hook:** rejected; it turns a narrow provider feature into arbitrary file/code injection.
- **Project to host-backed tmpfs:** incorrect for current safeslop. The host stage is private disk custody; only container home is tmpfs. Preserve that honest boundary.
- **Kill the whole session exactly at access expiry:** not required for authority safety because the issuer expires the bearer and Pi receives no refresh. MVP validates launch headroom and requires a new session after provider rejection; an active lifetime guard is deferred rather than introducing a new init/signal supervisor.

## Applied MVP

One policy shape, one provider, one model: `credentials.pi { provider:"openai-codex", model:"gpt-5.6-luna" }`; project profile only; Pi/container/deny only; default host Pi auth source only; access-only synthetic file; 15-minute launch headroom; no renewal/listener/refresh; existing value-free credential scope and structured failure surfaces; existing full teardown custody.
