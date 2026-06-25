# VM layer

The VM tier launches disposable sessions for high-risk workflows. It is the
heaviest boundary and should be used when code is untrusted or when a disposable
machine is the desired safety property.

Example profile:

```cue
profiles: vm_review: {
	agent:       "shell"
	environment: "vm"
	network:     "deny"
}
```

Use `safeslop run vm_review` to launch and `safeslop down` to clean up interrupted
container/VM sessions.
