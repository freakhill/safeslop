package creds

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/secrets"
)

// RefStatus classifies a declared credential's readiness. Ref-backed statuses come from a
// value-free probe (the resolved value is discarded — specs/0067 posture 2); ambient/ephemeral
// are honest non-probes (nothing static to resolve).
type RefStatus string

const (
	StatusResolvable    RefStatus = "resolvable"     // ref resolves now (value discarded)
	StatusMissing       RefStatus = "missing"        // env var unset / op item not found / unsupported ref shape
	StatusOpSignedOut   RefStatus = "op-signed-out"  // op:// ref but 1Password not signed in
	StatusOpUnavailable RefStatus = "op-unavailable" // op:// ref but `op` CLI absent
	StatusEphemeral     RefStatus = "ephemeral"      // minted per-session; no static ref to probe
	StatusAmbient       RefStatus = "ambient"        // host ambient auth (SSO/ADC/cloud), validated at stage time
)

// CredRow is one declared credential in one profile, with its source ref (never a value) and a
// value-free readiness status. It is the wire row behind `safeslop creds list` (specs/0067).
type CredRow struct {
	Profile string    `json:"profile"`
	Kind    string    `json:"kind"`  // secret|pnpm|ssh|forgejo|aws|gcp|kube
	Name    string    `json:"name"`  // env var (secret), host (pnpm), repo/"origin" (ssh/forgejo), cluster/profile (aws/gcp/kube)
	Scope   string    `json:"scope"` // extra detail: mode/write/ttl/region/scope
	Ref     string    `json:"ref"`   // op://.../env:NAME source ref, or "" for ambient/ephemeral (refs are not values)
	Status  RefStatus `json:"status"`
}

// OpState is the once-per-report 1Password CLI state, surfaced so the UI can explain
// op-signed-out/op-unavailable statuses without re-probing.
type OpState struct {
	Available bool `json:"available"`
	SignedIn  bool `json:"signedIn"`
}

// Report is the full credential posture of a config: the op state plus one row per declared
// credential across all profiles.
type Report struct {
	Op   OpState   `json:"op"`
	Rows []CredRow `json:"credentials"`
}

// Prober supplies the value-free resolvability primitives Inspect needs, injected so tests are
// hermetic (no live `op`, no process env). Production wires it via DefaultProber.
type Prober struct {
	OpAvailable func() bool
	OpSignedIn  func(ctx context.Context) bool
	LookupEnv   func(name string) (string, bool)
	// ResolveOp attempts to resolve an op:// ref, returning nil iff resolvable. The implementation
	// MUST discard the resolved value — Inspect never receives it (the redaction boundary).
	ResolveOp func(ctx context.Context, ref string) error
}

// DefaultProber wires a Prober to the real secrets package. ResolveOp calls secrets.Resolve and
// keeps only the error, discarding the value (specs/0067 posture 2).
func DefaultProber() Prober {
	return Prober{
		OpAvailable: secrets.OpAvailable,
		OpSignedIn:  secrets.OpSignedIn,
		LookupEnv:   os.LookupEnv,
		ResolveOp: func(ctx context.Context, ref string) error {
			_, err := secrets.Resolve(ctx, ref)
			return err
		},
	}
}

