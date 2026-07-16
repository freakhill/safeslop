package hostpath

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// proofCharacterizationAPI is a test-only seam for the policy-independent
// proof laws. Task 3 wires this matrix to the extracted descriptor core; keeping
// the seam here lets Task 2 fail on Pi behavior rather than absent package APIs.
type proofCharacterizationAPI struct {
	absoluteTarget func(root, target string) (string, bool)
	directorySafe  func(uid, currentUID uint32, mode fs.FileMode) bool
	mountSafe      func(rootMount, nodeMount uint64, known bool) bool
	linkCountSafe  func(dereferences int) bool
}

var sharedProofCharacterization = proofCharacterizationAPI{
	absoluteTarget: strictAbsoluteTarget,
	directorySafe:  safePiOAuthDirectoryMetadata,
	mountSafe:      sameProofMount,
	linkCountSafe:  proofLinkCountSafe,
}

func TestHostPathCharacterizationAbsoluteTargets(t *testing.T) {
	api := sharedProofCharacterization
	tests := []struct {
		name, root, target, want string
		ok                       bool
	}{
		{"descendant", "/home/user", "/home/user/dotfiles/auth.json", "dotfiles/auth.json", true},
		{"root", "/home/user", "/home/user", "", false},
		{"outside", "/home/user", "/tmp/auth.json", "", false},
		{"prefix", "/home/user", "/home/user-attacker/auth.json", "", false},
		{"dot", "/home/user", "/home/user/dotfiles/./auth.json", "", false},
		{"dot-dot", "/home/user", "/home/user/dotfiles/../auth.json", "", false},
		{"empty", "/home/user", "/home/user/dotfiles//auth.json", "", false},
		{"trailing", "/home/user", "/home/user/dotfiles/", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := api.absoluteTarget(tc.root, tc.target)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("absolute target = %q,%v want %q,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestHostPathProofRevalidatesChangedLink(t *testing.T) {
	home := t.TempDir()
	for name, body := range map[string]string{"old": "old", "new": "new"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(home, "source")
	if err := os.Symlink("old", link); err != nil {
		t.Fatal(err)
	}
	root, err := openProofRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	defer root.close()
	node, err := openPinnedPath(root, "source", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer node.close()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("new", link); err != nil {
		t.Fatal(err)
	}
	if root.revalidate() {
		t.Fatal("changed source link retained a valid proof epoch")
	}
}

func TestHostPathPinnedRootDoesNotReopenReplacementPath(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "source"), []byte("pinned"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := openProofRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	defer root.close()
	if err := os.Rename(home, filepath.Join(parent, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "source"), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	node, err := openPinnedPath(root, "source", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer node.close()
	body, err := io.ReadAll(node.file)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "pinned" {
		t.Fatalf("proof reopened replacement root path: %q", body)
	}
}

func TestHostPathCharacterizationAncestryMountAndLinkBudget(t *testing.T) {
	api := sharedProofCharacterization
	const currentUID = 501
	for _, tc := range []struct {
		name string
		uid  uint32
		mode fs.FileMode
		want bool
	}{
		{"owner-0700", currentUID, fs.ModeDir | 0o700, true},
		{"owner-0755", currentUID, fs.ModeDir | 0o755, true},
		{"group-writable", currentUID, fs.ModeDir | 0o775, false},
		{"other-writable", currentUID, fs.ModeDir | 0o757, false},
		{"wrong-owner", currentUID + 1, fs.ModeDir | 0o755, false},
		{"not-directory", currentUID, 0o600, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := api.directorySafe(tc.uid, currentUID, tc.mode); got != tc.want {
				t.Fatalf("directorySafe = %v, want %v", got, tc.want)
			}
		})
	}
	if !api.mountSafe(41, 41, true) {
		t.Fatal("same mount instance was rejected")
	}
	if api.mountSafe(41, 42, true) || api.mountSafe(41, 41, false) {
		t.Fatal("different or unknown mount instance was accepted")
	}
	if !api.linkCountSafe(40) || api.linkCountSafe(41) {
		t.Fatal("link dereference budget is not exactly 40")
	}
}
