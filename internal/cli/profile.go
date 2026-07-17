package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/freakhill/safeslop/internal/engine/container"
	"github.com/freakhill/safeslop/internal/engine/creds"
	"github.com/freakhill/safeslop/internal/engine/policy"
	workspaceboundary "github.com/freakhill/safeslop/internal/engine/workspace"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

type lockfile struct {
	RecipeID string            `json:"recipeID"`
	Agent    string            `json:"agent"`
	Base     string            `json:"base"`
	Bundles  []string          `json:"bundles,omitempty"`
	Packages []string          `json:"packages"`
	Versions map[string]string `json:"versions"`
}

func cmdLock() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "lock [profile] --output json",
		Short: "Write safeslop.lock.json for a profile's resolved image recipe",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("lock requires --output json")
			}
			path, err := findConfig("")
			if err != nil {
				return err
			}
			cfg, err := policy.Load(path)
			if err != nil {
				return err
			}
			name, prof, err := selectProfile(cfg, arg0(args))
			if err != nil {
				return err
			}
			resolved, err := policy.Resolve(prof)
			if err != nil {
				return err
			}
			recipe, err := container.ResolveRecipe(resolved.IdentitySet)
			if err != nil {
				return err
			}
			lf, err := buildLockfile(prof, resolved, recipe)
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(lf, "", "  ")
			if err != nil {
				return err
			}
			b = append(b, '\n')
			lockPath := filepath.Join(filepath.Dir(path), "safeslop.lock.json")
			if err := os.WriteFile(lockPath, b, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", lockPath, err)
			}
			emitContract(jsoncontract.OK(map[string]any{
				"path":     lockPath,
				"profile":  name,
				"recipeID": lf.RecipeID,
				"lock":     lf,
			}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func buildLockfile(prof policy.Profile, resolved *policy.Resolved, recipe *container.Recipe) (*lockfile, error) {
	versions := make(map[string]string, len(resolved.IdentitySet))
	cat := policy.DefaultCatalog()
	for _, name := range resolved.IdentitySet {
		p, ok := cat.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("lock: resolved package %q is not in the catalog", name)
		}
		versions[name] = p.Version
	}
	return &lockfile{
		RecipeID: recipe.RecipeID,
		Agent:    policy.NormalizeAgent(prof.Agent),
		Base:     recipe.SourceBaseImage,
		Bundles:  append([]string(nil), prof.Bundles...),
		Packages: append([]string(nil), resolved.IdentitySet...),
		Versions: versions,
	}, nil
}

// cmdProfile groups the enveloped policy surfaces the Emacs profiles view consumes
// (specs/0052 E2/E3, specs/0058 IW3).
// cmdCreds groups read-only credential-posture inspection over safeslop.cue (specs/0067). Authoring
// stays CUE-canonical (edit safeslop.cue itself); this surface only reads and reports value-free
// readiness status — it never handles or reveals a secret value.
func cmdCreds() *cobra.Command {
	return cmdCredsWithDeps(defaultDependencies())
}

func cmdCredsWithDeps(d *dependencies) *cobra.Command {
	c := &cobra.Command{Use: "creds", Short: "Inspect the credential posture of safeslop.cue profiles"}
	c.AddCommand(cmdCredsListWithDeps(d), cmdCredsShowWithDeps(d), cmdCredsLink(), cmdCredsUnlink(), cmdCredsStatus(), cmdCredsGCWithDeps(d))
	return c
}

func cmdCredsList() *cobra.Command {
	return cmdCredsListWithDeps(defaultDependencies())
}

func cmdCredsListWithDeps(d *dependencies) *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "list [safeslop.cue] --output json",
		Short: "List declared credentials across profiles with value-free readiness status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("creds list requires --output json")
			}
			path, err := findConfig(argAt(args, 0))
			if err != nil {
				return emitContractError(jsoncontract.CodeNotFound, "safeslop.cue not found", map[string]any{"error": err.Error()})
			}
			cfg, err := policy.Load(path)
			if err != nil {
				return emitContractError(jsoncontract.CodeSchemaViolation, "load safeslop.cue", map[string]any{"path": path, "error": err.Error()})
			}
			rep := creds.Inspect(context.Background(), cfg, d.credsProber())
			emitContract(jsoncontract.OK(map[string]any{
				"config":      path,
				"op":          rep.Op,
				"credentials": credRowsOrEmpty(rep.Rows),
			}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdCredsShow() *cobra.Command {
	return cmdCredsShowWithDeps(defaultDependencies())
}

func cmdCredsShowWithDeps(d *dependencies) *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "show <profile> [safeslop.cue] --output json",
		Short: "Show one profile's declared credentials with value-free readiness status",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("creds show requires --output json")
			}
			path, err := findConfig(argAt(args, 1))
			if err != nil {
				return emitContractError(jsoncontract.CodeNotFound, "safeslop.cue not found", map[string]any{"error": err.Error()})
			}
			cfg, err := policy.Load(path)
			if err != nil {
				return emitContractError(jsoncontract.CodeSchemaViolation, "load safeslop.cue", map[string]any{"path": path, "error": err.Error()})
			}
			prof, ok := cfg.Profiles[args[0]]
			if !ok {
				return emitContractError(jsoncontract.CodeNotFound, fmt.Sprintf("no profile %q in safeslop.cue", args[0]), map[string]any{"profile": args[0], "path": path})
			}
			// Scope Inspect to just this profile for the detail view.
			one := &policy.Config{Version: cfg.Version, Profiles: map[string]policy.Profile{args[0]: prof}}
			rep := creds.Inspect(context.Background(), one, d.credsProber())
			emitContract(jsoncontract.OK(map[string]any{
				"config":      path,
				"profile":     args[0],
				"op":          rep.Op,
				"credentials": credRowsOrEmpty(rep.Rows),
			}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

// credRowsOrEmpty coalesces a nil row slice to non-nil so the envelope always carries a
// `credentials: []` array (never `null`) for the Emacs client.
func credRowsOrEmpty(rows []creds.CredRow) []creds.CredRow {
	if rows == nil {
		return []creds.CredRow{}
	}
	return rows
}

func cmdProfile() *cobra.Command {
	return cmdProfileWithDeps(defaultDependencies())
}

func cmdProfileWithDeps(d *dependencies) *cobra.Command {
	c := &cobra.Command{Use: "profile", Short: "Inspect and author safeslop.cue profiles"}
	c.AddCommand(cmdProfileList(), cmdProfilePresets(), cmdProfileDefaults(), cmdProfileShowWithDeps(d), cmdProfileCreateWithDeps(d), cmdProfileDeleteWithDeps(d), cmdProfileCredentialsWithDeps(d), cmdProfileEgressWithDeps(d))
	return c
}

func cmdProfileList() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "list [safeslop.cue] --output json",
		Short: "List the profiles defined in a safeslop.cue (enveloped JSON contract)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("profile list requires --output json")
			}
			path, err := findConfig(arg0(args))
			if err != nil {
				if len(args) > 0 {
					return err
				}
				emitContract(jsoncontract.OK(map[string]any{
					"path":     "",
					"profiles": map[string]policy.Profile{},
					"builtins": profileBuiltinRows(),
				}))
				return nil
			}
			cfg, err := policy.Load(path)
			if err != nil {
				return err
			}
			emitContract(jsoncontract.OK(map[string]any{"path": path, "profiles": cfg.Profiles, "builtins": profileBuiltinRows()}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdProfilePresets() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "presets --output json",
		Short: "List the embedded policy presets offered as starting points",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("profile presets requires --output json")
			}
			emitContract(jsoncontract.OK(map[string]any{"presets": policy.Presets()}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func profileBuiltinRows() []map[string]any {
	profiles := make([]map[string]any, 0)
	for _, builtin := range policy.BuiltinProfiles() {
		profiles = append(profiles, map[string]any{
			"name": builtin.Name, "description": builtin.Description, "profile_source": "builtin",
			"policy_path": "builtin:" + builtin.Name, "policy_hash": builtin.Hash, "profile": builtin.Profile,
		})
	}
	return profiles
}

func cmdProfileDefaults() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "defaults --output json",
		Short: "List binary-embedded launchable profile defaults",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("profile defaults requires --output json")
			}
			emitContract(jsoncontract.OK(map[string]any{"profiles": profileBuiltinRows()}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdProfileShow() *cobra.Command {
	return cmdProfileShowWithDeps(defaultDependencies())
}

func cmdProfileShowWithDeps(d *dependencies) *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "show <name> [safeslop.cue] --output json",
		Short: "Show a resolved project or builtin profile with its image recipe",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("profile show requires --output json")
			}
			resolved, err := resolveProfile(args[0], argAt(args, 1))
			if err != nil {
				code := jsoncontract.CodeNotFound
				var resolutionErr *profileResolutionError
				if errors.As(err, &resolutionErr) {
					code = resolutionErr.code
				}
				return emitContractError(code, "profile not found", map[string]any{"profile": args[0], "error": err.Error()})
			}
			data, err := profileResolvedDataWithDeps(d, resolved.policyPath, resolved.name, resolved.profile, profileEvaluationInput{
				Source:      profileEvaluationSource(resolved.source),
				Name:        resolved.name,
				PolicyPath:  resolved.policyPath,
				PolicyHash:  resolved.policyHash,
				PolicyBytes: resolved.policyBytes,
				Profile:     resolved.profile,
			})
			if err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, "resolve profile image recipe", map[string]any{"profile": args[0], "error": err.Error()})
			}
			data["profile_source"] = resolved.source
			data["profile_name"] = resolved.name
			data["policy_path"] = resolved.policyPath
			data["policy_hash"] = resolved.policyHash
			emitContract(jsoncontract.OK(data))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdProfileCreate() *cobra.Command {
	return cmdProfileCreateWithDeps(defaultDependencies())
}

func cmdProfileCreateWithDeps(d *dependencies) *cobra.Command {
	var name, agent, environment, workspace, network, output string
	var bundles, packages []string
	var dryRun, noDefaultBundle bool
	c := &cobra.Command{
		Use:   "create --name N --agent A --environment E [--bundle B ...] [--package P ...] [--dry-run] --output json",
		Short: "Create or update a safeslop.cue profile",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("profile create requires --output json")
			}
			if name == "" || agent == "" || environment == "" {
				return emitContractError(jsoncontract.CodeInvalidArgument, "profile create requires --name, --agent, and --environment", nil)
			}
			if network == "" {
				network = "deny"
			}
			var cfg *policy.Config
			path, err := profileCreatePathForOutput()
			if err != nil {
				return emitContractError(jsoncontract.CodeIOError, "load safeslop.cue", map[string]any{"error": err.Error()})
			}
			if !dryRun {
				path, cfg, err = loadOrNewConfigForCreate()
				if err != nil {
					return emitContractError(jsoncontract.CodeIOError, "load safeslop.cue", map[string]any{"error": err.Error()})
				}
			}
			prof := policy.Profile{
				Agent:       policy.NormalizeAgent(agent),
				Environment: environment,
				Workspace:   workspace,
				Network:     network,
				Bundles:     append([]string(nil), bundles...),
				Packages:    append([]string(nil), packages...),
				BareAgent:   noDefaultBundle,
			}
			if _, err := policy.Resolve(prof); err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, "resolve profile packages", map[string]any{"profile": name, "error": err.Error()})
			}
			data, err := profileResolvedDataWithDeps(d, path, name, prof)
			if err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, "resolve profile image recipe", map[string]any{"profile": name, "error": err.Error()})
			}
			if dryRun {
				data["dryRun"] = true
				emitContract(jsoncontract.OK(data))
				return nil
			}
			cfg.Profiles[name] = prof
			rendered, err := renderConfigCUE(cfg)
			if err != nil {
				return emitContractError(jsoncontract.CodeInternal, "render safeslop.cue", map[string]any{"error": err.Error()})
			}
			if _, err := policy.LoadBytes(rendered); err != nil {
				return emitContractError(jsoncontract.CodeSchemaViolation, "rendered safeslop.cue did not validate; not writing", map[string]any{"error": err.Error()})
			}
			if err := d.writePolicy(path, rendered); err != nil {
				return emitContractError(jsoncontract.CodeIOError, "write safeslop.cue", map[string]any{"path": path, "error": err.Error()})
			}
			data["created"] = true
			emitContract(jsoncontract.OK(data))
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "profile name")
	c.Flags().StringVar(&agent, "agent", "", "agent: claude, claude-code, pi, fish, zsh, or shell")
	c.Flags().StringVar(&environment, "environment", "", "isolation environment: host or container")
	c.Flags().StringArrayVar(&bundles, "bundle", nil, "catalog bundle to include (repeatable)")
	c.Flags().StringArrayVar(&packages, "package", nil, "catalog package to include (repeatable)")
	c.Flags().StringVar(&workspace, "workspace", "", "workspace directory")
	c.Flags().StringVar(&network, "network", "deny", "network policy: deny or allow")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "resolve and print the profile without writing safeslop.cue")
	c.Flags().BoolVar(&noDefaultBundle, "no-default-bundle", false, "do not include the agent's default package bundle")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdProfileDelete() *cobra.Command {
	return cmdProfileDeleteWithDeps(defaultDependencies())
}

