package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedContentRejectsLatestPatterns(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "docker tag", body: `image: "node:latest"`},
		{name: "oci ref", body: `image: "ghcr.io/acme/tool@latest"`},
		{name: "python pin", body: `pkg==latest`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := PinningFindings([]byte(tc.body), "fixture.cue")
			if len(findings) != 1 {
				t.Fatalf("want one latest finding, got %+v", findings)
			}
			if findings[0].Line != 1 || findings[0].Pattern == "" || findings[0].Path != "fixture.cue" {
				t.Fatalf("bad finding metadata: %+v", findings[0])
			}
		})
	}
}

func TestPinnedContentIgnoresNonLatest(t *testing.T) {
	body := []byte(`image: "node:22-bookworm"
uv: "0.7.13"
# historical docs may say :latest without a closing quote
`)
	if got := PinningFindings(body, "fixture.cue"); len(got) != 0 {
		t.Fatalf("non-latest pins should pass, got %+v", got)
	}
}

func TestCheckNoLatestPinsWalksCueAndBuildConfigFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "safeslop.cue"), `package safeslop
image: "node:22-bookworm"`)
	mustWrite(t, filepath.Join(root, "internal", "engine", "container", "assets", "agent-tools.env"), `UV_VERSION=0.7.13`)
	mustWrite(t, filepath.Join(root, "internal", "engine", "container", "assets", "Dockerfile.agent"), `FROM node:latest"`)
	mustWrite(t, filepath.Join(root, "README.md"), `node:latest" in docs is not part of the pinning gate`)

	findings, err := CheckNoLatestPins(root)
	if err != nil {
		t.Fatalf("CheckNoLatestPins: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("want one finding, got %+v", findings)
	}
	if !strings.HasSuffix(findings[0].Path, "Dockerfile.agent") {
		t.Fatalf("should scan build configs, got %+v", findings[0])
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
