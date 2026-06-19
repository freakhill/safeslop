package safeslop

// "plaintext" is not a valid #SecretRef (must start with op:// or env:).
safeslop: profiles: x: {agent: "shell", secrets: {K: "plaintext"}}
