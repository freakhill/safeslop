package safeslop

// Embedded engine schema for safeslop.cue (specs/0001 §6.1). Compiled into the
// binary via go:embed; the external `cue` binary is never needed.
//
// SP1 scope: enough to launch claude/shell under the sandbox-exec boundary.
// credentials (SP2), container/vm (SP3/SP4), and toolchains (SP5) extend this.

// Where the agent runs. SP1 implements "sandbox" (default) and "host"; the
// others are accepted by the schema and land in later sub-projects.
#Environment: "sandbox" | "container" | "vm" | "host"

// What to launch.
#Agent: "claude" | "shell" | "opencode"

// Coarse egress policy for the sandbox-exec boundary. Not a URL allowlist —
// that is the container's job (specs/0001 §6.2).
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
#SshCreds: {
	write?: bool | *false
	ttl?:   string | *"1h"
}

// Credential providers a profile uses (SP2: pnpm; SP/0009: aws/gcp; SP/0010: kube; SP/0011: ssh).
#Credentials: {
	pnpm?: [...#PnpmRegistry]
	aws?:  #AwsSso
	gcp?:  #GcpAdc
	kube?: #KubeCluster
	ssh?:  #SshCreds
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
	environment: #Environment | *"sandbox"
	// Directory the boundary confines file access to. Empty (default) means the
	// directory safeslop was invoked from.
	workspace?: string
	network:    #Network | *"deny"
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
