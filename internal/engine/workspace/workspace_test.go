package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUsesPolicyDirectoryForNonEmptyRelativeWorkspace(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	caller := filepath.Join(root, "nested", "caller")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(caller, 0o700); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(root, "safeslop.cue")
	if err := os.WriteFile(policyPath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve("project", policyPath, caller)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Resolve = %q, want policy-relative %q", got, want)
	}
}

func TestResolveEmptyUsesInvocationDirectory(t *testing.T) {
	caller := t.TempDir()
	policyDir := t.TempDir()
	got, err := Resolve("", filepath.Join(policyDir, "safeslop.cue"), caller)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(caller)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Resolve empty = %q, want invocation directory %q", got, want)
	}
}

func TestResolveCanonicalizesSymlinksAndPreservesValidHostileText(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, `space: $ ${NOPE} "quoted" 雪`)
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Base(real), link); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(link, "", root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}

func TestResolveRejectsMissingFileAndControlCharacters(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"missing": filepath.Join(root, "missing"),
		"file":    file,
		"newline": filepath.Join(root, "safe") + "\n- /:/host:rw",
		"format":  filepath.Join(root, "safe") + "\u202e",
		"invalid": string([]byte{root[0], 0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Resolve(raw, "", root); err == nil {
				t.Fatalf("Resolve(%q) succeeded", raw)
			}
		})
	}
}

func TestRequireDisjointRejectsOverlapInBothDirections(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	stage := filepath.Join(root, "stage")
	for _, path := range []string{workspace, stage} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := RequireDisjoint(workspace, stage); err != nil {
		t.Fatalf("disjoint siblings rejected: %v", err)
	}
	notCreated := filepath.Join(workspace, "future-stage")
	if err := RequireDisjointPaths(workspace, notCreated); !errors.Is(err, ErrOverlap) {
		t.Fatalf("future stage under workspace error = %v, want ErrOverlap", err)
	}
	insideWorkspace := filepath.Join(workspace, "stage")
	if err := os.Mkdir(insideWorkspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RequireDisjoint(workspace, insideWorkspace); !errors.Is(err, ErrOverlap) {
		t.Fatalf("stage under workspace error = %v, want ErrOverlap", err)
	}
	insideStage := filepath.Join(stage, "workspace")
	if err := os.Mkdir(insideStage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RequireDisjoint(insideStage, stage); !errors.Is(err, ErrOverlap) {
		t.Fatalf("workspace under stage error = %v, want ErrOverlap", err)
	}
}
