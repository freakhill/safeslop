package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakhill/safeslop/internal/engine/hostexec"
)

func TestResolveEnvRef(t *testing.T) {
	t.Setenv("SAFESLOP_TEST_SECRET", "s3cr3t")
	v, err := Resolve(context.Background(), "env:SAFESLOP_TEST_SECRET")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v != "s3cr3t" {
		t.Fatalf("value = %q, want s3cr3t", v)
	}
}

func TestResolveEnvRefUnset(t *testing.T) {
	if _, err := Resolve(context.Background(), "env:SAFESLOP_DEFINITELY_UNSET_VAR"); err == nil {
		t.Fatal("expected an error for an unset env var")
	}
}

func TestResolveUnsupportedRef(t *testing.T) {
	if _, err := Resolve(context.Background(), "plaintext"); err == nil {
		t.Fatal("expected an error for an unsupported ref scheme")
	}
}

func TestResolveMapEnv(t *testing.T) {
	t.Setenv("SAFESLOP_A", "aa")
	t.Setenv("SAFESLOP_B", "bb")
	got, err := ResolveMap(context.Background(), map[string]string{
		"A": "env:SAFESLOP_A",
		"B": "env:SAFESLOP_B",
	})
	if err != nil {
		t.Fatalf("ResolveMap: %v", err)
	}
	if got["A"] != "aa" || got["B"] != "bb" {
		t.Fatalf("got %v", got)
	}
}

func TestResolveOpRefUsesHostExec(t *testing.T) {
	bin := t.TempDir()
	op := filepath.Join(bin, "op")
	writeExecutable(t, op, `#!/bin/sh
if [ "$1" = "whoami" ]; then exit 0; fi
if [ "$1" = "read" ]; then printf resolved-value; exit 0; fi
exit 99
`)
	withHostExecResolver(t, hostexec.New(secretFakeEnv{path: bin, all: map[string][]string{"op": {op}}}))

	if !OpAvailable() {
		t.Fatal("OpAvailable=false, want true through hostexec")
	}
	// This test proves resolver/argv routing, not the production doctor latency
	// budget. Package-parallel race CI can starve a subprocess for several seconds.
	if !opSignedIn(context.Background(), 30*time.Second) {
		t.Fatal("OpSignedIn=false, want true through hostexec command")
	}
	got, err := Resolve(context.Background(), "op://vault/item/field")
	if err != nil {
		t.Fatalf("Resolve(op://): %v", err)
	}
	if got != "resolved-value" {
		t.Fatalf("Resolve(op://)=%q", got)
	}
}

func TestResolveOpRefFailsClosedOnShadowedHelper(t *testing.T) {
	withHostExecResolver(t, hostexec.New(secretFakeEnv{path: "/safe/bin:/other/bin", all: map[string][]string{
		"op": {"/safe/bin/op", "/other/bin/op"},
	}}))

	_, err := Resolve(context.Background(), "op://vault/item/field")
	if !errors.Is(err, hostexec.ErrShadowed) {
		t.Fatalf("Resolve(op://) err=%v, want ErrShadowed", err)
	}
}

func TestResolveOpRefDoesNotSurfaceHelperStderr(t *testing.T) {
	bin := t.TempDir()
	op := filepath.Join(bin, "op")
	writeExecutable(t, op, `#!/bin/sh
echo SECRET_STDERR_SHOULD_NOT_LEAK >&2
exit 42
`)
	withHostExecResolver(t, hostexec.New(secretFakeEnv{path: bin, all: map[string][]string{"op": {op}}}))

	_, err := Resolve(context.Background(), "op://vault/item/field")
	if err == nil {
		t.Fatal("expected op read error")
	}
	if strings.Contains(err.Error(), "SECRET_STDERR_SHOULD_NOT_LEAK") {
		t.Fatalf("helper stderr leaked into error: %v", err)
	}
}

type secretFakeEnv struct {
	path string
	vars map[string]string
	all  map[string][]string
}

func (f secretFakeEnv) PATH() string { return f.path }

func (f secretFakeEnv) Get(name string) (string, bool) {
	v, ok := f.vars[name]
	return v, ok
}

func (f secretFakeEnv) LookPath(name string) (string, bool) {
	all := f.LookAll(name)
	if len(all) == 0 {
		return "", false
	}
	return all[0], true
}

func (f secretFakeEnv) LookAll(name string) []string {
	out := f.all[name]
	if len(out) == 0 {
		return nil
	}
	return append([]string(nil), out...)
}

func (f secretFakeEnv) SameFile(a, b string) (bool, error) { return a == b, nil }

func withHostExecResolver(t *testing.T, r *hostexec.Resolver) {
	t.Helper()
	old := hostExecResolver
	hostExecResolver = func() *hostexec.Resolver { return r }
	t.Cleanup(func() { hostExecResolver = old })
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake helper: %v", err)
	}
}