func cmdProfileDeleteWithDeps(d *dependencies) *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "delete <name> [safeslop.cue] --output json",
		Short: "Delete one profile from safeslop.cue",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("profile delete requires --output json")
			}
			path, err := findConfig(argAt(args, 1))
			if err != nil {
				return emitContractError(jsoncontract.CodeNotFound, "load safeslop.cue", map[string]any{"error": err.Error()})
			}
			cfg, err := policy.Load(path)
			if err != nil {
				return emitContractError(jsoncontract.CodeSchemaViolation, "load safeslop.cue", map[string]any{"path": path, "error": err.Error()})
			}
			name := args[0]
			if _, ok := cfg.Profiles[name]; !ok {
				return emitContractError(jsoncontract.CodeNotFound, fmt.Sprintf("no profile %q in safeslop.cue", name), map[string]any{"profile": name, "path": path})
			}
			delete(cfg.Profiles, name)
			rendered, err := renderConfigCUE(cfg)
			if err != nil {
				return emitContractError(jsoncontract.CodeInternal, "render safeslop.cue", map[string]any{"error": err.Error()})
			}
			if _, err := policy.LoadBytes(rendered); err != nil {
				return emitContractError(jsoncontract.CodeSchemaViolation, "rendered safeslop.cue did not validate; not writing", map[string]any{"error": err.Error()})
			}
			if err := d.writePolicy(path, rendered); err != nil {
				return emitContractError(jsoncontract.CodeIOError, "write safeslop.cue", map[string]any{"path": path, "error": err.Error()})
			}
			emitContract(jsoncontract.OK(map[string]any{"removed": true, "profile": name, "path": path}))
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func profileCreatePathForOutput() (string, error) {
	path, err := findConfig("")
	if err == nil {
		return path, nil
	}
	wd, werr := os.Getwd()
	if werr != nil {
		return "", werr
	}
	return filepath.Join(wd, "safeslop.cue"), nil
}

