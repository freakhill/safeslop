# Launch an agent with safeslop

Agent launches are policy-driven through `safeslop.cue`.

```cue
profiles: review: {
	agent:       "claude"
	environment: "sandbox"
	network:     "deny"
}
```

Run:

```bash
safeslop validate
safeslop trust
safeslop run review
```

For Claude and OpenCode profiles, safeslop seeds bundled project defaults
non-clobberingly before launch. Existing project settings are left untouched.
