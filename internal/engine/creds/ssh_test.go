package creds

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeygenArgv(t *testing.T) {
	got := strings.Join(keygenArgv("/stage/.ssh/id", "safeslop-acme/repo-run1"), " ")
	want := `ssh-keygen -t ed25519 -N  -C safeslop-acme/repo-run1 -f /stage/.ssh/id`
	if got != want {
		t.Fatalf("keygen argv = %q", got)
	}
}

func TestParseOwnerRepo(t *testing.T) {
	cases := map[string][2]string{
		"git@github.com:acme/repo.git\n":       {"acme", "repo"},
		"https://github.com/acme/repo.git\n":   {"acme", "repo"},
		"https://github.com/acme/repo\n":       {"acme", "repo"},
		"ssh://git@github.com/acme/repo.git\n": {"acme", "repo"},
	}
	for in, want := range cases {
		o, r, err := parseOwnerRepo([]byte(in))
		if err != nil || o != want[0] || r != want[1] {
			t.Fatalf("parseOwnerRepo(%q) = %q/%q err=%v", in, o, r, err)
		}
	}
	if _, _, err := parseOwnerRepo([]byte("/local/path\n")); err == nil {
		t.Fatal("expected error on non-github remote")
	}
}

func TestParseKeyID(t *testing.T) {
	id, err := parseKeyID([]byte(`{"id":1234567,"key":"ssh-ed25519 AAAA","read_only":true}`))
	if err != nil || id != "1234567" {
		t.Fatalf("parseKeyID = %q err=%v", id, err)
	}
	if _, err := parseKeyID([]byte(`{}`)); err == nil {
		t.Fatal("expected error on missing id")
	}
}

// fakeStub writes an executable /bin/sh stub with an arbitrary body (shared by the Forgejo
// deploy-key staging tests that fake ssh-keygen/ssh-keyscan).
func fakeStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
