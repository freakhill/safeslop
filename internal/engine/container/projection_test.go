package container

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

// projHome builds a temp $HOME skeleton with the given relative files/dirs and returns its path.
// Entries ending in "/" are directories; otherwise regular files (content = "x").
func projHome(t *testing.T, rels ...string) string {
	t.Helper()
	home := t.TempDir()
	for _, rel := range rels {
		full := filepath.Join(home, rel)
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func boolPtr(b bool) *bool { return &b }

func TestResolveProjectionFilePresent(t *testing.T) {
	home := projHome(t, ".pi/agent/AGENTS.md")
	m, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.pi/agent/AGENTS.md", Label: "pi-agent"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mounts := m.PresentMounts()
	if len(mounts) != 1 {
		t.Fatalf("want 1 present mount, got %d: %+v", len(mounts), m.Items)
	}
	mt := mounts[0]
	if mt.Container != "/safeslop/projected/0" {
		t.Errorf("staging = %q, want /safeslop/projected/0", mt.Container)
	}
	if mt.Target != ".pi/agent/AGENTS.md" {
		t.Errorf("target = %q, want .pi/agent/AGENTS.md", mt.Target)
	}
	if mt.Status != projPresent || mt.Label != "pi-agent" {
		t.Errorf("status/label = %q/%q", mt.Status, mt.Label)
	}
	if mt.Host != filepath.Join(home, ".pi/agent/AGENTS.md") {
		t.Errorf("host = %q", mt.Host)
	}
}

func TestResolveProjectionOptionalAbsentSkips(t *testing.T) {
	home := projHome(t) // empty home
	m, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.config/fish/config.fish"}, // optional defaults to true
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.PresentMounts()) != 0 {
		t.Fatalf("absent optional must yield no mounts: %+v", m.Items)
	}
	if len(m.Items) != 1 || m.Items[0].Status != projSkippedAbsent {
		t.Fatalf("absent optional must be recorded skipped-absent for legibility: %+v", m.Items)
	}
}

func TestResolveProjectionRequiredAbsentFailsClosed(t *testing.T) {
	home := projHome(t)
	_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.pi/agent/AGENTS.md", Optional: boolPtr(false)},
	}})
	if err == nil || !strings.Contains(err.Error(), "required source absent") {
		t.Fatalf("required absent must fail closed, got: %v", err)
	}
}

func TestResolveProjectionRejectsCredentialDirs(t *testing.T) {
	home := projHome(t, ".ssh/config", ".aws/credentials", ".kube/config", ".gnupg/pubring", ".docker/config.json")
	cases := []string{
		"~/.ssh/config", "~/.aws/credentials", "~/.kube/config", "~/.gnupg/pubring",
		"~/.docker/config.json", "~/.npmrc", "~/.pypirc", "~/.gitconfig", "~/.config/git/config",
		"~/.config/gcloud/credentials.db", "~/.config/safeslop/accounts.cue", "~/.cache/safeslop/x",
	}
	for _, src := range cases {
		// create the file so it's "present" and we test the excluded-root law, not the absent path
		rel := strings.TrimPrefix(strings.TrimPrefix(src, "~/"), ".")
		_ = os.MkdirAll(filepath.Dir(filepath.Join(home, rel)), 0o755)
		_ = os.WriteFile(filepath.Join(home, rel), []byte("x"), 0o644)
		_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{{Source: src}}})
		if err == nil || !strings.Contains(err.Error(), "excluded source") {
			t.Errorf("credential/cache source %q must be rejected as excluded, got: %v", src, err)
		}
	}
}

func TestResolveProjectionRejectsCargoCredentials(t *testing.T) {
	home := projHome(t, ".cargo/credentials", ".cargo/credentials.toml")
	for _, src := range []string{"~/.cargo/credentials", "~/.cargo/credentials.toml"} {
		_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{{Source: src}}})
		if err == nil || !strings.Contains(err.Error(), "cargo credentials") {
			t.Errorf("%q must be rejected as cargo credentials, got: %v", src, err)
		}
	}
}

func TestResolveProjectionRejectsBroadHome(t *testing.T) {
	home := projHome(t, ".zshrc")
	_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{{Source: "~"}}})
	if err == nil || !strings.Contains(err.Error(), "broad root rejected") {
		t.Fatalf("broad $HOME source must be rejected, got: %v", err)
	}
}

func TestResolveProjectionRejectsPathEscape(t *testing.T) {
	home := projHome(t)
	_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "/etc/passwd"},
	}})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("source outside $HOME must be rejected as escape, got: %v", err)
	}
}

