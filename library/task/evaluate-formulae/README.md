# Evaluate package formulae safely

For risky package or installer evaluation, prefer a disposable `environment:
"vm"` profile in `safeslop.cue`.

```cue
profiles: package_eval: {
	agent:       "shell"
	environment: "vm"
	network:     "deny"
}
```

Then run:

```bash
safeslop trust
safeslop run package_eval
```

Keep host file sharing explicit and copy only the artifacts you intend to review.
Use `safeslop down` to clean up interrupted VM/container sessions.
