// Package hostexec resolves and runs safeslop-owned host helpers through the sanitized hostenv PATH.
//
// The reconstructed host environment is rich and may contain credential authority. This package uses
// it only as a lookup source plus a value source for explicit allowlists; it never hands the whole
// environment to helper subprocesses.
package hostexec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/hostenv"
)

// Class identifies the security role of a host helper.
type Class string

const (
	ClassCredential Class = "credential"
	ClassRuntime    Class = "runtime"
	ClassDiagnostic Class = "diagnostic"
)

// EnvPolicy selects the minimal environment allowlist used for a helper subprocess.
type EnvPolicy string

const (
	EnvCredential EnvPolicy = "credential"
	EnvOp         EnvPolicy = "op"
	EnvAWS        EnvPolicy = "aws"
	EnvGCloud     EnvPolicy = "gcloud"
	EnvGitSSH     EnvPolicy = "git-ssh"
	EnvRuntime    EnvPolicy = "runtime"
	EnvDiagnostic EnvPolicy = "diagnostic"
)

var (
	ErrNotFound     = errors.New("host helper not found")
	ErrShadowed     = errors.New("host helper shadowed")
	ErrRelativePath = errors.New("host helper relative path refused")
)

// LookupEnv is the subset of hostenv.Env used by the resolver. Tests inject fakes.
type LookupEnv interface {
	PATH() string
	Get(string) (string, bool)
	LookPath(string) (string, bool)
	LookAll(string) []string
	// SameFile reports whether two resolved helper paths are the same file object
	// (dev+inode, following symlinks). It is the identity basis for collapsing
	// same-binary PATH aliases without weakening distinct-binary shadow refusal
	// (specs/0095). A stat/compare error is returned and never collapses to equal.
	SameFile(pathA, pathB string) (bool, error)
}

// Spec describes one helper resolution and execution policy.
type Spec struct {
	Name    string
	Class   Class
	Env     EnvPolicy
	Purpose string
}

// Resolved is the absolute helper identity selected from sanitized PATH.
type Resolved struct {
	Name     string
	Path     string
	All      []string
	Explicit bool
	Spec     Spec
}

// Inspection is a non-failing diagnostic view of a helper on sanitized PATH.
type Inspection struct {
	Name     string
	Path     string
	All      []string
	Present  bool
	Shadowed bool
	Err      error
}

// Resolver resolves helpers against a sanitized lookup environment.
type Resolver struct {
	env LookupEnv
}

// Default returns a resolver backed by the reconstructed host environment.
func Default() *Resolver { return New(hostenv.Reconstruct()) }

// New returns a resolver backed by env.
func New(env LookupEnv) *Resolver { return &Resolver{env: env} }

// CredentialSpec builds a credential-critical helper spec with the helper's narrow env policy.
func CredentialSpec(name, purpose string) Spec {
	return Spec{Name: name, Class: ClassCredential, Env: credentialEnvFor(name), Purpose: purpose}
}

// OpSpec builds the common 1Password helper spec.
func OpSpec(purpose string) Spec { return CredentialSpec("op", purpose) }

// RuntimeSpec builds a container-runtime helper spec.
func RuntimeSpec(name, purpose string) Spec {
	return Spec{Name: name, Class: ClassRuntime, Env: EnvRuntime, Purpose: purpose}
}

// DiagnosticSpec builds a diagnostic-only helper inspection/probe spec.
func DiagnosticSpec(name, purpose string) Spec {
	return Spec{Name: name, Class: ClassDiagnostic, Env: EnvDiagnostic, Purpose: purpose}
}

func credentialEnvFor(name string) EnvPolicy {
	switch name {
	case "op":
		return EnvOp
	case "aws":
		return EnvAWS
	case "gcloud", "gke-gcloud-auth-plugin":
		return EnvGCloud
	case "git", "ssh-keygen", "ssh-keyscan":
		return EnvGitSSH
	default:
		return EnvCredential
	}
}

// Resolve returns the one absolute executable path for spec, or a typed fail-closed error.
func (r *Resolver) Resolve(spec Spec) (Resolved, error) {
	if r == nil || r.env == nil {
		return Resolved{}, fmt.Errorf("%w: no host lookup environment", ErrNotFound)
	}
	name := spec.Name
	if name == "" {
		return Resolved{}, fmt.Errorf("%w: empty host helper name", ErrNotFound)
	}
	if strings.Contains(name, "/") && !filepath.IsAbs(name) {
		return Resolved{}, resolveError{kind: ErrRelativePath, spec: spec}
	}
	all := r.env.LookAll(name)
	if len(all) == 0 {
		return Resolved{}, resolveError{kind: ErrNotFound, spec: spec}
	}
	if !filepath.IsAbs(all[0]) {
		return Resolved{}, fmt.Errorf("%w: host helper %q resolved to non-absolute path %q", ErrNotFound, name, all[0])
	}
	if len(all) > 1 {
		return Resolved{}, resolveError{kind: ErrShadowed, spec: spec, paths: all}
	}
	return Resolved{Name: name, Path: all[0], All: append([]string(nil), all...), Explicit: filepath.IsAbs(name), Spec: spec}, nil
}