func loadOrNewConfigForCreate() (string, *policy.Config, error) {
	path, err := findConfig("")
	if err != nil {
		wd, werr := os.Getwd()
		if werr != nil {
			return "", nil, werr
		}
		path = filepath.Join(wd, "safeslop.cue")
		return path, &policy.Config{Version: 1, Profiles: map[string]policy.Profile{}}, nil
	}
	cfg, err := policy.Load(path)
	if err != nil {
		return "", nil, err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]policy.Profile{}
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	return path, cfg, nil
}

func renderConfigCUE(cfg *policy.Config) ([]byte, error) {
	b, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return nil, err
	}
	return append([]byte("package safeslop\n\nsafeslop: "), append(b, '\n')...), nil
}

type resolvedProfile struct {
	name        string
	profile     policy.Profile
	source      string
	policyPath  string
	policyHash  string
	policyBytes []byte
}

type profileResolutionError struct {
	code jsoncontract.ErrorCode
	err  error
}

func (e *profileResolutionError) Error() string { return e.err.Error() }
func (e *profileResolutionError) Unwrap() error { return e.err }

// resolveProfile gives a valid project config precedence over the signed binary's
// builtin registry. A present-but-invalid project config is never ignored: that
// would turn a typo or a damaged policy into an unintended authority change.
func resolveProfile(name, explicit string) (resolvedProfile, error) {
	path, findErr := findConfig(explicit)
	if findErr == nil {
		if _, err := os.Stat(path); err == nil {
			loaded, err := loadPolicyForLaunch(path)
			if err != nil {
				return resolvedProfile{}, &profileResolutionError{code: jsoncontract.CodeSchemaViolation, err: fmt.Errorf("load safeslop.cue: %w", err)}
			}
			if prof, ok := loaded.cfg.Profiles[name]; ok {
				return resolvedProfile{
					name: name, profile: prof, source: "project", policyPath: loaded.trustPath,
					policyHash: loaded.hash, policyBytes: append([]byte(nil), loaded.bytes...),
				}, nil
			}
		} else if !os.IsNotExist(err) {
			return resolvedProfile{}, fmt.Errorf("stat safeslop.cue: %w", err)
		}
	}
	if builtin, ok := policy.BuiltinProfileByName(name); ok {
		return resolvedProfile{name: name, profile: builtin.Profile, source: "builtin", policyPath: "builtin:" + name, policyHash: builtin.Hash}, nil
	}
	if findErr != nil {
		return resolvedProfile{}, findErr
	}
	return resolvedProfile{}, fmt.Errorf("no profile %q", name)
}