// Inspect enumerates every credential declared across cfg's profiles into rows, tagging each with a
// value-free readiness status (specs/0067). op state is probed once up front so op-down statuses
// short-circuit with no resolution attempt and no value touched. Rows are sorted by
// (profile, kind, name) for stable output.
func Inspect(ctx context.Context, cfg *policy.Config, p Prober) Report {
	op := OpState{Available: p.OpAvailable()}
	if op.Available {
		op.SignedIn = p.OpSignedIn(ctx)
	}
	var rows []CredRow
	if cfg != nil {
		for name, prof := range cfg.Profiles {
			rows = append(rows, inspectProfile(ctx, name, prof, op, p)...)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Profile != b.Profile {
			return a.Profile < b.Profile
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	return Report{Op: op, Rows: rows}
}

// inspectProfile turns one profile's secrets + credential providers into rows.
func inspectProfile(ctx context.Context, profile string, prof policy.Profile, op OpState, p Prober) []CredRow {
	var rows []CredRow
	row := func(kind, name, scope, ref string, status RefStatus) {
		rows = append(rows, CredRow{Profile: profile, Kind: kind, Name: name, Scope: scope, Ref: ref, Status: status})
	}

	// Secrets: env var name -> source ref.
	for name, ref := range prof.Secrets {
		row("secret", name, "", ref, probeRef(ctx, ref, op, p))
	}

	c := prof.Credentials
	if c == nil {
		return rows
	}

	// pnpm: one registry per host; the _authToken is a source ref.
	for _, r := range c.Pnpm {
		row("pnpm", hostOrElse(r.Host, "registry.npmjs.org"), r.Scope, r.Token, probeRef(ctx, r.Token, op, p))
	}

	// github: pat mode probes its token ref; app mode is ephemeral (minted per session). The row
	// kind stays "ssh" until the specs/0069 T4 inspect rework; the provider is credentials.github.
	if s := c.Github; s != nil {
		if s.Mode == "pat" {
			for _, name := range repoNames(s.Repos) {
				row("ssh", name, "pat "+access(s.Write), s.Pat, probeRef(ctx, s.Pat, op, p))
			}
		} else {
			for _, name := range repoNames(s.Repos) {
				row("ssh", name, "app "+access(s.Write)+ttl(s.Ttl), "", StatusEphemeral)
			}
		}
	}

	// forgejo: the deploy-key registration token now lives in ~/.config/safeslop/accounts.cue
	// (safeslop creds link forgejo), not in safeslop.cue, so readiness is link-dependent and resolved
	// per session — like GitHub app mode. inspect reports it value-free as ephemeral; probing the
	// linked token is `safeslop creds status` (specs/0069 T5/T6).
	if f := c.Forgejo; f != nil {
		for _, name := range repoNames(f.Repos) {
			row("forgejo", name, "deploy-key "+access(f.Write)+ttl(f.Ttl), "", StatusEphemeral)
		}
	}

	// Cloud creds use host ambient auth (SSO/ADC/cloud), validated only at stage time: ambient.
	if a := c.Aws; a != nil {
		row("aws", a.Profile, strings.TrimSpace(a.Region+" "+a.RoleArn), "", StatusAmbient)
	}
	if c.Gcp != nil {
		row("gcp", "adc", strings.Join(c.Gcp.Scopes, ","), "", StatusAmbient)
	}
	if k := c.Kube; k != nil {
		switch {
		case k.Eks != nil:
			row("kube", k.Eks.Name, "eks "+k.Eks.Region, "", StatusAmbient)
		case k.Gke != nil:
			row("kube", k.Gke.Name, "gke "+k.Gke.Location, "", StatusAmbient)
		}
	}
	return rows
}

// probeRef classifies a source ref without revealing its value. op state is precomputed, so an
// op:// ref with op down short-circuits to op-unavailable/op-signed-out with no resolution attempt
// (specs/0067 posture 3). An empty ref is treated as missing (a ref-backed field left unset).
func probeRef(ctx context.Context, ref string, op OpState, p Prober) RefStatus {
	switch {
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimPrefix(ref, "env:")
		if v, ok := p.LookupEnv(name); ok && v != "" {
			return StatusResolvable
		}
		return StatusMissing
	case strings.HasPrefix(ref, "op://"):
		if !op.Available {
			return StatusOpUnavailable
		}
		if !op.SignedIn {
			return StatusOpSignedOut
		}
		if err := p.ResolveOp(ctx, ref); err != nil {
			return StatusMissing
		}
		return StatusResolvable
	default:
		// "" (unset ref-backed field) or an unsupported shape: nothing resolvable.
		return StatusMissing
	}
}

// repoNames returns the declared repo names, or ["origin"] when none are listed (single-repo mode
// infers the repo from the cwd origin at stage time — the surface can't know it, so it shows origin).
func repoNames(repos []policy.RepoCred) []string {
	if len(repos) == 0 {
		return []string{"origin"}
	}
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Repo
	}
	return names
}

func access(write bool) string {
	if write {
		return "rw"
	}
	return "ro"
}

func ttl(t string) string {
	if t == "" {
		return ""
	}
	return " ttl=" + t
}

func hostOrElse(host, dflt string) string {
	if host == "" {
		return dflt
	}
	return host
}
