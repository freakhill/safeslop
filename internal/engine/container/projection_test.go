package container

import (
	"encoding/json"
	"errors"
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

func requireProjectionCode(t *testing.T, err error, want string) {
	t.Helper()
	var projectionErr *ProjectionError
	if !errors.As(err, &projectionErr) {
		t.Fatalf("error %v is not a ProjectionError", err)
	}
	if got := projectionErr.Failure().Code; got != want {
		t.Fatalf("projection code = %q, want %q", got, want)
	}
}

// ResolveProjection keeps the pre-snapshot test table concise while routing every case through the
// production snapshot API. The stage lives under t.TempDir-owned home and is removed by testing.
func ResolveProjection(home string, proj policy.Projection) (ProjectionManifest, error) {
	return SnapshotProjection(home, filepath.Join(home, ".test-projection-stage"), proj)
}

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
	if !strings.HasPrefix(mt.Host, filepath.Join(home, ".test-projection-stage", "projection-snapshots")+string(filepath.Separator)) {
		t.Errorf("host is not the private snapshot: %q", mt.Host)
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
	requireProjectionCode(t, err, ProjectionRequiredAbsent)
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
		if err == nil {
			t.Errorf("credential/cache source %q was accepted", src)
			continue
		}
		requireProjectionCode(t, err, ProjectionTargetExcluded)
	}
}

func TestResolveProjectionRejectsCargoCredentials(t *testing.T) {
	home := projHome(t, ".cargo/credentials", ".cargo/credentials.toml")
	for _, src := range []string{"~/.cargo/credentials", "~/.cargo/credentials.toml"} {
		_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{{Source: src}}})
		requireProjectionCode(t, err, ProjectionTargetExcluded)
	}
}

func TestResolveProjectionRejectsBroadHome(t *testing.T) {
	home := projHome(t, ".zshrc")
	_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{{Source: "~"}}})
	requireProjectionCode(t, err, ProjectionSourceType)
}

func TestResolveProjectionRejectsPathEscape(t *testing.T) {
	home := projHome(t)
	_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "/etc/passwd"},
	}})
	requireProjectionCode(t, err, ProjectionTargetOutsideRoot)
}

func TestResolveProjectionAcceptsRelativeInHomeSymlinkSource(t *testing.T) {
	home := projHome(t, "real.rc")
	if err := os.Symlink("real.rc", filepath.Join(home, ".zshrc")); err != nil {
		t.Fatal(err)
	}
	m, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.zshrc"},
	}})
	if err != nil {
		t.Fatalf("relative in-home source symlink must resolve safely: %v", err)
	}
	if len(m.PresentMounts()) != 1 {
		t.Fatalf("want one projected symlink source, got %+v", m.Items)
	}
}

func TestResolveProjectionAcceptsRelativeInHomeSymlinkComponent(t *testing.T) {
	home := projHome(t)
	realdir := filepath.Join(home, "realdir")
	if err := os.MkdirAll(realdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realdir, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// ~/linkdir -> realdir ; the pinned resolver follows it without reopening the source path.
	if err := os.Symlink("realdir", filepath.Join(home, "linkdir")); err != nil {
		t.Fatal(err)
	}
	m, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/linkdir/file"},
	}})
	if err != nil {
		t.Fatalf("relative in-home symlink component must resolve safely: %v", err)
	}
	if len(m.PresentMounts()) != 1 {
		t.Fatalf("want one projected file, got %+v", m.Items)
	}
}

func TestResolveProjectionRejectsDuplicateTarget(t *testing.T) {
	home := projHome(t, ".zshrc")
	_, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.zshrc"},
		{Source: "~/.zshrc"}, // same target
	}})
	requireProjectionCode(t, err, ProjectionSourceType)
}

