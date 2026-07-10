package creds

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/hostexec"
)

type credsFakeHostEnv struct {
	lookupDir string
	path      string
}

func (e credsFakeHostEnv) PATH() string { return e.path }

func (e credsFakeHostEnv) Get(name string) (string, bool) { return os.LookupEnv(name) }

func (e credsFakeHostEnv) LookPath(name string) (string, bool) {
	all := e.LookAll(name)
	if len(all) == 0 {
		return "", false
	}
	return all[0], true
}

func (e credsFakeHostEnv) LookAll(name string) []string {
	if filepath.IsAbs(name) {
		if isTestExec(name) {
			return []string{name}
		}
		return nil
	}
	if strings.Contains(name, "/") {
		return nil
	}
	p := filepath.Join(e.lookupDir, name)
	if !isTestExec(p) {
		return nil
	}
	return []string{p}
}

// SameFile uses real stat identity (credsFakeHostEnv is backed by real files in a tmp dir).
func (e credsFakeHostEnv) SameFile(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(fa, fb), nil
}

func isTestExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode().Perm()&0o111 != 0
}

func withCredsHostExecDir(t *testing.T, dir string) {
	t.Helper()
	old := hostExecResolver
	path := dir
	if p := os.Getenv("PATH"); p != "" {
		path += ":" + p
	}
	hostExecResolver = func() *hostexec.Resolver {
		return hostexec.New(credsFakeHostEnv{lookupDir: dir, path: path})
	}
	t.Cleanup(func() { hostExecResolver = old })
}

func TestCredentialHelperFailsClosedOnShadowedGit(t *testing.T) {
	old := hostExecResolver
	hostExecResolver = func() *hostexec.Resolver {
		return hostexec.New(credsShadowEnv{path: "/safe/bin:/other/bin", all: map[string][]string{
			"git": {"/safe/bin/git", "/other/bin/git"},
		}})
	}
	t.Cleanup(func() { hostExecResolver = old })

	_, err := runSSHCmd(context.Background(), []string{"git", "remote", "get-url", "origin"}, "origin inference")
	if !errors.Is(err, hostexec.ErrShadowed) {
		t.Fatalf("runSSHCmd err=%v, want ErrShadowed", err)
	}
}

type credsShadowEnv struct {
	path string
	all  map[string][]string
}

func (e credsShadowEnv) PATH() string              { return e.path }
func (e credsShadowEnv) Get(string) (string, bool) { return "", false }
func (e credsShadowEnv) LookPath(name string) (string, bool) {
	all := e.LookAll(name)
	if len(all) == 0 {
		return "", false
	}
	return all[0], true
}
func (e credsShadowEnv) LookAll(name string) []string { return append([]string(nil), e.all[name]...) }
func (e credsShadowEnv) SameFile(a, b string) (bool, error) { return a == b, nil }
