package userconfig

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"
)

//go:embed schema/accounts.cue
var accountsSchemaSrc string

// Accounts is the resolved ~/.config/safeslop/accounts.cue: forge account links keyed by
// "host/owner". It holds non-secret ids and secret *refs* only — never token/PEM values
// (specs/0069 L1) — and is host-only: it is never serialized into stage dirs, env, or IPC (L5).
type Accounts struct {
	Accounts map[string]Account `json:"accounts"`
}

// Account is one forge link. Exactly the per-forge block matching Forge is populated.
type Account struct {
	Forge   string          `json:"forge"`
	Host    string          `json:"host"`
	Owner   string          `json:"owner"`
	Github  *GithubAccount  `json:"github,omitempty"`
	Forgejo *ForgejoAccount `json:"forgejo,omitempty"`
}

// GithubAccount identifies a GitHub App installation (non-secret ids + a key ref).
type GithubAccount struct {
	AppID          int    `json:"appID"`
	InstallationID int    `json:"installationID"`
	PrivateKeyRef  string `json:"privateKeyRef"`
}

// ForgejoAccount holds a Forgejo/Gitea account token ref (+ optional non-standard ssh port).
type ForgejoAccount struct {
	TokenRef string `json:"tokenRef"`
	SSHPort  int    `json:"sshPort,omitempty"`
}

const accountsVirtualDir = "/__safeslopaccounts__"

// AccountKey is the map key convention: "host/owner".
func AccountKey(host, owner string) string { return host + "/" + owner }

// LoadAccounts reads + validates path against the embedded schema, then decodes it. A missing
// file yields an empty (but non-nil) account set.
func LoadAccounts(path string) (*Accounts, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		data = []byte("package safeslopaccounts\n") // missing file => no links
	}
	ctx := cuecontext.New()
	overlay := map[string]load.Source{
		filepath.Join(accountsVirtualDir, "cue.mod", "module.cue"): load.FromString(`module: "safeslop.local/accounts"` + "\n" + `language: version: "v0.9.0"`),
		filepath.Join(accountsVirtualDir, "schema.cue"):            load.FromString(accountsSchemaSrc),
		filepath.Join(accountsVirtualDir, "accounts.cue"):          load.FromBytes(data),
	}
	insts := load.Instances([]string{"."}, &load.Config{Dir: accountsVirtualDir, Overlay: overlay})
	if len(insts) == 0 {
		return nil, fmt.Errorf("no CUE instance produced for %s", path)
	}
	if insts[0].Err != nil {
		return nil, fmt.Errorf("load %s:\n%s", path, errors.Details(insts[0].Err, nil))
	}
	val := ctx.BuildInstance(insts[0])
	if err := val.Err(); err != nil {
		return nil, fmt.Errorf("build %s:\n%s", path, errors.Details(err, nil))
	}
	if err := val.Validate(cue.Concrete(true)); err != nil {
		return nil, fmt.Errorf("invalid %s:\n%s", path, errors.Details(err, nil))
	}
	var acc Accounts
	if err := val.Decode(&acc); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if acc.Accounts == nil {
		acc.Accounts = map[string]Account{}
	}
	return &acc, nil
}

// Lookup returns the link for (host, owner), or nil if absent.
func (a *Accounts) Lookup(host, owner string) *Account {
	if a == nil || a.Accounts == nil {
		return nil
	}
	if v, ok := a.Accounts[AccountKey(host, owner)]; ok {
		return &v
	}
	return nil
}

// Upsert inserts or replaces the link, keying by (Host, Owner).
func (a *Accounts) Upsert(acc Account) {
	if a.Accounts == nil {
		a.Accounts = map[string]Account{}
	}
	a.Accounts[AccountKey(acc.Host, acc.Owner)] = acc
}

// Remove deletes the link at key ("host/owner"); reports whether it was present.
func (a *Accounts) Remove(key string) bool {
	if a == nil || a.Accounts == nil {
		return false
	}
	if _, ok := a.Accounts[key]; ok {
		delete(a.Accounts, key)
		return true
	}
	return false
}

// SaveAccounts writes acc to path as CUE. The parent dir is created 0700 and the file written
// 0600 via a tmp-file + atomic rename (custody of secret refs, specs/0069 L1). Output is
// deterministic (keys sorted) so it is diff- and golden-friendly. The rendered text round-trips
// through LoadAccounts (verified in tests).
func SaveAccounts(path string, acc *Accounts) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	body := renderAccounts(acc)
	tmp, err := os.CreateTemp(dir, ".accounts-*.cue.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// renderAccounts emits deterministic CUE text for acc (package clause + sorted links).
func renderAccounts(acc *Accounts) string {
	var b strings.Builder
	b.WriteString("package safeslopaccounts\n\n")
	if acc == nil || len(acc.Accounts) == 0 {
		b.WriteString("accounts: {}\n")
		return b.String()
	}
	keys := make([]string, 0, len(acc.Accounts))
	for k := range acc.Accounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.WriteString("accounts: {\n")
	for _, k := range keys {
		a := acc.Accounts[k]
		fmt.Fprintf(&b, "\t%s: {\n", strconv.Quote(k))
		fmt.Fprintf(&b, "\t\tforge: %s\n", strconv.Quote(a.Forge))
		fmt.Fprintf(&b, "\t\thost:  %s\n", strconv.Quote(a.Host))
		fmt.Fprintf(&b, "\t\towner: %s\n", strconv.Quote(a.Owner))
		if a.Github != nil {
			b.WriteString("\t\tgithub: {\n")
			fmt.Fprintf(&b, "\t\t\tappID:          %d\n", a.Github.AppID)
			fmt.Fprintf(&b, "\t\t\tinstallationID: %d\n", a.Github.InstallationID)
			fmt.Fprintf(&b, "\t\t\tprivateKeyRef:  %s\n", strconv.Quote(a.Github.PrivateKeyRef))
			b.WriteString("\t\t}\n")
		}
		if a.Forgejo != nil {
			b.WriteString("\t\tforgejo: {\n")
			fmt.Fprintf(&b, "\t\t\ttokenRef: %s\n", strconv.Quote(a.Forgejo.TokenRef))
			if a.Forgejo.SSHPort != 0 {
				fmt.Fprintf(&b, "\t\t\tsshPort: %d\n", a.Forgejo.SSHPort)
			}
			b.WriteString("\t\t}\n")
		}
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// DefaultAccountsPath is ~/.config/safeslop/accounts.cue.
func DefaultAccountsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "safeslop", "accounts.cue"), nil
}
