package safeslop

// Embedded engine schema for safeslop.cue (specs/0001 §6.1). Compiled into the
// binary via go:embed; the external `cue` binary is never needed.
//
// Scope: launch claude/shell/pi under an isolation boundary. credentials (SP2),
// container/vm (SP3/SP4), and toolchains (SP5) extend this.

// Where the agent runs (specs/0053 removed the macOS sandbox/Seatbelt tier):
//   host      — no isolation boundary; runs as you
//   container — Docker + egress allowlist (network-bound agents belong here)
// Required — there is no default, so a profile must always state its isolation
// explicitly (a security tool must never silently run weaker than intended).
#Environment: "container" | "host"

// What to launch. "claude-code" is accepted as a user-facing alias and
// normalized to the canonical "claude" engine value after decode. fish/zsh are
// first-class shell agents; the generic "shell" is a profile-only legacy value
// (handled by `safeslop run` but not accepted by `session create`).
#Agent: "claude" | "claude-code" | "shell" | "pi" | "fish" | "zsh"

// Coarse egress policy. "deny" + environment:container is the egress-allowlisted
// path (the per-domain allowlist is the container's job, specs/0001 §6.2);
// "allow" opens egress. On host, network is always unrestricted regardless.
#Network: "deny" | "allow"

// A secret reference resolved at launch (specs/0001 §7): a 1Password URI
// ("op://vault/item/field") or "env:NAME" to read from the launching shell.
// Values are never written to disk except in the ephemeral, wiped-on-exit stage.
#SecretRef: string & =~"^(op://|env:).+"

// An npm/pnpm registry to authenticate. The token is sourced from a secret ref
// and staged into a scoped .npmrc, wiped on exit (specs/0001 §7.2).
#PnpmRegistry: {
	host:   string | *"registry.npmjs.org"
	token:  #SecretRef
	scope?: string
}

// AWS creds minted from an IAM Identity Center (SSO) profile. `aws configure
// export-credentials --profile <profile>` resolves SSO to short-lived role creds;
// the user runs `aws sso login --profile <profile>` on the host first (specs/0009).
#AwsSso: {
	profile: string // a named profile configured for SSO in ~/.aws/config
	region?: string
	// Optional scope-first downscope: assume roleArn with an inline sessionPolicy (JSON), using
	// the SSO creds, so the staged creds are least-privilege. Both required together; the role
	// must be assumable by your SSO identity.
	roleArn?:       string
	sessionPolicy?: string
}

// GCP creds from Application Default Credentials. A short-lived access token is
// minted via `gcloud auth application-default print-access-token` and delivered
// via CLOUDSDK_AUTH_ACCESS_TOKEN only; the long-lived refresh token is never
// staged (specs/0009, specs/0078).
#GcpAdc: {
	// Optional: downscope the minted access token to these OAuth scopes (scope-first
	// least-privilege). Empty = ADC's default (broad) scopes. E.g.
	// ["https://www.googleapis.com/auth/devstorage.read_only"].
	scopes?: [...string]
}

// A single Kubernetes cluster to pre-authenticate for (specs/0010). Set exactly one
// of eks/gke. The host mints a short-lived bearer token (aws eks get-token /
// gke-gcloud-auth-plugin) and stages a one-cluster kubeconfig, so the agent's kubectl
// needs neither cloud creds nor the cloud CLI inside the boundary. Decay-first: the
// token expires (~15m EKS / ~1h GKE); cleanup is the stageDir wipe.
#KubeCluster: {
	eks?: #EksCluster
	gke?: #GkeCluster
}

// An EKS cluster. The host resolves it with `aws eks get-token` + `aws eks
// describe-cluster`, using the named SSO profile (or the default) — run `aws sso
// login` first.
#EksCluster: {
	name:     string
	region?:  string
	profile?: string
}

// A GKE cluster. The host mints the token with `gke-gcloud-auth-plugin` (ADC) and
// resolves the endpoint/CA with `gcloud container clusters describe`. `location` is
// the cluster's zone or region (e.g. "europe-west1" or "europe-west1-b").
#GkeCluster: {
	name:     string
	location: string
	project?: string
}

// SSH/Git auth into the boundary as a per-run, repo-scoped ephemeral deploy key — the
// 1Password agent socket is never passed in (specs/0001 §7.1, specs/0011). The host mints
// the key (read-only by default); write:true is lint-gated on network:deny.
// One repository in a multi-repo credential, with its own access level (specs/0047 P2).
#RepoCred: {
	repo:   string // "owner/name"
	write?: bool | *false
}

#GithubCreds: {
	mode:   "app" | "pat" | *"app"
	write?: bool | *false
	ttl?:   string | *"1h"
	// PAT opt-in: stage one existing fine-grained token from this secret ref as an HTTPS credential
	// for every repo below. The token value is never embedded in git config. safeslop does not mint
	// or revoke account PATs; rotate/revoke them at the forge (specs/0047 P2.3).
	pat?: #SecretRef
	// App/PAT both stage over HTTPS via per-URL credential helpers (specs/0069, generalizing the
	// specs/0047 renderer). App-token permissions are token-wide, so repos partition into ro/rw
	// scopes by write. Omit repos in app mode to infer the single repo from the cwd origin.
	repos?: [...#RepoCred]
	// Opt-in staged API token; permissions are token-wide (specs/0068 F5). Staging errors in P1.
	api?: #GithubApi
	if mode == "pat" {
		pat:   #SecretRef
		repos: [...#RepoCred] & [_, ...]
	}
}

#GithubApi: {
	enabled?:     bool | *false
	permissions?: [...string]
}

