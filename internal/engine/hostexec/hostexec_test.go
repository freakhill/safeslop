package hostexec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeEnv struct {
	path string
	vars map[string]string
	all  map[string][]string
}

func (f fakeEnv) PATH() string { return f.path }

func (f fakeEnv) Get(name string) (string, bool) {
	v, ok := f.vars[name]
	return v, ok
}

func (f fakeEnv) LookPath(name string) (string, bool) {
	all := f.LookAll(name)
	if len(all) == 0 {
		return "", false
	}
	return all[0], true
}

func (f fakeEnv) LookAll(name string) []string {
	if f.all == nil {
		return nil
	}
	out := f.all[name]
	if len(out) == 0 {
		return nil
	}
	cp := append([]string(nil), out...)
	return cp
}

func TestResolveMissingShadowedAbsoluteAndRelative(t *testing.T) {
	r := New(fakeEnv{path: "/safe/bin", all: map[string][]string{
		"op":         {"/safe/bin/op"},
		"aws":        {"/safe/bin/aws", "/usr/local/bin/aws"},
		"/opt/bin/x": {"/opt/bin/x"},
	}})

	res, err := r.Resolve(CredentialSpec("op", "op:// secrets"))
	if err != nil {
		t.Fatalf("Resolve(op): %v", err)
	}
	if res.Path != "/safe/bin/op" || res.Name != "op" || res.Explicit {
		t.Fatalf("Resolve(op)=%+v", res)
	}

	if _, err := r.Resolve(CredentialSpec("missing", "unit test")); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "unit test") {
		t.Fatalf("Resolve(missing) err=%v, want ErrNotFound with purpose", err)
	}
	if _, err := r.Resolve(CredentialSpec("aws", "AWS credentials")); !errors.Is(err, ErrShadowed) || !strings.Contains(err.Error(), "/usr/local/bin/aws") {
		t.Fatalf("Resolve(shadowed) err=%v, want ErrShadowed with paths", err)
	}
	if _, err := r.Resolve(CredentialSpec("foo/bar", "relative helper")); !errors.Is(err, ErrRelativePath) {
		t.Fatalf("Resolve(relative slash) err=%v, want ErrRelativePath", err)
	}
	res, err = r.Resolve(Spec{Name: "/opt/bin/x", Class: ClassCredential, Env: EnvCredential, Purpose: "absolute helper"})
	if err != nil {
		t.Fatalf("Resolve(abs): %v", err)
	}
	if res.Path != "/opt/bin/x" || !res.Explicit {
		t.Fatalf("Resolve(abs)=%+v", res)
	}
}

func TestInspectReportsShadowWithoutFailing(t *testing.T) {
	r := New(fakeEnv{path: "/safe/bin:/usr/local/bin", all: map[string][]string{
		"docker": {"/safe/bin/docker", "/usr/local/bin/docker"},
	}})
	insp := r.Inspect("docker")
	if !insp.Present || !insp.Shadowed || insp.Path != "/safe/bin/docker" || len(insp.All) != 2 || insp.Err != nil {
		t.Fatalf("Inspect(docker)=%+v", insp)
	}
}

func TestCommandContextUsesAbsolutePathAndAllowlistedEnv(t *testing.T) {
	r := New(fakeEnv{
		path: "/safe/bin:/usr/bin",
		vars: map[string]string{
			"PATH":                           "/unsafe/bin",
			"HOME":                           "/Users/alice",
			"LANG":                           "en_US.UTF-8",
			"HTTP_PROXY":                     "http://proxy.invalid:8080",
			"OP_ACCOUNT":                     "alice",
			"OP_SESSION_example":             "secret-session",
			"AWS_CONFIG_FILE":                "/Users/alice/.aws/config",
			"AWS_PROFILE":                    "prod",
			"AWS_ACCESS_KEY_ID":              "ambient-access-key",
			"GOOGLE_APPLICATION_CREDENTIALS": "/tmp/adc.json",
			"GIT_SSH_COMMAND":                "ssh -i /tmp/key",
			"SSH_AUTH_SOCK":                  "/tmp/agent.sock",
			"ANTHROPIC_API_KEY":              "ambient-agent-token",
		},
		all: map[string][]string{"op": {"/safe/bin/op"}},
	})
	cmd, err := r.CommandContext(context.Background(), OpSpec("op:// secrets"), "read", "--no-newline", "op://vault/item/field")
	if err != nil {
		t.Fatalf("CommandContext: %v", err)
	}
	if cmd.Path != "/safe/bin/op" || len(cmd.Args) == 0 || cmd.Args[0] != "/safe/bin/op" {
		t.Fatalf("cmd path/args = Path %q Args %v", cmd.Path, cmd.Args)
	}
	env := envMap(cmd.Env)
	if env["PATH"] != "/safe/bin:/usr/bin" {
		t.Fatalf("PATH=%q, want sanitized resolver PATH", env["PATH"])
	}
	for _, name := range []string{"HOME", "LANG", "HTTP_PROXY", "OP_ACCOUNT"} {
		if env[name] == "" {
			t.Fatalf("%s missing from env %v", name, cmd.Env)
		}
	}
	for _, denied := range []string{"OP_SESSION_example", "AWS_PROFILE", "AWS_ACCESS_KEY_ID", "GOOGLE_APPLICATION_CREDENTIALS", "GIT_SSH_COMMAND", "SSH_AUTH_SOCK", "ANTHROPIC_API_KEY"} {
		if _, ok := env[denied]; ok {
			t.Fatalf("%s leaked into env %v", denied, cmd.Env)
		}
	}
}

func TestRuntimeEnvExcludesCredentialAuthority(t *testing.T) {
	r := New(fakeEnv{
		path: "/safe/bin",
		vars: map[string]string{
			"PATH":            "/unsafe/bin",
			"HOME":            "/Users/alice",
			"DOCKER_HOST":     "unix:///tmp/docker.sock",
			"XDG_RUNTIME_DIR": "/run/user/501",
			"AWS_PROFILE":     "prod",
			"OP_ACCOUNT":      "alice",
			"GITHUB_TOKEN":    "token",
		},
	})
	env := envMap(r.EnvFor(EnvRuntime))
	if env["PATH"] != "/safe/bin" || env["DOCKER_HOST"] == "" || env["XDG_RUNTIME_DIR"] == "" {
		t.Fatalf("runtime env missing expected values: %v", env)
	}
	for _, denied := range []string{"AWS_PROFILE", "OP_ACCOUNT", "GITHUB_TOKEN"} {
		if _, ok := env[denied]; ok {
			t.Fatalf("%s leaked into runtime env %v", denied, env)
		}
	}
}

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		name, val, ok := strings.Cut(kv, "=")
		if ok {
			out[name] = val
		}
	}
	return out
}
