package safeslop

// "plaintext" is not a valid #SecretRef (must start with op:// or env:).
safeslop: profiles: x: {agent: "shell", environment: "host", secrets: {K: "plaintext"}}
