package container

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type composeMountDocument struct {
	Services map[string]struct {
		Volumes []struct {
			Type     string `yaml:"type"`
			Source   string `yaml:"source"`
			Target   string `yaml:"target"`
			ReadOnly bool   `yaml:"read_only"`
			Bind     struct {
				CreateHostPath *bool `yaml:"create_host_path"`
			} `yaml:"bind"`
		} `yaml:"volumes"`
	} `yaml:"services"`
}

func composeBoundaryParams(t *testing.T, workspace string) composeParams {
	t.Helper()
	stage := t.TempDir()
	for _, name := range []string{"squid.conf", "allowlist.domains", "session-grants.conf", "entrypoint.sh"} {
		if err := os.WriteFile(filepath.Join(stage, name), []byte("fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return composeParams{
		RuntimeDir: stage,
		StageDir:   stage,
		Workspace:  workspace,
		SessionID:  "sess-boundary",
		AgentImage: "local/safeslop-tools:fixture",
	}
}

func TestComposeMountPlanHasExactlyOneReadWriteWorkspace(t *testing.T) {
	workspace := t.TempDir()
	params := composeBoundaryParams(t, workspace)
	yml, err := renderCompose(params)
	if err != nil {
		t.Fatalf("renderCompose: %v", err)
	}
	var doc composeMountDocument
	if err := yaml.Unmarshal([]byte(yml), &doc); err != nil {
		t.Fatalf("decode rendered Compose: %v\n%s", err, yml)
	}
	agent := doc.Services["agent"].Volumes
	readWrite := 0
	for _, mount := range agent {
		if mount.Type != "bind" || mount.Source == "" || mount.Target == "" {
			t.Fatalf("agent mount is not typed long-form bind: %#v", mount)
		}
		if mount.Bind.CreateHostPath == nil || *mount.Bind.CreateHostPath {
			t.Fatalf("agent bind does not explicitly disable host source creation: %#v", mount)
		}
		if !mount.ReadOnly {
			readWrite++
			if mount.Source != workspace || mount.Target != "/workspace" {
				t.Fatalf("unexpected writable bind: %#v", mount)
			}
		}
	}
	if readWrite != 1 {
		t.Fatalf("agent writable bind count = %d, want exactly 1; mounts=%#v", readWrite, agent)
	}
	for _, mount := range doc.Services["proxy"].Volumes {
		if mount.Type != "bind" || !mount.ReadOnly || mount.Bind.CreateHostPath == nil || *mount.Bind.CreateHostPath {
			t.Fatalf("proxy mount is not fail-closed read-only long form: %#v", mount)
		}
	}
}

func TestComposeRejectsWorkspaceStructureInjection(t *testing.T) {
	injected := filepath.Join(t.TempDir(), "safe") + "\n      - /:/host:rw"
	if _, err := renderCompose(composeBoundaryParams(t, injected)); err == nil {
		t.Fatal("renderCompose accepted a newline-bearing workspace that can add YAML structure")
	}
}

func TestComposePreservesHostileValidWorkspaceAsOneScalar(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), `space: $ ${NOPE} "quoted" 雪`)
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	yml, err := renderCompose(composeBoundaryParams(t, workspace))
	if err != nil {
		t.Fatalf("renderCompose: %v", err)
	}
	var doc composeMountDocument
	if err := yaml.Unmarshal([]byte(yml), &doc); err != nil {
		t.Fatalf("decode rendered Compose: %v\n%s", err, yml)
	}
	wantRaw := strings.ReplaceAll(workspace, "$", "$$")
	found := 0
	for _, mount := range doc.Services["agent"].Volumes {
		if mount.Target == "/workspace" {
			found++
			if mount.Source != wantRaw {
				t.Fatalf("workspace scalar = %q, want escaped literal %q", mount.Source, wantRaw)
			}
		}
	}
	if found != 1 {
		t.Fatalf("workspace target count = %d, want 1", found)
	}
}

func TestComposeConfigPreservesHostileWorkspaceWithoutInterpolation(t *testing.T) {
	if os.Getenv("SAFESLOP_REAL_COMPOSE_CONFIG") != "1" {
		t.Skip("set SAFESLOP_REAL_COMPOSE_CONFIG=1 for the real Compose parser gate")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI unavailable")
	}
	workspace := filepath.Join(t.TempDir(), `space: $ ${NOPE} "quoted" 雪`)
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	params := composeBoundaryParams(t, workspace)
	composeFile, err := materializeRun(params, false)
	if err != nil {
		t.Fatalf("materializeRun: %v", err)
	}
	args, err := composeProjectArgs(composeFile, "config", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, output)
	}
	var doc struct {
		Services map[string]struct {
			Volumes []struct {
				Type     string `json:"type"`
				Source   string `json:"source"`
				Target   string `json:"target"`
				ReadOnly bool   `json:"read_only"`
				Bind     struct {
					CreateHostPath *bool `json:"create_host_path"`
				} `json:"bind"`
			} `json:"volumes"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &doc); err != nil {
		t.Fatalf("decode Compose JSON: %v\n%s", err, output)
	}
	canonical, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	escapedCanonical := strings.ReplaceAll(canonical, "$", "$$")
	found := 0
	for _, mount := range doc.Services["agent"].Volumes {
		if mount.Target != "/workspace" {
			continue
		}
		found++
		if mount.Type != "bind" || mount.Source != escapedCanonical || mount.ReadOnly || mount.Bind.CreateHostPath == nil || *mount.Bind.CreateHostPath {
			t.Fatalf("Compose changed or interpolated workspace authority: %#v; want escaped source %q", mount, escapedCanonical)
		}
	}
	if found != 1 {
		t.Fatalf("Compose workspace mount count = %d, want 1", found)
	}
}

func TestComposeRejectsMissingWorkspaceAndStageOverlap(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := materializeRun(composeBoundaryParams(t, missing), false); err == nil {
		t.Fatal("materializeRun accepted a missing bind source")
	}
	params := composeBoundaryParams(t, t.TempDir())
	params.Workspace = params.StageDir
	if _, err := materializeRun(params, false); err == nil {
		t.Fatal("materializeRun accepted the stage itself as the writable workspace")
	}
	params = composeBoundaryParams(t, t.TempDir())
	params.StageDir = t.TempDir()
	if _, err := materializeRun(params, false); err == nil {
		t.Fatal("materializeRun accepted a runtime source outside the private stage")
	}
}