func TestResolveProjectionRejectsSymlinkSource(t *testing.T) {
	home := projHome(t, "real.rc")
	if err := os.Symlink(filepath.Join(home, "real.rc"), filepath.Join(home, ".zshrc")); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.zshrc"},
	}})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink source must be rejected (TOCTOU), got: %v", err)
	}
}

func TestResolveProjectionRejectsSymlinkComponent(t *testing.T) {
	home := projHome(t)
	realdir := filepath.Join(home, "realdir")
	if err := os.MkdirAll(realdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realdir, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// ~/linkdir -> realdir ; a source under ~/linkdir/... has a symlink component.
	if err := os.Symlink(realdir, filepath.Join(home, "linkdir")); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/linkdir/file"},
	}})
	if err == nil || !strings.Contains(err.Error(), "symlink component") {
		t.Fatalf("symlink component in path must be rejected, got: %v", err)
	}
}

func TestResolveProjectionRejectsDuplicateTarget(t *testing.T) {
	home := projHome(t, ".zshrc")
	_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.zshrc"},
		{Source: "~/.zshrc"}, // same target
	}})
	if err == nil || !strings.Contains(err.Error(), "duplicate target") {
		t.Fatalf("duplicate target must be rejected, got: %v", err)
	}
}

func TestResolveProjectionDirExpandsPerFile(t *testing.T) {
	home := projHome(t, ".pi/agent/skills/foo/SKILL.md", ".pi/agent/skills/bar/SKILL.md", ".pi/agent/skills/bar/handler.sh")
	// a stray symlink inside the corpus must be skipped, not followed.
	_ = os.Symlink(filepath.Join(home, ".pi/agent/skills/foo/SKILL.md"), filepath.Join(home, ".pi/agent/skills/link.md"))
	m, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.pi/agent/skills", Kind: "dir", Label: "pi-skills"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mounts := m.PresentMounts()
	if len(mounts) != 3 {
		t.Fatalf("dir must expand to 3 regular files (symlink skipped), got %d: %+v", len(mounts), mounts)
	}
	// sorted by target, opaque ids assigned in order
	want := []string{".pi/agent/skills/bar/SKILL.md", ".pi/agent/skills/bar/handler.sh", ".pi/agent/skills/foo/SKILL.md"}
	for i, mt := range mounts {
		if mt.Target != want[i] {
			t.Errorf("mount[%d].target = %q, want %q", i, mt.Target, want[i])
		}
		if mt.Container != "/safeslop/projected/"+string(rune('0'+i)) {
			t.Errorf("mount[%d].staging = %q", i, mt.Container)
		}
		if mt.Label != "pi-skills" {
			t.Errorf("label not propagated: %q", mt.Label)
		}
	}
}

func TestResolveProjectionGlobExpands(t *testing.T) {
	home := projHome(t, ".config/fish/conf.d/a.fish", ".config/fish/conf.d/b.fish", ".config/fish/conf.d/notfish.txt")
	m, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.config/fish/conf.d/*.fish", Kind: "glob", Label: "fish"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mounts := m.PresentMounts()
	if len(mounts) != 2 {
		t.Fatalf("glob *.fish must match 2 files (notfish.txt excluded), got %d: %+v", len(mounts), mounts)
	}
	if mounts[0].Target != ".config/fish/conf.d/a.fish" || mounts[1].Target != ".config/fish/conf.d/b.fish" {
		t.Errorf("glob targets wrong: %+v", mounts)
	}
}

func TestResolveProjectionGlobNoMatchOptionalSkips(t *testing.T) {
	home := projHome(t)
	m, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.config/fish/conf.d/*.fish", Kind: "glob"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.PresentMounts()) != 0 || len(m.Items) != 1 || m.Items[0].Status != projSkippedAbsent {
		t.Fatalf("no-match optional glob must skip-absent, got %+v", m.Items)
	}
}

func TestResolveProjectionMarshalsManifest(t *testing.T) {
	home := projHome(t, ".zshrc") // ~/.zshrc absent here actually; create it
	_ = os.WriteFile(filepath.Join(home, ".zshrc"), []byte("x"), 0o644)
	m, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.zshrc", Label: "zsh"},
		{Source: "~/.config/fish/config.fish"}, // absent optional
	}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"host"`, `"target"`, `"status"`, projPresent, projSkippedAbsent, ".zshrc"} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest JSON missing %q:\n%s", want, s)
		}
	}
}
