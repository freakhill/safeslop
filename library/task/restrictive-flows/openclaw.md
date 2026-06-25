# OpenClaw restrictive-flow notes

OpenClaw's risk surface is broader than a code-only agent because messaging
channels become both input and output paths. Run it only through explicit
`safeslop` profiles with narrow workspace and credential scope.

Recommended profile shape:

```cue
profiles: openclaw_review: {
	agent:       "shell"
	environment: "container"
	network:     "deny"
	egress:      ["api.messaging-provider.example"]
}
```

Guidelines:

- Enable only the channels needed for the task.
- Treat persona, memory, and channel messages as untrusted input.
- Stage credentials through `credentials:` or `secrets:`; do not mount host
  credential directories.
- Prefer deploy keys or narrowly scoped PATs for repository access.
