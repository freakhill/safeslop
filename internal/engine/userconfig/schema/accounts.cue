package safeslopaccounts

// Host-only forge account links (~/.config/safeslop/accounts.cue). Holds non-secret ids +
// secret *refs* ONLY — never token/PEM values (specs/0069 L1). This file is never serialized
// into stage dirs, compose env, or IPC (specs/0069 L5). Key convention: "host/owner".
accounts: [string]: #Account

#Account: {
	forge: "github" | "forgejo"
	host:  string
	owner: string

	github?:  #GithubAccount
	forgejo?: #ForgejoAccount

	// The per-forge block matching `forge` is required; the schema rejects a link that names a
	// forge kind but omits its block.
	if forge == "github" {
		github: #GithubAccount
	}
	if forge == "forgejo" {
		forgejo: #ForgejoAccount
	}
}

#GithubAccount: {
	appID:          int & >0
	installationID: int & >0
	privateKeyRef:  string // secret ref (e.g. op://…), resolved on the host at mint time
}

#ForgejoAccount: {
	tokenRef: string // secret ref, resolved on the host at stage time
	sshPort?: int & >0
}
