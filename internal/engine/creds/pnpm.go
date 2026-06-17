// Package creds stages credential artifacts for a launch and is wiped on exit
// by the run lifecycle (specs/0001 §7).
//
// SP2 implements the pnpm/npm registry helper: a scoped .npmrc whose _authToken
// is sourced from a secret ref (op:// or env:), so a sandboxed `pnpm install`
// can reach a private registry without ever exposing the user's permanent token
// to the agent. gh/forgejo ephemeral-key providers follow.
package creds

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/secrets"
)

// RenderNpmrc renders .npmrc content for the registries, using the already-
// resolved tokens (tokens[i] pairs with regs[i]). Kept pure for testing.
func RenderNpmrc(regs []policy.PnpmRegistry, tokens []string) string {
	var b strings.Builder
	for i, r := range regs {
		host := r.Host
		if host == "" {
			host = "registry.npmjs.org"
		}
		if r.Scope != "" {
			fmt.Fprintf(&b, "%s:registry=https://%s/\n", r.Scope, host)
		}
		fmt.Fprintf(&b, "//%s/:_authToken=%s\n", host, tokens[i])
	}
	return b.String()
}

// StagePnpm resolves each registry's token, writes a 0600 .npmrc into stageDir,
// and returns env additions pointing npm/pnpm at it. It is a no-op (nil env)
// when there are no pnpm credentials.
func StagePnpm(ctx context.Context, creds *policy.Credentials, stageDir string) ([]string, error) {
	if creds == nil || len(creds.Pnpm) == 0 {
		return nil, nil
	}
	tokens := make([]string, len(creds.Pnpm))
	for i, r := range creds.Pnpm {
		v, err := secrets.Resolve(ctx, r.Token)
		if err != nil {
			host := r.Host
			if host == "" {
				host = "registry.npmjs.org"
			}
			return nil, fmt.Errorf("pnpm registry %s: %w", host, err)
		}
		tokens[i] = v
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}
	npmrc := filepath.Join(stageDir, ".npmrc")
	if err := os.WriteFile(npmrc, []byte(RenderNpmrc(creds.Pnpm, tokens)), 0o600); err != nil {
		return nil, err
	}
	// npm and pnpm both honor NPM_CONFIG_USERCONFIG for the user-level .npmrc.
	return []string{"NPM_CONFIG_USERCONFIG=" + npmrc}, nil
}
