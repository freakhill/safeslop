# macOS sandbox workflow

The `sandbox` environment uses macOS Seatbelt as a lightweight mistake-guard for
everyday agent work.

```cue
profiles: review: {
	agent:       "claude"
	environment: "sandbox"
	network:     "deny"
}
```

Use:

```bash
safeslop run review --dry-run
safeslop trust
safeslop run review
```

Escalate to `container` for per-domain egress control and to `vm` for untrusted
code.