// Inspect reports sanitized-PATH helper state without failing on a shadowed helper.
func (r *Resolver) Inspect(name string) Inspection {
	if r == nil || r.env == nil {
		return Inspection{Name: name, Err: ErrNotFound}
	}
	if strings.Contains(name, "/") && !filepath.IsAbs(name) {
		return Inspection{Name: name, Err: ErrRelativePath}
	}
	all := r.env.LookAll(name)
	if len(all) == 0 {
		return Inspection{Name: name, Err: ErrNotFound}
	}
	return Inspection{
		Name:     name,
		Path:     all[0],
		All:      append([]string(nil), all...),
		Present:  true,
		Shadowed: len(all) > 1,
	}
}

// Preflight resolves every spec before a staging operation writes credential artifacts.
func (r *Resolver) Preflight(specs ...Spec) error {
	seen := map[string]bool{}
	for _, spec := range specs {
		key := string(spec.Class) + "\x00" + spec.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := r.Resolve(spec); err != nil {
			return err
		}
	}
	return nil
}

// CommandContext resolves spec and returns an exec.Cmd that executes the absolute resolved path.
func (r *Resolver) CommandContext(ctx context.Context, spec Spec, args ...string) (*exec.Cmd, error) {
	res, err := r.Resolve(spec)
	if err != nil {
		return nil, err
	}
	return r.CommandResolved(ctx, res, spec.Env, args...), nil
}

// CommandResolved returns an exec.Cmd that executes an already-resolved helper path.
func (r *Resolver) CommandResolved(ctx context.Context, res Resolved, policy EnvPolicy, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, res.Path, args...)
	cmd.Path = res.Path
	if len(cmd.Args) > 0 {
		cmd.Args[0] = res.Path
	}
	cmd.Env = r.EnvFor(policy)
	return cmd
}

// EnvFor returns the minimal helper environment for policy, plus optional explicit overrides.
func (r *Resolver) EnvFor(policy EnvPolicy, extra ...string) []string {
	if r == nil || r.env == nil {
		return AppendEnv(nil, extra...)
	}
	names := envNames(policy)
	out := make([]string, 0, len(names)+1+len(extra))
	if path := r.env.PATH(); path != "" {
		out = append(out, "PATH="+path)
	}
	for _, name := range names {
		if name == "PATH" {
			continue
		}
		if v, ok := r.env.Get(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return AppendEnv(out, extra...)
}

// AppendEnv appends or replaces KEY=value entries by key, preserving deterministic key order.
func AppendEnv(base []string, extra ...string) []string {
	vals := map[string]string{}
	for _, kv := range base {
		name, val, ok := strings.Cut(kv, "=")
		if ok && name != "" {
			vals[name] = val
		}
	}
	for _, kv := range extra {
		name, val, ok := strings.Cut(kv, "=")
		if ok && name != "" {
			vals[name] = val
		}
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+vals[k])
	}
	return out
}

func envNames(policy EnvPolicy) []string {
	common := []string{
		"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TMP", "TEMP",
		"LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
		"http_proxy", "https_proxy", "no_proxy", "all_proxy",
		"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
	}
	switch policy {
	case EnvOp:
		return append(common, "OP_ACCOUNT")
	case EnvAWS:
		return append(common, "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_DEFAULT_REGION", "AWS_REGION", "AWS_CA_BUNDLE")
	case EnvGCloud:
		return append(common, "CLOUDSDK_CONFIG")
	case EnvGitSSH, EnvCredential, EnvDiagnostic:
		return common
	case EnvRuntime:
		return append(common,
			"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH",
			"DOCKER_BUILDKIT", "COMPOSE_DOCKER_CLI_BUILD",
			"CONTAINER_HOST", "CONTAINERS_CONF", "REGISTRY_AUTH_FILE", "XDG_RUNTIME_DIR", "LIMA_HOME",
		)
	default:
		return common
	}
}

type resolveError struct {
	kind  error
	spec  Spec
	paths []string
}

func (e resolveError) Error() string {
	purpose := e.spec.Purpose
	if purpose == "" {
		purpose = string(e.spec.Class)
	}
	switch e.kind {
	case ErrNotFound:
		return fmt.Sprintf("host helper %q not found on sanitized PATH; required for %s; install it or fix PATH", e.spec.Name, purpose)
	case ErrShadowed:
		return fmt.Sprintf("host helper %q is shadowed on sanitized PATH: %s; safeslop refuses to choose for credential-bearing helpers; remove the duplicate or fix PATH order", e.spec.Name, strings.Join(e.paths, ", "))
	case ErrRelativePath:
		return fmt.Sprintf("host helper %q must be an absolute path or bare name; relative paths are refused", e.spec.Name)
	default:
		return e.kind.Error()
	}
}

func (e resolveError) Unwrap() error { return e.kind }
