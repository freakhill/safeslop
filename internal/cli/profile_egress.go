package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/trust"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

// cmdProfileEgress owns the durable, policy-trusted egress mutation path. It is
// intentionally separate from the session overlay: every write changes policy
// bytes and therefore requires the normal trust review before a future session
// can use the rule (specs/0103).
func cmdProfileEgress() *cobra.Command {
	return cmdProfileEgressWithDeps(defaultDependencies())
}

func cmdProfileEgressWithDeps(d *dependencies) *cobra.Command {
	c := &cobra.Command{Use: "egress", Short: "Review and edit persistent exact egress rules"}
	c.AddCommand(cmdProfileEgressMutationWithDeps(d, "preview"), cmdProfileEgressMutationWithDeps(d, "add"), cmdProfileEgressMutationWithDeps(d, "remove"))
	return c
}

func cmdProfileEgressMutation(operation string) *cobra.Command {
	return cmdProfileEgressMutationWithDeps(defaultDependencies(), operation)
}

func cmdProfileEgressMutationWithDeps(d *dependencies, operation string) *cobra.Command {
	var host, expectedHash, output string
	var port int
	c := &cobra.Command{
		Use:   operation + " <profile> [safeslop.cue] --host <fqdn> --port <80|443> --expected-policy-hash <hash> --output json",
		Short: profileEgressShort(operation),
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("profile egress %s requires --output json", operation)
			}
			if expectedHash == "" {
				return emitContractError(jsoncontract.CodeInvalidArgument, "--expected-policy-hash is required", nil)
			}
			ruleHost, rulePort, err := policy.ValidateExactEgress(host, port)
			if err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, err.Error(), nil)
			}
			path, cfg, currentHash, err := loadProfileEgressConfig(argAt(args, 1))
			if err != nil {
				// A CUE diagnostic can quote policy values, including secret refs; this
				// egress contract is deliberately value-free even on load failure.
				return emitContractError(jsoncontract.CodeNotFound, "load safeslop.cue", nil)
			}
			if expectedHash != currentHash {
				return emitContractError(jsoncontract.CodeInvalidArgument, "expected policy hash is stale", map[string]any{"current_policy_hash": currentHash})
			}
			name := args[0]
			prof, ok := cfg.Profiles[name]
			if !ok {
				return emitContractError(jsoncontract.CodeNotFound, fmt.Sprintf("no profile %q in safeslop.cue", name), map[string]any{"profile": name, "path": path})
			}
			candidate, err := mutateProfilePersistentEgress(cfg, name, prof, ruleHost, rulePort, operation)
			if err != nil {
				return emitContractError(jsoncontract.CodeInvalidArgument, err.Error(), map[string]any{"profile": name})
			}
			rendered, err := renderConfigCUE(candidate)
			if err != nil {
				return emitContractError(jsoncontract.CodeInternal, "render safeslop.cue", nil)
			}
			if _, err := policy.LoadBytes(rendered); err != nil {
				return emitContractError(jsoncontract.CodeSchemaViolation, "rendered safeslop.cue did not validate; not writing", nil)
			}
			candidateHash := trust.Hash(rendered)
			if operation != "preview" {
				if err := d.writePolicyAtomically(path, rendered); err != nil {
					return emitContractError(jsoncontract.CodeIOError, "write safeslop.cue", map[string]any{"path": path})
				}
			}
			emitContract(jsoncontract.OK(profileEgressMutationData(name, path, ruleHost, rulePort, operation, currentHash, candidateHash)))
			return nil
		},
	}
	c.Flags().StringVar(&host, "host", "", "exact FQDN rule host")
	c.Flags().IntVar(&port, "port", 0, "destination port (80 or 443)")
	c.Flags().StringVar(&expectedHash, "expected-policy-hash", "", "current policy hash reviewed by the operator")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func profileEgressShort(operation string) string {
	switch operation {
	case "preview":
		return "Preview one persistent exact egress policy change"
	case "add":
		return "Add one persistent exact egress rule to future sessions"
	default:
		return "Remove one persistent exact egress rule from future sessions"
	}
}

func loadProfileEgressConfig(pathArg string) (string, *policy.Config, string, error) {
	path, err := findConfig(pathArg)
	if err != nil {
		return "", nil, "", err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", nil, "", err
	}
	cfg, err := policy.LoadBytes(bytes)
	if err != nil {
		return "", nil, "", err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]policy.Profile{}
	}
	return path, cfg, trust.Hash(bytes), nil
}

func mutateProfilePersistentEgress(cfg *policy.Config, name string, prof policy.Profile, host string, port int, operation string) (*policy.Config, error) {
	candidate := *cfg
	candidate.Profiles = make(map[string]policy.Profile, len(cfg.Profiles))
	for n, existing := range cfg.Profiles {
		candidate.Profiles[n] = existing
	}
	key := fmt.Sprintf("%s:%d", host, port)
	index := -1
	for i, rule := range prof.PersistentEgress {
		if rule.FQDN == host && rule.Port == port {
			index = i
			break
		}
	}
	switch operation {
	case "preview", "add":
		if index >= 0 {
			return nil, fmt.Errorf("persistentEgress already contains %s", key)
		}
		prof.PersistentEgress = append(append([]policy.PersistentEgressRule(nil), prof.PersistentEgress...), policy.PersistentEgressRule{FQDN: host, Port: port})
	case "remove":
		if index < 0 {
			return nil, fmt.Errorf("persistentEgress does not contain %s", key)
		}
		out := append([]policy.PersistentEgressRule(nil), prof.PersistentEgress[:index]...)
		prof.PersistentEgress = append(out, prof.PersistentEgress[index+1:]...)
	default:
		return nil, fmt.Errorf("unknown persistent egress operation %q", operation)
	}
	candidate.Profiles[name] = prof
	return &candidate, nil
}

func profileEgressMutationData(name, path, host string, port int, operation, currentHash, candidateHash string) map[string]any {
	op := "+"
	if operation == "remove" {
		op = "-"
	}
	rule := map[string]any{"fqdn": host, "port": port}
	return map[string]any{
		"profile":               name,
		"path":                  path,
		"rule":                  rule,
		"source":                "profile-persistent",
		"lifetime":              "future-sessions",
		"current_policy_hash":   currentHash,
		"candidate_policy_hash": candidateHash,
		"delta":                 map[string]any{"op": op, "persistentEgress": rule},
	}
}

// writePolicyAtomically ensures an interrupted durable-rule review cannot leave
// a partial CUE file. The target is replaced only after the complete candidate
// was rendered and validated in memory.
func writePolicyAtomically(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".safeslop.cue-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
