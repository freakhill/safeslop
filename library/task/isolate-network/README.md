# Isolate network access

Use `environment: "container"` with `network: "deny"` for per-domain HTTP(S)
egress control. The Go engine launches the agent on an internal network and
routes HTTP(S) through the allowlist proxy.

```cue
profiles: net_review: {
	agent:       "claude"
	environment: "container"
	network:     "deny"
	egress:      [".internal.example.com"]
}
```

Inspect and run:

```bash
safeslop run net_review --dry-run
safeslop trust
safeslop run net_review
```

Use `network: "allow"` only when direct egress is explicitly intended.
