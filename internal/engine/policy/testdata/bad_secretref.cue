package slop

// "plaintext" is not a valid #SecretRef (must start with op:// or env:).
slop: profiles: x: {agent: "shell", secrets: {K: "plaintext"}}
