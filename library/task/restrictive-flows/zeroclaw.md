# ZeroClaw restrictive-flow notes

Run ZeroClaw with safeslop isolation profiles and keep its own guardrails enabled.

Recommended profile shape:

```cue
profiles: zeroclaw_review: {
	agent:       "shell"
	environment: "container"
	network:     "deny"
}
```

Guidelines:

- Keep workspace access project-scoped.
- Keep supervised autonomy and receipt checks enabled.
- Disable shell-style execution unless the task explicitly needs it.
- Stage repository credentials through safeslop deploy-key or PAT providers.
- Prefer `vm` for untrusted code that needs a disposable machine boundary.
