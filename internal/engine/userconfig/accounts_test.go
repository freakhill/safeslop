package userconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func loadAccStr(t *testing.T, src string) (*Accounts, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "accounts.cue")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadAccounts(p)
}

func TestLoadAccountsMissingFileIsEmpty(t *testing.T) {
	acc, err := LoadAccounts(filepath.Join(t.TempDir(), "nope.cue"))
	if err != nil {
		t.Fatalf("missing file must load clean: %v", err)
	}
	if acc.Accounts == nil || len(acc.Accounts) != 0 {
		t.Fatalf("missing file must yield empty non-nil map: %+v", acc.Accounts)
	}
}

func TestLoadAccountsGithub(t *testing.T) {
	acc, err := loadAccStr(t, `package safeslopaccounts
accounts: {
	"github.com/acme": {
		forge: "github"
		host:  "github.com"
		owner: "acme"
		github: {
			appID:          12345
			installationID: 67890
			privateKeyRef:  "op://vault/gh-app/private-key"
		}
	}
}`)
	if err != nil {
		t.Fatalf("LoadAccounts: %v", err)
	}
	a := acc.Lookup("github.com", "acme")
	if a == nil {
		t.Fatal("Lookup(github.com, acme) = nil")
	}
	if a.Forge != "github" || a.Github == nil {
		t.Fatalf("wrong shape: %+v", a)
	}
	if a.Github.AppID != 12345 || a.Github.InstallationID != 67890 || a.Github.PrivateKeyRef != "op://vault/gh-app/private-key" {
		t.Fatalf("github fields wrong: %+v", a.Github)
	}
}

func TestLoadAccountsForgejo(t *testing.T) {
	acc, err := loadAccStr(t, `package safeslopaccounts
accounts: {
	"git.example.org/bob": {
		forge: "forgejo"
		host:  "git.example.org"
		owner: "bob"
		forgejo: {
			tokenRef: "op://vault/forgejo/token"
			sshPort:  2222
		}
	}
}`)
	if err != nil {
		t.Fatalf("LoadAccounts: %v", err)
	}
	a := acc.Lookup("git.example.org", "bob")
	if a == nil || a.Forgejo == nil {
		t.Fatalf("Lookup forgejo link = %+v", a)
	}
	if a.Forgejo.TokenRef != "op://vault/forgejo/token" || a.Forgejo.SSHPort != 2222 {
		t.Fatalf("forgejo fields wrong: %+v", a.Forgejo)
	}
}

func TestLoadAccountsRejectsBadForgeKind(t *testing.T) {
	_, err := loadAccStr(t, `package safeslopaccounts
accounts: {
	"github.com/acme": {
		forge: "gitlab"
		host:  "github.com"
		owner: "acme"
	}
}`)
	if err == nil {
		t.Fatal("unknown forge kind must be rejected by the schema")
	}
}

func TestLoadAccountsRejectsMissingPerForgeBlock(t *testing.T) {
	_, err := loadAccStr(t, `package safeslopaccounts
accounts: {
	"github.com/acme": {
		forge: "github"
		host:  "github.com"
		owner: "acme"
	}
}`)
	if err == nil {
		t.Fatal("forge=github without a github block must be rejected")
	}
}

func TestLoadAccountsRejectsExtraFields(t *testing.T) {
	_, err := loadAccStr(t, `package safeslopaccounts
accounts: {
	"github.com/acme": {
		forge: "github"
		host:  "github.com"
		owner: "acme"
		github: {
			appID:          1
			installationID: 2
			privateKeyRef:  "op://x/y/z"
		}
		bogus: "nope"
	}
}`)
	if err == nil {
		t.Fatal("unknown fields must be rejected by the closed schema")
	}
}

func TestLoadAccountsRejectsSecretShapedValue(t *testing.T) {
	// A GitHub account requires positive ids; a zero/negative appID is a schema error. This
	// also guards the "ids only" intent — the schema has no field for a token/PEM value.
	_, err := loadAccStr(t, `package safeslopaccounts
accounts: {
	"github.com/acme": {
		forge: "github"
		host:  "github.com"
		owner: "acme"
		github: {
			appID:          0
			installationID: 2
			privateKeyRef:  "op://x/y/z"
		}
	}
}`)
	if err == nil {
		t.Fatal("appID: 0 must be rejected (int & >0)")
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "accounts.cue") // sub dir must be created 0700
	want := &Accounts{Accounts: map[string]Account{}}
	want.Upsert(Account{
		Forge: "github", Host: "github.com", Owner: "acme",
		Github: &GithubAccount{AppID: 12345, InstallationID: 67890, PrivateKeyRef: "op://vault/gh/key"},
	})
	want.Upsert(Account{
		Forge: "forgejo", Host: "git.example.org", Owner: "bob",
		Forgejo: &ForgejoAccount{TokenRef: "op://vault/fj/token", SSHPort: 2222},
	})
	if err := SaveAccounts(p, want); err != nil {
		t.Fatalf("SaveAccounts: %v", err)
	}

	got, err := LoadAccounts(p)
	if err != nil {
		t.Fatalf("LoadAccounts after save: %v", err)
	}
	gh := got.Lookup("github.com", "acme")
	fj := got.Lookup("git.example.org", "bob")
	if gh == nil || gh.Github == nil || gh.Github.AppID != 12345 || gh.Github.PrivateKeyRef != "op://vault/gh/key" {
		t.Fatalf("github link did not round-trip: %+v", gh)
	}
	if fj == nil || fj.Forgejo == nil || fj.Forgejo.TokenRef != "op://vault/fj/token" || fj.Forgejo.SSHPort != 2222 {
		t.Fatalf("forgejo link did not round-trip: %+v", fj)
	}

	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("accounts.cue must be 0600, got %o", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("accounts dir must be 0700, got %o", di.Mode().Perm())
	}
}

func TestRemove(t *testing.T) {
	acc := &Accounts{Accounts: map[string]Account{}}
	acc.Upsert(Account{Forge: "github", Host: "github.com", Owner: "acme",
		Github: &GithubAccount{AppID: 1, InstallationID: 2, PrivateKeyRef: "op://x/y/z"}})
	if !acc.Remove("github.com/acme") {
		t.Fatal("Remove must report present link as removed")
	}
	if acc.Remove("github.com/acme") {
		t.Fatal("Remove of absent link must report false")
	}
	if acc.Lookup("github.com", "acme") != nil {
		t.Fatal("link must be gone after Remove")
	}
}
