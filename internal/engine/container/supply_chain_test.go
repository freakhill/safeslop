package container

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const reviewedProxyIndexDigest = "sha256:723891b5bc74fe86d271589927c36c6b205fafca48b2bbe4932e3110c7d6bcc7"

func TestProxyImageLockAndComposeHardening(t *testing.T) {
	body, err := readAsset("proxy-image.lock.json")
	if err != nil {
		t.Fatalf("embedded proxy lock: %v", err)
	}
	var lock struct {
		SchemaVersion int               `json:"schemaVersion"`
		Image         string            `json:"image"`
		Tag           string            `json:"tag"`
		IndexDigest   string            `json:"indexDigest"`
		IndexFile     string            `json:"indexFile"`
		Manifests     map[string]string `json:"manifests"`
	}
	if err := json.Unmarshal(body, &lock); err != nil {
		t.Fatalf("decode proxy lock: %v", err)
	}
	if lock.SchemaVersion != 1 || lock.Image != "docker.io/ubuntu/squid" || lock.Tag != "5.2-22.04_beta" || lock.IndexDigest != reviewedProxyIndexDigest || lock.IndexFile != "proxy-image.index.json" {
		t.Fatalf("proxy lock identity = %+v", lock)
	}
	if len(lock.Manifests) != 2 || lock.Manifests["linux/amd64"] != "sha256:f47b90bc9f43e02229fc9451ea041f59ac09d51ac884fe5eafbfb46c008489ed" || lock.Manifests["linux/arm64"] != "sha256:2e069e3ea708f107cc5d5f18ff129370774ebac25873004143d367ab4caf7334" {
		t.Fatalf("proxy platform manifests = %+v", lock.Manifests)
	}
	indexBody, err := readAsset(lock.IndexFile)
	if err != nil {
		t.Fatalf("embedded raw proxy index: %v", err)
	}
	indexHash := sha256.Sum256(indexBody)
	if got := "sha256:" + hex.EncodeToString(indexHash[:]); got != lock.IndexDigest {
		t.Fatalf("raw proxy index digest = %s, want %s", got, lock.IndexDigest)
	}

	yml, err := renderCompose(composeParams{RuntimeDir: "/rt", Workspace: "/ws", StageDir: "/st"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"image: \"docker.io/ubuntu/squid@" + reviewedProxyIndexDigest + "\"",
		"    user: \"13:13\"\n",
		"    read_only: true\n",
		"    cap_drop: [ALL]\n",
		"    pids_limit: 128\n",
		"      - \"no-new-privileges:true\"\n",
		"/run:rw,nosuid,nodev,noexec,size=16m,uid=13,gid=13,mode=0750",
		"/var/log/squid:rw,nosuid,nodev,noexec,size=16m,uid=13,gid=13,mode=0750",
		"/var/spool/squid:rw,nosuid,nodev,noexec,size=16m,uid=13,gid=13,mode=0750",
	} {
		if !strings.Contains(yml, want) {
			t.Fatalf("hardened proxy compose missing %q:\n%s", want, yml)
		}
	}
	if strings.Contains(yml, "ubuntu/squid:5.2-22.04_beta") {
		t.Fatalf("moving proxy tag entered Compose:\n%s", yml)
	}
}

func TestProxyInputsStayReadableByTheNonRootService(t *testing.T) {
	runtimeDir := t.TempDir()
	if _, err := materializeRun(composeParams{RuntimeDir: runtimeDir, StageDir: runtimeDir, Workspace: t.TempDir()}, false); err != nil {
		t.Fatal(err)
	}
	generation, body, err := BuildEgressGeneration([]SessionGrant{{Host: "example.com", Port: 443}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installEgressGeneration(runtimeDir, generation, body, overlayOptions{}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(runtimeDir, "proxy-overlay"):                        0o755,
		filepath.Join(runtimeDir, "proxy-overlay", "session-grants.conf"): 0o644,
		filepath.Join(runtimeDir, "squid.conf"):                           0o644,
		filepath.Join(runtimeDir, "allowlist.domains"):                    0o644,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %04o, want %04o", filepath.Base(path), got, want)
		}
	}
}

func TestProxyImageLockRejectsMovingOrPlaceholderInputs(t *testing.T) {
	valid, err := loadProxyImageLock()
	if err != nil {
		t.Fatal(err)
	}
	indexBody, err := readAsset(valid.IndexFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*proxyImageLock)
	}{
		{name: "moving image tag", mutate: func(lock *proxyImageLock) { lock.Image = "docker.io/ubuntu/squid:latest" }},
		{name: "placeholder index", mutate: func(lock *proxyImageLock) { lock.IndexDigest = "sha256:" + strings.Repeat("0", 64) }},
		{name: "missing platform", mutate: func(lock *proxyImageLock) { delete(lock.Manifests, "linux/arm64") }},
		{name: "foreign platform", mutate: func(lock *proxyImageLock) { lock.Manifests["linux/s390x"] = "sha256:" + strings.Repeat("a", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := valid
			candidate.Manifests = map[string]string{}
			for platform, digest := range valid.Manifests {
				candidate.Manifests[platform] = digest
			}
			tc.mutate(&candidate)
			if err := validateProxyImageLock(candidate, indexBody); err == nil {
				t.Fatal("unreviewed proxy lock was accepted")
			}
		})
	}
	tamperedIndex := append(append([]byte(nil), indexBody...), '\n')
	if err := validateProxyImageLock(valid, tamperedIndex); err == nil {
		t.Fatal("proxy lock accepted raw index bytes with the wrong digest")
	}
}

func TestNPMToolLocksPinCatalogPackageIntegrityAndBinary(t *testing.T) {
	contracts := []struct {
		name, packageName, version, binary string
	}{
		{name: "claude-code", packageName: "@anthropic-ai/claude-code", version: "2.1.121", binary: "claude"},
		{name: "pi", packageName: "@earendil-works/pi-coding-agent", version: "0.80.7", binary: "pi"},
		{name: "pnpm", packageName: "pnpm", version: "9.15.0", binary: "pnpm"},
	}
	for _, contract := range contracts {
		t.Run(contract.name, func(t *testing.T) {
			packageBody, err := readAsset("npm-locks/" + contract.name + "/package.json")
			if err != nil {
				t.Fatalf("embedded package project: %v", err)
			}
			lockBody, err := readAsset("npm-locks/" + contract.name + "/package-lock.json")
			if err != nil {
				t.Fatalf("embedded package lock: %v", err)
			}
			var project struct {
				Private      bool              `json:"private"`
				Dependencies map[string]string `json:"dependencies"`
				Scripts      map[string]string `json:"scripts"`
			}
			if err := json.Unmarshal(packageBody, &project); err != nil {
				t.Fatal(err)
			}
			if !project.Private || len(project.Dependencies) != 1 || project.Dependencies[contract.packageName] != contract.version || len(project.Scripts) != 0 {
				t.Fatalf("package project is not closed: %+v", project)
			}
			var lock struct {
				LockfileVersion int `json:"lockfileVersion"`
				Packages        map[string]struct {
					Resolved  string            `json:"resolved"`
					Integrity string            `json:"integrity"`
					Bin       map[string]string `json:"bin"`
				} `json:"packages"`
			}
			if err := json.Unmarshal(lockBody, &lock); err != nil {
				t.Fatal(err)
			}
			if lock.LockfileVersion != 3 {
				t.Fatalf("lockfileVersion = %d", lock.LockfileVersion)
			}
			root, ok := lock.Packages["node_modules/"+contract.packageName]
			if !ok || root.Bin[contract.binary] == "" {
				t.Fatalf("locked root has wrong binary: %+v", root.Bin)
			}
			for path, pkg := range lock.Packages {
				if path == "" {
					continue
				}
				if !strings.HasPrefix(pkg.Resolved, "https://registry.npmjs.org/") || !strings.HasPrefix(pkg.Integrity, "sha512-") {
					t.Fatalf("%s has foreign or missing integrity source: resolved=%q integrity=%q", path, pkg.Resolved, pkg.Integrity)
				}
			}
		})
	}
}

func TestNPMToolLockRejectsMissingIntegrityForeignSourcesAndPolicyDrift(t *testing.T) {
	var contract npmToolContract
	for _, candidate := range npmToolContracts {
		if candidate.CatalogName == "claude-code" {
			contract = candidate
			break
		}
	}
	packageJSON, err := readAsset("npm-locks/claude-code/package.json")
	if err != nil {
		t.Fatal(err)
	}
	packageLock, err := readAsset("npm-locks/claude-code/package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	mutateLock := func(t *testing.T, mutate func(map[string]any)) []byte {
		t.Helper()
		var decoded map[string]any
		if err := json.Unmarshal(packageLock, &decoded); err != nil {
			t.Fatal(err)
		}
		mutate(decoded)
		body, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	rootPath := "node_modules/@anthropic-ai/claude-code"
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing transitive SRI", mutate: func(lock map[string]any) {
			packages := lock["packages"].(map[string]any)
			for path, raw := range packages {
				if path != "" && path != rootPath {
					delete(raw.(map[string]any), "integrity")
					return
				}
			}
		}},
		{name: "foreign source", mutate: func(lock map[string]any) {
			lock["packages"].(map[string]any)[rootPath].(map[string]any)["resolved"] = "git+https://example.invalid/tool.git"
		}},
		{name: "wrong registry package", mutate: func(lock map[string]any) {
			lock["packages"].(map[string]any)[rootPath].(map[string]any)["resolved"] = "https://registry.npmjs.org/not-claude/-/not-claude-2.1.121.tgz"
		}},
		{name: "wrong binary", mutate: func(lock map[string]any) {
			lock["packages"].(map[string]any)[rootPath].(map[string]any)["bin"] = map[string]any{"not-claude": "bin/claude.exe"}
		}},
		{name: "unreviewed lifecycle script", mutate: func(lock map[string]any) {
			packages := lock["packages"].(map[string]any)
			for path, raw := range packages {
				if path != "" && path != rootPath {
					raw.(map[string]any)["hasInstallScript"] = true
					return
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateNPMToolLock(contract, "2.1.121", packageJSON, mutateLock(t, tc.mutate)); err == nil {
				t.Fatal("unreviewed npm lock was accepted")
			}
		})
	}
	if err := validateNPMToolLock(contract, "2.1.122", packageJSON, packageLock); err == nil {
		t.Fatal("catalog/lock version drift was accepted")
	}
}

func TestBuildContextMaterializesOnlySelectedNPMLocks(t *testing.T) {
	dir, cleanup, err := materializeBuildContext([]string{"node", "pi"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("build context root entries = %v", entries)
	}
	lockEntries, err := os.ReadDir(filepath.Join(dir, "npm-locks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lockEntries) != 1 || lockEntries[0].Name() != "pi" || !lockEntries[0].IsDir() {
		t.Fatalf("selected npm lock set = %v", lockEntries)
	}
	if _, err := os.Stat(filepath.Join(dir, "secrets.env")); !os.IsNotExist(err) {
		t.Fatalf("credential stage data entered build context: %v", err)
	}
	for _, file := range []string{"package.json", "package-lock.json"} {
		got, err := os.ReadFile(filepath.Join(dir, "npm-locks", "pi", file))
		if err != nil {
			t.Fatal(err)
		}
		want, err := readAsset("npm-locks/pi/" + file)
		if err != nil || string(got) != string(want) {
			t.Fatalf("materialized %s drifted: %v", file, err)
		}
	}
}

func TestNPMToolRecipeAndDockerfileUseOnlyReviewedLocks(t *testing.T) {
	contracts := []struct {
		name, policy string
	}{
		{name: "claude-code", policy: "reviewed-root-postinstall"},
		{name: "pi", policy: "ignore-scripts"},
		{name: "pnpm", policy: "ignore-scripts"},
	}
	for _, contract := range contracts {
		recipe, err := ResolveRecipe([]string{contract.name, "node"})
		if err != nil {
			t.Fatalf("ResolveRecipe(%s): %v", contract.name, err)
		}
		prefix := argPrefix(contract.name)
		if digest := recipe.BuildArgs[prefix+"_NPM_LOCK_SHA256"]; len(digest) != 64 {
			t.Errorf("%s lock digest = %q", contract.name, digest)
		}
		if got := recipe.BuildArgs[prefix+"_NPM_SCRIPT_POLICY"]; got != contract.policy {
			t.Errorf("%s script policy = %q", contract.name, got)
		}
		if _, arbitrary := recipe.BuildArgs[prefix+"_NPM_PACKAGE"]; arbitrary {
			t.Errorf("%s recipe exposes an arbitrary npm package argument", contract.name)
		}
	}

	body, err := readAsset("Dockerfile.agent.tools")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(body)
	const frontend = "# syntax=docker/dockerfile:1@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89"
	firstLine, _, _ := strings.Cut(dockerfile, "\n")
	if firstLine != frontend || strings.Count(dockerfile, "# syntax=") != 1 {
		t.Fatalf("Dockerfile frontend must be the exact first and sole parser directive: %q", firstLine)
	}
	for _, want := range []string{"npm ci", "npm-locks/claude-code", "npm-locks/pi", "npm-locks/pnpm", "CLAUDE_CODE_NPM_LOCK_SHA256", "PI_NPM_LOCK_SHA256", "PNPM_NPM_LOCK_SHA256"} {
		if !strings.Contains(dockerfile, want) {
			t.Errorf("locked npm Dockerfile missing %q", want)
		}
	}
	for _, forbidden := range []string{"npm install -g", "CLAUDE_CODE_NPM_PACKAGE", "PI_NPM_PACKAGE", "PNPM_NPM_PACKAGE"} {
		if strings.Contains(dockerfile, forbidden) {
			t.Errorf("Dockerfile retains unreviewed npm surface %q", forbidden)
		}
	}
}