func TestResolveProjectionDirExpandsPerFile(t *testing.T) {
	home := projHome(t, ".pi/agent/skills/foo/SKILL.md", ".pi/agent/skills/bar/SKILL.md", ".pi/agent/skills/bar/handler.sh")
	m, err := ResolveProjection(home, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{
		{Source: "~/.pi/agent/skills", Kind: "dir", Label: "pi-skills"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mounts := m.PresentMounts()
	if len(mounts) != 3 {
		t.Fatalf("dir must snapshot 3 regular files, got %d: %+v", len(mounts), mounts)
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

func TestSnapshotProjectionFishBuiltinExcludesEagerStartup(t *testing.T) {
	home := projHome(t,
		".config/fish/config.fish",
		".config/fish/conf.d/eager.fish",
		".config/fish/functions/projected_fn.fish",
		".config/fish/completions/flo_probe.fish",
	)
	contents := map[string]string{
		".config/fish/config.fish":                 "EAGER_CONFIG_RAN",
		".config/fish/conf.d/eager.fish":           "EAGER_CONFD_RAN",
		".config/fish/functions/projected_fn.fish": "DEMAND_FUNCTION",
		".config/fish/completions/flo_probe.fish":  "DEMAND_COMPLETION",
	}
	for rel, content := range contents {
		if err := os.WriteFile(filepath.Join(home, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	builtin, ok := policy.BuiltinProfileByName("fish")
	if !ok || builtin.Profile.Projection == nil {
		t.Fatal("fish builtin projection missing")
	}
	m, err := SnapshotProjection(home, t.TempDir(), *builtin.Profile.Projection)
	if err != nil {
		t.Fatal(err)
	}
	mounts := m.PresentMounts()
	var targets []string
	var snapshotBytes strings.Builder
	for _, mount := range mounts {
		targets = append(targets, mount.Target)
		data, err := os.ReadFile(mount.Host)
		if err != nil {
			t.Fatal(err)
		}
		snapshotBytes.Write(data)
	}
	wantTargets := ".config/fish/functions/projected_fn.fish,.config/fish/completions/flo_probe.fish"
	if got := strings.Join(targets, ","); got != wantTargets {
		t.Fatalf("fish builtin snapshot targets = %q, want %q", got, wantTargets)
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	published := string(encoded) + snapshotBytes.String()
	for _, sentinel := range []string{"config.fish", "conf.d", "EAGER_CONFIG_RAN", "EAGER_CONFD_RAN"} {
		if strings.Contains(published, sentinel) {
			t.Fatalf("fish builtin snapshot published eager sentinel %q", sentinel)
		}
	}
}

func TestSnapshotProjectionOptionalGlobSkipsNonRegularMatches(t *testing.T) {
	home := projHome(t, ".config/fish/completions/ok.fish")
	regular := filepath.Join(home, ".config/fish/completions/ok.fish")
	if err := os.WriteFile(regular, []byte("regular-safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside-target-sentinel")
	if err := os.WriteFile(outside, []byte("outside-content-sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	completions := filepath.Dir(regular)
	if err := os.Symlink(outside, filepath.Join(completions, "outside-name-sentinel.fish")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(completions, "directory-name-sentinel.fish"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldReadlink := projectionAfterReadlink
	readlinks := 0
	projectionAfterReadlink = func(string) { readlinks++ }
	t.Cleanup(func() { projectionAfterReadlink = oldReadlink })

	m, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{
		Source: "~/.config/fish/completions/*.fish", Kind: "glob", Label: "fish-completions",
	}}})
	if err != nil {
		t.Fatalf("optional glob must omit non-regular matches: %v", err)
	}
	mounts := m.PresentMounts()
	if len(mounts) != 1 || mounts[0].Target != ".config/fish/completions/ok.fish" {
		t.Fatalf("optional glob mounts = %+v, want only ok.fish", mounts)
	}
	got, err := os.ReadFile(mounts[0].Host)
	if err != nil || string(got) != "regular-safe" {
		t.Fatalf("regular snapshot = %q, err=%v", got, err)
	}
	if readlinks != 0 {
		t.Fatalf("terminal glob candidates must never be readlinked, calls=%d", readlinks)
	}
	if len(m.Items) != 2 || m.Items[1].Status != "skipped-nonregular" {
		t.Fatalf("want one present and one aggregate omission, got %+v", m.Items)
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{"outside-name-sentinel", "directory-name-sentinel", outside, "outside-content-sentinel"} {
		if strings.Contains(string(encoded), sentinel) {
			t.Fatalf("manifest leaked omitted candidate sentinel %q: %s", sentinel, encoded)
		}
	}
}

func TestSnapshotProjectionOptionalGlobAllNonRegularSucceeds(t *testing.T) {
	home := projHome(t, ".config/fish/completions/")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("never-read"), 0o600); err != nil {
		t.Fatal(err)
	}
	completions := filepath.Join(home, ".config/fish/completions")
	if err := os.Symlink(outside, filepath.Join(completions, "linked.fish")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(completions, "directory.fish"), 0o755); err != nil {
		t.Fatal(err)
	}

	m, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{
		Source: "~/.config/fish/completions/*.fish", Kind: "glob", Label: "fish-completions",
	}}})
	if err != nil {
		t.Fatalf("all-nonregular optional glob must succeed: %v", err)
	}
	if len(m.PresentMounts()) != 0 || len(m.Items) != 1 || m.Items[0].Status != "skipped-nonregular" {
		t.Fatalf("all-nonregular optional glob = %+v", m.Items)
	}
}

func TestSnapshotProjectionRequiredGlobRejectsNonRegularMatch(t *testing.T) {
	home := projHome(t, ".config/fish/completions/ok.fish")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("never-read"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".config/fish/completions/linked.fish")); err != nil {
		t.Fatal(err)
	}
	_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{
		Source: "~/.config/fish/completions/*.fish", Kind: "glob", Optional: boolPtr(false),
	}}})
	requireProjectionCode(t, err, ProjectionUnsafeDescendant)
}

func TestSnapshotProjectionOptionalGlobRejectsReplacementAfterClassification(t *testing.T) {
	home := projHome(t, ".config/fish/completions/selected.fish")
	selected := filepath.Join(home, ".config/fish/completions/selected.fish")
	replacement := filepath.Join(home, ".config/fish/completions/replacement.tmp")
	if err := os.WriteFile(replacement, []byte("replacement-sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldHook := projectionAfterGlobLstat
	projectionAfterGlobLstat = func(_, name string) {
		if name != "selected.fish" {
			return
		}
		projectionAfterGlobLstat = nil
		if err := os.Remove(selected); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, selected); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { projectionAfterGlobLstat = oldHook })

	_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{
		Source: "~/.config/fish/completions/*.fish", Kind: "glob",
	}}})
	requireProjectionCode(t, err, ProjectionSnapshotChanged)
}

func TestSnapshotProjectionFollowsRelativeConfigSymlinkIntoPrivateStage(t *testing.T) {
	home := projHome(t, "dotfiles/files/.config/fish/config.fish")
	if err := os.RemoveAll(filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dotfiles/files/.config", filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	m, err := SnapshotProjection(home, stage, policy.Projection{Enabled: true, Items: []policy.ProjectionItem{{
		Source: "~/.config/fish/config.fish", Label: "fish",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	mounts := m.PresentMounts()
	if len(mounts) != 1 {
		t.Fatalf("want one mount, got %+v", m.Items)
	}
	if !strings.HasPrefix(mounts[0].Host, filepath.Join(stage, "projection-snapshots")+string(filepath.Separator)) {
		t.Fatalf("mount host is not a private snapshot: %q", mounts[0].Host)
	}
	if strings.HasPrefix(mounts[0].Host, home+string(filepath.Separator)) {
		t.Fatalf("mount host points into live home: %q", mounts[0].Host)
	}
	got, err := os.ReadFile(mounts[0].Host)
	if err != nil || string(got) != "x" {
		t.Fatalf("snapshot bytes = %q, err=%v", got, err)
	}
	info, err := os.Stat(filepath.Join(stage, "projection-snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("snapshot dir mode = %#o, want 0700", info.Mode().Perm())
	}
	fileInfo, err := os.Stat(mounts[0].Host)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o444 {
		t.Fatalf("snapshot file mode = %#o, want container-readable 0444", fileInfo.Mode().Perm())
	}
}

func TestStrictAbsoluteTarget(t *testing.T) {
	tests := []struct {
		name, root, target, want string
		ok                       bool
	}{
		{name: "descendant", root: "/Users/jojo", target: "/Users/jojo/workspace/pi", want: "workspace/pi", ok: true},
		{name: "root", root: "/Users/jojo", target: "/Users/jojo"},
		{name: "prefix collision", root: "/Users/jojo", target: "/Users/jojo-attacker/pi"},
		{name: "outside", root: "/Users/jojo", target: "/tmp/pi"},
		{name: "dot", root: "/Users/jojo", target: "/Users/jojo/./pi"},
		{name: "dot dot", root: "/Users/jojo", target: "/Users/jojo/workspace/../pi"},
		{name: "duplicate separator", root: "/Users/jojo", target: "/Users/jojo//pi"},
		{name: "trailing separator", root: "/Users/jojo", target: "/Users/jojo/pi/"},
		{name: "alternate case", root: "/Users/jojo", target: "/users/jojo/pi"},
		{name: "filesystem root", root: "/", target: "/Users/jojo"},
		{name: "relative root", root: "Users/jojo", target: "/Users/jojo/pi"},
		{name: "unclean root", root: "/Users//jojo", target: "/Users//jojo/pi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := strictAbsoluteTarget(tt.root, tt.target)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("strictAbsoluteTarget(%q, %q) = %q, %v; want %q, %v", tt.root, tt.target, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSnapshotProjectionAcceptsBuiltinAbsoluteInHomeSymlink(t *testing.T) {
	for _, name := range []string{"pi", "claude"} {
		t.Run(name, func(t *testing.T) {
			home := projHome(t)
			agent := filepath.Join(home, "workspace", "pi-dev-setup", "agent")
			if err := os.MkdirAll(filepath.Join(agent, "skills", "probe"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agent, "AGENTS.md"), []byte("absolute-agent-instructions"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agent, "skills", "probe", "SKILL.md"), []byte("absolute-agent-skill"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(home, ".pi"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(agent, filepath.Join(home, ".pi", "agent")); err != nil {
				t.Fatal(err)
			}
			builtin, ok := policy.BuiltinProfileByName(name)
			if !ok || builtin.Profile.Projection == nil {
				t.Fatalf("%s builtin projection missing", name)
			}

			manifest, err := SnapshotProjection(home, t.TempDir(), *builtin.Profile.Projection)
			if err != nil {
				var projectionErr *ProjectionError
				if errors.As(err, &projectionErr) && projectionErr.Failure().Code == ProjectionTargetOutsideRoot {
					t.Fatalf("exact in-home absolute symlink was rejected: %v", err)
				}
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, mount := range manifest.PresentMounts() {
				contents, err := os.ReadFile(mount.Host)
				if err != nil {
					t.Fatal(err)
				}
				got[mount.Target] = string(contents)
			}
			want := map[string]string{
				".pi/agent/AGENTS.md":             "absolute-agent-instructions",
				".pi/agent/skills/probe/SKILL.md": "absolute-agent-skill",
			}
			if len(got) != len(want) {
				t.Fatalf("%s projected targets = %#v, want %#v", name, got, want)
			}
			for target, contents := range want {
				if got[target] != contents {
					t.Fatalf("%s snapshot %s = %q, want %q", name, target, got[target], contents)
				}
			}
		})
	}
}

func TestSnapshotProjectionRejectsUnsafeAbsoluteTargetSpellings(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "home")
	if err := os.MkdirAll(filepath.Join(home, "store"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside-target-sentinel")
	prefixCollision := home + "-attacker"
	for _, dir := range []string{outside, prefixCollision} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{
		"root":                home,
		"outside":             outside,
		"prefix-collision":    filepath.Join(prefixCollision, "file"),
		"dot-component":       home + "/store/./file",
		"dot-dot-component":   home + "/store/../file",
		"duplicate-separator": home + "//store/file",
		"trailing-separator":  home + "/store/",
		"alternate-case":      strings.ToUpper(home) + "/store/file",
		"unicode-alternate":   home + "\u0301/store/file",
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			link := filepath.Join(home, "link")
			_ = os.Remove(link)
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/link/file", Optional: boolPtr(false)}}})
			requireProjectionCode(t, err, ProjectionTargetOutsideRoot)
			var projectionErr *ProjectionError
			if !errors.As(err, &projectionErr) {
				t.Fatal("missing projection failure")
			}
			failure := projectionErr.Failure()
			if failure.Summary != "Config projection target is not safely within its approved root." || failure.Action != "Use an exact in-root relative or absolute symlink target." {
				t.Fatalf("outside-root guidance = %q / %q", failure.Summary, failure.Action)
			}
			encoded, marshalErr := json.Marshal(failure)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			for _, sentinel := range []string{target, outside, prefixCollision} {
				if sentinel != "" && strings.Contains(string(encoded), sentinel) {
					t.Fatalf("failure leaked absolute target sentinel %q: %s", sentinel, encoded)
				}
			}
		})
	}
}

func TestSnapshotProjectionRejectsAbsoluteExcludedSymlink(t *testing.T) {
	home := projHome(t, ".ssh/config")
	if err := os.Symlink(filepath.Join(home, ".ssh", "config"), filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/link", Optional: boolPtr(false)}}})
	requireProjectionCode(t, err, ProjectionTargetExcluded)
}

func TestSnapshotProjectionDetectsAbsoluteSymlinkReadRace(t *testing.T) {
	home := projHome(t, "old", "new")
	link := filepath.Join(home, ".zshrc")
	if err := os.Symlink(filepath.Join(home, "old"), link); err != nil {
		t.Fatal(err)
	}
	oldHook := projectionAfterReadlink
	projectionAfterReadlink = func(string) {
		projectionAfterReadlink = nil
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(home, "new"), link); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { projectionAfterReadlink = oldHook })
	_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/.zshrc"}}})
	requireProjectionCode(t, err, ProjectionSnapshotChanged)
}

func TestSnapshotProjectionUsesPinnedRootForAbsoluteSymlink(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "home")
	originalFile := filepath.Join(home, "store", "file")
	if err := os.MkdirAll(filepath.Dir(originalFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originalFile, []byte("original-pinned-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(originalFile, filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	oldHome := filepath.Join(parent, "original-home")
	oldHook := projectionAfterReadlink
	projectionAfterReadlink = func(string) {
		projectionAfterReadlink = nil
		if err := os.Rename(home, oldHome); err != nil {
			t.Fatal(err)
		}
		attackerFile := filepath.Join(home, "store", "file")
		if err := os.MkdirAll(filepath.Dir(attackerFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(attackerFile, []byte("replacement-path-sentinel"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { projectionAfterReadlink = oldHook })

	manifest, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/link", Optional: boolPtr(false)}}})
	if err != nil {
		t.Fatal(err)
	}
	mounts := manifest.PresentMounts()
	if len(mounts) != 1 {
		t.Fatalf("present mounts = %+v, want one", mounts)
	}
	got, err := os.ReadFile(mounts[0].Host)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original-pinned-bytes" || strings.Contains(string(got), "replacement-path-sentinel") {
		t.Fatalf("snapshot followed replaced root pathname: %q", got)
	}
}

func TestSnapshotProjectionRejectsAbsoluteEscapeExcludedAndLoopingSymlinks(t *testing.T) {
	t.Run("absolute-outside", func(t *testing.T) {
		home := projHome(t)
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("never-read"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(home, "link")); err != nil {
			t.Fatal(err)
		}
		_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/link"}}})
		requireProjectionCode(t, err, ProjectionTargetOutsideRoot)
	})
	t.Run("escape", func(t *testing.T) {
		parent := t.TempDir()
		home := filepath.Join(parent, "home")
		if err := os.Mkdir(home, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parent, "outside"), []byte("no"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../outside", filepath.Join(home, "link")); err != nil {
			t.Fatal(err)
		}
		_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/link"}}})
		requireProjectionCode(t, err, ProjectionTargetOutsideRoot)
	})
	t.Run("excluded", func(t *testing.T) {
		home := projHome(t, ".ssh/config")
		if err := os.MkdirAll(filepath.Join(home, ".config"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../.ssh/config", filepath.Join(home, ".config", "fish")); err != nil {
			t.Fatal(err)
		}
		_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/.config/fish"}}})
		requireProjectionCode(t, err, ProjectionTargetExcluded)
	})
	t.Run("loop", func(t *testing.T) {
		home := projHome(t)
		if err := os.Symlink("b", filepath.Join(home, "a")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("a", filepath.Join(home, "b")); err != nil {
			t.Fatal(err)
		}
		_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/a"}}})
		requireProjectionCode(t, err, ProjectionSymlinkLoop)
	})
}

func TestSnapshotProjectionRejectsInternalSymlink(t *testing.T) {
	home := projHome(t, "tree/a", "outside")
	if err := os.Symlink("../outside", filepath.Join(home, "tree", "link")); err != nil {
		t.Fatal(err)
	}
	_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/tree", Kind: "dir"}}})
	requireProjectionCode(t, err, ProjectionUnsafeDescendant)
}

func TestSnapshotProjectionRejectsInjectedMountCrossing(t *testing.T) {
	home := projHome(t, "tree/cross/file")
	original := projectionMountID
	rootID, rootOK := uint64(0), false
	projectionMountID = func(file *os.File) (uint64, bool) {
		id, ok := original(file)
		if !ok {
			return 0, false
		}
		if !rootOK {
			rootID, rootOK = id, true
		}
		clean := filepath.ToSlash(filepath.Clean(file.Name()))
		if strings.HasSuffix(clean, "/cross") || strings.Contains(clean, "/cross/") {
			return rootID + 1, true
		}
		return rootID, true
	}
	t.Cleanup(func() { projectionMountID = original })
	_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/tree", Kind: "dir"}}})
	requireProjectionCode(t, err, ProjectionUnsafeDescendant)
}

func TestSnapshotProjectionDetectsSymlinkReadRace(t *testing.T) {
	home := projHome(t, "old", "new")
	link := filepath.Join(home, ".zshrc")
	if err := os.Symlink("old", link); err != nil {
		t.Fatal(err)
	}
	projectionAfterReadlink = func(string) {
		projectionAfterReadlink = nil
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("new", link); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { projectionAfterReadlink = nil })
	_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/.zshrc"}}})
	requireProjectionCode(t, err, ProjectionSnapshotChanged)
}

func TestSnapshotProjectionPinsOpenedFileAcrossPathReplacement(t *testing.T) {
	home := projHome(t)
	oldPath := filepath.Join(home, "old")
	newPath := filepath.Join(home, "new")
	if err := os.WriteFile(oldPath, []byte("old-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".zshrc")
	if err := os.Symlink("old", link); err != nil {
		t.Fatal(err)
	}
	projectionAfterOpen = func(string) {
		projectionAfterOpen = nil
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("new", link); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { projectionAfterOpen = nil })
	m, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/.zshrc"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(m.PresentMounts()[0].Host)
	if err != nil || string(got) != "old-bytes" {
		t.Fatalf("snapshot followed replacement: bytes=%q err=%v", got, err)
	}
}

func TestSnapshotProjectionDetectsEarlierDirectoryFileMutation(t *testing.T) {
	home := projHome(t)
	first := filepath.Join(home, "tree", "a")
	second := filepath.Join(home, "tree", "b")
	if err := os.MkdirAll(filepath.Dir(first), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	opens := 0
	projectionAfterOpen = func(string) {
		opens++
		if opens == 2 {
			projectionAfterOpen = nil
			if err := os.WriteFile(first, []byte("first-mutated-after-copy"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { projectionAfterOpen = nil })
	_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/tree", Kind: "dir"}}})
	requireProjectionCode(t, err, ProjectionSnapshotChanged)
}

func TestSnapshotProjectionIgnoresUnusedMissingExternalXDGRoot(t *testing.T) {
	home := projHome(t, ".zshrc")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "missing"))
	if _, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/.zshrc"}}}); err != nil {
		t.Fatalf("unused external XDG root broke home projection: %v", err)
	}
}

func TestSnapshotProjectionDetectsOpenedFileMutation(t *testing.T) {
	home := projHome(t)
	source := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(source, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	projectionAfterOpen = func(string) {
		projectionAfterOpen = nil
		if err := os.WriteFile(source, []byte("after-content-is-longer"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { projectionAfterOpen = nil })
	_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/.zshrc"}}})
	requireProjectionCode(t, err, ProjectionSnapshotChanged)
}

func TestProjectionSharedProofCharacterization(t *testing.T) {
	t.Run("relative-link-private-snapshot-and-manifest", func(t *testing.T) {
		home := projHome(t, "store/config")
		mustProjection(t, os.Symlink("store/config", filepath.Join(home, "config-link")))
		stage := t.TempDir()
		manifest, err := SnapshotProjection(home, stage, policy.Projection{Items: []policy.ProjectionItem{{
			Source: "~/config-link", Label: "characterization", Optional: boolPtr(false),
		}}})
		if err != nil {
			t.Fatal(err)
		}
		mounts := manifest.PresentMounts()
		if len(mounts) != 1 || mounts[0].Target != "config-link" || mounts[0].Status != projPresent {
			t.Fatalf("manifest semantic delta: %+v", manifest.Items)
		}
		if !strings.HasPrefix(mounts[0].Host, filepath.Join(stage, "projection-snapshots")+string(filepath.Separator)) {
			t.Fatalf("snapshot escaped private stage: %q", mounts[0].Host)
		}
		body, readErr := os.ReadFile(mounts[0].Host)
		if readErr != nil || string(body) != "x" {
			t.Fatalf("snapshot bytes = %q err=%v", body, readErr)
		}
	})

	t.Run("rewritten-exclusion", func(t *testing.T) {
		home := projHome(t, ".ssh/config")
		mustProjection(t, os.Symlink(".ssh/config", filepath.Join(home, "config-link")))
		_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/config-link"}}})
		requireProjectionCode(t, err, ProjectionTargetExcluded)
	})

	t.Run("external-xdg-lazy-and-used", func(t *testing.T) {
		home := projHome(t, ".zshrc")
		external := filepath.Join(t.TempDir(), "xdg")
		t.Setenv("XDG_CONFIG_HOME", external)
		if _, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/.zshrc"}}}); err != nil {
			t.Fatalf("unused external XDG root was opened: %v", err)
		}
		mustProjection(t, os.MkdirAll(external, 0o755))
		mustProjection(t, os.WriteFile(filepath.Join(external, "used.conf"), []byte("xdg"), 0o644))
		manifest, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: filepath.Join(external, "used.conf")}}})
		if err != nil || len(manifest.PresentMounts()) != 1 || manifest.PresentMounts()[0].Target != ".config/used.conf" {
			t.Fatalf("external XDG semantics changed: manifest=%+v err=%v", manifest.Items, err)
		}
	})

	t.Run("tree-link-remains-rejected", func(t *testing.T) {
		home := projHome(t, "tree/file", "outside")
		mustProjection(t, os.Symlink("../outside", filepath.Join(home, "tree", "linked")))
		_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/tree", Kind: "dir"}}})
		requireProjectionCode(t, err, ProjectionUnsafeDescendant)
	})

	t.Run("optional-glob-omits-terminal-link", func(t *testing.T) {
		home := projHome(t, ".config/fish/completions/ok.fish", "outside")
		dir := filepath.Join(home, ".config/fish/completions")
		mustProjection(t, os.Symlink(filepath.Join(home, "outside"), filepath.Join(dir, "linked.fish")))
		manifest, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{
			Source: "~/.config/fish/completions/*.fish", Kind: "glob", Label: "fish",
		}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(manifest.PresentMounts()) != 1 || len(manifest.Items) != 2 || manifest.Items[1].Status != projSkippedNonRegular {
			t.Fatalf("glob/manifest semantics changed: %+v", manifest.Items)
		}
	})

	t.Run("same-device-different-mount-rejected-value-free", func(t *testing.T) {
		home := projHome(t, "tree/cross/file")
		original := projectionMountID
		rootID, haveRoot := uint64(0), false
		projectionMountID = func(file *os.File) (uint64, bool) {
			id, ok := original(file)
			if !ok {
				return 0, false
			}
			if !haveRoot {
				rootID, haveRoot = id, true
			}
			if strings.HasSuffix(filepath.ToSlash(filepath.Clean(file.Name())), "/cross") {
				return rootID + 1, true
			}
			return rootID, true
		}
		t.Cleanup(func() { projectionMountID = original })
		_, err := SnapshotProjection(home, t.TempDir(), policy.Projection{Items: []policy.ProjectionItem{{Source: "~/tree", Kind: "dir"}}})
		requireProjectionCode(t, err, ProjectionUnsafeDescendant)
		var projectionErr *ProjectionError
		if !errors.As(err, &projectionErr) {
			t.Fatal("missing value-free projection failure")
		}
		encoded, marshalErr := json.Marshal(projectionErr.Failure())
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(encoded), home) || strings.Contains(string(encoded), filepath.Join(home, "tree", "cross")) || strings.Contains(string(encoded), "42") {
			t.Fatalf("failure leaked proof details: %s", encoded)
		}
	})
}

func mustProjection(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
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
