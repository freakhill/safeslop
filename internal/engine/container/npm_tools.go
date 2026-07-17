package container

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"

	"github.com/freakhill/safeslop/internal/engine/policy"
)

const (
	npmScriptPolicyIgnoreScripts           = "ignore-scripts"
	npmScriptPolicyReviewedRootPostinstall = "reviewed-root-postinstall"
)

type npmToolContract struct {
	CatalogName            string
	PackageName            string
	PrimaryBinary          string
	Binaries               map[string]string
	ScriptPolicy           string
	ReviewedInstallScripts []string
}

var npmToolContracts = []npmToolContract{
	{
		CatalogName: "claude-code", PackageName: "@anthropic-ai/claude-code", PrimaryBinary: "claude",
		Binaries:               map[string]string{"claude": "bin/claude.exe"},
		ScriptPolicy:           npmScriptPolicyReviewedRootPostinstall,
		ReviewedInstallScripts: []string{"node_modules/@anthropic-ai/claude-code"},
	},
	{
		CatalogName: "pi", PackageName: "@earendil-works/pi-coding-agent", PrimaryBinary: "pi",
		Binaries: map[string]string{"pi": "dist/cli.js"}, ScriptPolicy: npmScriptPolicyIgnoreScripts,
	},
	{
		CatalogName: "pnpm", PackageName: "pnpm", PrimaryBinary: "pnpm",
		Binaries: map[string]string{"pnpm": "bin/pnpm.cjs", "pnpx": "bin/pnpx.cjs"}, ScriptPolicy: npmScriptPolicyIgnoreScripts,
	},
}

type npmToolLock struct {
	Contract    npmToolContract
	Version     string
	PackageJSON []byte
	PackageLock []byte
	SHA256      string
}

type npmProject struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Private      bool              `json:"private"`
	Dependencies map[string]string `json:"dependencies"`
	Scripts      map[string]string `json:"scripts,omitempty"`
}

type npmLockedPackage struct {
	Name                 string            `json:"name,omitempty"`
	Version              string            `json:"version,omitempty"`
	Resolved             string            `json:"resolved,omitempty"`
	Integrity            string            `json:"integrity,omitempty"`
	Link                 bool              `json:"link,omitempty"`
	Bin                  map[string]string `json:"bin,omitempty"`
	HasInstallScript     bool              `json:"hasInstallScript,omitempty"`
	Dependencies         map[string]string `json:"dependencies,omitempty"`
	OptionalDependencies map[string]string `json:"optionalDependencies,omitempty"`
}

type npmPackageLock struct {
	Name            string                      `json:"name"`
	Version         string                      `json:"version"`
	LockfileVersion int                         `json:"lockfileVersion"`
	Requires        bool                        `json:"requires"`
	Packages        map[string]npmLockedPackage `json:"packages"`
}