func profileResolvedData(path, name string, prof policy.Profile, contexts ...profileEvaluationInput) (map[string]any, error) {
	return profileResolvedDataWithDeps(defaultDependencies(), path, name, prof, contexts...)
}

func profileResolvedDataWithDeps(d *dependencies, path, name string, prof policy.Profile, contexts ...profileEvaluationInput) (map[string]any, error) {
	resolved, err := policy.Resolve(prof)
	if err != nil {
		return nil, err
	}
	recipe, err := container.ResolveRecipe(resolved.IdentitySet)
	if err != nil {
		return nil, err
	}
	evaluationInput := profileEvaluationInput{Source: profileEvaluationSourceUnsaved, Name: name, PolicyPath: path, Profile: prof}
	if len(contexts) > 0 {
		evaluationInput = contexts[0]
		evaluationInput.Name = name
		evaluationInput.PolicyPath = path
		evaluationInput.Profile = prof
	}
	invocationDir, cwdErr := os.Getwd()
	policyPath := ""
	if evaluationInput.Source != profileEvaluationSourceBuiltin {
		policyPath = evaluationInput.PolicyPath
	}
	if cwdErr == nil {
		if canonical, err := workspaceboundary.Resolve(prof.Workspace, policyPath, invocationDir); err == nil {
			prof.Workspace = canonical
			evaluationInput.Profile = prof
		}
	}
	return map[string]any{
		"path":       path,
		"name":       name,
		"profile":    prof,
		"evaluation": evaluateProfileWithDeps(d, evaluationInput),
		"risk":       policy.RiskSummary(prof),
		"risk_axes":  policy.RiskAxes(prof),
		"resolved":   resolved,
		"recipeID":   recipe.RecipeID,
		"image":      recipe.AgentImage,
		"base":       recipe.SourceBaseImage,
		"baseImage":  recipe.BaseImage,
		"recipe":     recipe,
	}, nil
}
