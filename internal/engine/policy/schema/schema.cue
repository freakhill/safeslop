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
// minted via `gcloud auth application-default print-access-token`; the long-lived
// refresh token is never staged (specs/0009).
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

#SshCreds: {
	mode:   "deploy-key" | "pat" | *"deploy-key"
	write?: bool | *false
	ttl?:   string | *"1h"
	// PAT opt-in (specs/0047 P2.3): stage one existing fine-grained token from this secret ref
	// as an HTTPS credential for every repo below. The token value is never embedded in git config.
	// safeslop does not mint or revoke account tokens; rotate/revoke PATs at the forge.
	pat?: #SecretRef
	// Multi-repo: deploy-key mode mints one ephemeral key per entry, staged with distinct SSH host
	// aliases + git insteadOf rewrites. PAT mode uses the same repo list for HTTPS rewrites.
	// Omit to infer the single repo from the cwd origin in deploy-key mode.
	repos?: [...#RepoCred]
	if mode == "pat" {
		pat:   #SecretRef
		repos: [...#RepoCred] & [_, ...]
	}
}

// Forgejo/Gitea ephemeral deploy key — the non-GitHub-forge sibling of #SshCreds (specs/0047). The
// instance has no `gh`-style ambient auth, so `token` is an explicit secret ref. `url` is the
// instance base (e.g. "https://codeberg.org"); when omitted the host is inferred from the cwd
// origin remote. The SSH host key is pinned per run via ssh-keyscan at stage time.
#ForgejoCreds: {
	mode:   "deploy-key" | "pat" | *"deploy-key"
	write?: bool | *false
	ttl?:   string | *"1h"
	url?:   string
	// Deploy-key mode API token, used to register/revoke ephemeral keys. PAT mode does not need it.
	token?: #SecretRef
	// PAT opt-in (specs/0047 P2.3): stage one existing fine-grained Forgejo/Gitea token from this
	// secret ref as an HTTPS credential for every repo below. The token value is never embedded in git config.
	// safeslop does not mint or revoke account tokens; rotate/revoke PATs at the forge.
	pat?: #SecretRef
	// Multi-repo: deploy-key mode mints one deploy key per entry; PAT mode uses the same repo list
	// for HTTPS rewrites. `url` is required in multi-repo/PAT mode (no single origin to infer the instance from).
	repos?:      [...#RepoCred]
	"ssh-port"?: int | *22
	if mode == "deploy-key" {
		token: #SecretRef
	}
	if mode == "pat" {
		url:   string
		pat:   #SecretRef
		repos: [...#RepoCred] & [_, ...]
	}
}

// Credential providers a profile uses (SP2: pnpm; SP/0009: aws/gcp; SP/0010: kube; SP/0011: ssh;
// specs/0047: forgejo).
#Credentials: {
	pnpm?:    [...#PnpmRegistry]
	aws?:     #AwsSso
	gcp?:     #GcpAdc
	kube?:    #KubeCluster
	ssh?:     #SshCreds
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
}

#Slop: {
	version:  int | *1
	profiles: {[string]: #Profile}
}

safeslop: #Slop