func decodeStrictJSON(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func validateNPMToolLock(contract npmToolContract, catalogVersion string, packageJSON, packageLock []byte) error {
	var project npmProject
	if err := decodeStrictJSON(packageJSON, &project); err != nil {
		return fmt.Errorf("%s package.json: %w", contract.CatalogName, err)
	}
	if project.Name != "safeslop-lock-"+contract.CatalogName || project.Version != "1.0.0" || !project.Private || len(project.Dependencies) != 1 || project.Dependencies[contract.PackageName] != catalogVersion || len(project.Scripts) != 0 {
		return fmt.Errorf("%s package.json is not the reviewed one-package project", contract.CatalogName)
	}

	var lock npmPackageLock
	if err := json.Unmarshal(packageLock, &lock); err != nil {
		return fmt.Errorf("%s package-lock.json: %w", contract.CatalogName, err)
	}
	if lock.Name != project.Name || lock.Version != project.Version || lock.LockfileVersion != 3 || !lock.Requires || len(lock.Packages) < 2 {
		return fmt.Errorf("%s package lock header is invalid", contract.CatalogName)
	}
	root, ok := lock.Packages[""]
	if !ok || root.Name != project.Name || root.Version != project.Version || len(root.Dependencies) != 1 || root.Dependencies[contract.PackageName] != catalogVersion {
		return fmt.Errorf("%s package lock root drifted", contract.CatalogName)
	}
	rootPath := "node_modules/" + contract.PackageName
	lockedRoot, ok := lock.Packages[rootPath]
	if !ok || lockedRoot.Version != catalogVersion || !sameStringMap(lockedRoot.Bin, contract.Binaries) {
		return fmt.Errorf("%s package lock has the wrong root package or binary", contract.CatalogName)
	}

	installScripts := make([]string, 0)
	packageNames := make(map[string]bool, len(lock.Packages))
	for packagePath, locked := range lock.Packages {
		if packagePath == "" {
			continue
		}
		packageName := lockedPackageName(packagePath)
		if !validLockPackagePath(packagePath) || packageName == "" || locked.Link || locked.Version == "" || !validRegistryTarball(locked.Resolved, packageName, locked.Version) || !validSHA512Integrity(locked.Integrity) {
			return fmt.Errorf("%s lock package %q has an unpinned or foreign source", contract.CatalogName, packagePath)
		}
		packageNames[packageName] = true
		if locked.HasInstallScript {
			installScripts = append(installScripts, packagePath)
		}
	}
	for packagePath, locked := range lock.Packages {
		if packagePath == "" {
			continue
		}
		for dependency := range locked.Dependencies {
			if !packageNames[dependency] {
				return fmt.Errorf("%s lock package %q references missing dependency %q", contract.CatalogName, packagePath, dependency)
			}
		}
		for dependency := range locked.OptionalDependencies {
			if !packageNames[dependency] {
				return fmt.Errorf("%s lock package %q references missing optional dependency %q", contract.CatalogName, packagePath, dependency)
			}
		}
	}
	sort.Strings(installScripts)
	reviewedScripts := append([]string(nil), contract.ReviewedInstallScripts...)
	sort.Strings(reviewedScripts)
	switch contract.ScriptPolicy {
	case npmScriptPolicyIgnoreScripts:
		if len(reviewedScripts) != 0 {
			return fmt.Errorf("%s ignore-scripts policy has lifecycle exceptions", contract.CatalogName)
		}
	case npmScriptPolicyReviewedRootPostinstall:
		if !sameStrings(installScripts, reviewedScripts) {
			return fmt.Errorf("%s lifecycle script set is not the reviewed exception", contract.CatalogName)
		}
	default:
		return fmt.Errorf("%s has an unknown npm script policy", contract.CatalogName)
	}
	return nil
}

func validatedNPMToolLocks() (map[string]npmToolLock, error) {
	catalog := policy.DefaultCatalog()
	contracts := make(map[string]npmToolContract, len(npmToolContracts))
	for _, contract := range npmToolContracts {
		if contract.CatalogName == "" || contract.PackageName == "" || contract.PrimaryBinary == "" || len(contract.Binaries) == 0 {
			return nil, fmt.Errorf("npm tool registry has an incomplete contract")
		}
		if _, duplicate := contracts[contract.CatalogName]; duplicate {
			return nil, fmt.Errorf("npm tool registry has duplicate %q", contract.CatalogName)
		}
		contracts[contract.CatalogName] = contract
	}
	entries, err := assetsFS.ReadDir("assets/npm-locks")
	if err != nil || len(entries) != len(contracts) {
		return nil, fmt.Errorf("embedded npm lock project set does not match the registry")
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return nil, fmt.Errorf("embedded npm lock root contains a foreign file")
		}
		if _, ok := contracts[entry.Name()]; !ok {
			return nil, fmt.Errorf("embedded npm lock project %q is not registered", entry.Name())
		}
		files, err := assetsFS.ReadDir("assets/npm-locks/" + entry.Name())
		if err != nil || len(files) != 2 || files[0].Name() != "package-lock.json" || files[1].Name() != "package.json" || files[0].IsDir() || files[1].IsDir() {
			return nil, fmt.Errorf("embedded npm lock project %q has foreign files", entry.Name())
		}
	}

	locks := make(map[string]npmToolLock, len(contracts))
	for name, contract := range contracts {
		catalogPackage, ok := catalog.Lookup(name)
		if !ok || catalogPackage.Kind != policy.KindNpm || !iw2BuildablePackages[name] {
			return nil, fmt.Errorf("npm tool registry %q does not match a buildable catalog package", name)
		}
		packageJSON, err := readAsset("npm-locks/" + name + "/package.json")
		if err != nil {
			return nil, err
		}
		packageLock, err := readAsset("npm-locks/" + name + "/package-lock.json")
		if err != nil {
			return nil, err
		}
		if err := validateNPMToolLock(contract, catalogPackage.Version, packageJSON, packageLock); err != nil {
			return nil, err
		}
		locks[name] = npmToolLock{
			Contract: contract, Version: catalogPackage.Version,
			PackageJSON: packageJSON, PackageLock: packageLock,
			SHA256: npmLockSHA256(packageJSON, packageLock),
		}
	}
	for name := range iw2BuildablePackages {
		catalogPackage, ok := catalog.Lookup(name)
		if ok && catalogPackage.Kind == policy.KindNpm {
			if _, registered := contracts[name]; !registered {
				return nil, fmt.Errorf("buildable npm package %q has no lock contract", name)
			}
		}
	}
	return locks, nil
}

func npmLockSHA256(packageJSON, packageLock []byte) string {
	digest := sha256.New()
	_, _ = digest.Write(packageJSON)
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(packageLock)
	return hex.EncodeToString(digest.Sum(nil))
}

func validLockPackagePath(value string) bool {
	return strings.HasPrefix(value, "node_modules/") && pathpkg.Clean(value) == value && !strings.Contains(value, "\\") && !strings.ContainsAny(value, "\x00\r\n")
}

func validRegistryTarball(value, packageName, version string) bool {
	parsed, err := url.Parse(value)
	baseName := packageName
	if slash := strings.LastIndexByte(baseName, '/'); slash >= 0 {
		baseName = baseName[slash+1:]
	}
	wantPrefix := "/" + packageName + "/-/"
	wantSuffix := "/" + baseName + "-" + version + ".tgz"
	return err == nil && parsed.Scheme == "https" && parsed.Host == "registry.npmjs.org" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && strings.HasPrefix(parsed.Path, wantPrefix) && strings.HasSuffix(parsed.Path, wantSuffix)
}

func validSHA512Integrity(value string) bool {
	if !strings.HasPrefix(value, "sha512-") || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	digest, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "sha512-"))
	return err == nil && len(digest) == sha512.Size
}

func lockedPackageName(packagePath string) string {
	parts := strings.Split(packagePath, "/node_modules/")
	leaf := parts[len(parts)-1]
	leaf = strings.TrimPrefix(leaf, "node_modules/")
	if strings.HasPrefix(leaf, "@") {
		components := strings.Split(leaf, "/")
		if len(components) >= 2 {
			return components[0] + "/" + components[1]
		}
	}
	return strings.SplitN(leaf, "/", 2)[0]
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