// Forgejo/Gitea ephemeral deploy key — the non-GitHub-forge sibling of #GithubCreds (specs/0047). The
// instance has no `gh`-style ambient auth, so `token` is an explicit secret ref. `url` is the
// instance base (e.g. "https://codeberg.org"); when omitted the host is inferred from the cwd
// origin remote. The SSH host key is pinned per run via ssh-keyscan at stage time.
#ForgejoCreds: {
	write?: bool | *false
	ttl?:   string | *"1h"
	url?:   string
	// Multi-repo: one deploy key per entry. `url` is required in multi-repo mode (no single origin to
	// infer the instance from). The account token that registers each key comes from
	// ~/.config/safeslop/accounts.cue (safeslop creds link forgejo), never from this file (specs/0069).
	repos?:      [...#RepoCred]
	"ssh-port"?: int | *22
	// Opt-in staged API token (P2 staging). Forgejo tokens are account-wide, so enabling requires
	// an explicit ackAccountWide (enforced at load, specs/0068 F5).
	api?: #ForgejoApi
}

#ForgejoApi: {
	enabled?:        bool | *false
	ackAccountWide?: bool | *false
}

// Credential providers a profile uses (SP2: pnpm; SP/0009: aws/gcp; SP/0010: kube; SP/0011: github,
// specs/0069; specs/0047: forgejo).
#Credentials: {
	pnpm?:    [...#PnpmRegistry]
	aws?:     #AwsSso
	gcp?:     #GcpAdc
	kube?:    #KubeCluster
	github?:  #GithubCreds
	forgejo?: #ForgejoCreds
}

// A pinned toolchain layered onto any environment (SP5), orthogonal to `environment`.
//   kind: which provider provisions tools — mise (version manager + task runner) or nix
//         (flakes; pinned inputs = the safe-install story).
//   run:  optional — a mise task name (kind=mise) or a nix app ref like ".#app" (kind=nix)
//         to launch INSTEAD of the profile's agent. Absent => the agent is wrapped so the
//         pinned toolchain is on PATH.
#Toolchain: {
	kind: "mise" | "nix" | "none"
	run?: string
}

// One allowlisted host source copied read-only into the ephemeral home (specs/0096 T1 FLO
// verdict). Engine-owned in MVP: user-authored projection in safeslop.cue is rejected at
// load (see policy.go); only embedded builtins populate this. Copy-based, never symlinked:
// sources stage read-only under opaque /safeslop/projected/<id> paths and the entrypoint
// copies them into /home/agent tmpfs.
#ProjectionItem: {
	source: string
	// Destination under /home/agent; derived from source when omitted.
	target?: string
	// file = one regular file; dir = a directory expanded per-file; glob = a filepath.Match
	// pattern expanded per-file. Defaults to file.
	kind: "file" | "dir" | "glob" | *"file"
	// true => absent/unreadable source skips legibly; false => required, fail closed. Defaults
	// to true so convenience config (shell rc, optional skills) does not block launch.
	optional?: bool | *true
	// Provenance/legibility label (e.g. "pi-agent", "fish", "zsh", "starship").
	label?: string
}

// Projection is the engine-owned read-only host config projection model: a positive
// allowlist of host config sources copied into the ephemeral home. There is no broad
// $HOME mount and no credential-directory projection; the resolver (container package)
// hard-rejects excluded roots, symlink components, path escapes, duplicates, and
// non-regular files (specs/0096, specs/research/2026-07-12-safe-home-projection-flo.md).
#Projection: {
	enabled?: bool | *false
	items?:  [...#ProjectionItem]
}

#Profile: {
	agent:       #Agent
	environment: #Environment
	// Directory the boundary confines file access to. Empty (default) means the
	// directory safeslop was invoked from.
	workspace?: string
	network: #Network | *"deny"
	// Extra egress domains for environment:container with network:deny — unioned with the
	// base allowlist + the agent's built-in providers (specs/0046). A leading dot
	// (".example.com") is a subdomain suffix match; a bare host is exact. Ignored on
	// network:allow and on host.
	egress?: [...string]
	// Env var name -> secret ref; injected into the agent's environment at launch.
	secrets?: {[string]: #SecretRef}
	// Credentials staged before launch and wiped on exit.
	credentials?: #Credentials
	// Optional pinned toolchain, provisioned into the chosen environment (SP5).
	toolchain?: #Toolchain
	// Build-time packages baked into the container image, from the curated catalog
	// (specs/0058). `bundles` are named sets; `packages` are à la carte. Names are
	// validated against the catalog by the engine (unknown => error); the agent's
	// default bundle is included unless `bareAgent` explicitly opts out. Orthogonal
	// to `toolchain` (a build-time bake vs a runtime version-manager).
	bundles?:   [...string]
	packages?:  [...string]
	bareAgent?: bool | *false
	// Read-only allowlist projection of host config into the ephemeral home (specs/0096).
	// Engine-owned in MVP: a safeslop.cue that sets this is rejected at load with a
	// spec-cited error; only embedded builtin profiles (pi/claude/fish/zsh) populate it.
	projection?: #Projection
}

#Slop: {
	version:  int | *1
	profiles: {[string]: #Profile}
}

safeslop: #Slop
